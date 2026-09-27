//go:build !darwin

package process

import "errors"

// System is not available on this platform. Every lookup fails, so the
// monitor records unknown liveness.
type System struct{}

// Lookup always returns an error on this platform.
func (System) Lookup(pid int) (Proc, error) {
	return Proc{}, errors.New("process inspection is supported only on macOS")
}

// Running is not available on this platform.
func Running(comm string) (bool, error) {
	return false, errors.New("process inspection is supported only on macOS")
}
