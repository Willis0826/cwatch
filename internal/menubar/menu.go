// Package menubar shows the cwatch state in the macOS menu bar. The menu
// bar item shows an owl and the state counts. A click on the item opens a
// panel with one card for each live session. A click on a card focuses the
// iTerm2 pane of the session.
package menubar

import (
	"fmt"
	"strings"
	"time"

	"cwatch/internal/app"
	"cwatch/internal/state"
	"cwatch/internal/textutil"
)

// RefreshInterval is the time between two reads of the state.
const RefreshInterval = 2 * time.Second

// MaxRows is the maximum number of session cards in the panel.
const MaxRows = 15

// MaxLabel is the maximum length of a text on a card, in runes.
const MaxLabel = 90

// Mood is the face of the owl in the menu bar.
type Mood string

// Owl moods.
const (
	MoodSleep Mood = "sleep" // no session works: the owl closes its eyes
	MoodAwake Mood = "awake" // a session works or needs the user
	MoodAlert Mood = "alert" // a session needs the user, and the user did not see it yet
)

// Count is the number of live sessions in one state.
type Count struct {
	State state.State `json:"state"`
	N     int         `json:"n"`
	Label string      `json:"label"` // for example "2 working"
}

// Row is one session card in the panel.
type Row struct {
	ID       string      `json:"id"` // instance ID, for focus
	State    state.State `json:"state"`
	Project  string      `json:"project"`
	Branch   string      `json:"branch,omitempty"`
	Icon     string      `json:"icon"`
	Activity string      `json:"activity"`
	Age      string      `json:"age"`
	Tooltip  string      `json:"tooltip"`
}

// View is the content of the menu bar item and its panel.
type View struct {
	Title  string  `json:"title"` // the counts as a sentence, for VoiceOver and the tooltip
	Mood   Mood    `json:"mood"`
	Counts []Count `json:"counts"`
	Notice string  `json:"notice,omitempty"` // a status or an error at the top of the panel
	Rows   []Row   `json:"rows"`
	More   int     `json:"more"` // live sessions that do not fit in the panel
}

// countOrder is the order of the counts. The states that need attention
// come first.
var countOrder = []state.State{state.NeedsPermission, state.Error, state.Working, state.Running, state.Idle}

// Build makes the view of a snapshot. The cards have no message excerpts,
// because other people can see the screen.
func Build(snap app.Snapshot, now time.Time) View {
	v := View{Mood: MoodSleep, Rows: []Row{}, Counts: []Count{}}
	n := map[state.State]int{}
	for _, in := range snap.Instances {
		n[in.State]++
		if in.State != state.Idle && in.State != state.Ended {
			v.Mood = MoodAwake
		}
	}
	var words []string
	for _, st := range countOrder {
		if n[st] > 0 {
			c := Count{State: st, N: n[st], Label: countLabel(st, n[st])}
			v.Counts = append(v.Counts, c)
			words = append(words, c.Label)
		}
	}
	v.Title = "cwatch: no live sessions"
	if len(words) > 0 {
		v.Title = "cwatch: " + strings.Join(words, ", ")
	}
	if len(snap.Instances) == 0 {
		v.Notice = app.StatusMessage(snap.Status, snap.Err)
		if v.Notice == "" {
			v.Notice = "No live Claude Code sessions."
		}
	}
	for i, in := range snap.Instances {
		if i == MaxRows {
			v.More = len(snap.Instances) - MaxRows
			break
		}
		icon, activity := activity(in)
		v.Rows = append(v.Rows, Row{
			ID:       in.InstanceID,
			State:    in.State,
			Project:  textutil.OneLine(app.Project(in), MaxLabel),
			Branch:   textutil.OneLine(in.Branch, MaxLabel),
			Icon:     icon,
			Activity: textutil.OneLine(activity, MaxLabel),
			Age:      app.Age(now, in.LastEventAt),
			Tooltip:  textutil.OneLine(in.Cwd, 300),
		})
	}
	return v
}

// countLabel returns the text of one count, with the same words as the
// dashboard header.
func countLabel(st state.State, n int) string {
	switch st {
	case state.NeedsPermission:
		return fmt.Sprintf("%d need you", n)
	case state.Error:
		if n == 1 {
			return "1 error"
		}
		return fmt.Sprintf("%d errors", n)
	}
	return fmt.Sprintf("%d %s", n, st)
}

// activity returns the icon and the text of the second line of a card. It
// uses the same icons as the dashboard.
func activity(in state.Instance) (string, string) {
	text := app.Activity(in, false)
	switch in.State {
	case state.NeedsPermission:
		return "✋", text
	case state.Working:
		if in.CurrentTool != "" {
			return "🔧", text
		}
		return "💬", text
	case state.Running:
		return "🤖", text
	case state.Error:
		return "💥", text
	case state.Idle:
		return "💤", "waits for your next prompt"
	}
	return "", text
}
