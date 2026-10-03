// Package menubar shows the cwatch state in the macOS menu bar. The menu
// lists the live sessions. A click on a session focuses its iTerm2 pane.
package menubar

import (
	"time"

	"cwatch/internal/app"
	"cwatch/internal/textutil"
)

// RefreshInterval is the time between two reads of the state.
const RefreshInterval = 2 * time.Second

// MaxRows is the maximum number of session rows in the menu.
const MaxRows = 15

// MaxLabel is the maximum length of a row label, in runes.
const MaxLabel = 90

// IdleTitle is the menu bar text when no session is live.
const IdleTitle = "👀"

// Row is one session in the menu.
type Row struct {
	ID      string // instance ID, for focus
	Label   string
	Tooltip string
}

// View is the content of the menu bar item and its menu.
type View struct {
	Title  string // text in the menu bar
	Notice string // text at the top of the menu when it has no rows
	Rows   []Row
	More   int // live sessions that do not fit in the menu
}

// Build makes the view of a snapshot. The rows have no message excerpts,
// because other people can see the menu bar.
func Build(snap app.Snapshot) View {
	v := View{Title: app.StateCounts(snap.Instances)}
	if v.Title == "" {
		v.Title = IdleTitle
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
		label := app.StateIcon(in.State) + " " + in.Project
		if in.Branch != "" {
			label += " · " + in.Branch
		}
		if a := app.Activity(in, false); a != "" {
			label += " — " + a
		}
		v.Rows = append(v.Rows, Row{
			ID:      in.InstanceID,
			Label:   textutil.OneLine(label, MaxLabel),
			Tooltip: textutil.OneLine(in.Cwd, 300),
		})
	}
	return v
}
