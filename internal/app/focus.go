package app

import (
	"context"
	"errors"
	"fmt"
	"os"

	"cwatch/internal/process"
	"cwatch/internal/state"
	"cwatch/internal/terminal"
)

// RefusedError means that focus refused an unsafe or unsupported target.
type RefusedError struct{ Msg string }

func (e *RefusedError) Error() string { return e.Msg }

func refuse(format string, args ...any) error {
	return &RefusedError{Msg: fmt.Sprintf(format, args...)}
}

// Focus revalidates an instance and selects its iTerm2 pane. It refuses
// ended, unverified, remote, and unsupported targets. It never selects a
// pane from the project path or the window title.
func (e *Env) Focus(ctx context.Context, id string) (terminal.Pane, state.Instance, error) {
	st, err := state.OpenExisting(ctx, e.StateDir)
	if errors.Is(err, os.ErrNotExist) {
		return terminal.Pane{}, state.Instance{}, refuse("no state exists in %s; no instance can match", e.StateDir)
	}
	if err != nil {
		return terminal.Pane{}, state.Instance{}, err
	}
	defer st.Close()
	in, err := st.Get(ctx, id)
	if errors.Is(err, state.ErrNotFound) {
		return terminal.Pane{}, in, refuse("no instance matches %q", id)
	}
	if errors.Is(err, state.ErrAmbiguous) {
		return terminal.Pane{}, in, refuse("%q matches more than one instance; give more characters", id)
	}
	if err != nil {
		return terminal.Pane{}, in, err
	}
	if in.State == state.Ended {
		return terminal.Pane{}, in, refuse("instance %s ended (%s); cwatch does not focus ended instances", in.ShortID(), in.Reason)
	}
	switch in.TerminalKind {
	case state.TerminalITerm:
	case state.TerminalTmux:
		return terminal.Pane{}, in, refuse("instance %s runs inside tmux; focus inside tmux is not supported", in.ShortID())
	case state.TerminalSSH:
		return terminal.Pane{}, in, refuse("instance %s runs in an SSH session; focus of remote sessions is not supported", in.ShortID())
	default:
		return terminal.Pane{}, in, refuse("instance %s does not run in a recognised iTerm2 pane (terminal: %s)", in.ShortID(), in.TerminalKind)
	}
	if in.OwnerPID <= 0 || in.OwnerStart <= 0 {
		return terminal.Pane{}, in, refuse("the owning process of instance %s is unknown, so cwatch cannot verify its pane", in.ShortID())
	}
	live, proc := process.Check(e.Inspector, in.OwnerPID, in.OwnerStart)
	switch live {
	case process.Dead:
		_, _ = st.MarkProcessExit(ctx, in, e.Now())
		return terminal.Pane{}, in, refuse("the Claude process of instance %s (PID %d) no longer runs", in.ShortID(), in.OwnerPID)
	case process.Unknown:
		return terminal.Pane{}, in, refuse("cwatch cannot check the process of instance %s", in.ShortID())
	}
	if in.TTY == "" || !terminal.ValidTTY(in.TTY) {
		return terminal.Pane{}, in, refuse("instance %s has no recorded terminal device", in.ShortID())
	}
	if proc.TTY != in.TTY {
		return terminal.Pane{}, in, refuse("the process of instance %s now has terminal %q, not %q", in.ShortID(), proc.TTY, in.TTY)
	}
	pane, err := e.Focuser.Focus(ctx, in.ITermSessionID, in.TTY)
	if err != nil {
		return pane, in, err
	}
	return pane, in, nil
}

// FocusCommand runs "cwatch focus <id>".
func (e *Env) FocusCommand(ctx context.Context, id string) int {
	pane, in, err := e.Focus(ctx, id)
	var ref *RefusedError
	switch {
	case errors.As(err, &ref):
		fmt.Fprintln(e.Stderr, "cwatch: focus refused:", ref.Msg)
		return ExitRefused
	case err != nil:
		fmt.Fprintln(e.Stderr, "cwatch: focus failed:", err)
		return ExitError
	}
	fmt.Fprintf(e.Stdout, "Focused %s (%s) in iTerm2 window %s, tab %d.\n", in.ShortID(), ShortTTY(pane.TTY), pane.WindowID, pane.TabIndex)
	return ExitOK
}
