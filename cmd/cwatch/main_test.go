package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"cwatch/internal/state"
)

var binPath string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "cwatch-bin")
	if err != nil {
		panic(err)
	}
	binPath = filepath.Join(dir, "cwatch")
	build := exec.Command("go", "build", "-o", binPath, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=1")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build failed: %v\n%s", err, out)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// hookEnv removes the terminal variables of the developer's shell, so the
// tests do not depend on where they run.
func hookEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		k := kv[:strings.IndexByte(kv+"=", '=')]
		switch k {
		case "ITERM_SESSION_ID", "TMUX", "CLAUDE_PID", "SSH_CONNECTION", "SSH_TTY", "CWATCH_DISABLE":
			continue
		}
		env = append(env, kv)
	}
	return env
}

func runHook(t *testing.T, stateDir string, input []byte) (string, string, int) {
	t.Helper()
	cmd := exec.Command(binPath, "hook", "--managed-by=cwatch", "--state-dir", stateDir)
	cmd.Env = hookEnv()
	cmd.Stdin = bytes.NewReader(input)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return out.String(), errb.String(), code
}

func payload(event, session string, extra string) []byte {
	return []byte(fmt.Sprintf(`{"session_id":%q,"hook_event_name":%q,"cwd":"/work/repo"%s}`, session, event, extra))
}

// Acceptance test 14: the hook prints nothing, exits 0, and never blocks
// Claude Code, for good and bad input.
func TestHookOutputContract(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state with space's")
	inputs := [][]byte{
		payload("UserPromptSubmit", "s1", `,"prompt":"hello"`),
		payload("PermissionRequest", "s1", `,"tool_name":"Bash","tool_input":{"command":"rm -rf /"}`),
		payload("SomeFutureEvent", "s1", ""),
		[]byte("{not json"),
		[]byte(""),
		payload("PostToolUse", "s1", `,"tool_name":"Write","tool_use_id":"t","tool_input":{"content":"`+strings.Repeat("x", 5<<20)+`"}`),
	}
	for i, in := range inputs {
		out, _, code := runHook(t, dir, in)
		if out != "" || code != 0 {
			t.Fatalf("input %d: stdout %q, exit %d", i, out, code)
		}
	}
	// An unwritable state directory still gives exit 0 and no stdout.
	out, stderr, code := runHook(t, "/dev/null/cwatch", payload("Stop", "s1", ""))
	if out != "" || code != 0 || !strings.Contains(stderr, "cwatch hook:") {
		t.Fatalf("unwritable: stdout %q, stderr %q, exit %d", out, stderr, code)
	}
	st, err := state.OpenExisting(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	stats, _ := st.Stats(context.Background())
	// The prompt, the permission request, and the oversized tool event.
	if stats.Events != 3 {
		t.Fatalf("%d events recorded, want 3", stats.Events)
	}
	list, _ := st.List(context.Background())
	if len(list) != 1 || list[0].State != state.NeedsPermission && list[0].State != state.Working {
		t.Fatalf("instances %+v", list)
	}
	// No raw tool input is stored.
	raw, _ := os.ReadFile(st.Path)
	if bytes.Contains(raw, []byte("rm -rf /")) {
		t.Fatal("tool input stored in the database")
	}
}

// Acceptance test 8 (subprocess part).
func TestConcurrentHookProcesses(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	const procs, perProc = 12, 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	var failures []string
	start := time.Now()
	for p := 0; p < procs; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			session := "shared"
			if p%3 == 0 {
				session = fmt.Sprintf("own-%d", p)
			}
			for i := 0; i < perProc; i++ {
				in := payload("PreToolUse", session, fmt.Sprintf(`,"tool_name":"Bash","tool_use_id":"t-%d-%d"`, p, i))
				out, stderr, code := runHook(t, dir, in)
				if out != "" || code != 0 || stderr != "" {
					mu.Lock()
					failures = append(failures, fmt.Sprintf("proc %d: %q %q %d", p, out, stderr, code))
					mu.Unlock()
				}
			}
		}(p)
	}
	wg.Wait()
	elapsed := time.Since(start)
	if len(failures) > 0 {
		t.Fatalf("hook failures: %v", failures)
	}
	st, err := state.OpenExisting(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	stats, _ := st.Stats(context.Background())
	if stats.Events != procs*perProc {
		t.Fatalf("%d events, want %d (lost updates)", stats.Events, procs*perProc)
	}
	list, _ := st.List(context.Background())
	if len(list) != 1+procs/3 {
		t.Fatalf("%d instances, want %d", len(list), 1+procs/3)
	}
	for _, in := range list {
		evs, _ := st.Events(context.Background(), in.InstanceID, 10000)
		if in.LastSeq != evs[len(evs)-1].Seq {
			t.Fatalf("instance %s missed its latest event", in.ShortID())
		}
	}
	t.Logf("%d hook processes in %s (%.1f ms per event, 12 concurrent writers)", procs*perProc, elapsed, float64(elapsed.Milliseconds())/float64(procs*perProc)*float64(procs))
}

func TestBareCommandWithoutTerminalDoesNotStartDashboard(t *testing.T) {
	cmd := exec.Command(binPath, "--state-dir", t.TempDir())
	cmd.Stdin = strings.NewReader(`{"session_id":"s","hook_event_name":"Stop"}`)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if err == nil || out.Len() != 0 || !strings.Contains(errb.String(), "needs a terminal") {
		t.Fatalf("err %v, stdout %q, stderr %q", err, out.String(), errb.String())
	}
}

func TestListJSONAfterHooks(t *testing.T) {
	dir := t.TempDir()
	runHook(t, dir, payload("SessionStart", "abc", ""))
	runHook(t, dir, payload("UserPromptSubmit", "abc", `,"prompt":"hi"`))
	cmd := exec.Command(binPath, "list", "--json", "--all", "--state-dir", dir, "--settings-file", filepath.Join(dir, "none.json"))
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		SchemaVersion int              `json:"schema_version"`
		Instances     []state.Instance `json:"instances"`
	}
	if err := json.Unmarshal(out, &doc); err != nil || doc.SchemaVersion != 1 || len(doc.Instances) != 1 {
		t.Fatalf("%v: %s", err, out)
	}
	if doc.Instances[0].SessionID != "abc" || doc.Instances[0].PromptExcerpt != "hi" {
		t.Fatalf("instance %+v", doc.Instances[0])
	}
}

func TestUsageErrors(t *testing.T) {
	for _, args := range [][]string{{"bogus"}, {"focus"}, {"list", "--nope"}, {"summary"}, {"summary", "month"}, {"summary", "yesterday", "week"}} {
		err := exec.Command(binPath, args...).Run()
		ee, ok := err.(*exec.ExitError)
		if !ok || ee.ExitCode() != 2 {
			t.Errorf("%v: %v", args, err)
		}
	}
}

func TestSummaryWithoutActivityAndFromCache(t *testing.T) {
	cfg, dir := t.TempDir(), t.TempDir()
	run := func(args ...string) (string, error) {
		cmd := exec.Command(binPath, append(args, "--state-dir", dir)...)
		cmd.Env = append(hookEnv(), "CLAUDE_CONFIG_DIR="+cfg)
		out, err := cmd.Output()
		return string(out), err
	}
	// No transcripts: cwatch does not start claude.
	out, err := run("summary", "week")
	if err != nil || !strings.HasPrefix(out, "No Claude Code activity in last week") {
		t.Fatalf("%q %v", out, err)
	}
	// A stored summary prints without a model call.
	name := "yesterday-" + time.Now().AddDate(0, 0, -1).Format("2006-01-02") + ".v2.md"
	os.MkdirAll(filepath.Join(dir, "summaries"), 0o700)
	os.WriteFile(filepath.Join(dir, "summaries", name), []byte("- stored\n"), 0o600)
	if out, err := run("summary", "yesterday"); err != nil || out != "- stored\n" {
		t.Fatalf("%q %v", out, err)
	}
}

// Part of acceptance test 14: the binary links no HTTP or TLS client.
func TestNoNetworkClientDependencies(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Skip("go list is not available:", err)
	}
	for _, pkg := range strings.Fields(string(out)) {
		switch pkg {
		case "net/http", "crypto/tls", "net/rpc", "net/smtp":
			t.Errorf("the binary depends on %s", pkg)
		}
	}
}

// Stress test: concurrent processes create the database and write to it at
// the same time, and no event is lost. SQLite can return a lock error
// without a wait during this phase. The race is rare, so this test does not
// always trigger it; TestRetryBusy in package state tests the retry itself.
func TestConcurrentFirstUse(t *testing.T) {
	const rounds, workers, perWorker = 4, 8, 6
	for r := 0; r < rounds; r++ {
		dir := filepath.Join(t.TempDir(), "fresh")
		var wg sync.WaitGroup
		gate := make(chan struct{})
		var mu sync.Mutex
		var errs []string
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				<-gate
				for i := 0; i < perWorker; i++ {
					out, stderr, code := runHook(t, dir, payload("PreToolUse", fmt.Sprintf("s-%d", w%3), fmt.Sprintf(`,"tool_name":"Bash","tool_use_id":"t%d"`, i)))
					if out != "" || stderr != "" || code != 0 {
						mu.Lock()
						errs = append(errs, stderr)
						mu.Unlock()
					}
				}
			}(w)
		}
		close(gate)
		wg.Wait()
		if len(errs) > 0 {
			t.Fatalf("round %d: %d hook errors: %v", r, len(errs), errs[0])
		}
		st, err := state.OpenExisting(context.Background(), dir)
		if err != nil {
			t.Fatal(err)
		}
		stats, _ := st.Stats(context.Background())
		st.Close()
		if stats.Events != workers*perWorker {
			t.Fatalf("round %d: %d events, want %d", r, stats.Events, workers*perWorker)
		}
	}
}
