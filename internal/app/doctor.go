package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"cwatch/internal/hooks"
	"cwatch/internal/process"
	"cwatch/internal/setup"
	"cwatch/internal/state"
)

// TestedClaudeVersion is the Claude Code version that this build was
// verified against.
const TestedClaudeVersion = "2.1.283"

type check struct {
	level string // OK, WARN, FAIL, INFO
	name  string
	msg   string
}

// Doctor checks the configuration, the executable, the platform, and the
// tracking state. It changes nothing.
func (e *Env) Doctor(ctx context.Context) int {
	var checks []check
	add := func(level, name, format string, args ...any) {
		checks = append(checks, check{level, name, fmt.Sprintf(format, args...)})
	}

	// Platform.
	if runtime.GOOS == "darwin" {
		add("OK", "platform", "macOS %s", runtime.GOARCH)
	} else {
		add("FAIL", "platform", "%s/%s is not supported; cwatch supports macOS", runtime.GOOS, runtime.GOARCH)
	}

	// Executable.
	exe, err := e.Executable()
	switch {
	case err != nil:
		add("FAIL", "executable", "%v", err)
	case setup.IsTemporaryBuild(exe):
		add("WARN", "executable", "%s is a temporary build; install cwatch before you run setup", exe)
	default:
		add("OK", "executable", "%s", exe)
	}

	// Settings and hooks.
	add("INFO", "settings", "%s", e.SettingsFile)
	data, info, err := setup.ReadSettings(e.SettingsFile)
	switch {
	case err != nil:
		add("FAIL", "settings", "cannot read: %v", err)
	case info == nil:
		add("WARN", "hooks", "the settings file does not exist; run \"cwatch setup\"")
	default:
		inst, err := setup.Installed(data)
		if err != nil {
			add("FAIL", "settings", "%v", err)
			break
		}
		var missing []string
		for _, ev := range hooks.Events {
			if len(inst[ev]) == 0 {
				missing = append(missing, ev)
			}
		}
		switch {
		case len(inst) == 0:
			add("WARN", "hooks", "not installed; run \"cwatch setup --dry-run\", then \"cwatch setup\"")
		case len(missing) > 0:
			add("WARN", "hooks", "missing for %s; run \"cwatch setup\"", strings.Join(missing, ", "))
		default:
			add("OK", "hooks", "installed for %d events", len(hooks.Events))
		}
		paths := map[string]bool{}
		for _, list := range inst {
			for _, p := range list {
				paths[p] = true
			}
		}
		var sorted []string
		for p := range paths {
			sorted = append(sorted, p)
		}
		sort.Strings(sorted)
		for _, p := range sorted {
			fi, err := os.Stat(p)
			switch {
			case err != nil:
				add("FAIL", "hook executable", "%s does not exist; run \"cwatch setup\" again", p)
			case fi.Mode()&0o111 == 0:
				add("FAIL", "hook executable", "%s is not executable", p)
			case exe != "" && p != exe:
				add("WARN", "hook executable", "hooks use %s, but this command is %s", p, exe)
			default:
				add("OK", "hook executable", "%s", p)
			}
		}
	}

	// State.
	add("INFO", "state directory", "%s", e.StateDir)
	if fi, err := os.Stat(e.StateDir); err == nil {
		if fi.Mode().Perm()&0o077 != 0 {
			add("WARN", "state directory", "mode %o allows other users; run: chmod 700 %s", fi.Mode().Perm(), e.StateDir)
		}
		st, err := state.OpenExisting(ctx, e.StateDir)
		switch {
		case errors.Is(err, os.ErrNotExist):
			add("INFO", "tracking", "no events recorded yet")
		case err != nil:
			add("FAIL", "state", "%v", err)
		default:
			stats, err := st.Stats(ctx)
			st.Close()
			if err != nil {
				add("FAIL", "state", "%v", err)
				break
			}
			if dbi, err := os.Stat(filepath.Join(e.StateDir, state.DBFile)); err == nil && dbi.Mode().Perm()&0o077 != 0 {
				add("WARN", "state", "database mode %o allows other users", dbi.Mode().Perm())
			}
			last := "never"
			if !stats.LastEventAt.IsZero() {
				last = Age(e.Now(), stats.LastEventAt) + " ago"
			}
			add("OK", "tracking", "%d instances, %d events, last event %s", stats.Instances, stats.Events, last)
		}
	} else {
		add("INFO", "tracking", "no state directory yet; hooks create it on the first event")
	}
	if fi, err := os.Stat(filepath.Join(e.StateDir, HookLogFile)); err == nil && fi.Size() > 0 {
		add("WARN", "hook errors", "see %s", filepath.Join(e.StateDir, HookLogFile))
	}

	// Terminal.
	itermApp := ""
	for _, p := range []string{"/Applications/iTerm.app", filepath.Join(e.Getenv("HOME"), "Applications", "iTerm.app")} {
		if _, err := os.Stat(p); err == nil {
			itermApp = p
			break
		}
	}
	if itermApp == "" {
		add("WARN", "iTerm2", "not found in /Applications or ~/Applications; focus needs iTerm2")
	} else {
		running, err := process.Running("iTerm2")
		switch {
		case err != nil:
			add("WARN", "iTerm2", "%s (cannot check whether it runs: %v)", itermApp, err)
		case running:
			add("OK", "iTerm2", "%s, running", itermApp)
		default:
			add("INFO", "iTerm2", "%s, not running", itermApp)
		}
	}
	add("INFO", "automation", "focus needs macOS Automation access to iTerm2; the first \"cwatch focus\" asks for it")
	if e.Getenv("TMUX") != "" {
		add("WARN", "terminal", "this shell runs inside tmux; focus of tmux panes is not supported")
	}

	// Menu bar.
	e.doctorMenubar(ctx, exe, add)

	// Claude Code.
	if v, err := claudeVersion(ctx); err != nil {
		add("WARN", "claude code", "cannot read the version: %v", err)
	} else if compareVersion(v, "2.1.198") < 0 {
		add("WARN", "claude code", "version %s is older than the versions that cwatch was checked against (%s); exec-form hooks may not work", v, TestedClaudeVersion)
	} else {
		add("OK", "claude code", "version %s (cwatch was checked with %s)", v, TestedClaudeVersion)
	}

	failed := false
	for _, c := range checks {
		fmt.Fprintf(e.Stdout, "%-5s %-16s %s\n", c.level, c.name, c.msg)
		if c.level == "FAIL" {
			failed = true
		}
	}
	if failed {
		return ExitError
	}
	return ExitOK
}

var versionRE = regexp.MustCompile(`\d+\.\d+\.\d+`)

func claudeVersion(ctx context.Context) (string, error) {
	path, err := exec.LookPath("claude")
	if err != nil {
		return "", errors.New("claude is not on PATH")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, path, "--version")
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", err
	}
	v := versionRE.FindString(out.String())
	if v == "" {
		return "", errors.New("unexpected version output")
	}
	return v, nil
}

func compareVersion(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < 3; i++ {
		x, _ := strconv.Atoi(pa[i])
		y, _ := strconv.Atoi(pb[i])
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}
