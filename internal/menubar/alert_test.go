package menubar

import (
	"testing"

	"cwatch/internal/state"
)

func inst(id string, st state.State) state.Instance {
	return state.Instance{InstanceID: id, State: st, Project: "p-" + id, PromptExcerpt: "secret"}
}

func TestTrackerFirstCallHasNoAlerts(t *testing.T) {
	var tr Tracker
	if a := tr.Update([]state.Instance{inst("a", state.NeedsPermission)}); len(a) != 0 {
		t.Fatalf("alerts %+v", a)
	}
	if tr.Count() != 0 {
		t.Fatalf("count %d", tr.Count())
	}
}

func TestTrackerChanges(t *testing.T) {
	var tr Tracker
	tr.Update([]state.Instance{inst("a", state.Working), inst("b", state.Working), inst("c", state.Running)})

	perm := inst("a", state.NeedsPermission)
	perm.Branch = "main"
	perm.Pending = []state.Pending{{ToolName: "Bash"}, {ToolName: "Edit"}}
	errd := inst("c", state.Error)
	errd.ErrorType = "overloaded"
	a := tr.Update([]state.Instance{inst("b", state.Idle), perm, errd, inst("d", state.Idle)})
	if len(a) != 3 {
		t.Fatalf("alerts %+v", a)
	}
	if a[0].ID != "a" || a[0].Kind != AlertPermission || !a[0].Sticky() ||
		a[0].Title != "🟡 p-a · main" || a[0].Body != "Waits for permission: Bash, Edit" {
		t.Fatalf("alert %+v", a[0])
	}
	if a[1].ID != "c" || a[1].Kind != AlertError || a[1].Body != "Stopped because of an API error: overloaded" {
		t.Fatalf("alert %+v", a[1])
	}
	if a[2].ID != "b" || a[2].Kind != AlertIdle || a[2].Sticky() || a[2].Body != "Finished. Waits for your next prompt." {
		t.Fatalf("alert %+v", a[2])
	}
	if tr.Count() != 3 || tr.Unseen("d") {
		t.Fatalf("count %d; a new idle session must not alert", tr.Count())
	}

	// No change: no new alert, and the old alerts stay.
	if a := tr.Update([]state.Instance{inst("b", state.Idle), perm, errd}); len(a) != 0 || tr.Count() != 3 {
		t.Fatalf("alerts %+v, count %d", a, tr.Count())
	}

	// The user approves the tool in the pane, and session c ends.
	if a := tr.Update([]state.Instance{inst("b", state.Idle), inst("a", state.Working)}); len(a) != 0 {
		t.Fatalf("alerts %+v", a)
	}
	if tr.Unseen("a") || tr.Unseen("c") || !tr.Unseen("b") {
		t.Fatalf("unseen %v", tr.unseen)
	}
	tr.Seen("b")
	if tr.Count() != 0 {
		t.Fatalf("count %d", tr.Count())
	}
}

func TestTrackerDeniedPermissionIsNotIdleAlert(t *testing.T) {
	var tr Tracker
	tr.Update([]state.Instance{inst("a", state.NeedsPermission)})
	if a := tr.Update([]state.Instance{inst("a", state.Idle)}); len(a) != 0 {
		t.Fatalf("alerts %+v", a)
	}
}

func TestTrackerNewSessionInPermission(t *testing.T) {
	var tr Tracker
	tr.Update(nil)
	if a := tr.Update([]state.Instance{inst("a", state.NeedsPermission)}); len(a) != 1 {
		t.Fatalf("alerts %+v", a)
	}
	tr.SeenAll()
	if tr.Count() != 0 {
		t.Fatalf("count %d", tr.Count())
	}
}

func TestWithBell(t *testing.T) {
	if WithBell("🟢1", 0) != "🟢1" || WithBell("🟡1", 2) != "🔔 🟡1" {
		t.Fatal("bell title")
	}
}
