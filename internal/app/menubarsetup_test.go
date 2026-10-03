package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cwatch/internal/launchagent"
)

type fakeLaunchctl struct {
	calls   []string
	running bool
}

func (f *fakeLaunchctl) Run(_ context.Context, args ...string) (string, error) {
	f.calls = append(f.calls, args[0])
	switch args[0] {
	case "bootstrap", "kickstart":
		f.running = true
	case "bootout":
		f.running = false
	case "print":
		if f.running {
			return "state = running", nil
		}
		return "", os.ErrNotExist
	}
	return "", nil
}

func menubarEnv(t *testing.T) (*Env, *fakeLaunchctl, string) {
	env, _, _, _, _ := testEnv(t)
	lc := &fakeLaunchctl{}
	env.Launchctl = lc
	env.LaunchAgentsDir = filepath.Join(t.TempDir(), "LaunchAgents")
	env.MenubarSupported = true
	return env, lc, launchagent.Path(env.LaunchAgentsDir)
}

func TestSetupMenubarDryRunChangesNothing(t *testing.T) {
	env, lc, plist := menubarEnv(t)
	if code := env.Setup(SetupOptions{DryRun: true, Menubar: true}); code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	if _, err := os.Stat(plist); err == nil || len(lc.calls) != 0 {
		t.Fatalf("dry run wrote the plist or ran launchctl: %v", lc.calls)
	}
	if out := env.Stdout.(interface{ String() string }).String(); !strings.Contains(out, "<string>menubar</string>") {
		t.Fatalf("dry run does not show the plist: %s", out)
	}
}

func TestSetupMenubarInstallsOnceAndUninstallRemoves(t *testing.T) {
	env, lc, plist := menubarEnv(t)
	if code := env.Setup(SetupOptions{Menubar: true}); code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	b := readFile(t, plist)
	if !strings.Contains(string(b), "<string>/opt/cwatch/bin/cwatch</string>") || !strings.Contains(string(b), env.StateDir) {
		t.Fatalf("plist %s", b)
	}
	if strings.Join(lc.calls, ",") != "bootout,bootstrap" {
		t.Fatalf("calls %v", lc.calls)
	}
	// A second setup finds the agent installed and running.
	lc.calls = nil
	if code := env.Setup(SetupOptions{Menubar: true}); code != ExitOK || strings.Join(lc.calls, ",") != "print" {
		t.Fatalf("exit %d, calls %v", code, lc.calls)
	}
	// An upgrade restarts the agent.
	lc.calls = nil
	env.restartMenubar(context.Background())
	if strings.Join(lc.calls, ",") != "kickstart" {
		t.Fatalf("calls %v", lc.calls)
	}
	lc.calls = nil
	if code := env.Uninstall(false); code != ExitOK {
		t.Fatalf("uninstall exit %d", code)
	}
	if _, err := os.Stat(plist); !os.IsNotExist(err) || strings.Join(lc.calls, ",") != "bootout" {
		t.Fatalf("plist %v, calls %v", err, lc.calls)
	}
}

func TestSetupWithoutMenubarOrSupport(t *testing.T) {
	env, lc, plist := menubarEnv(t)
	if code := env.Setup(SetupOptions{}); code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	env.MenubarSupported = false
	if code := env.Setup(SetupOptions{Menubar: true}); code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	if _, err := os.Stat(plist); err == nil || len(lc.calls) != 0 {
		t.Fatalf("setup installed the agent: %v", lc.calls)
	}
}
