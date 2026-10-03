package launchagent

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type fakeLC struct {
	calls []string
	fail  map[string]int // number of failures left for a subcommand
	out   map[string]string
}

func (f *fakeLC) Run(_ context.Context, args ...string) (string, error) {
	f.calls = append(f.calls, strings.Join(args, " "))
	if f.fail[args[0]] > 0 {
		f.fail[args[0]]--
		return "Bootstrap failed: 5: Input/output error", errors.New("exit status 5")
	}
	return f.out[args[0]], nil
}

func TestPlistIsValidAndEscaped(t *testing.T) {
	c := Config{Exe: "/opt/my <apps>/cwatch", StateDir: "/Users/a/.cwatch", SettingsFile: "/Users/a/.claude/settings.json", LogFile: "/Users/a/.cwatch/menubar.log"}
	p := filepath.Join(t.TempDir(), "a.plist")
	if err := Write(p, c.Plist()); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o644 {
		t.Fatalf("mode %o", fi.Mode().Perm())
	}
	if !strings.Contains(string(c.Plist()), "<string>/opt/my &lt;apps&gt;/cwatch</string>") {
		t.Fatalf("plist does not escape the path:\n%s", c.Plist())
	}
	if _, err := exec.LookPath("plutil"); err != nil {
		t.Skip("plutil is not available")
	}
	if out, err := exec.Command("plutil", "-lint", p).CombinedOutput(); err != nil {
		t.Fatalf("plutil: %v %s", err, out)
	}
	out, err := exec.Command("plutil", "-extract", "ProgramArguments.1", "raw", p).Output()
	if err != nil || strings.TrimSpace(string(out)) != "menubar" {
		t.Fatalf("ProgramArguments.1 = %q, %v", out, err)
	}
}

func TestLoadRetriesBootstrap(t *testing.T) {
	lc := &fakeLC{fail: map[string]int{"bootstrap": 2}}
	if err := Load(context.Background(), lc, "/x.plist"); err != nil {
		t.Fatal(err)
	}
	if len(lc.calls) != 4 || !strings.HasPrefix(lc.calls[0], "bootout gui/") || !strings.HasSuffix(lc.calls[0], "/"+Label) {
		t.Fatalf("calls %q", lc.calls)
	}
	lc = &fakeLC{fail: map[string]int{"bootstrap": 100}}
	if err := Load(context.Background(), lc, "/x.plist"); err == nil || !strings.Contains(err.Error(), "Input/output error") {
		t.Fatalf("err %v", err)
	}
}

func TestRunning(t *testing.T) {
	lc := &fakeLC{out: map[string]string{"print": "gui/501/x = {\n\tstate = running\n}"}}
	if !Running(context.Background(), lc) {
		t.Fatal("not running")
	}
	lc.out["print"] = "state = not running"
	if Running(context.Background(), lc) {
		t.Fatal("running")
	}
}
