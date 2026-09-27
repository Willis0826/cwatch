package terminal

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestDetect(t *testing.T) {
	env := func(kv ...string) func(string) string {
		m := map[string]string{}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return func(k string) string { return m[k] }
	}
	id := "w0t1p0:3017789F-C400-468F-A7A8-5EBDC5461A48"
	cases := []struct {
		env  func(string) string
		kind string
	}{
		{env("ITERM_SESSION_ID", id, "TERM_PROGRAM", "iTerm.app"), KindITerm},
		{env("ITERM_SESSION_ID", id, "TMUX", "/tmp/tmux-501/default,1,0"), KindTmux},
		{env("ITERM_SESSION_ID", id, "SSH_CONNECTION", "1 2 3 4"), KindSSH},
		{env("ITERM_SESSION_ID", "garbage"), KindUnknown},
		{env("TERM_PROGRAM", "Apple_Terminal"), KindOther},
		{env(), KindUnknown},
	}
	for i, c := range cases {
		if got := Detect(c.env); got.Kind != c.kind {
			t.Errorf("case %d: kind %q, want %q", i, got.Kind, c.kind)
		}
	}
	if got := Detect(cases[0].env).ITermSessionID; got != "3017789F-C400-468F-A7A8-5EBDC5461A48" {
		t.Fatalf("session id %q", got)
	}
}

var panes = []Pane{
	{WindowID: "1", TabIndex: 1, SessionID: "AAAA0000-0000-0000-0000-000000000001", TTY: "/dev/ttys001"},
	{WindowID: "1", TabIndex: 1, SessionID: "AAAA0000-0000-0000-0000-000000000002", TTY: "/dev/ttys002"}, // split pane
	{WindowID: "2", TabIndex: 3, SessionID: "AAAA0000-0000-0000-0000-000000000003", TTY: "/dev/ttys003"},
	{WindowID: "2", TabIndex: 4, SessionID: "AAAA0000-0000-0000-0000-000000000004", TTY: "/dev/ttys009"},
	{WindowID: "3", TabIndex: 1, SessionID: "AAAA0000-0000-0000-0000-000000000005", TTY: "/dev/ttys009"},
}

// Acceptance test 12 (matching).
func TestMatch(t *testing.T) {
	p, err := Match(panes, "aaaa0000-0000-0000-0000-000000000002", "/dev/ttys002")
	if err != nil || p.WindowID != "1" || p.TTY != "/dev/ttys002" {
		t.Fatalf("by id: %+v %v", p, err)
	}
	if _, err := Match(panes, "AAAA0000-0000-0000-0000-000000000002", "/dev/ttys005"); !errors.Is(err, ErrTTYMismatch) {
		t.Fatalf("tty mismatch: %v", err)
	}
	if _, err := Match(panes, "AAAA0000-0000-0000-0000-00000000FFFF", "/dev/ttys002"); !errors.Is(err, ErrPaneNotFound) {
		t.Fatalf("missing id: %v", err)
	}
	if p, err := Match(panes, "", "/dev/ttys003"); err != nil || p.TabIndex != 3 {
		t.Fatalf("by tty: %+v %v", p, err)
	}
	if _, err := Match(panes, "", "/dev/ttys009"); !errors.Is(err, ErrAmbiguousPane) {
		t.Fatalf("ambiguous: %v", err)
	}
	if _, err := Match(panes, "", ""); !errors.Is(err, ErrPaneNotFound) {
		t.Fatalf("no data: %v", err)
	}
}

type fakeRunner struct {
	enumerate string
	selectOut string
	stderr    string
	err       error
	calls     [][]string
	delay     time.Duration
}

func (f *fakeRunner) Run(ctx context.Context, script string, args ...string) (string, string, error) {
	f.calls = append(f.calls, args)
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return "", "", ctx.Err()
		}
	}
	if f.err != nil {
		return "", f.stderr, f.err
	}
	if strings.Contains(script, "tell s to select") {
		return f.selectOut, "", nil
	}
	return f.enumerate, "", nil
}

func enumOutput(ps []Pane) string {
	var b strings.Builder
	b.WriteString("OK\x1e")
	for _, p := range ps {
		b.WriteString(p.WindowID + "\x1f" + string(rune('0'+p.TabIndex)) + "\x1f" + p.SessionID + "\x1f" + p.TTY + "\x1e")
	}
	return b.String()
}

// Acceptance test 12 (adapter).
func TestFocusAdapter(t *testing.T) {
	r := &fakeRunner{enumerate: enumOutput(panes), selectOut: "OK"}
	it := ITerm{Runner: r, Timeout: time.Second}
	p, err := it.Focus(context.Background(), "AAAA0000-0000-0000-0000-000000000003", "/dev/ttys003")
	if err != nil || p.WindowID != "2" {
		t.Fatalf("focus: %+v %v", p, err)
	}
	if last := r.calls[len(r.calls)-1]; len(last) != 2 || last[0] != "AAAA0000-0000-0000-0000-000000000003" || last[1] != "/dev/ttys003" {
		t.Fatalf("select arguments %v", last)
	}

	denied := &fakeRunner{err: errors.New("exit status 1"), stderr: "execution error: Not authorized to send Apple events to iTerm. (-1743)"}
	if _, err := (ITerm{Runner: denied}).Focus(context.Background(), "", "/dev/ttys003"); !errors.Is(err, ErrAutomationDenied) {
		t.Fatalf("denied: %v", err)
	}

	notRunning := &fakeRunner{enumerate: "NOT_RUNNING"}
	if _, err := (ITerm{Runner: notRunning}).Focus(context.Background(), "", "/dev/ttys003"); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("not running: %v", err)
	}

	slow := &fakeRunner{enumerate: enumOutput(panes), delay: time.Second}
	if _, err := (ITerm{Runner: slow, Timeout: 20 * time.Millisecond}).Focus(context.Background(), "", "/dev/ttys003"); !errors.Is(err, ErrTimeout) {
		t.Fatalf("timeout: %v", err)
	}

	gone := &fakeRunner{enumerate: enumOutput(panes), selectOut: "NOT_FOUND"}
	if _, err := (ITerm{Runner: gone}).Focus(context.Background(), "", "/dev/ttys003"); !errors.Is(err, ErrPaneNotFound) {
		t.Fatalf("pane closed between the two scripts: %v", err)
	}

	if _, err := (ITerm{Runner: r}).Focus(context.Background(), "", `/dev/ttys003" & do shell script "x`); err == nil {
		t.Fatal("invalid tty accepted")
	}
}
