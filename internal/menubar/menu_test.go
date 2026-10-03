package menubar

import (
	"fmt"
	"strings"
	"testing"

	"cwatch/internal/app"
	"cwatch/internal/state"
)

func TestBuildEmpty(t *testing.T) {
	v := Build(app.Snapshot{Status: app.StatusNoLive})
	if v.Title != IdleTitle || len(v.Rows) != 0 || !strings.Contains(v.Notice, "No live") {
		t.Fatalf("view %+v", v)
	}
}

func TestBuildRows(t *testing.T) {
	snap := app.Snapshot{Status: app.StatusOK}
	snap.Instances = append(snap.Instances,
		state.Instance{InstanceID: "a", State: state.NeedsPermission, Project: "api", Branch: "main", Cwd: "/w/api",
			Pending: []state.Pending{{ToolName: "Bash"}}},
		state.Instance{InstanceID: "b", State: state.Idle, Project: "docs", PromptExcerpt: "secret prompt"},
		state.Instance{InstanceID: "c", State: state.Running, Project: "web",
			Background: []state.Task{{Kind: state.TaskSubagent, ID: "x"}}},
	)
	v := Build(snap)
	if v.Title != "🟡1 🔵1 ⚪1" || v.Notice != "" || len(v.Rows) != 3 {
		t.Fatalf("view %+v", v)
	}
	if v.Rows[0].Label != "🟡 api · main — waits for permission: Bash" || v.Rows[0].ID != "a" || v.Rows[0].Tooltip != "/w/api" {
		t.Fatalf("row %+v", v.Rows[0])
	}
	if v.Rows[1].Label != "⚪ docs" {
		t.Fatalf("row %+v shows an excerpt", v.Rows[1])
	}
	if v.Rows[2].Label != "🔵 web — background: 1 subagent" {
		t.Fatalf("row %+v", v.Rows[2])
	}
}

func TestBuildMore(t *testing.T) {
	var snap app.Snapshot
	for i := 0; i < MaxRows+3; i++ {
		snap.Instances = append(snap.Instances, state.Instance{InstanceID: fmt.Sprint(i), State: state.Working, Project: "p"})
	}
	v := Build(snap)
	if len(v.Rows) != MaxRows || v.More != 3 {
		t.Fatalf("rows %d, more %d", len(v.Rows), v.More)
	}
}
