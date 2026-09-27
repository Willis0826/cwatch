// Package process finds the Claude Code process that owns a hook and checks
// whether a recorded process is still the same running process.
package process

import (
	"errors"
	"path/filepath"
	"strings"
)

// ErrNotFound means no process has the requested PID.
var ErrNotFound = errors.New("process not found")

// Proc describes one process.
type Proc struct {
	PID   int
	PPID  int
	Start int64  // process start time, in microseconds since the Unix epoch
	Comm  string // short executable name
	TTY   string // controlling terminal path, for example /dev/ttys003, or ""
}

// Inspector reads process information. Tests use a fake inspector.
type Inspector interface {
	Lookup(pid int) (Proc, error)
}

// Ownership methods. The monitor records the method with each instance.
const (
	MethodEnvAncestor  = "claude_pid_env_ancestor"
	MethodNameAncestor = "ancestor_name"
	MethodNotFound     = "not_found"
	MethodError        = "lookup_error"
)

// MaxDepth limits the ancestor walk.
const MaxDepth = 12

// Owner is the Claude process that owns a hook invocation.
type Owner struct {
	PID    int
	Start  int64
	TTY    string
	Comm   string
	Method string
}

// Known reports whether the owner was identified.
func (o Owner) Known() bool { return o.PID > 0 && o.Start > 0 }

// IsClaudeName reports whether an executable name looks like Claude Code.
func IsClaudeName(comm string) bool {
	base := strings.ToLower(filepath.Base(comm))
	return base == "claude" || base == "claude.exe" || strings.HasPrefix(base, "claude-code")
}

// FindOwner walks the ancestors of self. It selects the process named by
// hintPID (the CLAUDE_PID variable) when that process is an ancestor. Else it
// selects the nearest ancestor with a Claude executable name. It never
// selects an ancestor only because that ancestor has a terminal.
func FindOwner(in Inspector, self int, hintPID int) Owner {
	cur, err := in.Lookup(self)
	if err != nil {
		return Owner{Method: MethodError}
	}
	var chain []Proc
	for depth := 0; depth < MaxDepth && cur.PPID > 1; depth++ {
		parent, err := in.Lookup(cur.PPID)
		if err != nil {
			break
		}
		chain = append(chain, parent)
		cur = parent
	}
	if hintPID > 1 {
		for _, p := range chain {
			if p.PID == hintPID {
				return ownerFrom(p, MethodEnvAncestor)
			}
		}
	}
	for _, p := range chain {
		if IsClaudeName(p.Comm) {
			return ownerFrom(p, MethodNameAncestor)
		}
	}
	return Owner{Method: MethodNotFound}
}

func ownerFrom(p Proc, method string) Owner {
	return Owner{PID: p.PID, Start: p.Start, TTY: p.TTY, Comm: p.Comm, Method: method}
}

// Liveness is the result of a process check.
type Liveness string

// Liveness values.
const (
	Alive   Liveness = "alive"
	Dead    Liveness = "dead"
	Unknown Liveness = "unknown"
)

// Check reports whether the process pid with start time start still runs.
// A process with the same PID and a different start time is a different
// process, so Check reports Dead for it.
func Check(in Inspector, pid int, start int64) (Liveness, Proc) {
	if pid <= 0 || start <= 0 {
		return Unknown, Proc{}
	}
	p, err := in.Lookup(pid)
	if errors.Is(err, ErrNotFound) {
		return Dead, Proc{}
	}
	if err != nil {
		return Unknown, Proc{}
	}
	if p.Start != start {
		return Dead, p
	}
	return Alive, p
}
