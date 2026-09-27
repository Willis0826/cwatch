package tui

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/charmbracelet/x/ansi"

	"cwatch/internal/app"
	"cwatch/internal/state"
	"cwatch/internal/textutil"
)

// theme holds the colours for a light or a dark terminal background.
type theme struct {
	accent, text, faint, border, selBg, headBg  color.Color
	red, yellow, green, gray, blue              color.Color
	titleFg, titleBg                            color.Color
	title, chip, head, key, dim, box, errorText lipgloss.Style
}

func newTheme(dark bool) theme {
	ld := lipgloss.LightDark(dark)
	t := theme{
		accent: ld(lipgloss.Color("#2F7A4F"), lipgloss.Color("#7CC99A")),
		text:   ld(lipgloss.Color("#1F1F1F"), lipgloss.Color("#E6E6E6")),
		faint:  ld(lipgloss.Color("#8A8A8A"), lipgloss.Color("#7A8580")),
		border: ld(lipgloss.Color("#BCD9C6"), lipgloss.Color("#3E5A48")),
		selBg:  ld(lipgloss.Color("#DDEFE3"), lipgloss.Color("#243A2D")),
		headBg: ld(lipgloss.Color("#EEF6F0"), lipgloss.Color("#1A2820")),
		red:    ld(lipgloss.Color("#C62828"), lipgloss.Color("#FF6B6B")),
		yellow: ld(lipgloss.Color("#B26A00"), lipgloss.Color("#FFC857")),
		green:  ld(lipgloss.Color("#2E7D32"), lipgloss.Color("#6BD68A")),
		gray:   ld(lipgloss.Color("#9E9E9E"), lipgloss.Color("#6C6C75")),
		blue:   ld(lipgloss.Color("#1565C0"), lipgloss.Color("#7FB3FF")),
		// The title uses a muted green, not a bright one.
		titleFg: ld(lipgloss.Color("#1E5E3A"), lipgloss.Color("#A9D9BA")),
		titleBg: ld(lipgloss.Color("#DCEFE3"), lipgloss.Color("#1F3A2B")),
	}
	t.title = lipgloss.NewStyle().Bold(true).Foreground(t.titleFg).Background(t.titleBg).Padding(0, 1)
	t.chip = lipgloss.NewStyle().Padding(0, 1)
	t.head = lipgloss.NewStyle().Bold(true).Foreground(t.accent).Background(t.headBg).Padding(0, 1)
	t.key = lipgloss.NewStyle().Bold(true).Foreground(t.accent)
	t.dim = lipgloss.NewStyle().Foreground(t.faint)
	t.box = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(t.border).Padding(0, 1)
	t.errorText = lipgloss.NewStyle().Foreground(t.red).Bold(true)
	return t
}

// stateIcon returns the emoji and the label of a state.
func stateIcon(in state.Instance) (string, string) {
	switch in.State {
	case state.NeedsPermission:
		return "🟡", "permission"
	case state.Working:
		return "🟢", "working"
	case state.Idle:
		return "⚪", "idle"
	case state.Error:
		return "🔴", "error"
	case state.Ended:
		return "⚫", "ended"
	}
	return "❔", string(in.State)
}

func (t theme) stateColor(in state.Instance) color.Color {
	switch in.State {
	case state.NeedsPermission:
		return t.yellow
	case state.Working:
		return t.green
	case state.Error:
		return t.red
	case state.Ended:
		return t.gray
	}
	return t.text
}

// stateCell is the text of the state column. A "❔" marks unknown
// liveness.
func stateCell(in state.Instance) string {
	icon, label := stateIcon(in)
	s := icon + " " + label
	if in.State != state.Ended && in.Liveness == state.Unknown {
		s += " ❔"
	}
	return s
}

// activityCell is the text of the activity column. Quoted text is an
// excerpt of a real message, not a summary.
func activityCell(in state.Instance, excerpts bool) string {
	q := func(s string) string { return "“" + textutil.OneLine(s, 300) + "”" }
	switch in.State {
	case state.NeedsPermission:
		var tools []string
		for _, p := range in.Pending {
			if p.ToolName != "" {
				tools = append(tools, p.ToolName)
			}
		}
		if len(tools) > 0 {
			return "✋ waits for you: " + strings.Join(tools, ", ")
		}
		return "✋ waits for you"
	case state.Working:
		if in.CurrentTool != "" {
			s := "🔧 " + in.CurrentTool
			if in.CurrentToolAgent != "" {
				s += " · 🤖 subagent"
			}
			return s
		}
		if excerpts && in.PromptExcerpt != "" {
			return "💬 " + q(in.PromptExcerpt)
		}
		return "💭 thinking"
	case state.Error:
		s := "💥 API error"
		if in.ErrorType != "" {
			s += ": " + in.ErrorType
		}
		return s
	case state.Idle:
		if excerpts && in.AssistantExcerpt != "" {
			return "🤖 " + q(in.AssistantExcerpt)
		}
		if excerpts && in.PromptExcerpt != "" {
			return "💬 " + q(in.PromptExcerpt)
		}
		return "💤 waits for a prompt"
	case state.Ended:
		return "🏁 " + in.Reason
	}
	return ""
}

// fit truncates s to w terminal cells. It counts wide characters, such as
// emoji, as two cells.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	return ansi.Truncate(s, w, "…")
}

type column struct {
	title string
	width int
}

// columns returns the column layout for a terminal width. It drops the
// less important columns on narrow terminals.
func columns(w int) []column {
	cols := []column{
		{"STATE", 15}, {"PROJECT", 18}, {"BRANCH", 16}, {"ACTIVITY", 0},
		{"AGE", 4}, {"CONTEXT", 7}, {"ID", 8},
	}
	// Each column adds 2 cells of padding and 1 cell of border.
	used := func() int {
		n := 1
		for _, c := range cols {
			n += c.width + 3
		}
		return n
	}
	drop := func(title string) {
		for i, c := range cols {
			if c.title == title {
				cols = append(cols[:i], cols[i+1:]...)
				return
			}
		}
	}
	for _, t := range []string{"ID", "BRANCH", "CONTEXT", "AGE"} {
		if w-used() >= 20 {
			break
		}
		drop(t)
	}
	// Shrink the project and state columns before the activity column gets
	// narrower than its minimum.
	const minActivity = 12
	for _, s := range []struct {
		title string
		min   int
	}{{"PROJECT", 10}, {"STATE", 13}} {
		for i := range cols {
			if cols[i].title == s.title {
				if over := minActivity - (w - used()); over > 0 {
					cols[i].width -= min(over, cols[i].width-s.min)
				}
			}
		}
	}
	for i := range cols {
		if cols[i].title == "ACTIVITY" {
			cols[i].width = max(w-used(), 6)
		}
	}
	return cols
}

func cellText(in state.Instance, title string, m Model) string {
	switch title {
	case "STATE":
		return stateCell(in)
	case "PROJECT":
		return app.Project(in)
	case "BRANCH":
		if in.Branch == "" {
			return "—"
		}
		return textutil.OneLine(in.Branch, 60)
	case "ACTIVITY":
		return activityCell(in, m.opts.Excerpts)
	case "AGE":
		return app.Age(m.now, in.LastEventAt)
	case "CONTEXT":
		return app.ContextSize(in)
	case "ID":
		return textutil.OneLine(in.ShortID(), 16)
	}
	return ""
}

// View renders the dashboard.
func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = m.windowTitle()
	return v
}

// windowTitle returns the terminal title: the state counts, with the states
// that need attention first, so that a narrow iTerm2 tab shows them.
// Bubble Tea writes the title only when it changes, and clears it on exit.
func (m Model) windowTitle() string {
	if !m.loaded {
		return "cwatch"
	}
	counts := map[state.State]int{}
	for _, in := range m.snap.Instances {
		counts[in.State]++
	}
	var parts []string
	for _, c := range []struct {
		st   state.State
		icon string
	}{{state.NeedsPermission, "🟡"}, {state.Error, "🔴"}, {state.Working, "🟢"}, {state.Idle, "⚪"}} {
		if n := counts[c.st]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s%d", c.icon, n))
		}
	}
	if len(parts) == 0 {
		return "cwatch · no sessions"
	}
	return strings.Join(parts, " ") + " · cwatch"
}

// renderWidth returns the width that the dashboard uses for a terminal
// width.
func renderWidth(width int) int {
	if width < 40 {
		return 40
	}
	return width
}

func (m Model) render() string {
	w := renderWidth(m.width)
	var b strings.Builder
	b.WriteString(m.titleBar(w) + "\n")
	switch {
	case m.sum.mode == summaryMenu:
		b.WriteString(m.renderSummaryMenu(w))
	case m.sum.mode == summaryView:
		b.WriteString(m.renderSummary(w))
	case m.details:
		b.WriteString(m.renderDetails(w))
	case !m.loaded:
		b.WriteString(m.notice(w, "⏳", "Loading…"))
	case len(m.snap.Instances) == 0:
		b.WriteString(m.notice(w, statusIcon(m.snap.Status), app.StatusMessage(m.snap.Status, m.snap.Err)))
	case len(m.visible()) == 0:
		b.WriteString(m.notice(w, "🔎", "No instances match the filter."))
	default:
		b.WriteString(m.renderTable(w))
	}
	b.WriteString(m.footer(w))
	return b.String()
}

func statusIcon(s app.Status) string {
	switch s {
	case app.StatusHooksAbsent:
		return "🔌"
	case app.StatusNoEvents:
		return "⏳"
	case app.StatusNoLive:
		return "😴"
	case app.StatusUnavailable:
		return "🚧"
	}
	return "💡"
}

func (m Model) notice(w int, icon, msg string) string {
	body := icon + "  " + wrap(msg, w-10)
	return m.theme.box.Width(w).Render(body) + "\n"
}

func (m Model) titleBar(w int) string {
	t := m.theme
	counts := map[state.State]int{}
	for _, in := range m.snap.Instances {
		counts[in.State]++
	}
	left := t.title.Render("👀 cwatch")
	var chips []string
	add := func(n int, icon, label string, c color.Color) {
		if n > 0 {
			chips = append(chips, t.chip.Foreground(c).Render(fmt.Sprintf("%s %d %s", icon, n, label)))
		}
	}
	add(counts[state.NeedsPermission], "🟡", "need you", t.yellow)
	add(counts[state.Error], "🔴", "error", t.red)
	add(counts[state.Working], "🟢", "working", t.green)
	add(counts[state.Idle], "⚪", "idle", t.text)
	add(counts[state.Ended], "⚫", "ended", t.gray)
	line := left + " " + strings.Join(chips, "")
	if m.filter != "" || m.filtering {
		f := "🔎 " + m.filter
		if m.filtering {
			f += "▏"
		}
		line += "  " + t.key.Render(f)
	}
	if m.opts.All {
		line += "  " + t.dim.Render("(with ended)")
	}
	return fit(line, w)
}

func (m Model) renderTable(w int) string {
	t := m.theme
	vis := m.visible()
	cols := columns(w)

	// The title bar, the table borders, the header, and the footer use
	// about 7 lines.
	rows := m.height - 8
	if rows < 3 {
		rows = 3
	}
	offset := 0
	if m.cursor >= rows {
		offset = m.cursor - rows + 1
	}
	end := offset + rows
	if end > len(vis) {
		end = len(vis)
	}
	page := vis[offset:end]

	headers := make([]string, len(cols))
	for i, c := range cols {
		headers[i] = fit(c.title, c.width)
	}
	data := make([][]string, len(page))
	for r, in := range page {
		row := make([]string, len(cols))
		for i, c := range cols {
			row[i] = fit(cellText(in, c.title, m), c.width)
		}
		data[r] = row
	}

	tbl := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(t.border)).
		BorderColumn(true).
		Headers(headers...).
		Rows(data...).
		StyleFunc(func(row, col int) lipgloss.Style {
			c := cols[col]
			if row == table.HeaderRow {
				return t.head.Width(c.width + 2)
			}
			s := lipgloss.NewStyle().Padding(0, 1).Width(c.width + 2).Foreground(t.text)
			in := page[row]
			switch c.title {
			case "STATE":
				s = s.Foreground(t.stateColor(in)).Bold(in.State == state.NeedsPermission || in.State == state.Error)
			case "BRANCH":
				s = s.Foreground(t.blue)
			case "AGE", "ID":
				s = s.Foreground(t.faint)
			case "ACTIVITY":
				if in.State == state.Ended {
					s = s.Foreground(t.faint)
				}
			}
			if offset+row == m.cursor {
				s = s.Background(t.selBg).Bold(true)
			}
			return s
		})

	var b strings.Builder
	b.WriteString(tbl.Render() + "\n")
	var notes []string
	if offset > 0 || end < len(vis) {
		notes = append(notes, fmt.Sprintf("rows %d–%d of %d", offset+1, end, len(vis)))
	}
	for _, in := range vis {
		if in.State != state.Ended && in.Liveness == state.Unknown {
			notes = append(notes, "❔ = cwatch cannot confirm that the process runs")
			break
		}
	}
	if len(notes) > 0 {
		b.WriteString(t.dim.Render(fit(strings.Join(notes, " · "), w)) + "\n")
	}
	return b.String()
}

func (m Model) footer(w int) string {
	t := m.theme
	var b strings.Builder
	if m.message != "" {
		msg := textutil.OneLine(m.message, w)
		if m.messageErr {
			b.WriteString(t.errorText.Render(fit("🚨 "+msg, w)) + "\n")
		} else {
			b.WriteString(lipgloss.NewStyle().Foreground(t.green).Render(fit("✅ "+msg, w)) + "\n")
		}
	}
	type hint struct{ key, desc string }
	hints := []hint{{"↑↓/jk", "select"}, {"enter", "focus"}, {"/", "filter"}, {"→/d", "details"}, {"s", "summary"}, {"a", "all"}, {"q", "quit"}}
	switch {
	case m.sum.mode == summaryMenu:
		hints = []hint{{"↑↓/jk", "select"}, {"enter/y/w", "summarise"}, {"esc", "close"}, {"q", "quit"}}
	case m.sum.mode == summaryView:
		hints = []hint{{"↑↓/jk", "scroll"}, {"pgup/pgdn", "page"}, {"r", "make again"}, {"←/esc", "close"}, {"q", "quit"}}
	case m.filtering:
		hints = []hint{{"type", "filter"}, {"enter", "keep"}, {"esc", "clear"}}
	case m.details:
		hints = []hint{{"↑↓/jk", "select"}, {"enter", "focus"}, {"←/d/esc", "close"}, {"q", "quit"}}
	}
	var parts []string
	for _, h := range hints {
		parts = append(parts, t.key.Render(h.key)+" "+t.dim.Render(h.desc))
	}
	b.WriteString(fit(strings.Join(parts, t.dim.Render("  ·  ")), w))
	return b.String()
}

func (m Model) renderDetails(w int) string {
	t := m.theme
	in, ok := m.selected()
	if !ok {
		return m.notice(w, "🔎", "No instance selected.")
	}
	inner := w - 4
	var b strings.Builder
	label := lipgloss.NewStyle().Foreground(t.faint).Width(16)
	line := func(k, v string) {
		if v == "" {
			v = "—"
		}
		b.WriteString(label.Render(k) + fit(textutil.OneLine(v, 1000), inner-16) + "\n")
	}
	icon, st := stateIcon(in)
	b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(t.stateColor(in)).Render(icon+" "+st) +
		"  " + t.dim.Render(in.Reason) + "\n\n")
	line("📁 Directory", in.Cwd)
	line("🌿 Branch", in.Branch)
	line("🆔 Instance", in.InstanceID)
	line("🧵 Session", in.SessionID)
	live := string(in.Liveness)
	if in.Liveness == state.Unknown {
		live += " ❔"
	}
	line("🧩 Process", fmt.Sprintf("PID %d · %s · found by %s", in.OwnerPID, live, in.OwnerMethod))
	line("💻 Terminal", fmt.Sprintf("%s %s · pane %s", in.TerminalKind, in.TTY, in.ITermSessionID))
	line("🕒 Last event", fmt.Sprintf("%s, %s ago", in.LastEvent, app.Age(m.now, in.LastEventAt)))
	line("🔧 Tool", in.CurrentTool)
	if in.LastToolError != "" {
		line("🚨 Tool error", in.LastToolError)
	}
	if in.ErrorType != "" {
		line("💥 API error", in.ErrorType+" "+in.ErrorDetail)
	}
	if u := in.Tokens; u != nil {
		s := fmt.Sprintf("context %s · output %s · cache read %s · cache write %s · input %s · %d responses",
			app.Tokens(u.Context), app.Tokens(u.Output), app.Tokens(u.CacheRead), app.Tokens(u.CacheWrite), app.Tokens(u.Input), u.Responses)
		if u.Partial {
			s += " · still reading"
		}
		line("🧮 Tokens", s)
		line("🧠 Model", u.Model)
	} else {
		line("🧮 Tokens", "no usage in the transcript yet")
	}
	line("📜 Transcript", in.TranscriptPath)

	section := func(s string) { b.WriteString("\n" + t.key.Render(s) + "\n") }
	if m.opts.Excerpts {
		section("💬 Latest prompt (excerpt, not a summary)")
		b.WriteString(indent(wrap(textutil.Truncate(pick(in.PromptExcerpt, m.detail.tail.User), 600), inner-2), "  ") + "\n")
		section("🤖 Latest Claude text (excerpt, not a summary)")
		b.WriteString(indent(wrap(textutil.Truncate(pick(m.detail.tail.Assistant, in.AssistantExcerpt), 600), inner-2), "  ") + "\n")
	} else {
		b.WriteString("\n" + t.dim.Render("Excerpts are turned off.") + "\n")
	}
	section("📋 Recent events")
	if m.detail.err != nil && !errors.Is(m.detail.err, context.Canceled) {
		b.WriteString("  " + fit(textutil.OneLine(m.detail.err.Error(), inner), inner) + "\n")
	}
	for _, e := range m.detail.events {
		extra := e.Detail.ToolName
		if e.Detail.NotificationType != "" {
			extra = e.Detail.NotificationType
		}
		if e.Detail.AgentID != "" {
			extra += " · subagent"
		}
		b.WriteString(fit(t.dim.Render(fmt.Sprintf("  #%-5d %s  ", e.Seq, e.ReceivedAt.Format("15:04:05")))+
			fmt.Sprintf("%-19s %s", e.Event, textutil.OneLine(extra, 80)), inner) + "\n")
	}
	return t.box.Width(w).Render(strings.TrimRight(b.String(), "\n")) + "\n"
}
