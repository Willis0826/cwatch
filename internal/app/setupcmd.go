package app

import (
	"context"
	"fmt"
	"strings"

	"cwatch/internal/setup"
)

// SetupOptions are the options of the setup command.
type SetupOptions struct {
	DryRun     bool
	NoExcerpts bool
	Menubar    bool // also install the menu bar agent
}

func (e *Env) hookConfig(o SetupOptions) (setup.Config, error) {
	exe, err := e.Executable()
	if err != nil {
		return setup.Config{}, fmt.Errorf("find the cwatch executable: %w", err)
	}
	c := setup.Config{Exe: exe, StateDir: e.StateDir, NoExcerpts: o.NoExcerpts}
	return c, c.Validate()
}

// Setup installs the hook entries of this tool. With o.Menubar, it also
// installs the menu bar agent.
func (e *Env) Setup(o SetupOptions) int {
	c, err := e.hookConfig(o)
	if err != nil {
		fmt.Fprintln(e.Stderr, "cwatch: setup:", err)
		return ExitError
	}
	if code := e.setupHooks(c, o); code != ExitOK || !o.Menubar {
		return code
	}
	return e.setupMenubar(context.Background(), c.Exe, o.DryRun)
}

func (e *Env) setupHooks(c setup.Config, o SetupOptions) int {
	fmt.Fprintln(e.Stdout, "Settings file:", e.SettingsFile)
	fmt.Fprintln(e.Stdout, "Hook command: ", ShellQuote(append([]string{c.Exe}, c.Args()...)))
	data, info, err := setup.ReadSettings(e.SettingsFile)
	if err != nil {
		fmt.Fprintln(e.Stderr, "cwatch: setup: read settings:", err)
		return ExitError
	}
	out, changes, modified, err := setup.PlanInstall(data, c)
	if err != nil {
		fmt.Fprintf(e.Stderr, "cwatch: setup: %v\nThe settings file was not changed. Correct it, then run setup again.\n", err)
		return ExitError
	}
	for _, ch := range changes {
		fmt.Fprintf(e.Stdout, "  %-20s %s\n", ch.Event, ch.Action)
	}
	if e.SettingsFile != setup.ResolveTarget(e.SettingsFile) {
		fmt.Fprintln(e.Stdout, "The settings file is a symbolic link. cwatch edits its target:", setup.ResolveTarget(e.SettingsFile))
	}
	if o.DryRun {
		if modified {
			fmt.Fprintln(e.Stdout, "\nDry run. cwatch made no changes. The new settings content is:")
			fmt.Fprint(e.Stdout, string(out))
		} else {
			fmt.Fprintln(e.Stdout, "\nDry run. The hooks are already installed. cwatch made no changes.")
		}
		return ExitOK
	}
	if !modified {
		fmt.Fprintln(e.Stdout, "The hooks are already installed. cwatch made no changes.")
		return ExitOK
	}
	backup, err := setup.WriteSettings(e.SettingsFile, data, info, out, e.Now())
	if err != nil {
		fmt.Fprintln(e.Stderr, "cwatch: setup:", err)
		return ExitError
	}
	if backup != "" {
		fmt.Fprintln(e.Stdout, "Backup:", backup)
	}
	fmt.Fprintln(e.Stdout, "Installed. Restart running Claude Code sessions so that they load the hooks, then submit a prompt in each.")
	return ExitOK
}

// Uninstall removes the hook entries and the menu bar agent of this tool.
// It keeps the history.
func (e *Env) Uninstall(dryRun bool) int {
	code := e.uninstallHooks(dryRun)
	if c := e.uninstallMenubar(context.Background(), dryRun); c != ExitOK {
		code = c
	}
	return code
}

func (e *Env) uninstallHooks(dryRun bool) int {
	fmt.Fprintln(e.Stdout, "Settings file:", e.SettingsFile)
	data, info, err := setup.ReadSettings(e.SettingsFile)
	if err != nil {
		fmt.Fprintln(e.Stderr, "cwatch: uninstall: read settings:", err)
		return ExitError
	}
	if info == nil {
		fmt.Fprintln(e.Stdout, "The settings file does not exist. cwatch made no changes.")
		return ExitOK
	}
	out, changes, modified, err := setup.PlanUninstall(data)
	if err != nil {
		fmt.Fprintf(e.Stderr, "cwatch: uninstall: %v\nThe settings file was not changed.\n", err)
		return ExitError
	}
	for _, ch := range changes {
		fmt.Fprintf(e.Stdout, "  %-20s %s\n", ch.Event, ch.Action)
	}
	if !modified {
		fmt.Fprintln(e.Stdout, "No cwatch hooks are installed. cwatch made no changes.")
		return ExitOK
	}
	if dryRun {
		fmt.Fprintln(e.Stdout, "\nDry run. cwatch made no changes. The new settings content is:")
		fmt.Fprint(e.Stdout, string(out))
		return ExitOK
	}
	backup, err := setup.WriteSettings(e.SettingsFile, data, info, out, e.Now())
	if err != nil {
		fmt.Fprintln(e.Stderr, "cwatch: uninstall:", err)
		return ExitError
	}
	fmt.Fprintln(e.Stdout, "Backup:", backup)
	dirs := setup.StateDirs(data)
	if len(dirs) == 0 {
		dirs = []string{e.StateDir}
	}
	fmt.Fprintf(e.Stdout, "Removed the cwatch hooks. cwatch kept the history in %s. Delete it to remove the history.\n", strings.Join(dirs, ", "))
	return ExitOK
}

// ShellQuote quotes argv for a POSIX shell.
func ShellQuote(argv []string) string {
	out := ""
	for i, a := range argv {
		if i > 0 {
			out += " "
		}
		safe := a != ""
		for _, r := range a {
			if !(r == '/' || r == '-' || r == '_' || r == '.' || r == '=' || r == ':' ||
				(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
				safe = false
				break
			}
		}
		if safe {
			out += a
			continue
		}
		q := "'"
		for _, r := range a {
			if r == '\'' {
				q += `'\''`
			} else {
				q += string(r)
			}
		}
		out += q + "'"
	}
	return out
}
