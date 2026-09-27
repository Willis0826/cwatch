package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"cwatch/internal/app"
	"cwatch/internal/state"
)

var now = time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)

func inst(id string, st state.State, created int, excerpt string) state.Instance {
	return state.Instance{
		InstanceID: id, SessionID: "s-" + id, State: st, Liveness: state.Alive, Project: "p" + id,
		CreatedAt: now.Add(time.Duration(created) * time.Second), LastEventAt: now, TTY: "/dev/ttys00" + id[:1],
		PromptExcerpt: excerpt,
	}
}

func model(list ...state.Instance) Model {
	env := &app.Env{Now: func() time.Time { return now }}
	m := New(env, Options{Excerpts: true})
	next, _ := m.Update(snapMsg{snap: app.Snapshot{Instances: list, Status: app.StatusOK}, at: now})
	return next.(Model)
}

func key(m Model, k string) Model {
	next, _ := m.Update(keyMsg(k))
	return next.(Model)
}

func keyMsg(k string) tea.KeyPressMsg {
	var msg tea.KeyPressMsg
	switch k {
	case "down":
		msg = tea.KeyPressMsg{Code: tea.KeyDown}
	case "up":
		msg = tea.KeyPressMsg{Code: tea.KeyUp}
	case "esc":
		msg = tea.KeyPressMsg{Code: tea.KeyEscape}
	case "enter":
		msg = tea.KeyPressMsg{Code: tea.KeyEnter}
	case "right":
		msg = tea.KeyPressMsg{Code: tea.KeyRight}
	case "left":
		msg = tea.KeyPressMsg{Code: tea.KeyLeft}
	default:
		r := []rune(k)[0]
		msg = tea.KeyPressMsg{Code: r, Text: k}
	}
	return msg
}

func TestSelectionSurvivesRefresh(t *testing.T) {
	a, b, c := inst("1aaaaaaa", state.Idle, 1, ""), inst("2bbbbbbb", state.Idle, 2, ""), inst("3ccccccc", state.Idle, 3, "")
	m := model(a, b, c)
	m = key(m, "j")
	if m.selectedID != b.InstanceID {
		t.Fatalf("selected %s", m.selectedID)
	}
	// b now needs permission and sorts first. The selection follows it.
	b.State = state.NeedsPermission
	list := []state.Instance{a, b, c}
	app.SortInstances(list)
	next, _ := m.Update(snapMsg{snap: app.Snapshot{Instances: list, Status: app.StatusOK}, at: now})
	m = next.(Model)
	if m.selectedID != b.InstanceID || m.cursor != 0 {
		t.Fatalf("selected %s at %d", m.selectedID, m.cursor)
	}
	// The selected instance disappears. The cursor stays in range.
	next, _ = m.Update(snapMsg{snap: app.Snapshot{Instances: []state.Instance{a}, Status: app.StatusOK}, at: now})
	m = next.(Model)
	if m.cursor != 0 || m.selectedID != a.InstanceID {
		t.Fatalf("after removal: %s at %d", m.selectedID, m.cursor)
	}
}

func TestRenderSanitizesAndResizes(t *testing.T) {
	evil := inst("1aaaaaaa", state.Working, 1, "look \x1b]0;owned\x07\x1b[2J here")
	m := model(evil)
	for _, size := range [][2]int{{200, 50}, {80, 24}, {20, 5}, {0, 0}} {
		next, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m = next.(Model)
		out := m.render()
		if strings.Contains(out, "owned") || strings.Contains(out, "\x1b]") || strings.Contains(out, "\x1b[2J") {
			t.Fatalf("escape sequence rendered at %v: %q", size, out)
		}
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = next.(Model)
	m = key(m, "d")
	if !m.details || !strings.Contains(m.render(), "excerpt, not a summary") {
		t.Fatal("details view missing")
	}
}

func TestFilter(t *testing.T) {
	m := model(inst("1aaaaaaa", state.Idle, 1, ""), inst("2bbbbbbb", state.Idle, 2, ""))
	m = key(m, "/")
	m = key(m, "p")
	m = key(m, "2")
	if len(m.visible()) != 1 || m.visible()[0].InstanceID != "2bbbbbbb" {
		t.Fatalf("filter %q shows %d", m.filter, len(m.visible()))
	}
	m = key(m, "esc")
	if m.filter != "" || len(m.visible()) != 2 {
		t.Fatal("esc did not clear the filter")
	}
}

func TestEmptyStates(t *testing.T) {
	env := &app.Env{Now: func() time.Time { return now }}
	for _, st := range []app.Status{app.StatusHooksAbsent, app.StatusNoEvents, app.StatusNoLive, app.StatusUnavailable} {
		m := New(env, Options{})
		next, _ := m.Update(snapMsg{snap: app.Snapshot{Status: st}, at: now})
		out := next.(Model).render()
		if !strings.Contains(out, app.StatusMessage(st, nil)[:20]) {
			t.Fatalf("%s: %q", st, out)
		}
	}
}

func TestUnknownLivenessIsMarked(t *testing.T) {
	in := inst("1aaaaaaa", state.Working, 1, "")
	in.Liveness = state.Unknown
	out := model(in).render()
	if !strings.Contains(out, "working ❔") || !strings.Contains(out, "cannot confirm") {
		t.Fatalf("render %q", out)
	}
}

func TestEnterStartsFocusOnlyOnKeyPress(t *testing.T) {
	m := model(inst("1aaaaaaa", state.Idle, 1, ""))
	// Refreshes never focus.
	next, cmd := m.Update(tickMsg(now))
	if next.(Model).focusActive {
		t.Fatal("refresh started focus")
	}
	_ = cmd
	m = key(m, "enter")
	if !m.focusActive {
		t.Fatal("enter did not start focus")
	}
}

func TestTableFitsWidth(t *testing.T) {
	list := []state.Instance{
		inst("1aaaaaaa", state.NeedsPermission, 1, ""),
		inst("2bbbbbbb", state.Working, 2, "a long prompt excerpt 🚀 with emoji and 日本語 text that must be cut"),
		inst("3ccccccc", state.Error, 3, ""),
	}
	for _, w := range []int{40, 60, 80, 100, 160, 240} {
		m := model(list...)
		next, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: 30})
		out := next.(Model).render()
		for _, line := range strings.Split(out, "\n") {
			if lw := lipgloss.Width(line); lw > w && w >= 40 {
				t.Fatalf("width %d: line has %d cells: %q", w, lw, line)
			}
		}
		if !strings.Contains(out, "╭") || !strings.Contains(out, "STATE") {
			t.Fatalf("width %d: no table", w)
		}
	}
}

func TestEmojiStates(t *testing.T) {
	cases := map[state.State]string{
		state.NeedsPermission: "🟡 permission", state.Working: "🟢 working", state.Idle: "⚪ idle",
		state.Error: "🔴 error", state.Ended: "⚫ ended",
	}
	for st, want := range cases {
		if got := stateCell(inst("1aaaaaaa", st, 1, "")); got != want {
			t.Errorf("%s: %q", st, got)
		}
	}
}

func TestArrowKeysOpenAndCloseDetails(t *testing.T) {
	m := model(inst("1aaaaaaa", state.Idle, 1, ""))
	m = key(m, "right")
	if !m.details {
		t.Fatal("right arrow did not open the details")
	}
	m = key(m, "right")
	if !m.details {
		t.Fatal("a second right arrow closed the details")
	}
	m = key(m, "left")
	if m.details {
		t.Fatal("left arrow did not close the details")
	}
	m = key(m, "left")
	if m.details {
		t.Fatal("left arrow opened the details")
	}
	// "d" still toggles.
	m = key(m, "d")
	m = key(m, "d")
	if m.details {
		t.Fatal("d did not toggle")
	}
}

func TestWindowTitle(t *testing.T) {
	m := model(
		inst("1aaaaaaa", state.Idle, 1, ""), inst("2bbbbbbb", state.Working, 2, ""),
		inst("3ccccccc", state.Working, 3, ""), inst("4ddddddd", state.NeedsPermission, 4, ""),
		inst("5eeeeeee", state.Ended, 5, ""),
	)
	if got := m.windowTitle(); got != "🟡1 🟢2 ⚪1 · cwatch" {
		t.Fatalf("title %q", got)
	}
	if got := model().windowTitle(); got != "cwatch · no sessions" {
		t.Fatalf("empty title %q", got)
	}
	if got := New(&app.Env{Now: func() time.Time { return now }}, Options{}).windowTitle(); got != "cwatch" {
		t.Fatalf("loading title %q", got)
	}
}
