package menubar

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"cwatch/internal/app"
	"cwatch/internal/state"
)

var now = time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)

func TestBuildEmpty(t *testing.T) {
	v := Build(app.Snapshot{Status: app.StatusNoLive}, now)
	if v.Mood != MoodSleep || len(v.Rows) != 0 || len(v.Counts) != 0 || !strings.Contains(v.Notice, "No live") {
		t.Fatalf("view %+v", v)
	}
	if v.Title != "cwatch: no live sessions" {
		t.Fatalf("title %q", v.Title)
	}
	// The panel reads arrays, not null.
	b, _ := json.Marshal(v)
	if !strings.Contains(string(b), `"rows":[]`) || !strings.Contains(string(b), `"counts":[]`) {
		t.Fatalf("json %s", b)
	}
}

func TestBuildRows(t *testing.T) {
	snap := app.Snapshot{Status: app.StatusOK}
	snap.Instances = append(snap.Instances,
		state.Instance{InstanceID: "a", State: state.NeedsPermission, Project: "api", Branch: "main", Cwd: "/w/api",
			Pending: []state.Pending{{ToolName: "Bash"}}, LastEventAt: now.Add(-12 * time.Second)},
		state.Instance{InstanceID: "b", State: state.Idle, Project: "docs", PromptExcerpt: "secret prompt",
			AssistantExcerpt: "secret answer", LastEventAt: now.Add(-25 * time.Minute)},
		state.Instance{InstanceID: "c", State: state.Running, Project: "web",
			Background: []state.Task{{Kind: state.TaskSubagent, ID: "x"}}},
	)
	v := Build(snap, now)
	if v.Mood != MoodAwake || v.Notice != "" || len(v.Rows) != 3 {
		t.Fatalf("view %+v", v)
	}
	if v.Title != "cwatch: 1 need you, 1 running, 1 idle" {
		t.Fatalf("title %q", v.Title)
	}
	want := Row{ID: "a", State: state.NeedsPermission, Project: "api", Branch: "main", Icon: "✋",
		Activity: "waits for permission: Bash", Age: "12s", Tooltip: "/w/api"}
	if v.Rows[0] != want {
		t.Fatalf("row %+v", v.Rows[0])
	}
	if r := v.Rows[1]; strings.Contains(r.Activity, "secret") || r.Icon != "💤" || r.Age != "25m" {
		t.Fatalf("row %+v shows an excerpt", r)
	}
	if r := v.Rows[2]; r.Icon != "🤖" || r.Activity != "background: 1 subagent" {
		t.Fatalf("row %+v", r)
	}
}

func TestBuildMoodSleep(t *testing.T) {
	snap := app.Snapshot{Instances: []state.Instance{{InstanceID: "a", State: state.Idle}}}
	if v := Build(snap, now); v.Mood != MoodSleep {
		t.Fatalf("mood %q", v.Mood)
	}
}

func TestBuildCounts(t *testing.T) {
	snap := app.Snapshot{}
	for _, st := range []state.State{state.Idle, state.Error, state.Error, state.Working} {
		snap.Instances = append(snap.Instances, state.Instance{State: st})
	}
	v := Build(snap, now)
	got := fmt.Sprint(v.Counts)
	if got != "[{error 2 2 errors} {working 1 1 working} {idle 1 1 idle}]" {
		t.Fatalf("counts %s", got)
	}
}

func TestBuildMore(t *testing.T) {
	var snap app.Snapshot
	for i := 0; i < MaxRows+3; i++ {
		snap.Instances = append(snap.Instances, state.Instance{InstanceID: fmt.Sprint(i), State: state.Working, Project: "p"})
	}
	v := Build(snap, now)
	if len(v.Rows) != MaxRows || v.More != 3 {
		t.Fatalf("rows %d, more %d", len(v.Rows), v.More)
	}
}
