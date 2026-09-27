package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"cwatch/internal/hooks"
	"cwatch/internal/process"
	"cwatch/internal/state"
	"cwatch/internal/terminal"
)

// Hook limits.
const (
	MaxHookInput  = 4 << 20 // bytes read from stdin
	HookDeadline  = 3 * time.Second
	HookLogFile   = "hook-errors.log"
	MaxHookLogLen = 256 << 10
)

// HookOptions are the arguments of the hook command.
type HookOptions struct {
	StateDir   string
	NoExcerpts bool
}

// ParseHookArgs parses the hook arguments. It ignores unknown arguments so
// that a newer settings entry does not break an older binary.
func ParseHookArgs(args []string) HookOptions {
	var o HookOptions
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--state-dir" && i+1 < len(args):
			o.StateDir = args[i+1]
			i++
		case len(a) > len("--state-dir=") && a[:len("--state-dir=")] == "--state-dir=":
			o.StateDir = a[len("--state-dir="):]
		case a == "--no-excerpts":
			o.NoExcerpts = true
		}
	}
	return o
}

// RunHook consumes one hook event from stdin and records it. It always
// returns 0 and never writes to stdout, so it never blocks Claude Code, adds
// context, or makes a permission decision. It writes diagnostics to stderr
// (the Claude Code debug log) and to a bounded local log file.
func RunHook(args []string, stdin io.Reader, stderr io.Writer, getenv func(string) string, insp process.Inspector) int {
	// A summary starts Claude Code with this variable. That process is not
	// a session of the user.
	if getenv(DisableEnv) != "" {
		return 0
	}
	syscall.Umask(0o077)
	opts := ParseHookArgs(args)
	if opts.StateDir == "" {
		dir, err := DefaultStateDir(getenv)
		if err != nil {
			fmt.Fprintf(stderr, "cwatch hook: %v\n", err)
			return 0
		}
		opts.StateDir = dir
	}
	done := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- fmt.Errorf("panic: %v", r)
			}
		}()
		done <- recordHook(opts, stdin, getenv, insp)
	}()
	var err error
	select {
	case err = <-done:
	case <-time.After(HookDeadline):
		err = fmt.Errorf("hook exceeded %s", HookDeadline)
	}
	if err != nil {
		fmt.Fprintf(stderr, "cwatch hook: %v\n", err)
		logHookError(opts.StateDir, err)
	}
	return 0
}

func recordHook(opts HookOptions, stdin io.Reader, getenv func(string) string, insp process.Inspector) error {
	data, truncated, err := hooks.ReadLimited(stdin, MaxHookInput)
	if err != nil {
		return fmt.Errorf("read input: %w", err)
	}
	ev, err := hooks.Parse(data, truncated)
	if err != nil {
		return fmt.Errorf("parse input: %w", err)
	}
	if ev.Kind == hooks.KindUnknown {
		if getenv("CWATCH_DEBUG") != "" {
			return fmt.Errorf("ignored unknown event %q", ev.HookEventName)
		}
		return nil
	}
	hint, _ := strconv.Atoi(getenv("CLAUDE_PID"))
	owner := process.FindOwner(insp, os.Getpid(), hint)
	term := terminal.Detect(getenv)
	obs := state.Observation{
		OwnerPID:        owner.PID,
		OwnerStart:      owner.Start,
		OwnerMethod:     owner.Method,
		TTY:             owner.TTY,
		TerminalKind:    term.Kind,
		TerminalProgram: term.Program,
		ITermSessionID:  term.ITermSessionID,
	}
	ctx, cancel := context.WithTimeout(context.Background(), HookDeadline)
	defer cancel()
	st, err := state.Open(ctx, opts.StateDir)
	if err != nil {
		return fmt.Errorf("open state: %w", err)
	}
	defer st.Close()
	if _, err := st.Record(ctx, obs, ev, !opts.NoExcerpts, time.Now()); err != nil {
		return fmt.Errorf("record %s: %w", ev.HookEventName, err)
	}
	return nil
}

func logHookError(dir string, err error) {
	if dir == "" {
		return
	}
	if mkErr := os.MkdirAll(dir, 0o700); mkErr != nil {
		return
	}
	path := filepath.Join(dir, HookLogFile)
	if fi, statErr := os.Stat(path); statErr == nil && fi.Size() > MaxHookLogLen {
		_ = os.Rename(path, path+".1")
	}
	f, openErr := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if openErr != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %v\n", time.Now().UTC().Format(time.RFC3339), err)
}
