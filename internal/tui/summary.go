package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"cwatch/internal/app"
	"cwatch/internal/summary"
	"cwatch/internal/textutil"
)

// Summary modes.
const (
	summaryOff = iota
	summaryMenu
	summaryView
)

var summaryKinds = []struct{ kind, label, key string }{
	{summary.KindYesterday, "Yesterday", "y"},
	{summary.KindWeek, "Last week (Monday to Sunday)", "w"},
}

type summaryMsg struct {
	seq    int
	text   string
	cached bool
	err    error
}

// summaryState is the state of the summary menu and the summary view.
type summaryState struct {
	mode    int
	cursor  int
	rng     summary.Range
	seq     int
	loading bool
	started time.Time
	cancel  context.CancelFunc
	text    string
	cached  bool
	err     error
	scroll  int
}

// startSummary starts the summary of a range. It cancels a running summary.
func (m Model) startSummary(kind string, refresh bool) (Model, tea.Cmd) {
	r, err := summary.Parse(kind, m.env.Now())
	if err != nil {
		m.message, m.messageErr = err.Error(), true
		return m, nil
	}
	if m.sum.cancel != nil {
		m.sum.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.sum = summaryState{mode: summaryView, cursor: m.sum.cursor, rng: r, seq: m.sum.seq + 1,
		loading: true, started: m.env.Now(), cancel: cancel}
	env, seq := m.env, m.sum.seq
	return m, func() tea.Msg {
		defer cancel()
		text, cached, err := env.Summary(ctx, r, refresh)
		return summaryMsg{seq: seq, text: text, cached: cached, err: err}
	}
}

func (m Model) closeSummary() Model {
	if m.sum.cancel != nil {
		m.sum.cancel()
	}
	m.sum = summaryState{cursor: m.sum.cursor, seq: m.sum.seq}
	return m
}

func (m Model) summaryResult(msg summaryMsg) Model {
	if msg.seq != m.sum.seq || m.sum.mode != summaryView {
		return m // a closed or replaced summary
	}
	m.sum.loading, m.sum.cancel = false, nil
	m.sum.text, m.sum.cached, m.sum.err = msg.text, msg.cached, msg.err
	return m
}

func (m Model) summaryKey(k string) (tea.Model, tea.Cmd) {
	if m.sum.mode == summaryMenu {
		switch k {
		case "up", "k":
			if m.sum.cursor > 0 {
				m.sum.cursor--
			}
		case "down", "j":
			if m.sum.cursor < len(summaryKinds)-1 {
				m.sum.cursor++
			}
		case "enter":
			return m.startSummary(summaryKinds[m.sum.cursor].kind, false)
		case "esc", "left", "s":
			return m.closeSummary(), nil
		case "q":
			return m.closeSummary(), tea.Quit
		default:
			for i, c := range summaryKinds {
				if k == c.key {
					m.sum.cursor = i
					return m.startSummary(c.kind, false)
				}
			}
		}
		return m, nil
	}
	page := m.summaryHeight() - 1
	switch k {
	case "up", "k":
		m.sum.scroll--
	case "down", "j":
		m.sum.scroll++
	case "pgup", "b":
		m.sum.scroll -= page
	case "pgdown", "space", " ", "f":
		m.sum.scroll += page
	case "home", "g":
		m.sum.scroll = 0
	case "end", "G":
		m.sum.scroll = 1 << 20
	case "r":
		return m.startSummary(m.sum.rng.Kind, true)
	case "esc", "left":
		return m.closeSummary(), nil
	case "q":
		return m.closeSummary(), tea.Quit
	}
	m.clampSummaryScroll()
	return m, nil
}

// summaryHeight is the number of text lines that the summary view shows.
func (m Model) summaryHeight() int {
	// The title bar, the box borders, the heading, and the footer.
	h := m.height - 6
	if h < 3 {
		h = 3
	}
	return h
}

func (m Model) renderSummaryMenu(w int) string {
	t := m.theme
	var b strings.Builder
	b.WriteString(t.key.Render("📝 Summarise your work") + "\n\n")
	for i, c := range summaryKinds {
		line := fmt.Sprintf("  %s  %s", c.key, c.label)
		if i == m.sum.cursor {
			b.WriteString(t.key.Render(fit("▶"+line[1:], w-4)) + "\n")
		} else {
			b.WriteString(fit(line, w-4) + "\n")
		}
	}
	b.WriteString("\n" + t.dim.Render(fit("cwatch sends a digest of your sessions to Claude with \"claude -p\".", w-4)))
	return t.box.Width(w).Render(b.String()) + "\n"
}

func (m Model) renderSummary(w int) string {
	t := m.theme
	inner := w - 4
	head := "📝 Summary of " + m.sum.rng.Label()
	var note string
	switch {
	case m.sum.loading:
		note = ""
	case m.sum.cached:
		note = "stored · r makes it again"
	case m.sum.err == nil:
		note = "new · stored"
	}
	var body []string
	switch {
	case m.sum.loading:
		secs := int(m.now.Sub(m.sum.started).Seconds())
		if secs < 0 {
			secs = 0
		}
		body = []string{fmt.Sprintf("⏳ Summarising… this can take up to a minute (%ds)", secs)}
	case errors.Is(m.sum.err, app.ErrNoActivity):
		body = []string{"😴 No Claude Code activity in " + m.sum.rng.Label() + "."}
	case m.sum.err != nil:
		body = []string{t.errorText.Render("🚨 " + textutil.OneLine(m.sum.err.Error(), 1000))}
		body = append(body, "", t.dim.Render("Push r to try again."))
	default:
		body = m.summaryLines(inner)
	}

	h := m.summaryHeight()
	maxScroll := len(body) - h
	if maxScroll < 0 {
		maxScroll = 0
	}
	scroll := m.sum.scroll
	if scroll > maxScroll {
		scroll = maxScroll
	}
	if scroll < 0 {
		scroll = 0
	}
	end := scroll + h
	if end > len(body) {
		end = len(body)
	}
	if maxScroll > 0 {
		note = strings.TrimPrefix(note+fmt.Sprintf(" · %d–%d/%d", scroll+1, end, len(body)), " · ")
	}
	var b strings.Builder
	b.WriteString(fit(t.key.Render(head)+"  "+t.dim.Render(note), inner) + "\n")
	for _, l := range body[scroll:end] {
		b.WriteString(fit(l, inner) + "\n")
	}
	return t.box.Width(w).Render(strings.TrimRight(b.String(), "\n")) + "\n"
}

// clampSummaryScroll keeps the scroll offset in range after a key press.
func (m *Model) clampSummaryScroll() {
	if m.sum.mode != summaryView || m.sum.loading || m.sum.err != nil {
		m.sum.scroll = 0
		return
	}
	maxScroll := len(m.summaryLines(renderWidth(m.width)-4)) - m.summaryHeight()
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.sum.scroll > maxScroll {
		m.sum.scroll = maxScroll
	}
	if m.sum.scroll < 0 {
		m.sum.scroll = 0
	}
}

// summaryLines wraps the Markdown text of the summary. It keeps the indent
// of list items. It shows a Markdown heading, such as "## Done", in the
// title style, and a bold line, such as a project name, as a heading.
func (m Model) summaryLines(w int) []string {
	t := m.theme
	var out []string
	for _, raw := range strings.Split(textutil.Sanitize(m.sum.text, true), "\n") {
		trimmed := strings.TrimLeft(raw, " \t")
		indent := strings.Repeat(" ", len(raw)-len(trimmed))
		section := strings.HasPrefix(trimmed, "#")
		heading := section || strings.HasPrefix(trimmed, "**")
		text := strings.ReplaceAll(trimmed, "**", "")
		marker := ""
		for _, p := range []string{"- ", "* ", "+ "} {
			if strings.HasPrefix(text, p) {
				marker, text = "• ", text[len(p):]
				break
			}
		}
		if heading {
			text = strings.TrimSpace(strings.TrimLeft(text, "#"))
		}
		prefix := indent + marker
		lines := wrapPara(text, w-len([]rune(prefix)))
		for i, l := range lines {
			p := prefix
			if i > 0 {
				p = strings.Repeat(" ", len([]rune(prefix)))
			}
			l = p + l
			switch {
			case section:
				l = t.title.Render(strings.TrimSpace(l))
			case heading:
				l = t.key.Render(l)
			}
			out = append(out, l)
		}
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	return out
}
