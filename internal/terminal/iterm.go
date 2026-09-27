package terminal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Focus errors.
var (
	ErrNotRunning       = errors.New("iTerm2 is not running")
	ErrAutomationDenied = errors.New("macOS denied Automation access to iTerm2; allow it in System Settings > Privacy & Security > Automation")
	ErrPaneNotFound     = errors.New("no iTerm2 pane matches the recorded terminal")
	ErrAmbiguousPane    = errors.New("more than one iTerm2 pane matches the recorded terminal")
	ErrTTYMismatch      = errors.New("the iTerm2 pane now has a different terminal device")
	ErrTimeout          = errors.New("iTerm2 did not answer in time")
)

// Runner runs a fixed AppleScript with arguments. Tests use a fake runner.
type Runner interface {
	Run(ctx context.Context, script string, args ...string) (stdout, stderr string, err error)
}

// OSAScript runs scripts with /usr/bin/osascript. It passes each script
// line with -e and passes values only as arguments, never inside the script.
type OSAScript struct{}

// Run runs script with args.
func (OSAScript) Run(ctx context.Context, script string, args ...string) (string, string, error) {
	var argv []string
	for _, line := range strings.Split(strings.TrimSpace(script), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		argv = append(argv, "-e", line)
	}
	argv = append(argv, args...)
	cmd := exec.CommandContext(ctx, "/usr/bin/osascript", argv...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	return out.String(), errb.String(), err
}

// Pane is one iTerm2 session (a tab or a split pane).
type Pane struct {
	WindowID  string
	TabIndex  int
	SessionID string
	TTY       string
}

// The scripts refer to iTerm2 by bundle identifier. They check that iTerm2
// runs before they address it, so they never launch it.
const enumerateScript = `
on run argv
if application id "com.googlecode.iterm2" is not running then return "NOT_RUNNING"
set fs to character id 31
set rs to character id 30
set out to ""
tell application id "com.googlecode.iterm2"
repeat with w in windows
set wid to (id of w) as text
set ti to 0
repeat with t in tabs of w
set ti to ti + 1
repeat with s in sessions of t
set out to out & wid & fs & ti & fs & (id of s) & fs & (tty of s) & rs
end repeat
end repeat
end repeat
end tell
return "OK" & rs & out
end run
`

const selectScript = `
on run argv
set targetID to item 1 of argv
set targetTTY to item 2 of argv
if application id "com.googlecode.iterm2" is not running then return "NOT_RUNNING"
tell application id "com.googlecode.iterm2"
repeat with w in windows
repeat with t in tabs of w
repeat with s in sessions of t
if (id of s) is targetID then
if (tty of s) is not targetTTY then return "TTY_MISMATCH"
tell s to select
tell t to select
tell w to select
activate
return "OK"
end if
end repeat
end repeat
end repeat
end tell
return "NOT_FOUND"
end run
`

// ITerm is the iTerm2 adapter.
type ITerm struct {
	Runner  Runner
	Timeout time.Duration
}

// NewITerm returns an adapter that uses osascript.
func NewITerm() ITerm { return ITerm{Runner: OSAScript{}, Timeout: 5 * time.Second} }

func (t ITerm) run(ctx context.Context, script string, args ...string) (string, error) {
	timeout := t.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, stderr, err := t.Runner.Run(ctx, script, args...)
	if ctx.Err() == context.DeadlineExceeded {
		return "", ErrTimeout
	}
	if err != nil {
		return "", classify(stderr, err)
	}
	return strings.TrimRight(out, "\r\n"), nil
}

func classify(stderr string, err error) error {
	switch {
	case strings.Contains(stderr, "-1743") || strings.Contains(stderr, "Not authorized"):
		return ErrAutomationDenied
	case strings.Contains(stderr, "-600") || strings.Contains(stderr, "isn’t running") || strings.Contains(stderr, "isn't running"):
		return ErrNotRunning
	case strings.Contains(stderr, "-1712"):
		return ErrTimeout
	}
	msg := strings.TrimSpace(stderr)
	if len(msg) > 300 {
		msg = msg[:300]
	}
	return fmt.Errorf("osascript failed: %v: %s", err, msg)
}

// Panes lists all iTerm2 panes.
func (t ITerm) Panes(ctx context.Context) ([]Pane, error) {
	out, err := t.run(ctx, enumerateScript)
	if err != nil {
		return nil, err
	}
	return parsePanes(out)
}

func parsePanes(out string) ([]Pane, error) {
	if out == "NOT_RUNNING" {
		return nil, ErrNotRunning
	}
	records := strings.Split(out, "\x1e")
	if len(records) == 0 || records[0] != "OK" {
		return nil, fmt.Errorf("unexpected iTerm2 answer")
	}
	var panes []Pane
	for _, r := range records[1:] {
		if r == "" {
			continue
		}
		f := strings.Split(r, "\x1f")
		if len(f) != 4 {
			continue
		}
		ti, _ := strconv.Atoi(f[1])
		panes = append(panes, Pane{WindowID: f[0], TabIndex: ti, SessionID: strings.ToUpper(f[2]), TTY: f[3]})
	}
	return panes, nil
}

// Match selects the pane for a recorded iTerm2 session ID and TTY. When the
// session ID is known, the pane must have that ID and the same TTY. When
// only the TTY is known, exactly one pane must have it.
func Match(panes []Pane, sessionID, tty string) (Pane, error) {
	sessionID = strings.ToUpper(sessionID)
	if sessionID != "" {
		for _, p := range panes {
			if p.SessionID == sessionID {
				if tty != "" && p.TTY != tty {
					return Pane{}, ErrTTYMismatch
				}
				return p, nil
			}
		}
		return Pane{}, ErrPaneNotFound
	}
	if tty == "" {
		return Pane{}, ErrPaneNotFound
	}
	var found []Pane
	for _, p := range panes {
		if p.TTY == tty {
			found = append(found, p)
		}
	}
	switch len(found) {
	case 0:
		return Pane{}, ErrPaneNotFound
	case 1:
		return found[0], nil
	default:
		return Pane{}, ErrAmbiguousPane
	}
}

// Focus finds and selects the pane, then activates iTerm2.
func (t ITerm) Focus(ctx context.Context, sessionID, tty string) (Pane, error) {
	if !ValidTTY(tty) {
		return Pane{}, fmt.Errorf("invalid terminal device %q", tty)
	}
	panes, err := t.Panes(ctx)
	if err != nil {
		return Pane{}, err
	}
	p, err := Match(panes, sessionID, tty)
	if err != nil {
		return Pane{}, err
	}
	out, err := t.run(ctx, selectScript, p.SessionID, p.TTY)
	if err != nil {
		return Pane{}, err
	}
	switch out {
	case "OK":
		return p, nil
	case "NOT_RUNNING":
		return Pane{}, ErrNotRunning
	case "TTY_MISMATCH":
		return Pane{}, ErrTTYMismatch
	case "NOT_FOUND":
		return Pane{}, ErrPaneNotFound
	default:
		return Pane{}, fmt.Errorf("unexpected iTerm2 answer")
	}
}
