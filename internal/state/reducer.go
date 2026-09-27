package state

import (
	"time"

	"cwatch/internal/hooks"
	"cwatch/internal/textutil"
)

// IdleNotificationGrace is the time after a prompt submission in which an
// idle_prompt notification is treated as stale. Claude Code sends
// idle_prompt about 60 seconds after a response, so a notification that
// arrives just after a new prompt belongs to the earlier turn.
const IdleNotificationGrace = 10 * time.Second

// Meta holds values that the reducer needs but that are not in the event.
type Meta struct {
	Seq           int64
	At            time.Time
	StoreExcerpts bool
}

// Reduce applies one event to an instance and returns the new instance.
// Reduce is a pure function: it does no I/O.
func Reduce(in Instance, ev hooks.Event, m Meta) Instance {
	out := in
	out.Pending = append([]Pending(nil), in.Pending...)
	out.LastEvent = ev.HookEventName
	out.LastEventAt = m.At
	out.LastSeq = m.Seq
	out.UpdatedAt = m.At
	if ev.TranscriptPath != "" {
		out.TranscriptPath = ev.TranscriptPath
	}
	if ev.Cwd != "" && !ev.IsSubagent() {
		out.Cwd = ev.Cwd
	}
	if ev.PermissionMode != "" && !ev.IsSubagent() {
		out.PermissionMode = ev.PermissionMode
	}

	if out.State == Ended {
		return reduceEnded(out, ev, m)
	}

	switch ev.Kind {
	case hooks.KindSessionStart:
		// A compaction can start a new SessionStart during a turn. Keep the
		// current state for it. Other sources start a new conversation view.
		if ev.Source != "compact" || out.State == "" {
			out.State = Idle
			out.Pending = nil
			out.CurrentTool, out.CurrentToolUseID, out.CurrentToolAgent = "", "", ""
		}
		out.Reason = ev.Source

	case hooks.KindPromptSubmit:
		out.State = Working
		out.Reason = ""
		out.Pending = nil
		out.ErrorType, out.ErrorDetail = "", ""
		out.LastToolError = ""
		out.CurrentTool, out.CurrentToolUseID, out.CurrentToolAgent = "", "", ""
		out.LastPromptAt = m.At
		out.LastPromptID = ev.PromptID
		if m.StoreExcerpts && ev.Prompt != "" {
			out.PromptExcerpt = ev.Prompt
		}

	case hooks.KindPreTool:
		if ev.IsSubagent() {
			out.SubagentAt = m.At
			// A subagent that runs after the main turn stopped does not make
			// the main session busy again.
			if out.State != Working && out.State != NeedsPermission {
				break
			}
			out.Pending = removePending(out.Pending, ev.ToolName, ev.AgentID, true)
		} else {
			// Permission prompts on the main thread are modal. A new main
			// tool call means that the earlier prompt was answered. A
			// permission_prompt notification restores the state if a prompt
			// still waits.
			out.Pending = removeAgentPending(out.Pending, "")
			if out.State == Idle || out.State == Error {
				out.Reason = ""
			}
			out.State = Working
		}
		out.CurrentTool = ev.ToolName
		out.CurrentToolUseID = ev.ToolUseID
		out.CurrentToolAgent = ev.AgentID
		if len(out.Pending) > 0 {
			out.State = NeedsPermission
		}

	case hooks.KindPermission:
		if ev.IsSubagent() {
			out.SubagentAt = m.At
		}
		out.Pending = addPending(out.Pending, Pending{ToolName: ev.ToolName, AgentID: ev.AgentID, Source: "permission_request", Since: m.At})
		out.State = NeedsPermission
		out.Reason = ""

	case hooks.KindPostTool, hooks.KindPostToolFailure:
		if ev.IsSubagent() {
			out.SubagentAt = m.At
		}
		out.Pending = removePending(out.Pending, ev.ToolName, ev.AgentID, false)
		if ev.ToolUseID != "" && ev.ToolUseID == out.CurrentToolUseID || ev.ToolUseID == "" && ev.ToolName == out.CurrentTool {
			out.CurrentTool, out.CurrentToolUseID, out.CurrentToolAgent = "", "", ""
		}
		if ev.Kind == hooks.KindPostToolFailure {
			// A failed tool is recorded. Claude can recover from it, so the
			// turn continues.
			detail := ev.ErrorDetail
			if ev.IsInterrupt {
				detail = "interrupted: " + detail
			}
			out.LastToolError = textutil.OneLine(ev.ToolName+": "+detail, hooks.MaxDetail)
		}
		switch {
		case out.State == NeedsPermission && len(out.Pending) == 0:
			out.State = Working
		case out.State == Working || out.State == NeedsPermission:
			// Keep the state.
		}
		// A late tool event does not revive an idle or failed session.

	case hooks.KindNotification:
		out.LastNotification = textutil.OneLine(ev.NotificationType+": "+ev.Message, hooks.MaxDetail)
		switch ev.NotificationType {
		case "permission_prompt":
			if out.State == Working || out.State == NeedsPermission {
				if len(out.Pending) == 0 {
					out.Pending = addPending(out.Pending, Pending{Source: "notification", Since: m.At})
				}
				out.State = NeedsPermission
			}
		case "idle_prompt":
			stale := !out.LastPromptAt.IsZero() && m.At.Sub(out.LastPromptAt) < IdleNotificationGrace
			if (out.State == Working || out.State == NeedsPermission) && !stale {
				out.State = Idle
				out.Reason = "idle_notification"
				out.Pending = nil
				out.CurrentTool, out.CurrentToolUseID, out.CurrentToolAgent = "", "", ""
			}
		}

	case hooks.KindStop:
		if ev.IsSubagent() {
			// A subagent stop never marks the main session idle.
			out.SubagentAt = m.At
			break
		}
		out.State = Idle
		out.Reason = ""
		out.Pending = nil
		out.CurrentTool, out.CurrentToolUseID, out.CurrentToolAgent = "", "", ""
		if m.StoreExcerpts && ev.AssistantMessage != "" {
			out.AssistantExcerpt = ev.AssistantMessage
		}

	case hooks.KindStopFailure:
		if ev.IsSubagent() {
			out.SubagentAt = m.At
			break
		}
		out.State = Error
		out.Reason = "api_error"
		out.ErrorType = ev.ErrorType
		out.ErrorDetail = ev.ErrorDetail
		out.Pending = nil
		out.CurrentTool, out.CurrentToolUseID, out.CurrentToolAgent = "", "", ""

	case hooks.KindSessionEnd:
		out = markEnded(out, ReasonSessionEnd+":"+ev.Reason, m.At)

	default:
		// Unknown events change no state.
	}
	return out
}

// reduceEnded handles events for an ended instance. Only a new session
// start or a new prompt in the same process revives it. A late notification
// or tool event does not.
func reduceEnded(out Instance, ev hooks.Event, m Meta) Instance {
	if out.Reason == ReasonProcessExit {
		return out
	}
	switch ev.Kind {
	case hooks.KindSessionStart, hooks.KindPromptSubmit:
		out.State = Idle
		out.Reason = ""
		out.EndedAt = time.Time{}
		return Reduce(out, ev, m)
	}
	return out
}

func markEnded(out Instance, reason string, at time.Time) Instance {
	out.State = Ended
	out.Reason = reason
	out.EndedAt = at
	out.Pending = nil
	out.CurrentTool, out.CurrentToolUseID, out.CurrentToolAgent = "", "", ""
	return out
}

// MarkProcessExit marks an instance ended because its process is gone.
func MarkProcessExit(in Instance, at time.Time) Instance {
	out := markEnded(in, ReasonProcessExit, at)
	out.Liveness = Dead
	out.UpdatedAt = at
	return out
}

func addPending(list []Pending, p Pending) []Pending {
	for _, q := range list {
		if q.ToolName == p.ToolName && q.AgentID == p.AgentID {
			return list
		}
	}
	return append(list, p)
}

// removePending removes the first pending entry for the tool and agent. It
// also removes notification entries, which name no tool. When anyForAgent is
// true, it removes all entries of the agent.
func removePending(list []Pending, tool, agent string, anyForAgent bool) []Pending {
	out := list[:0]
	removed := false
	for _, p := range list {
		switch {
		case p.AgentID == agent && anyForAgent:
			continue
		case p.AgentID == agent && p.Source == "notification":
			continue
		case !removed && p.AgentID == agent && p.ToolName == tool:
			removed = true
			continue
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func removeAgentPending(list []Pending, agent string) []Pending {
	return removePending(list, "", agent, true)
}
