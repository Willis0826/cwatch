//go:build darwin

package process

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// System reads process information from the macOS kernel with sysctl. It
// does not start subprocesses.
type System struct{}

// Lookup returns information for pid.
func (System) Lookup(pid int) (Proc, error) {
	if pid <= 0 {
		return Proc{}, ErrNotFound
	}
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		if errors.Is(err, syscall.ESRCH) || errors.Is(err, syscall.EIO) || errors.Is(err, syscall.ENOENT) {
			return Proc{}, ErrNotFound
		}
		return Proc{}, err
	}
	if int(kp.Proc.P_pid) != pid {
		// The kernel returns an empty record for a PID that does not exist.
		return Proc{}, ErrNotFound
	}
	comm := kp.Proc.P_comm[:]
	if i := bytes.IndexByte(comm, 0); i >= 0 {
		comm = comm[:i]
	}
	st := kp.Proc.P_starttime
	return Proc{
		PID:   pid,
		PPID:  int(kp.Eproc.Ppid),
		Start: int64(st.Sec)*1_000_000 + int64(st.Usec),
		Comm:  string(comm),
		TTY:   ttyName(kp.Eproc.Tdev),
	}, nil
}

// ttyName converts a terminal device number to a /dev path.
func ttyName(dev int32) string {
	if dev == -1 || dev == 0 {
		return ""
	}
	want := uint64(uint32(dev))
	// macOS names pseudo-terminals /dev/ttysNNN with the minor number.
	guess := fmt.Sprintf("/dev/ttys%03d", unix.Minor(want))
	if rdevMatches(guess, want) {
		return guess
	}
	matches, _ := filepath.Glob("/dev/tty*")
	for _, m := range matches {
		if strings.HasPrefix(m, "/dev/tty.") {
			continue
		}
		if rdevMatches(m, want) {
			return m
		}
	}
	return ""
}

func rdevMatches(path string, want uint64) bool {
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	return uint64(uint32(st.Rdev)) == want
}

// Running reports whether a process with the executable name comm runs. It
// reads the process table and does not start or contact the process.
func Running(comm string) (bool, error) {
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return false, err
	}
	for i := range procs {
		c := procs[i].Proc.P_comm[:]
		if j := bytes.IndexByte(c, 0); j >= 0 {
			c = c[:j]
		}
		if string(c) == comm {
			return true, nil
		}
	}
	return false, nil
}
