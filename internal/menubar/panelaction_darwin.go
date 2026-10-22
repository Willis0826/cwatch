//go:build darwin && cgo

package menubar

// This file has the Go function that the card calls. It is not in
// panel_darwin.go, because a cgo preamble with an export must contain only
// declarations.

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
