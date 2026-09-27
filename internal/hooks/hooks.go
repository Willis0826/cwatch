// Package hooks parses Claude Code hook input and normalizes it into events.
//
// The parser reads the top-level JSON object key by key. It keeps only the
// fields that the monitor needs and skips all other fields. It does not keep
// tool input, tool output, or the raw payload.
package hooks

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"cwatch/internal/textutil"
)

// Kind is a normalized event kind.
type Kind string

// Normalized event kinds.
const (
	KindSessionStart    Kind = "session_start"
	KindPromptSubmit    Kind = "prompt_submit"
	KindPreTool         Kind = "pre_tool"
	KindPostTool        Kind = "post_tool"
	KindPostToolFailure Kind = "post_tool_failure"
	KindPermission      Kind = "permission_request"
	KindNotification    Kind = "notification"
	KindStop            Kind = "stop"
	KindStopFailure     Kind = "stop_failure"
	KindSessionEnd      Kind = "session_end"
	KindUnknown         Kind = "unknown"
)

// Events lists the Claude Code hook events that setup registers.
var Events = []string{
	"SessionStart",
	"UserPromptSubmit",
	"PreToolUse",
	"PostToolUse",
	"PostToolUseFailure",
	"PermissionRequest",
	"Notification",
	"Stop",
	"StopFailure",
	"SessionEnd",
}

var kindByName = map[string]Kind{
	"SessionStart":       KindSessionStart,
	"UserPromptSubmit":   KindPromptSubmit,
	"PreToolUse":         KindPreTool,
	"PostToolUse":        KindPostTool,
	"PostToolUseFailure": KindPostToolFailure,
	"PermissionRequest":  KindPermission,
	"Notification":       KindNotification,
	"Stop":               KindStop,
	"StopFailure":        KindStopFailure,
	"SessionEnd":         KindSessionEnd,
}

// Excerpt and detail limits, in runes.
const (
	MaxPromptExcerpt    = 1000
	MaxAssistantExcerpt = 2000
	MaxDetail           = 300
	MaxIdentifier       = 200
	MaxPath             = 4096
)

// Event is a normalized hook event. It holds no tool input or tool output.
type Event struct {
	HookEventName    string `json:"hook_event_name"`
	Kind             Kind   `json:"kind"`
	SessionID        string `json:"session_id"`
	PromptID         string `json:"prompt_id,omitempty"`
	TranscriptPath   string `json:"transcript_path,omitempty"`
	Cwd              string `json:"cwd,omitempty"`
	PermissionMode   string `json:"permission_mode,omitempty"`
	AgentID          string `json:"agent_id,omitempty"`
	AgentType        string `json:"agent_type,omitempty"`
	ToolName         string `json:"tool_name,omitempty"`
	ToolUseID        string `json:"tool_use_id,omitempty"`
	Source           string `json:"source,omitempty"`            // SessionStart
	Reason           string `json:"reason,omitempty"`            // SessionEnd
	NotificationType string `json:"notification_type,omitempty"` // Notification
	Message          string `json:"message,omitempty"`           // Notification (bounded)
	ErrorType        string `json:"error_type,omitempty"`        // StopFailure
	ErrorDetail      string `json:"error_detail,omitempty"`      // StopFailure or PostToolUseFailure (bounded)
	IsInterrupt      bool   `json:"is_interrupt,omitempty"`      // PostToolUseFailure
	Prompt           string `json:"-"`                           // UserPromptSubmit (bounded)
	AssistantMessage string `json:"-"`                           // Stop (bounded)
	Truncated        bool   `json:"truncated,omitempty"`         // input exceeded the read limit

	errorText string
}

// IsSubagent reports whether the event comes from a subagent.
func (e Event) IsSubagent() bool { return e.AgentID != "" }

// ErrNoSession means the input has no session identifier.
var ErrNoSession = errors.New("hook input has no session_id")

// Parse decodes hook JSON. When truncated is true, the input is a prefix of
// a larger payload. Parse then keeps the fields that it decoded before the
// end of the prefix.
func Parse(data []byte, truncated bool) (Event, error) {
	var ev Event
	ev.Truncated = truncated
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return ev, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return ev, errors.New("hook input is not a JSON object")
	}
	var parseErr error
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			parseErr = err
			break
		}
		key, _ := tok.(string)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			parseErr = err
			break
		}
		assign(&ev, key, raw)
	}
	if parseErr != nil && !truncated {
		// A malformed payload. Keep the fields that decoded, but report the
		// error when the payload has no session.
		if ev.SessionID == "" {
			return ev, parseErr
		}
	}
	ev.Kind = kindByName[ev.HookEventName]
	if ev.Kind == "" {
		ev.Kind = KindUnknown
	}
	if ev.errorText != "" {
		if ev.Kind == KindStopFailure {
			ev.ErrorType = textutil.OneLine(ev.errorText, MaxIdentifier)
		} else if ev.ErrorDetail == "" {
			ev.ErrorDetail = textutil.OneLine(ev.errorText, MaxDetail)
		}
		ev.errorText = ""
	}
	if ev.SessionID == "" {
		return ev, ErrNoSession
	}
	return ev, nil
}

func str(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}

func ident(raw json.RawMessage) string { return textutil.OneLine(str(raw), MaxIdentifier) }

func assign(ev *Event, key string, raw json.RawMessage) {
	switch key {
	case "hook_event_name":
		ev.HookEventName = ident(raw)
	case "session_id":
		ev.SessionID = ident(raw)
	case "prompt_id":
		ev.PromptID = ident(raw)
	case "transcript_path":
		ev.TranscriptPath = textutil.Truncate(textutil.Sanitize(str(raw), false), MaxPath)
	case "cwd":
		ev.Cwd = textutil.Truncate(textutil.Sanitize(str(raw), false), MaxPath)
	case "permission_mode":
		ev.PermissionMode = ident(raw)
	case "agent_id":
		ev.AgentID = ident(raw)
	case "agent_type":
		ev.AgentType = ident(raw)
	case "tool_name":
		ev.ToolName = ident(raw)
	case "tool_use_id":
		ev.ToolUseID = ident(raw)
	case "source":
		ev.Source = ident(raw)
	case "reason":
		ev.Reason = ident(raw)
	case "notification_type":
		ev.NotificationType = ident(raw)
	case "message":
		ev.Message = textutil.OneLine(str(raw), MaxDetail)
	case "error":
		// StopFailure sends an error type. PostToolUseFailure sends a text.
		// The event name decides the meaning after all keys are read.
		ev.errorText = str(raw)
	case "error_details":
		ev.ErrorDetail = textutil.OneLine(str(raw), MaxDetail)
	case "is_interrupt":
		var b bool
		_ = json.Unmarshal(raw, &b)
		ev.IsInterrupt = b
	case "prompt":
		ev.Prompt = textutil.Bounded(str(raw), MaxPromptExcerpt)
	case "last_assistant_message":
		ev.AssistantMessage = textutil.Bounded(str(raw), MaxAssistantExcerpt)
	}
}

// ReadLimited reads at most limit bytes from r. It then discards the rest
// of r so that the writer does not get a broken pipe. It reports whether the
// input was longer than limit.
func ReadLimited(r io.Reader, limit int64) ([]byte, bool, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return data, false, err
	}
	if int64(len(data)) > limit {
		_, _ = io.Copy(io.Discard, r)
		return data[:limit], true, nil
	}
	return data, false, nil
}
