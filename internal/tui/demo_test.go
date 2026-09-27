package tui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"cwatch/internal/app"
	"cwatch/internal/hooks"
	"cwatch/internal/state"
	"cwatch/internal/transcript"
)

// TestRenderDemo writes the dashboard and the details view with demo data
// as ANSI text. "make screenshots" uses it to make the README images. It
// runs only when CWATCH_DEMO_DIR is set.
func TestRenderDemo(t *testing.T) {
	dir := os.Getenv("CWATCH_DEMO_DIR")
	if dir == "" {
		t.Skip("set CWATCH_DEMO_DIR to render the demo screens")
	}
	mk := func(id, proj, branch string, st state.State, age time.Duration, ctx int64, f func(*state.Instance)) state.Instance {
		in := state.Instance{
			InstanceID: id, SessionID: "5d0c9a4e-2b7f-4c1a-9e3d-" + id[:12], State: st, Liveness: state.Alive,
			Project: proj, Branch: branch, Cwd: "/Users/you/src/" + proj, TTY: "/dev/ttys004",
			LastEventAt: now.Add(-age), CreatedAt: now.Add(-time.Hour), LastEvent: "PreToolUse",
			OwnerPID: 48213, OwnerMethod: "ancestor_name", TerminalKind: "iterm2",
			ITermSessionID: "8C1F2A7E-3B44-4D0A-9F61-2E5B7C9D0A13",
			TranscriptPath: "/Users/you/.claude/projects/-Users-you-src-" + proj + "/" + id + ".jsonl",
		}
		if ctx > 0 {
			in.Tokens = &transcript.Usage{Context: ctx, Output: ctx / 3, CacheRead: ctx * 38, CacheWrite: ctx, Input: 312, Responses: 96, Model: "claude-opus-5-5"}
		}
		if f != nil {
			f(&in)
		}
		return in
	}
	list := []state.Instance{
		mk("7f3a91c2d4e5f601", "api-server", "main", state.NeedsPermission, 12*time.Second, 341_000, func(i *state.Instance) {
			i.Pending = []state.Pending{{ToolName: "Bash"}}
			i.PromptExcerpt = "run the migration on the staging database"
			i.AssistantExcerpt = "The migration adds two indexes. I will run it with the staging credentials."
		}),
		mk("2b8e44d09a1c7733", "api-server", "fix/auth-timeout", state.Working, 3*time.Second, 126_400, func(i *state.Instance) { i.CurrentTool = "Edit" }),
		mk("c41d0e5b6f2a9981", "web-app", "feature/search", state.Working, 40*time.Second, 57_200, func(i *state.Instance) {
			i.PromptExcerpt = "add debounce to the search box and update the tests"
		}),
		mk("0c0c3d2e1f0a9b88", "billing", "main", state.Error, 90*time.Second, 88_400, func(i *state.Instance) { i.ErrorType = "rate_limit" }),
		mk("9e02f7a3b1c4d556", "docs", "main", state.Idle, 4*time.Minute, 212_000, func(i *state.Instance) {
			i.AssistantExcerpt = "I updated the install guide and fixed the broken links."
		}),
		mk("51aa3c8d2e7f0b14", "mobile", "release/2.4", state.Idle, 25*time.Minute, 23_900, func(i *state.Instance) {
			i.AssistantExcerpt = "All 214 tests pass. The build is ready for review."
		}),
	}
	app.SortInstances(list)
	m := model(list...)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 118, Height: 17})
	m = next.(Model)
	m = key(m, "j")
	if err := os.WriteFile(filepath.Join(dir, "dashboard.ans"), []byte(m.render()), 0o644); err != nil {
		t.Fatal(err)
	}
	next, _ = m.Update(tea.WindowSizeMsg{Width: 118, Height: 36})
	m = next.(Model)
	m.details = true
	names := []struct{ ev, tool string }{
		{"UserPromptSubmit", ""}, {"PreToolUse", "Read"}, {"PostToolUse", "Read"},
		{"PreToolUse", "Bash"}, {"PermissionRequest", "Bash"},
	}
	for i, n := range names {
		m.detail.events = append(m.detail.events, state.EventRecord{
			Seq: int64(4810 + i), Event: n.ev, ReceivedAt: now.Add(time.Duration(i-len(names)) * 9 * time.Second),
			Detail: hooks.Event{HookEventName: n.ev, ToolName: n.tool},
		})
	}
	m.detail.id = m.selectedID
	if err := os.WriteFile(filepath.Join(dir, "details.ans"), []byte(m.render()), 0o644); err != nil {
		t.Fatal(err)
	}
}
