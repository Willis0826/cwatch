package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"cwatch/internal/app"
	"cwatch/internal/state"
)

type fakeSummarizer struct {
	calls int
	out   string
	err   error
}

func (f *fakeSummarizer) Summarize(context.Context, string) (string, error) {
	f.calls++
	return f.out, f.err
}

// summaryModel returns a dashboard with one transcript of yesterday.
func summaryModel(t *testing.T) (Model, *fakeSummarizer) {
	t.Helper()
	cfg := t.TempDir()
	dir := filepath.Join(cfg, "projects", "p")
	os.MkdirAll(dir, 0o700)
	line := `{"type":"user","timestamp":"2026-09-26T09:00:00Z","cwd":"/w","message":{"role":"user","content":"write docs"}}`
	os.WriteFile(filepath.Join(dir, "s.jsonl"), []byte(line+"\n"), 0o600)
	fake := &fakeSummarizer{out: "**cwatch**\n- Added the summary command, with a long line that the view must wrap at the width of the box\n  - nested item\n"}
	m := model(inst("1aaaaaaa", state.Idle, 1, ""))
	m.env = &app.Env{
		StateDir:   t.TempDir(),
		Now:        func() time.Time { return now },
		Getenv:     func(k string) string { return map[string]string{"CLAUDE_CONFIG_DIR": cfg}[k] },
		Summarizer: fake,
	}
	return m, fake
}

// press sends a key and runs the command that it returns, if the command
// gives a summary result.
func press(t *testing.T, m Model, k string) Model {
	t.Helper()
	next, cmd := m.Update(keyMsg(k))
	m = next.(Model)
	if cmd != nil {
		if msg, ok := cmd().(summaryMsg); ok {
			next, _ = m.Update(msg)
			m = next.(Model)
		}
	}
	return m
}

func plain(m Model) string { return ansi.Strip(m.render()) }

func TestSummaryMenu(t *testing.T) {
	m, fake := summaryModel(t)
	m = key(m, "s")
	if m.sum.mode != summaryMenu || !strings.Contains(plain(m), "Summarise your work") {
		t.Fatalf("menu not open:\n%s", plain(m))
	}
	m = key(m, "esc")
	if m.sum.mode != summaryOff {
		t.Fatal("esc did not close the menu")
	}
	m = key(m, "s")
	m = key(m, "j")
	if m.sum.cursor != 1 {
		t.Fatalf("cursor %d", m.sum.cursor)
	}
	m = key(m, "k")
	m = press(t, m, "enter")
	if m.sum.mode != summaryView || m.sum.rng.Kind != "yesterday" || fake.calls != 1 {
		t.Fatalf("view %+v calls %d", m.sum, fake.calls)
	}
	out := plain(m)
	for _, want := range []string{"Summary of yesterday (Sat 26 Sep)", "cwatch", "• Added the summary command", "  • nested item", "new · stored"} {
		if !strings.Contains(out, want) {
			t.Errorf("view has no %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "**") {
		t.Errorf("bold markers in the view:\n%s", out)
	}

	// A second open uses the stored result.
	m = key(m, "esc")
	m = key(m, "s")
	m = press(t, m, "y")
	if fake.calls != 1 || !m.sum.cached || !strings.Contains(plain(m), "stored · r makes") {
		t.Fatalf("not cached: calls %d", fake.calls)
	}
	// r makes it again.
	m = press(t, m, "r")
	if fake.calls != 2 || m.sum.cached {
		t.Fatalf("r: calls %d", fake.calls)
	}
}

func TestSummaryLoadingAndCancel(t *testing.T) {
	m, _ := summaryModel(t)
	m = key(m, "s")
	next, cmd := m.Update(keyMsg("w"))
	m = next.(Model)
	if cmd == nil || !m.sum.loading || !strings.Contains(plain(m), "Summarising") {
		t.Fatalf("not loading:\n%s", plain(m))
	}
	seq := m.sum.seq
	m = key(m, "esc")
	if m.sum.mode != summaryOff {
		t.Fatal("esc did not close the view")
	}
	// A late result of the closed view does not open it again.
	next, _ = m.Update(summaryMsg{seq: seq, text: "late"})
	if next.(Model).sum.mode != summaryOff {
		t.Fatal("a late result opened the view")
	}
}

func TestSummaryErrors(t *testing.T) {
	m, fake := summaryModel(t)
	fake.err = errors.New("not logged in")
	m = key(m, "s")
	m = press(t, m, "y")
	if out := plain(m); !strings.Contains(out, "not logged in") || !strings.Contains(out, "Push r to try again") {
		t.Fatalf("error view:\n%s", out)
	}
	m = key(m, "esc")
	m = key(m, "s")
	m = press(t, m, "w") // no activity last week
	if out := plain(m); !strings.Contains(out, "No Claude Code activity in last week") {
		t.Fatalf("no-activity view:\n%s", out)
	}
}

func TestSummaryScroll(t *testing.T) {
	m, fake := summaryModel(t)
	var b strings.Builder
	for i := 0; i < 60; i++ {
		b.WriteString("- item " + strings.Repeat("x", i%5) + "\n")
	}
	fake.out = b.String()
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	m = next.(Model)
	m = key(m, "s")
	m = press(t, m, "y")
	if !strings.Contains(plain(m), "1–14/60") {
		t.Fatalf("first page:\n%s", plain(m))
	}
	m = key(m, "j")
	if m.sum.scroll != 1 {
		t.Fatalf("scroll %d", m.sum.scroll)
	}
	m = key(m, "G")
	if m.sum.scroll != 46 || !strings.Contains(plain(m), "47–60/60") {
		t.Fatalf("end: scroll %d\n%s", m.sum.scroll, plain(m))
	}
	m = key(m, "k")
	if m.sum.scroll != 45 {
		t.Fatalf("up after end: %d", m.sum.scroll)
	}
	for _, l := range strings.Split(plain(m), "\n") {
		if ansi.StringWidth(l) > 80 {
			t.Fatalf("line wider than the terminal: %q", l)
		}
	}
}
