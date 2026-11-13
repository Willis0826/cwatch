//go:build darwin && cgo

package menubar

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"time"

	"cwatch/internal/app"
	"cwatch/internal/terminal"
	"cwatch/internal/textutil"
)

// Supported tells whether this build can show the menu bar.
const Supported = true

// noticeTime is how long the panel shows a focus error.
const noticeTime = 15 * time.Second

// openDashboardScript opens a new iTerm2 window that runs the dashboard.
const openDashboardScript = `
on run argv
tell application id "com.googlecode.iterm2"
activate
create window with default profile command (item 1 of argv)
end tell
end run
`

// AppKit must run on the main thread. The main goroutine starts on the main
// thread, and this lock keeps it there.
func init() { runtime.LockOSThread() }

// Run shows the menu bar item and blocks until the user quits it. It must
// run on the main goroutine.
func Run(env *app.Env) error {
	m := &menu{env: env, iterm: terminal.NewITerm(), refreshCh: make(chan struct{}, 1)}
	m.start()
	runApp()
	return nil
}

type menu struct {
	env   *app.Env
	iterm terminal.ITerm

	mu        sync.Mutex
	err       string // last focus error
	errUntil  time.Time
	alerts    Tracker
	shown     string // instance ID of the alert on the card
	refreshCh chan struct{}
}

func (m *menu) start() {
	go func() {
		for e := range menuEvents {
			switch e.kind {
			case eventOpened:
				// The user sees all sessions in the panel.
				m.mu.Lock()
				m.alerts.SeenAll()
				m.mu.Unlock()
				m.requestRefresh()
			case eventFocus:
				closePanel()
				m.focus(e.id)
			case eventDashboard:
				m.openDashboard()
			}
		}
	}()
	go func() {
		for a := range panelActions {
			m.mu.Lock()
			m.alerts.Seen(a.id)
			m.shown = ""
			m.mu.Unlock()
			if a.goTo {
				m.focus(a.id)
			}
			m.requestRefresh()
		}
	}()
	go m.loop()
}

// loop reads the state at each interval, and each time the menu opens.
func (m *menu) loop() {
	t := time.NewTicker(RefreshInterval)
	defer t.Stop()
	m.refresh()
	for {
		select {
		case <-t.C:
		case <-m.refreshCh:
		}
		m.refresh()
	}
}

func (m *menu) refresh() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	snap := m.env.Load(ctx, app.LoadOptions{NoTokens: true})
	v := Build(snap, time.Now())

	m.mu.Lock()
	alerts := m.alerts.Update(snap.Instances)
	if m.shown != "" && !m.alerts.Unseen(m.shown) {
		m.shown = ""
		hidePanel()
	}
	m.mu.Unlock()
	if len(alerts) > 0 {
		m.alert(ctx, alerts)
	}

	m.mu.Lock()
	if m.alerts.Count() > 0 {
		v.Mood = MoodAlert
	}
	if m.err != "" && time.Now().Before(m.errUntil) {
		v.Notice = m.err
	}
	m.mu.Unlock()
	setView(v)
}

// alert shows the most important alert on the card. It shows no card for
// the pane that the user looks at, because the user already sees it.
func (m *menu) alert(ctx context.Context, alerts []Alert) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	current, _ := m.iterm.CurrentSession(ctx)
	for _, a := range alerts {
		if current != "" && strings.EqualFold(a.ITermSessionID, current) {
			m.mu.Lock()
			m.alerts.Seen(a.ID)
			m.mu.Unlock()
			continue
		}
		m.mu.Lock()
		m.shown = a.ID
		m.mu.Unlock()
		showPanel(a)
		return
	}
}

func (m *menu) focus(id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, _, err := m.env.Focus(ctx, id)
	m.showError(err)
}

func (m *menu) openDashboard() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	exe, err := m.env.Executable()
	if err == nil {
		cmd := app.ShellQuote([]string{exe, "--state-dir", m.env.StateDir, "--settings-file", m.env.SettingsFile})
		var stderr string
		if _, stderr, err = (terminal.OSAScript{}).Run(ctx, openDashboardScript, cmd); err != nil && stderr != "" {
			err = errors.New(textutil.OneLine(stderr, 200))
		}
	}
	m.showError(err)
}

// showError shows err at the top of the panel for a short time.
func (m *menu) showError(err error) {
	if err == nil {
		return
	}
	var ref *app.RefusedError
	msg := "⚠️ " + err.Error()
	if errors.As(err, &ref) {
		msg = "⚠️ cannot focus: " + ref.Msg
	}
	m.mu.Lock()
	m.err, m.errUntil = msg, time.Now().Add(noticeTime)
	m.mu.Unlock()
	m.requestRefresh()
}

// requestRefresh asks the loop to read the state now.
func (m *menu) requestRefresh() {
	select {
	case m.refreshCh <- struct{}{}:
	default:
	}
}
