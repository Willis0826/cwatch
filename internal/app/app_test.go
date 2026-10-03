package app

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"cwatch/internal/hooks"
	"cwatch/internal/process"
	"cwatch/internal/state"
	"cwatch/internal/terminal"
)

type procs struct {
	mu sync.Mutex
	m  map[int]process.Proc
}

func (p *procs) Lookup(pid int) (process.Proc, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if v, ok := p.m[pid]; ok {
		return v, nil
	}
	return process.Proc{}, process.ErrNotFound
}

type focuser struct {
	called  bool
	gotID   string
	gotTTY  string
	err     error
	panes   []terminal.Pane
	matched terminal.Pane
}

func (f *focuser) Focus(ctx context.Context, id, tty string) (terminal.Pane, error) {
	f.called, f.gotID, f.gotTTY = true, id, tty
	if f.err != nil {
		return terminal.Pane{}, f.err
	}
	p, err := terminal.Match(f.panes, id, tty)
	f.matched = p
	return p, err
}

var now = time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)

func testEnv(t *testing.T) (*Env, *procs, *focuser, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	dir := t.TempDir()
	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	p := &procs{m: map[int]process.Proc{}}
	f := &focuser{}
	env := &Env{
		StateDir:     filepath.Join(dir, "state"),
		SettingsFile: filepath.Join(dir, "settings.json"),
		Stdout:       out, Stderr: errb,
		Inspector:  p,
		Focuser:    f,
		Now:        func() time.Time { return now },
		Getenv:     func(string) string { return "" },
		Executable: func() (string, error) { return "/opt/cwatch/bin/cwatch", nil },
	}
	return env, p, f, out, errb
}

func seed(t *testing.T, env *Env, o state.Observation, sid string, names ...string) state.Instance {
	t.Helper()
	st, err := state.Open(context.Background(), env.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var in state.Instance
	for _, n := range names {
		ev, err := hooks.Parse([]byte(`{"session_id":"`+sid+`","hook_event_name":"`+n+`","cwd":"/work/app","prompt":"fix the \u001b[31mbug"}`), false)
		if err != nil {
			t.Fatal(err)
		}
		if in, err = st.Record(context.Background(), o, ev, true, now); err != nil {
			t.Fatal(err)
		}
	}
	return in
}

const pane1 = "BBBB0000-0000-0000-0000-000000000001"

func itermObs(pid int, start int64, tty, id string) state.Observation {
	return state.Observation{OwnerPID: pid, OwnerStart: start, OwnerMethod: process.MethodNameAncestor,
		TTY: tty, TerminalKind: state.TerminalITerm, ITermSessionID: id}
}

// Acceptance test 13.
func TestLoadShowsPersistedSessions(t *testing.T) {
	env, p, _, _, _ := testEnv(t)
	p.m[50] = process.Proc{PID: 50, Start: 5, TTY: "/dev/ttys005"}
	seed(t, env, itermObs(50, 5, "/dev/ttys005", pane1), "s1", "SessionStart", "UserPromptSubmit")
	// A later process (the dashboard) reads the store.
	snap := env.Load(context.Background(), LoadOptions{})
	if snap.Status != StatusOK || len(snap.Instances) != 1 {
		t.Fatalf("snapshot %+v", snap)
	}
	in := snap.Instances[0]
	if in.State != state.Working || in.Liveness != state.Alive || in.Project != "app" {
		t.Fatalf("instance %+v", in)
	}
	var b bytes.Buffer
	WriteTable(&b, snap.Instances, now, 120, true)
	if !strings.Contains(b.String(), `you: "fix the bug"`) || strings.ContainsRune(b.String(), 0x1b) {
		t.Fatalf("table:\n%s", b.String())
	}
	b.Reset()
	if err := WriteJSON(&b, snap, now, false); err != nil {
		t.Fatal(err)
	}
	var doc ListOutput
	if err := json.Unmarshal(b.Bytes(), &doc); err != nil || doc.SchemaVersion != 1 || len(doc.Instances) != 1 {
		t.Fatalf("json: %v %s", err, b.String())
	}
	if doc.Instances[0].PromptExcerpt != "" {
		t.Fatal("excerpt in JSON with excerpts off")
	}
}

func TestLoadEmptyStates(t *testing.T) {
	env, _, _, _, _ := testEnv(t)
	if s := env.Load(context.Background(), LoadOptions{}); s.Status != StatusHooksAbsent {
		t.Fatalf("status %q", s.Status)
	}
	if code := env.Setup(SetupOptions{}); code != 0 {
		t.Fatalf("setup exit %d", code)
	}
	if s := env.Load(context.Background(), LoadOptions{}); s.Status != StatusNoEvents {
		t.Fatalf("status %q", s.Status)
	}
	seed(t, env, itermObs(60, 6, "/dev/ttys006", pane1), "s", "SessionStart")
	// PID 60 does not run, so reconciliation ends the instance.
	s := env.Load(context.Background(), LoadOptions{})
	if s.Status != StatusNoLive {
		t.Fatalf("status %q", s.Status)
	}
	if s := env.Load(context.Background(), LoadOptions{All: true}); len(s.Instances) != 1 || s.Instances[0].Reason != state.ReasonProcessExit {
		t.Fatalf("all: %+v", s.Instances)
	}
}

func TestFocusSuccess(t *testing.T) {
	env, p, f, out, _ := testEnv(t)
	p.m[70] = process.Proc{PID: 70, Start: 7, TTY: "/dev/ttys007"}
	f.panes = []terminal.Pane{{WindowID: "4", TabIndex: 2, SessionID: pane1, TTY: "/dev/ttys007"}}
	in := seed(t, env, itermObs(70, 7, "/dev/ttys007", pane1), "s", "UserPromptSubmit")
	if code := env.FocusCommand(context.Background(), in.ShortID()); code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	if f.gotID != pane1 || f.gotTTY != "/dev/ttys007" || !strings.Contains(out.String(), "window 4, tab 2") {
		t.Fatalf("focus args %q %q, output %q", f.gotID, f.gotTTY, out.String())
	}
}

// Acceptance tests 7, 11, and 12 (refusals).
func TestFocusRefusals(t *testing.T) {
	type tc struct {
		name  string
		setup func(env *Env, p *procs) string
	}
	cases := []tc{
		{"ended", func(env *Env, p *procs) string {
			p.m[80] = process.Proc{PID: 80, Start: 8, TTY: "/dev/ttys008"}
			return seed(t, env, itermObs(80, 8, "/dev/ttys008", pane1), "s", "SessionStart", "SessionEnd").InstanceID
		}},
		{"unknown owner", func(env *Env, p *procs) string {
			o := state.Observation{OwnerMethod: process.MethodNotFound, TerminalKind: state.TerminalITerm, ITermSessionID: pane1}
			return seed(t, env, o, "s", "UserPromptSubmit").InstanceID
		}},
		{"tmux", func(env *Env, p *procs) string {
			p.m[81] = process.Proc{PID: 81, Start: 8, TTY: "/dev/ttys008"}
			o := itermObs(81, 8, "/dev/ttys008", "")
			o.TerminalKind = state.TerminalTmux
			return seed(t, env, o, "s", "UserPromptSubmit").InstanceID
		}},
		{"ssh", func(env *Env, p *procs) string {
			p.m[82] = process.Proc{PID: 82, Start: 8, TTY: "/dev/ttys008"}
			o := itermObs(82, 8, "/dev/ttys008", "")
			o.TerminalKind = state.TerminalSSH
			return seed(t, env, o, "s", "UserPromptSubmit").InstanceID
		}},
		{"process gone, tty reused", func(env *Env, p *procs) string {
			// The PID and TTY now belong to a new process in a new pane.
			p.m[83] = process.Proc{PID: 83, Start: 999, TTY: "/dev/ttys008"}
			return seed(t, env, itermObs(83, 8, "/dev/ttys008", pane1), "s", "UserPromptSubmit").InstanceID
		}},
		{"tty changed", func(env *Env, p *procs) string {
			p.m[84] = process.Proc{PID: 84, Start: 8, TTY: "/dev/ttys099"}
			return seed(t, env, itermObs(84, 8, "/dev/ttys008", pane1), "s", "UserPromptSubmit").InstanceID
		}},
		{"no tty", func(env *Env, p *procs) string {
			p.m[85] = process.Proc{PID: 85, Start: 8}
			return seed(t, env, itermObs(85, 8, "", pane1), "s", "UserPromptSubmit").InstanceID
		}},
		{"missing id", func(env *Env, p *procs) string {
			seed(t, env, itermObs(86, 8, "/dev/ttys008", pane1), "s", "UserPromptSubmit")
			return "ffffffff"
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env, p, f, _, errb := testEnv(t)
			id := c.setup(env, p)
			code := env.FocusCommand(context.Background(), id)
			if code != ExitRefused || f.called {
				t.Fatalf("exit %d, focuser called %v, stderr %q", code, f.called, errb.String())
			}
		})
	}
}

func TestFocusAdapterErrors(t *testing.T) {
	for _, want := range []error{terminal.ErrAutomationDenied, terminal.ErrPaneNotFound, terminal.ErrAmbiguousPane, terminal.ErrNotRunning} {
		env, p, f, _, errb := testEnv(t)
		p.m[90] = process.Proc{PID: 90, Start: 9, TTY: "/dev/ttys009"}
		f.err = want
		in := seed(t, env, itermObs(90, 9, "/dev/ttys009", pane1), "s", "UserPromptSubmit")
		if code := env.FocusCommand(context.Background(), in.InstanceID); code != ExitError {
			t.Fatalf("%v: exit %d", want, code)
		}
		if !strings.Contains(errb.String(), want.Error()) {
			t.Fatalf("stderr %q does not explain %v", errb.String(), want)
		}
	}
}

func TestSetupDryRunCreatesNothing(t *testing.T) {
	env, _, _, out, _ := testEnv(t)
	if code := env.Setup(SetupOptions{DryRun: true}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if _, err := filepathStat(env.SettingsFile); err == nil {
		t.Fatal("dry run created the settings file")
	}
	if _, err := filepathStat(env.StateDir); err == nil {
		t.Fatal("dry run created the state directory")
	}
	if !strings.Contains(out.String(), env.SettingsFile) || !strings.Contains(out.String(), "Dry run") {
		t.Fatalf("output %q", out.String())
	}
}

func TestSetupMalformedSettingsUnchanged(t *testing.T) {
	env, _, _, _, errb := testEnv(t)
	bad := []byte(`{"hooks": {"Stop": [`)
	writeFile(t, env.SettingsFile, bad)
	if code := env.Setup(SetupOptions{}); code != ExitError {
		t.Fatalf("exit %d", code)
	}
	if got := readFile(t, env.SettingsFile); !bytes.Equal(got, bad) {
		t.Fatal("malformed settings changed")
	}
	if !strings.Contains(errb.String(), "not valid JSON") {
		t.Fatalf("stderr %q", errb.String())
	}
}

func TestShellQuote(t *testing.T) {
	got := ShellQuote([]string{"/Users/o'neil/My Tools/cwatch", "hook", "--state-dir", "/a b"})
	want := `'/Users/o'\''neil/My Tools/cwatch' hook --state-dir '/a b'`
	if got != want {
		t.Fatalf("got %s", got)
	}
}

func TestHookArgs(t *testing.T) {
	o := ParseHookArgs([]string{"--managed-by=cwatch", "--state-dir", "/x y", "--no-excerpts", "--future-flag"})
	if o.StateDir != "/x y" || !o.NoExcerpts {
		t.Fatalf("%+v", o)
	}
}

func TestRunHookNeverFails(t *testing.T) {
	var errb bytes.Buffer
	dir := t.TempDir()
	// Malformed input, then an unwritable state directory.
	if code := RunHook([]string{"--state-dir", dir}, strings.NewReader("{not json"), &errb, func(string) string { return "" }, &procs{m: map[int]process.Proc{}}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if code := RunHook([]string{"--state-dir", "/dev/null/x"}, strings.NewReader(`{"session_id":"s","hook_event_name":"Stop"}`), &errb, func(string) string { return "" }, &procs{m: map[int]process.Proc{}}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(errb.String(), "cwatch hook:") {
		t.Fatalf("no diagnostics: %q", errb.String())
	}
}

func TestTokensFormat(t *testing.T) {
	cases := map[int64]string{0: "0", 950: "950", 1000: "1k", 12345: "12.3k", 99_949: "99.9k", 340_652: "341k", 1_000_000: "1M", 1_260_000: "1.3M", 27_936_894: "27.9M"}
	for n, want := range cases {
		if got := Tokens(n); got != want {
			t.Errorf("Tokens(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestLoadAddsTokenUsage(t *testing.T) {
	env, p, _, _, _ := testEnv(t)
	p.m[50] = process.Proc{PID: 50, Start: 5, TTY: "/dev/ttys005"}
	tp := filepath.Join(t.TempDir(), "t.jsonl")
	writeFile(t, tp, []byte(`{"type":"assistant","message":{"id":"m1","model":"claude-opus-5-5","usage":{"input_tokens":3,"cache_creation_input_tokens":400,"cache_read_input_tokens":340000,"output_tokens":900}}}`+"\n"))
	st, _ := state.Open(context.Background(), env.StateDir)
	ev, _ := hooks.Parse([]byte(`{"session_id":"s","hook_event_name":"UserPromptSubmit","cwd":"/w","transcript_path":"`+tp+`"}`), false)
	st.Record(context.Background(), itermObs(50, 5, "/dev/ttys005", pane1), ev, true, now)
	st.Close()
	snap := env.Load(context.Background(), LoadOptions{})
	if len(snap.Instances) != 1 || snap.Instances[0].Tokens == nil || snap.Instances[0].Tokens.Context != 340403 {
		t.Fatalf("tokens %+v", snap.Instances[0].Tokens)
	}
	if got := ContextSize(snap.Instances[0]); got != "340k" {
		t.Fatalf("context %q", got)
	}
	var b bytes.Buffer
	WriteTable(&b, snap.Instances, now, 120, true)
	if !strings.Contains(b.String(), "CONTEXT") || !strings.Contains(b.String(), "340k") {
		t.Fatalf("table:\n%s", b.String())
	}
	// The read-time value is never stored.
	st, _ = state.Open(context.Background(), env.StateDir)
	defer st.Close()
	stored, _ := st.Get(context.Background(), snap.Instances[0].InstanceID)
	if stored.Tokens != nil {
		t.Fatal("token usage stored in the database")
	}
}
