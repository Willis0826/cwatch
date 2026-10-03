package app

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"

	"cwatch/internal/launchagent"
)

// MenubarLogFile is the log file of the menu bar agent in the state
// directory.
const MenubarLogFile = "menubar.log"

// agentPath returns the plist path of the menu bar agent. It returns false
// when the Env has no launchctl or no LaunchAgents directory, as in tests.
// Then cwatch does not touch launchd.
func (e *Env) agentPath() (string, bool) {
	if e.Launchctl == nil || e.LaunchAgentsDir == "" {
		return "", false
	}
	return launchagent.Path(e.LaunchAgentsDir), true
}

func (e *Env) agentConfig(exe string) launchagent.Config {
	return launchagent.Config{
		Exe:          exe,
		StateDir:     e.StateDir,
		SettingsFile: e.SettingsFile,
		LogFile:      filepath.Join(e.StateDir, MenubarLogFile),
	}
}

// setupMenubar writes the plist of the menu bar agent and loads it. It
// loads the agent again when the plist changed or the agent does not run.
func (e *Env) setupMenubar(ctx context.Context, exe string, dryRun bool) int {
	path, ok := e.agentPath()
	if !ok {
		return ExitOK
	}
	fmt.Fprintln(e.Stdout, "\nMenu bar agent:", path)
	if !e.MenubarSupported {
		fmt.Fprintln(e.Stdout, "  This build of cwatch has no menu bar. cwatch skipped the menu bar agent.")
		return ExitOK
	}
	want := e.agentConfig(exe).Plist()
	have, err := launchagent.Read(path)
	if err != nil {
		fmt.Fprintln(e.Stderr, "cwatch: setup: menu bar:", err)
		return ExitError
	}
	changed := !bytes.Equal(have, want)
	switch {
	case have == nil:
		fmt.Fprintln(e.Stdout, "  install the agent; it starts the menu bar now and at each login")
	case changed:
		fmt.Fprintln(e.Stdout, "  update the agent")
	default:
		fmt.Fprintln(e.Stdout, "  the agent is already installed")
	}
	if dryRun {
		if changed {
			fmt.Fprintln(e.Stdout, "\nDry run. cwatch made no changes. The new agent content is:")
			fmt.Fprint(e.Stdout, string(want))
		}
		return ExitOK
	}
	lc := e.Launchctl
	if !changed && launchagent.Running(ctx, lc) {
		return ExitOK
	}
	if err := os.MkdirAll(e.StateDir, 0o700); err != nil {
		fmt.Fprintln(e.Stderr, "cwatch: setup: menu bar:", err)
		return ExitError
	}
	if changed {
		if err := launchagent.Write(path, want); err != nil {
			fmt.Fprintln(e.Stderr, "cwatch: setup: menu bar:", err)
			return ExitError
		}
	}
	if err := launchagent.Load(ctx, lc, path); err != nil {
		fmt.Fprintln(e.Stderr, "cwatch: setup: menu bar:", err)
		return ExitError
	}
	fmt.Fprintln(e.Stdout, "Started the menu bar. It starts again at each login.")
	return ExitOK
}

// uninstallMenubar stops the menu bar agent and removes its plist.
func (e *Env) uninstallMenubar(ctx context.Context, dryRun bool) int {
	path, ok := e.agentPath()
	if !ok {
		return ExitOK
	}
	have, err := launchagent.Read(path)
	if err != nil {
		fmt.Fprintln(e.Stderr, "cwatch: uninstall: menu bar:", err)
		return ExitError
	}
	if have == nil {
		return ExitOK
	}
	fmt.Fprintln(e.Stdout, "\nMenu bar agent:", path)
	if dryRun {
		fmt.Fprintln(e.Stdout, "  Dry run. cwatch would stop the menu bar and remove the agent.")
		return ExitOK
	}
	launchagent.Unload(ctx, e.Launchctl)
	if err := os.Remove(path); err != nil {
		fmt.Fprintln(e.Stderr, "cwatch: uninstall: menu bar:", err)
		return ExitError
	}
	fmt.Fprintln(e.Stdout, "Stopped the menu bar and removed the agent.")
	return ExitOK
}

// restartMenubar starts the menu bar again after an upgrade, so that it
// runs the new binary. It does nothing when the agent is not installed.
func (e *Env) restartMenubar(ctx context.Context) {
	path, ok := e.agentPath()
	if !ok {
		return
	}
	if have, err := launchagent.Read(path); err != nil || have == nil {
		return
	}
	if err := launchagent.Restart(ctx, e.Launchctl); err != nil {
		fmt.Fprintln(e.Stderr, "cwatch: cannot restart the menu bar:", err)
		return
	}
	fmt.Fprintln(e.Stdout, "Restarted the menu bar with the new version.")
}

// doctorMenubar checks the menu bar agent.
func (e *Env) doctorMenubar(ctx context.Context, exe string, add func(level, name, format string, args ...any)) {
	path, ok := e.agentPath()
	if !ok {
		return
	}
	have, err := launchagent.Read(path)
	switch {
	case err != nil:
		add("FAIL", "menu bar", "%v", err)
	case have == nil && !e.MenubarSupported:
		add("INFO", "menu bar", "this build of cwatch has no menu bar")
	case have == nil:
		add("INFO", "menu bar", "not installed; run \"cwatch setup\" to add it")
	case exe != "" && !bytes.Equal(have, e.agentConfig(exe).Plist()):
		add("WARN", "menu bar", "%s does not match this cwatch; run \"cwatch setup\"", path)
	case !launchagent.Running(ctx, e.Launchctl):
		add("WARN", "menu bar", "installed but not running; run \"cwatch setup\", or see %s", filepath.Join(e.StateDir, MenubarLogFile))
	default:
		add("OK", "menu bar", "running")
	}
}
