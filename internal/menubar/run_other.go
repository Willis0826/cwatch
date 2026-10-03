//go:build !darwin || !cgo

package menubar

import (
	"errors"

	"cwatch/internal/app"
)

// Supported tells whether this build can show the menu bar.
const Supported = false

// Run returns an error, because this build has no menu bar. The menu bar
// needs macOS and a build with cgo.
func Run(env *app.Env) error {
	return errors.New("this build of cwatch has no menu bar; it needs macOS and a build with CGO_ENABLED=1")
}
