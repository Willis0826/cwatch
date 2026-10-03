// Package app implements the cwatch commands. The command functions take
// their dependencies as values, so tests can replace the process, terminal,
// and clock adapters.
package app

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"cwatch/internal/launchagent"
	"cwatch/internal/process"
	"cwatch/internal/summary"
	"cwatch/internal/terminal"
	"cwatch/internal/transcript"
)

// Exit codes of user-facing commands.
const (
	ExitOK      = 0
	ExitError   = 1
	ExitUsage   = 2
	ExitRefused = 3 // focus refused because the target is not safe
)

// Focuser selects a terminal pane.
type Focuser interface {
	Focus(ctx context.Context, sessionID, tty string) (terminal.Pane, error)
}

// Env holds the dependencies of the commands.
type Env struct {
	StateDir     string
	SettingsFile string
	Stdout       io.Writer
	Stderr       io.Writer
	Inspector    process.Inspector
	Focuser      Focuser
	Now          func() time.Time
	Getenv       func(string) string
	Executable   func() (string, error)
	// Usage reads token usage from transcripts. The dashboard keeps one
	// tracker, so each refresh reads only new transcript lines.
	Usage *transcript.UsageTracker
	// Summarizer makes the summary text. Nil means "claude -p".
	Summarizer Summarizer
	// Commits lists the commits of the user for a summary. Nil means none.
	Commits summary.CommitFunc
	// Releases finds and downloads releases for an upgrade. Nil means
	// GitHub through curl.
	Releases Releases
	// Launchctl manages the menu bar agent. Nil means that cwatch does not
	// touch launchd, as in tests.
	Launchctl launchagent.Launchctl
	// LaunchAgentsDir holds the plist of the menu bar agent. Resolve sets
	// ~/Library/LaunchAgents when Launchctl is set.
	LaunchAgentsDir string
	// MenubarSupported tells whether this build can show the menu bar.
	MenubarSupported bool
}

// DefaultEnv returns an Env for the real system.
func DefaultEnv() *Env {
	return &Env{
		Stdout:     os.Stdout,
		Stderr:     os.Stderr,
		Inspector:  process.System{},
		Focuser:    terminal.NewITerm(),
		Now:        time.Now,
		Getenv:     os.Getenv,
		Executable: ResolvedExecutable,
		Usage:      transcript.NewUsageTracker(),
		Commits:    summary.GitCommits,
		Launchctl:  launchagent.System{},
	}
}

// ResolvedExecutable returns the absolute path of the running executable
// with symbolic links resolved.
func ResolvedExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return filepath.Abs(exe)
}

// DefaultStateDir returns ~/.cwatch.
func DefaultStateDir(getenv func(string) string) (string, error) {
	home := getenv("HOME")
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		home = h
	}
	if home == "" {
		return "", errors.New("cannot find the home directory")
	}
	return filepath.Join(home, ".cwatch"), nil
}

// DefaultSettingsFile returns the user settings file of Claude Code. It
// honours CLAUDE_CONFIG_DIR.
func DefaultSettingsFile(getenv func(string) string) (string, error) {
	if dir := getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "settings.json"), nil
	}
	home := getenv("HOME")
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		home = h
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}

// Resolve fills in default paths and makes them absolute.
func (e *Env) Resolve() error {
	var err error
	if e.StateDir == "" {
		if e.StateDir, err = DefaultStateDir(e.Getenv); err != nil {
			return err
		}
	}
	if e.SettingsFile == "" {
		if e.SettingsFile, err = DefaultSettingsFile(e.Getenv); err != nil {
			return err
		}
	}
	if e.LaunchAgentsDir == "" && e.Launchctl != nil {
		home := e.Getenv("HOME")
		if home == "" {
			if home, err = os.UserHomeDir(); err != nil {
				return err
			}
		}
		e.LaunchAgentsDir = launchagent.DefaultDir(home)
	}
	if e.StateDir, err = filepath.Abs(e.StateDir); err != nil {
		return err
	}
	e.SettingsFile, err = filepath.Abs(e.SettingsFile)
	return err
}
