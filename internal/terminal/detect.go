// Package terminal identifies the terminal of a Claude Code process and
// focuses iTerm2 panes.
package terminal

import (
	"regexp"
	"strings"

	"cwatch/internal/textutil"
)

// Terminal kinds. They match the values in package state.
const (
	KindITerm   = "iterm2"
	KindTmux    = "tmux"
	KindSSH     = "ssh"
	KindOther   = "other"
	KindUnknown = "unknown"
)

// Info describes the terminal that the hook environment reports.
type Info struct {
	Kind           string
	Program        string
	ITermSessionID string // the pane UUID, without the "w0t1p0:" prefix
}

var uuidRE = regexp.MustCompile(`^[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}$`)

// ParseITermSessionID extracts the pane UUID from an ITERM_SESSION_ID value
// such as "w0t1p0:3017789F-C400-468F-A7A8-5EBDC5461A48". It returns "" for
// a value that does not have this form.
func ParseITermSessionID(v string) string {
	if i := strings.LastIndexByte(v, ':'); i >= 0 {
		v = v[i+1:]
	}
	if !uuidRE.MatchString(v) {
		return ""
	}
	return strings.ToUpper(v)
}

// Detect reads the terminal variables that Claude Code passes to hooks. It
// reads only these named variables. It does not store the environment.
func Detect(getenv func(string) string) Info {
	info := Info{Program: textutil.OneLine(getenv("TERM_PROGRAM"), 64)}
	switch {
	case getenv("TMUX") != "":
		info.Kind = KindTmux
	case getenv("SSH_CONNECTION") != "" || getenv("SSH_TTY") != "":
		info.Kind = KindSSH
	case getenv("ITERM_SESSION_ID") != "":
		info.ITermSessionID = ParseITermSessionID(getenv("ITERM_SESSION_ID"))
		if info.ITermSessionID != "" {
			info.Kind = KindITerm
		} else {
			info.Kind = KindUnknown
		}
	case info.Program != "":
		info.Kind = KindOther
	default:
		info.Kind = KindUnknown
	}
	return info
}

var ttyRE = regexp.MustCompile(`^/dev/ttys?[0-9]{1,4}$`)

// ValidTTY reports whether tty is a well-formed macOS terminal path.
func ValidTTY(tty string) bool { return ttyRE.MatchString(tty) }
