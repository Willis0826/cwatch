// Package tui is the interactive dashboard.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"cwatch/internal/app"
	"cwatch/internal/state"
	"cwatch/internal/textutil"
	"cwatch/internal/transcript"
)

// RefreshInterval is the time between two reloads of the store.
const RefreshInterval = time.Second

// Options configure the dashboard.
type Options struct {
	Excerpts bool
	All      bool
}

type tickMsg time.Time

type snapMsg struct {
	snap app.Snapshot
	at   time.Time
}

type focusMsg struct {
	text string
	err  error
}

type detailMsg struct {
	id     string
	events []state.EventRecord
	tail   transcript.Excerpts
	tailOK bool
	err    error
}

// Model is the dashboard model.
type Model struct {
	env  *app.Env
	opts Options

	snap    app.Snapshot
	loaded  bool
	loading bool
	now     time.Time

	selectedID string
	cursor     int
	offset     int
	width      int
	height     int

	filter    string
	filtering bool

	details     bool
	detail      detailMsg
	message     string
	messageErr  bool
	focusActive bool

	dark  bool
	theme theme
}

// New returns a dashboard model.
func New(env *app.Env, opts Options) Model {
	return Model{env: env, opts: opts, width: 100, height: 24, now: env.Now(), dark: true, theme: newTheme(true)}
}

// Run starts the dashboard and restores the terminal on exit.
func Run(env *app.Env, opts Options) error {
	_, err := tea.NewProgram(New(env, opts)).Run()
	return err
}

func tick() tea.Cmd {
	return tea.Tick(RefreshInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m Model) load(prune bool) tea.Cmd {
	env, all := m.env, m.opts.All
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return snapMsg{snap: env.Load(ctx, app.LoadOptions{All: all, Prune: prune}), at: env.Now()}
	}
}

// Init loads the first snapshot and starts the refresh timer.
func (m Model) Init() tea.Cmd {
	return tea.Batch(m.load(true), tick(), tea.RequestBackgroundColor)
}

func (m Model) visible() []state.Instance {
	if m.filter == "" {
		return m.snap.Instances
	}
	f := strings.ToLower(m.filter)
	var out []state.Instance
	for _, in := range m.snap.Instances {
		hay := strings.ToLower(strings.Join([]string{
			in.Project, in.Cwd, in.Branch, in.InstanceID, string(in.State), in.TTY, in.SessionID,
		}, " "))
		if strings.Contains(hay, f) {
			out = append(out, in)
		}
	}
	return out
}

// syncCursor keeps the selected instance selected across refreshes.
func (m *Model) syncCursor() {
	vis := m.visible()
	if len(vis) == 0 {
		m.cursor = 0
		return
	}
	for i, in := range vis {
		if in.InstanceID == m.selectedID {
			m.cursor = i
			return
		}
	}
	if m.cursor >= len(vis) {
		m.cursor = len(vis) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	m.selectedID = vis[m.cursor].InstanceID
}

func (m *Model) move(delta int) {
	vis := m.visible()
	if len(vis) == 0 {
		return
	}
	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(vis) {
		m.cursor = len(vis) - 1
	}
	m.selectedID = vis[m.cursor].InstanceID
}

func (m Model) selected() (state.Instance, bool) {
	vis := m.visible()
	if m.cursor >= 0 && m.cursor < len(vis) {
		return vis[m.cursor], true
	}
	return state.Instance{}, false
}

func (m Model) focus(id string) tea.Cmd {
	env := m.env
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		pane, in, err := env.Focus(ctx, id)
		if err != nil {
			return focusMsg{err: err}
		}
		return focusMsg{text: fmt.Sprintf("Focused %s (%s)", in.ShortID(), app.ShortTTY(pane.TTY))}
	}
}

func (m Model) loadDetail(in state.Instance) tea.Cmd {
	env, excerpts := m.env, m.opts.Excerpts
	return func() tea.Msg {
		d := detailMsg{id: in.InstanceID}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		st, err := state.OpenExisting(ctx, env.StateDir)
		if err != nil {
			d.err = err
			return d
		}
		defer st.Close()
		d.events, d.err = st.Events(ctx, in.InstanceID, 12)
		if excerpts && in.TranscriptPath != "" {
			if ex, err := transcript.ReadTail(in.TranscriptPath, transcript.DefaultTailBytes, 1500); err == nil {
				d.tail, d.tailOK = ex, true
			}
		}
		return d
	}
}

// Update handles messages.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tea.BackgroundColorMsg:
		m.dark = msg.IsDark()
		m.theme = newTheme(m.dark)
		return m, nil

	case tickMsg:
		m.now = time.Time(msg)
		if m.loading {
			return m, tick()
		}
		m.loading = true
		return m, tea.Batch(m.load(false), tick())

	case snapMsg:
		m.loading = false
		m.loaded = true
		m.snap = msg.snap
		m.now = msg.at
		m.syncCursor()
		return m, nil

	case focusMsg:
		m.focusActive = false
		if msg.err != nil {
			m.message, m.messageErr = msg.err.Error(), true
		} else {
			m.message, m.messageErr = msg.text, false
		}
		return m, nil

	case detailMsg:
		if msg.id == m.selectedID {
			m.detail = msg
		}
		return m, nil

	case tea.KeyPressMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m Model) key(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	if k == "ctrl+c" {
		return m, tea.Quit
	}
	if m.filtering {
		switch k {
		case "enter":
			m.filtering = false
		case "esc":
			m.filtering, m.filter = false, ""
		case "backspace":
			if r := []rune(m.filter); len(r) > 0 {
				m.filter = string(r[:len(r)-1])
			}
		default:
			if t := msg.Text; t != "" {
				m.filter += textutil.OneLine(t, 40)
			}
		}
		m.syncCursor()
		return m, nil
	}
	switch k {
	case "q":
		return m, tea.Quit
	case "up", "k", "down", "j", "home", "g", "end", "G":
		switch k {
		case "up", "k":
			m.move(-1)
		case "down", "j":
			m.move(1)
		case "home", "g":
			m.move(-1 << 20)
		default:
			m.move(1 << 20)
		}
		if in, ok := m.selected(); ok && m.details && in.InstanceID != m.detail.id {
			m.detail = detailMsg{}
			return m, m.loadDetail(in)
		}
	case "/":
		m.filtering = true
		m.message = ""
	case "esc":
		if m.details {
			m.details = false
		} else if m.filter != "" {
			m.filter = ""
			m.syncCursor()
		}
	case "d", "i", "right":
		// "d" toggles the details. The right arrow only opens them.
		if m.details && k == "right" {
			break
		}
		m.details = !m.details
		if in, ok := m.selected(); ok && m.details {
			m.detail = detailMsg{}
			return m, m.loadDetail(in)
		}
	case "left":
		m.details = false
	case "a":
		m.opts.All = !m.opts.All
		m.loading = true
		return m, m.load(false)
	case "r":
		m.loading = true
		return m, m.load(false)
	case "enter":
		// Focus happens only on an explicit key press.
		if in, ok := m.selected(); ok && !m.focusActive {
			m.focusActive = true
			m.message, m.messageErr = "Focusing "+in.ShortID()+"…", false
			return m, m.focus(in.InstanceID)
		}
	}
	return m, nil
}

func pick(a, b string) string {
	if a != "" {
		return a
	}
	if b != "" {
		return b
	}
	return "(none)"
}

func indent(s, p string) string {
	return p + strings.ReplaceAll(s, "\n", "\n"+p)
}

// wrap breaks s into lines of at most w runes. It sanitizes s first.
func wrap(s string, w int) string {
	if w < 10 {
		w = 10
	}
	var out []string
	for _, para := range strings.Split(textutil.Sanitize(s, true), "\n") {
		line := ""
		for _, word := range strings.Fields(para) {
			for len([]rune(word)) > w {
				r := []rune(word)
				if line != "" {
					out = append(out, line)
					line = ""
				}
				out = append(out, string(r[:w]))
				word = string(r[w:])
			}
			switch {
			case line == "":
				line = word
			case len([]rune(line))+1+len([]rune(word)) <= w:
				line += " " + word
			default:
				out = append(out, line)
				line = word
			}
		}
		out = append(out, line)
	}
	for len(out) > 12 {
		out = out[:12]
		out[11] += " …"
	}
	return strings.Join(out, "\n")
}
