//go:build darwin && cgo

package menubar

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"fyne.io/systray"

	"cwatch/internal/app"
	"cwatch/internal/terminal"
	"cwatch/internal/textutil"
)

// Supported tells whether this build can show the menu bar.
const Supported = true

// noticeTime is how long the menu shows a focus error.
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

// Run shows the menu bar item and blocks until the user quits it. It must
// run on the main thread.
func Run(env *app.Env) error {
	systray.Run(func() { newMenu(env).start() }, nil)
	return nil
}

type menu struct {
	env       *app.Env
	notice    *systray.MenuItem
	rows      []*systray.MenuItem
	more      *systray.MenuItem
	dashboard *systray.MenuItem
	quit      *systray.MenuItem

	mu        sync.Mutex
	ids       []string // instance ID of each visible row
	err       string   // last focus error
	errUntil  time.Time
	refreshCh chan struct{}
}

func newMenu(env *app.Env) *menu {
	systray.SetTitle(IdleTitle)
	systray.SetTooltip("cwatch: your Claude Code sessions")
	m := &menu{env: env, refreshCh: make(chan struct{}, 1)}
	m.notice = systray.AddMenuItem("", "")
	m.notice.Disable()
	m.notice.Hide()
	for i := 0; i < MaxRows; i++ {
		item := systray.AddMenuItem("", "")
		item.Hide()
		m.rows = append(m.rows, item)
	}
	m.more = systray.AddMenuItem("", "")
	m.more.Disable()
	m.more.Hide()
	systray.AddSeparator()
	m.dashboard = systray.AddMenuItem("Open the dashboard in iTerm2", "Run cwatch in a new iTerm2 window")
	m.quit = systray.AddMenuItem("Quit the menu bar", "The menu bar starts again at the next login")
	m.ids = make([]string, MaxRows)
	return m
}

func (m *menu) start() {
	for i, item := range m.rows {
		go m.watchRow(i, item)
	}
	go func() {
		for range m.dashboard.ClickedCh {
			m.openDashboard()
		}
	}()
	go func() {
		<-m.quit.ClickedCh
		systray.Quit()
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
		case <-systray.TrayOpenedCh:
		case <-m.refreshCh:
		}
		m.refresh()
	}
}

func (m *menu) refresh() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	v := Build(m.env.Load(ctx, app.LoadOptions{NoTokens: true}))

	m.mu.Lock()
	notice := v.Notice
	if m.err != "" && time.Now().Before(m.errUntil) {
		notice = m.err
	}
	for i := range m.ids {
		m.ids[i] = ""
		if i < len(v.Rows) {
			m.ids[i] = v.Rows[i].ID
		}
	}
	m.mu.Unlock()

	systray.SetTitle(v.Title)
	if notice != "" {
		m.notice.SetTitle(textutil.OneLine(notice, MaxLabel))
		m.notice.Show()
	} else {
		m.notice.Hide()
	}
	for i, item := range m.rows {
		if i < len(v.Rows) {
			item.SetTitle(v.Rows[i].Label)
			item.SetTooltip(v.Rows[i].Tooltip)
			item.Show()
		} else {
			item.Hide()
		}
	}
	if v.More > 0 {
		m.more.SetTitle(fmt.Sprintf("%d more; open the dashboard to see all", v.More))
		m.more.Show()
	} else {
		m.more.Hide()
	}
}

func (m *menu) watchRow(i int, item *systray.MenuItem) {
	for range item.ClickedCh {
		m.mu.Lock()
		id := m.ids[i]
		m.mu.Unlock()
		if id != "" {
			m.focus(id)
		}
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

// showError shows err at the top of the menu for a short time.
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
	select {
	case m.refreshCh <- struct{}{}:
	default:
	}
}
