package state

import (
	"testing"
	"time"

	"cwatch/internal/hooks"
)

var t0 = time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)

type step struct {
	ev   hooks.Event
	want State
}

func ev(name string, mods ...func(*hooks.Event)) hooks.Event {
	e := hooks.Event{HookEventName: name, SessionID: "s1"}
	e.Kind = map[string]hooks.Kind{
		"SessionStart": hooks.KindSessionStart, "UserPromptSubmit": hooks.KindPromptSubmit,
		"PreToolUse": hooks.KindPreTool, "PostToolUse": hooks.KindPostTool,
		"PostToolUseFailure": hooks.KindPostToolFailure, "PermissionRequest": hooks.KindPermission,
		"Notification": hooks.KindNotification, "Stop": hooks.KindStop, "StopFailure": hooks.KindStopFailure,
		"SessionEnd": hooks.KindSessionEnd, "SubagentStart": hooks.KindSubagentStart,
		"SubagentStop": hooks.KindSubagentStop,
	}[name]
	if e.Kind == "" {
		e.Kind = hooks.KindUnknown
	}
	for _, m := range mods {
		m(&e)
	}
	return e
}

func tool(name, id string) func(*hooks.Event) {
	return func(e *hooks.Event) { e.ToolName, e.ToolUseID = name, id }
}
func agent(id string) func(*hooks.Event) { return func(e *hooks.Event) { e.AgentID = id } }
func notif(t string) func(*hooks.Event)  { return func(e *hooks.Event) { e.NotificationType = t } }

func runSteps(t *testing.T, in Instance, steps []step) Instance {
	t.Helper()
	for i, s := range steps {
		in = Reduce(in, s.ev, Meta{Seq: int64(i + 1), At: t0.Add(time.Duration(i) * 30 * time.Second), StoreExcerpts: true})
		if in.State != s.want {
			t.Fatalf("step %d (%s %s): state %q, want %q", i, s.ev.HookEventName, s.ev.NotificationType, in.State, s.want)
		}
	}
	return in
}

// Acceptance test 3.
func TestPermissionFlow(t *testing.T) {
	in := runSteps(t, Instance{}, []step{
		{ev("SessionStart"), Idle},
		{ev("UserPromptSubmit"), Working},
		{ev("PreToolUse", tool("Bash", "t1")), Working},
		{ev("PermissionRequest", tool("Bash", "")), NeedsPermission},
		{ev("Notification", notif("permission_prompt")), NeedsPermission},
		{ev("PostToolUse", tool("Bash", "t1")), Working},
		{ev("Stop"), Idle},
	})
	if len(in.Pending) != 0 || in.CurrentTool != "" {
		t.Fatalf("pending %v, tool %q after Stop", in.Pending, in.CurrentTool)
	}
}

// Acceptance test 4.
func TestStopThenSessionEnd(t *testing.T) {
	in := runSteps(t, Instance{}, []step{
		{ev("SessionStart"), Idle},
		{ev("UserPromptSubmit"), Working},
		{ev("Stop"), Idle},
		{ev("SessionEnd", func(e *hooks.Event) { e.Reason = "prompt_input_exit" }), Ended},
	})
	if in.Reason != "session_end:prompt_input_exit" || in.EndedAt.IsZero() {
		t.Fatalf("reason %q ended_at %v", in.Reason, in.EndedAt)
	}
}

// Acceptance test 5.
func TestAPIFailureAndToolFailureDiffer(t *testing.T) {
	in := runSteps(t, Instance{}, []step{
		{ev("UserPromptSubmit"), Working},
		{ev("PreToolUse", tool("Bash", "t1")), Working},
		{ev("PostToolUseFailure", tool("Bash", "t1"), func(e *hooks.Event) { e.ErrorDetail = "Exit code 1" }), Working},
	})
	if in.LastToolError == "" {
		t.Fatal("tool failure not recorded")
	}
	in = runSteps(t, in, []step{
		{ev("StopFailure", func(e *hooks.Event) { e.ErrorType = "rate_limit" }), Error},
	})
	if in.ErrorType != "rate_limit" {
		t.Fatalf("error type %q", in.ErrorType)
	}
	runSteps(t, in, []step{{ev("UserPromptSubmit"), Working}})
}

func TestSubagentEvents(t *testing.T) {
	in := runSteps(t, Instance{}, []step{
		{ev("UserPromptSubmit"), Working},
		{ev("PreToolUse", tool("Agent", "t1")), Working},
		// A subagent stop never marks the main session idle.
		{ev("Stop", agent("a1")), Working},
		{ev("Stop"), Idle},
		// Late subagent tool events do not revive the idle main session.
		{ev("PreToolUse", tool("Read", "t2"), agent("a1")), Idle},
		{ev("PostToolUse", tool("Read", "t2"), agent("a1")), Idle},
	})
	if in.SubagentAt.IsZero() {
		t.Fatal("subagent activity not recorded")
	}
}

func TestSubagentPermission(t *testing.T) {
	runSteps(t, Instance{}, []step{
		{ev("UserPromptSubmit"), Working},
		{ev("PermissionRequest", tool("Bash", ""), agent("a1")), NeedsPermission},
		// A main-thread tool does not clear the subagent's pending prompt.
		{ev("PreToolUse", tool("Read", "t3")), NeedsPermission},
		{ev("PostToolUse", tool("Bash", "t9"), agent("a1")), Working},
	})
}

func TestLateNotificationsDoNotRevive(t *testing.T) {
	in := runSteps(t, Instance{}, []step{
		{ev("UserPromptSubmit"), Working},
		{ev("Stop"), Idle},
		{ev("Notification", notif("permission_prompt")), Idle},
		{ev("SessionEnd"), Ended},
		{ev("Notification", notif("permission_prompt")), Ended},
		{ev("Notification", notif("idle_prompt")), Ended},
		{ev("PreToolUse", tool("Bash", "t1")), Ended},
		{ev("Stop"), Ended},
	})
	// A new session start in the same process revives the instance.
	in = runSteps(t, in, []step{{ev("SessionStart", func(e *hooks.Event) { e.Source = "resume" }), Idle}})
	if !in.EndedAt.IsZero() {
		t.Fatal("ended_at not cleared")
	}
}

func TestProcessExitIsFinal(t *testing.T) {
	in := MarkProcessExit(Instance{State: Idle}, t0)
	runSteps(t, in, []step{
		{ev("SessionStart"), Ended},
		{ev("UserPromptSubmit"), Ended},
	})
}

func TestIdleNotification(t *testing.T) {
	// An interrupt fires no Stop. The idle notification corrects the state.
	runSteps(t, Instance{}, []step{
		{ev("UserPromptSubmit"), Working},
		{ev("Notification", notif("idle_prompt")), Idle},
	})
	// An idle notification just after a new prompt is stale.
	in := Reduce(Instance{}, ev("UserPromptSubmit"), Meta{Seq: 1, At: t0})
	in = Reduce(in, ev("Notification", notif("idle_prompt")), Meta{Seq: 2, At: t0.Add(2 * time.Second)})
	if in.State != Working {
		t.Fatalf("state %q after stale idle notification", in.State)
	}
}

func TestDeniedPermissionClearsOnNextMainTool(t *testing.T) {
	runSteps(t, Instance{}, []step{
		{ev("UserPromptSubmit"), Working},
		{ev("PreToolUse", tool("Bash", "t1")), Working},
		{ev("PermissionRequest", tool("Bash", "")), NeedsPermission},
		// The user denied. Claude continues with another tool.
		{ev("PreToolUse", tool("Read", "t2")), Working},
		{ev("Notification", notif("permission_prompt")), NeedsPermission},
		{ev("PostToolUse", tool("Read", "t2")), Working},
	})
}

func TestCompactKeepsState(t *testing.T) {
	runSteps(t, Instance{}, []step{
		{ev("UserPromptSubmit"), Working},
		{ev("SessionStart", func(e *hooks.Event) { e.Source = "compact" }), Working},
	})
}

func TestUnknownEventChangesNothing(t *testing.T) {
	in := runSteps(t, Instance{}, []step{{ev("UserPromptSubmit"), Working}})
	in = runSteps(t, in, []step{{ev("SomethingNew"), Working}})
	if in.LastEvent != "SomethingNew" {
		t.Fatal("last event not recorded")
	}
}

func TestExcerptsCanBeDisabled(t *testing.T) {
	p := ev("UserPromptSubmit", func(e *hooks.Event) { e.Prompt = "secret" })
	in := Reduce(Instance{}, p, Meta{Seq: 1, At: t0, StoreExcerpts: false})
	if in.PromptExcerpt != "" {
		t.Fatal("excerpt stored while disabled")
	}
	in = Reduce(Instance{}, p, Meta{Seq: 1, At: t0, StoreExcerpts: true})
	if in.PromptExcerpt != "secret" {
		t.Fatal("excerpt not stored")
	}
}

func TestBackgroundSubagentKeepsRunning(t *testing.T) {
	in := runSteps(t, Instance{}, []step{
		{ev("UserPromptSubmit"), Working},
		{ev("PreToolUse", tool("Agent", "t1")), Working},
		{ev("SubagentStart", agent("a1")), Working},
		{ev("PostToolUse", tool("Agent", "t1")), Working},
		{ev("Stop"), Running},
		{ev("PreToolUse", tool("Read", "t2"), agent("a1")), Running},
		{ev("PostToolUse", tool("Read", "t2"), agent("a1")), Running},
		{ev("Notification", notif("idle_prompt")), Running},
		{ev("SubagentStop", agent("a1")), Idle},
	})
	if in.Background != nil {
		t.Fatalf("background %v after SubagentStop", in.Background)
	}
}

func TestForegroundSubagentEndsBeforeStop(t *testing.T) {
	runSteps(t, Instance{}, []step{
		{ev("UserPromptSubmit"), Working},
		{ev("SubagentStart", agent("a1")), Working},
		{ev("SubagentStop", agent("a1")), Working},
		{ev("Stop"), Idle},
	})
}

func TestBackgroundShellAndMonitor(t *testing.T) {
	bg := func(e *hooks.Event) { e.Background = true }
	done := func(ids ...string) func(*hooks.Event) {
		return func(e *hooks.Event) { e.IsTaskNotification, e.TasksDone, e.Prompt = true, ids, "<task-notification>" }
	}
	in := runSteps(t, Instance{}, []step{
		{ev("UserPromptSubmit", func(e *hooks.Event) { e.Prompt = "build it" }), Working},
		{ev("PostToolUse", tool("Bash", "t1"), bg), Working},
		{ev("PostToolUse", tool("Monitor", "t2")), Working},
		{ev("PostToolUse", tool("Bash", "t3")), Working},
		// A background shell of a subagent reports to the subagent.
		{ev("PostToolUse", tool("Bash", "t4"), bg, agent("a1")), Working},
		{ev("Stop"), Running},
		{ev("UserPromptSubmit", done("t1")), Working},
		{ev("Stop"), Running},
		{ev("UserPromptSubmit", done()), Working},
		{ev("Stop"), Running},
		{ev("UserPromptSubmit", done("t2")), Working},
		{ev("Stop"), Idle},
	})
	if in.PromptExcerpt != "build it" {
		t.Fatalf("prompt excerpt %q, want the user prompt", in.PromptExcerpt)
	}
}

func TestBackgroundClearedOnNewSessionAndEnd(t *testing.T) {
	in := runSteps(t, Instance{}, []step{
		{ev("UserPromptSubmit"), Working},
		{ev("SubagentStart", agent("a1")), Working},
		{ev("Stop"), Running},
		{ev("SessionStart", func(e *hooks.Event) { e.Source = "compact" }), Running},
		{ev("SessionStart", func(e *hooks.Event) { e.Source = "resume" }), Idle},
		{ev("UserPromptSubmit"), Working},
		{ev("SubagentStart", agent("a2")), Working},
		{ev("Stop"), Running},
		{ev("SessionEnd"), Ended},
	})
	if in.Background != nil {
		t.Fatalf("background %v after SessionEnd", in.Background)
	}
}
