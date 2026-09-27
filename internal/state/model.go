// Package state holds the instance model, the pure state reducer, and the
// SQLite store.
package state

import (
	"time"

	"cwatch/internal/transcript"
)

// SchemaVersion is the version of the stored data and of the JSON output.
const SchemaVersion = 1

// State is an observable lifecycle state. It never asserts that a task
// succeeded.
type State string

// Lifecycle states.
const (
	Idle            State = "idle"
	Working         State = "working"
	NeedsPermission State = "needs_permission"
	Error           State = "error"
	Ended           State = "ended"
)

// Liveness tells whether the owning process is known to run.
type Liveness string

// Liveness values.
const (
	Alive   Liveness = "alive"
	Dead    Liveness = "dead"
	Unknown Liveness = "unknown"
)

// Ended reasons.
const (
	ReasonSessionEnd  = "session_end"
	ReasonProcessExit = "process_exit"
	ReasonSuperseded  = "superseded"
)

// Terminal kinds.
const (
	TerminalITerm   = "iterm2"
	TerminalTmux    = "tmux"
	TerminalSSH     = "ssh"
	TerminalOther   = "other"
	TerminalUnknown = "unknown"
)

// Pending is a permission request that waits for the user.
type Pending struct {
	ToolName string    `json:"tool_name,omitempty"`
	AgentID  string    `json:"agent_id,omitempty"`
	Source   string    `json:"source"` // permission_request or notification
	Since    time.Time `json:"since"`
}

// Instance is one Claude Code process running one conversation in one
// terminal. It is not the same as a Claude session ID.
type Instance struct {
	SchemaVersion int    `json:"schema_version"`
	InstanceID    string `json:"instance_id"`
	SessionID     string `json:"session_id"`
	Cwd           string `json:"cwd"`
	Project       string `json:"project"`
	Branch        string `json:"branch,omitempty"` // filled at read time, never by the hook

	OwnerPID    int    `json:"owner_pid,omitempty"`
	OwnerStart  int64  `json:"owner_start_us,omitempty"`
	OwnerMethod string `json:"owner_method"`
	TTY         string `json:"tty,omitempty"`

	TerminalKind    string `json:"terminal_kind"`
	TerminalProgram string `json:"terminal_program,omitempty"`
	ITermSessionID  string `json:"iterm_session_id,omitempty"`

	State    State    `json:"state"`
	Reason   string   `json:"reason,omitempty"`
	Liveness Liveness `json:"liveness"`

	LastEvent      string    `json:"last_event"`
	LastEventAt    time.Time `json:"last_event_at"`
	LastSeq        int64     `json:"last_seq"`
	LastPromptID   string    `json:"last_prompt_id,omitempty"`
	LastPromptAt   time.Time `json:"last_prompt_at,omitempty"`
	PermissionMode string    `json:"permission_mode,omitempty"`

	CurrentTool      string    `json:"current_tool,omitempty"`
	CurrentToolUseID string    `json:"current_tool_use_id,omitempty"`
	CurrentToolAgent string    `json:"current_tool_agent_id,omitempty"`
	LastToolError    string    `json:"last_tool_error,omitempty"`
	LastNotification string    `json:"last_notification,omitempty"`
	SubagentAt       time.Time `json:"subagent_activity_at,omitempty"`
	Pending          []Pending `json:"pending_permissions,omitempty"`

	ErrorType   string `json:"error_type,omitempty"`
	ErrorDetail string `json:"error_detail,omitempty"`

	TranscriptPath string `json:"transcript_path,omitempty"`
	PromptExcerpt  string `json:"prompt_excerpt,omitempty"`
	// Tokens is filled at read time from the transcript, never by the hook.
	Tokens           *transcript.Usage `json:"tokens,omitempty"`
	AssistantExcerpt string            `json:"assistant_excerpt,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	EndedAt   time.Time `json:"ended_at,omitempty"`
}

// ShortID returns the first 8 characters of the instance ID.
func (in Instance) ShortID() string {
	if len(in.InstanceID) > 8 {
		return in.InstanceID[:8]
	}
	return in.InstanceID
}

// Live reports whether a default listing shows the instance.
func (in Instance) Live() bool {
	return in.State != Ended && in.Liveness != Dead
}

// AttentionRank orders instances for display: attention first, then
// working, then idle, then ended.
func (in Instance) AttentionRank() int {
	switch in.State {
	case NeedsPermission, Error:
		return 0
	case Working:
		return 1
	case Idle:
		return 2
	default:
		return 3
	}
}
