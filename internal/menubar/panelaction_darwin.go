//go:build darwin && cgo

package menubar

// This file has the Go functions that the alert card and the session panel
// call. They are not in panel_darwin.go or statusitem_darwin.go, because a
// cgo preamble with an export must contain only declarations.

import "C"

// panelAction is a click on a button of the card.
type panelAction struct {
	id   string
	goTo bool // true for "Go to pane", false for "Dismiss"
}

// panelActions receives the clicks on the card.
var panelActions = make(chan panelAction, 4)

//export cwatchPanelAction
func cwatchPanelAction(id *C.char, goTo C.int) {
	select {
	case panelActions <- panelAction{id: C.GoString(id), goTo: goTo != 0}:
	default:
	}
}

// Kinds of menuEvent. They are the same as the event enum in
// statusitem_darwin.go.
const (
	eventOpened    = 0 // the user opened the session panel
	eventFocus     = 1 // the user clicked a session card
	eventDashboard = 2 // the user clicked "Open the dashboard"
)

// menuEvent is a click in the session panel.
type menuEvent struct {
	kind int
	id   string // instance ID, for eventFocus
}

// menuEvents receives the clicks in the session panel.
var menuEvents = make(chan menuEvent, 8)

//export cwatchMenuEvent
func cwatchMenuEvent(kind C.int, id *C.char) {
	select {
	case menuEvents <- menuEvent{kind: int(kind), id: C.GoString(id)}:
	default:
	}
}
