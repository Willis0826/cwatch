package menubar

import (
	"sort"
	"strings"

	"cwatch/internal/app"
	"cwatch/internal/state"
	"cwatch/internal/textutil"
)

// BellTitle is the mark in the menu bar when a session needs the user and
// the user did not see it yet.
const BellTitle = "🔔"

// MaxAlertLabel is the maximum length of an alert line, in runes.
const MaxAlertLabel = 60

// AlertKind tells why a session needs the user.
type AlertKind int

// Alert kinds, in order of importance.
const (
	AlertPermission AlertKind = iota
	AlertError
	AlertIdle
)

// Alert is a state change that needs the user. It has no message excerpts,
// because other people can see the screen.
type Alert struct {
	ID             string // instance ID, for focus
	ITermSessionID string
	Kind           AlertKind
	Title          string // state, project, and branch
	Body           string
}

// Sticky tells whether the alert stays until the user acts.
func (a Alert) Sticky() bool { return a.Kind != AlertIdle }

// Tracker finds the state changes that need the user. It also keeps the
// alerts that the user did not see yet.
type Tracker struct {
	primed bool
	last   map[string]state.State
	unseen map[string]state.State // instance ID -> state that made the alert
}

// Update compares the live instances with the last call. It returns the new
// alerts, the most important first. The first call returns no alerts, so a
// restart of the menu bar does not show old changes.
func (t *Tracker) Update(list []state.Instance) []Alert {
	if t.unseen == nil {
		t.unseen = map[string]state.State{}
	}
	next := make(map[string]state.State, len(list))
	var alerts []Alert
	for _, in := range list {
		next[in.InstanceID] = in.State
		prev, seen := t.last[in.InstanceID]
		kind, ok := alertKind(prev, seen, in.State)
		if !ok || !t.primed {
			continue
		}
		t.unseen[in.InstanceID] = in.State
		alerts = append(alerts, newAlert(in, kind))
	}
	// An alert is old when its session changed state again, for example
	// when the user approved the tool in the pane.
	for id, st := range t.unseen {
		if next[id] != st {
			delete(t.unseen, id)
		}
	}
	t.last, t.primed = next, true
	sortAlerts(alerts)
	return alerts
}

// Seen removes the alert of one session.
func (t *Tracker) Seen(id string) { delete(t.unseen, id) }

// SeenAll removes all alerts.
func (t *Tracker) SeenAll() { clear(t.unseen) }

// Unseen tells whether the alert of a session waits for the user.
func (t *Tracker) Unseen(id string) bool {
	_, ok := t.unseen[id]
	return ok
}

// Count returns the number of alerts that wait for the user.
func (t *Tracker) Count() int { return len(t.unseen) }

// WithBell adds the bell to the menu bar title when n alerts wait.
func WithBell(title string, n int) string {
	if n == 0 {
		return title
	}
	return BellTitle + " " + title
}

func alertKind(prev state.State, seen bool, cur state.State) (AlertKind, bool) {
	if seen && prev == cur {
		return 0, false
	}
	switch cur {
	case state.NeedsPermission:
		return AlertPermission, true
	case state.Error:
		return AlertError, true
	case state.Idle:
		// Only a finished turn needs the user. A new session or a denied
		// permission does not.
		if seen && (prev == state.Working || prev == state.Running) {
			return AlertIdle, true
		}
	}
	return 0, false
}

func newAlert(in state.Instance, kind AlertKind) Alert {
	title := app.StateIcon(in.State) + " " + app.Project(in)
	if in.Branch != "" {
		title += " · " + in.Branch
	}
	var body string
	switch kind {
	case AlertPermission:
		body = "Waits for permission"
		var tools []string
		for _, p := range in.Pending {
			if p.ToolName != "" {
				tools = append(tools, p.ToolName)
			}
		}
		if len(tools) > 0 {
			body += ": " + strings.Join(tools, ", ")
		}
	case AlertError:
		body = "Stopped because of an API error"
		if in.ErrorType != "" {
			body += ": " + in.ErrorType
		}
	case AlertIdle:
		body = "Finished. Waits for your next prompt."
	}
	return Alert{
		ID:             in.InstanceID,
		ITermSessionID: in.ITermSessionID,
		Kind:           kind,
		Title:          textutil.OneLine(title, MaxAlertLabel),
		Body:           textutil.OneLine(body, MaxAlertLabel),
	}
}

func sortAlerts(list []Alert) {
	sort.SliceStable(list, func(i, j int) bool { return list[i].Kind < list[j].Kind })
}
