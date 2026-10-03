package app

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"cwatch/internal/state"
	"cwatch/internal/textutil"
)

// StateLabel returns the display label of the state. A "?" marks an
// instance whose process liveness is unknown.
func StateLabel(in state.Instance) string {
	s := string(in.State)
	if in.State == state.NeedsPermission {
		s = "permission"
	}
	if in.State != state.Ended && in.Liveness == state.Unknown {
		s += "?"
	}
	return s
}

// StateIcon returns the emoji of a state.
func StateIcon(st state.State) string {
	switch st {
	case state.NeedsPermission:
		return "🟡"
	case state.Working:
		return "🟢"
	case state.Running:
		return "🔵"
	case state.Idle:
		return "⚪"
	case state.Error:
		return "🔴"
	case state.Ended:
		return "⚫"
	}
	return "❔"
}

// StateCounts counts the instances by state, for example "🟡1 🟢2 ⚪1".
// The states that need attention come first. Ended instances are not
// counted. The result is empty when no instance is live.
func StateCounts(list []state.Instance) string {
	counts := map[state.State]int{}
	for _, in := range list {
		counts[in.State]++
	}
	var parts []string
	for _, st := range []state.State{state.NeedsPermission, state.Error, state.Working, state.Running, state.Idle} {
		if n := counts[st]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s%d", StateIcon(st), n))
		}
	}
	return strings.Join(parts, " ")
}

// Activity returns the current tool or the latest message excerpt. Quoted
// text is an excerpt of a real message, not a summary.
func Activity(in state.Instance, excerpts bool) string {
	switch in.State {
	case state.NeedsPermission:
		tools := []string{}
		for _, p := range in.Pending {
			if p.ToolName != "" {
				tools = append(tools, p.ToolName)
			}
		}
		if len(tools) > 0 {
			return "waits for permission: " + strings.Join(tools, ", ")
		}
		return "waits for permission"
	case state.Working:
		if in.CurrentTool != "" {
			s := "tool: " + in.CurrentTool
			if in.CurrentToolAgent != "" {
				s += " (subagent)"
			}
			return s
		}
		if excerpts && in.PromptExcerpt != "" {
			return "you: \"" + textutil.OneLine(in.PromptExcerpt, 200) + "\""
		}
		return "responding"
	case state.Running:
		return "background: " + in.BackgroundSummary()
	case state.Error:
		s := "API error"
		if in.ErrorType != "" {
			s += ": " + in.ErrorType
		}
		return s
	case state.Idle:
		if excerpts && in.AssistantExcerpt != "" {
			return "claude: \"" + textutil.OneLine(in.AssistantExcerpt, 200) + "\""
		}
		if excerpts && in.PromptExcerpt != "" {
			return "you: \"" + textutil.OneLine(in.PromptExcerpt, 200) + "\""
		}
		return ""
	case state.Ended:
		return "ended (" + in.Reason + ")"
	}
	return ""
}

// Age formats the time since t.
func Age(now, t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := now.Sub(t)
	switch {
	case d < 0:
		return "0s"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// Tokens formats a token count: 950, 12.3k, 341k, 1.2M.
func Tokens(n int64) string {
	switch {
	case n < 1000:
		return fmt.Sprintf("%d", n)
	case n < 100_000:
		return trimZero(fmt.Sprintf("%.1f", float64(n)/1000)) + "k"
	case n < 1_000_000:
		return fmt.Sprintf("%dk", (n+500)/1000)
	default:
		return trimZero(fmt.Sprintf("%.1f", float64(n)/1_000_000)) + "M"
	}
}

func trimZero(s string) string { return strings.TrimSuffix(s, ".0") }

// ContextSize returns the context size of an instance, or "—" when the
// transcript gives no usage.
func ContextSize(in state.Instance) string {
	if in.Tokens == nil {
		return "—"
	}
	s := Tokens(in.Tokens.Context)
	if in.Tokens.Partial {
		s += "…"
	}
	return s
}

// ShortTTY returns "ttys003" for "/dev/ttys003".
func ShortTTY(tty string) string {
	if tty == "" {
		return "-"
	}
	return filepath.Base(tty)
}

// Project returns the display name of the working directory.
func Project(in state.Instance) string {
	if in.Project != "" {
		return textutil.OneLine(in.Project, 60)
	}
	if in.Cwd != "" {
		return textutil.OneLine(filepath.Base(in.Cwd), 60)
	}
	return "-"
}

// Column widths of the plain table.
type columns struct{ state, project, branch, activity, age, ctx, id int }

func layout(width int) columns {
	c := columns{state: 12, project: 18, branch: 14, age: 4, ctx: 7, id: 8}
	fixed := c.state + c.project + c.branch + c.age + c.ctx + c.id + 6*2
	c.activity = width - fixed
	if c.activity < 20 {
		// Drop the branch column on narrow terminals.
		c.activity += c.branch + 2
		c.branch = 0
	}
	if c.activity < 12 {
		c.activity = 12
	}
	return c
}

func pad(s string, w int) string {
	s = textutil.Truncate(s, w)
	n := len([]rune(s))
	if n < w {
		s += strings.Repeat(" ", w-n)
	}
	return s
}

// TableHeader returns the header row for width.
func TableHeader(width int) string {
	c := layout(width)
	return row(c, "STATE", "PROJECT", "BRANCH", "TOOL / EXCERPT", "AGE", "CONTEXT", "ID")
}

// TableRow returns one table row for width.
func TableRow(in state.Instance, now time.Time, width int, excerpts bool) string {
	c := layout(width)
	return row(c, StateLabel(in), Project(in), textutil.OneLine(in.Branch, 40),
		textutil.OneLine(Activity(in, excerpts), 400), Age(now, in.LastEventAt),
		ContextSize(in), textutil.OneLine(in.ShortID(), 16))
}

func row(c columns, st, project, branch, activity, age, ctx, id string) string {
	parts := []string{pad(st, c.state), pad(project, c.project)}
	if c.branch > 0 {
		parts = append(parts, pad(branch, c.branch))
	}
	parts = append(parts, pad(activity, c.activity), pad(age, c.age), pad(ctx, c.ctx), pad(id, c.id))
	return strings.TrimRight(strings.Join(parts, "  "), " ")
}

// WriteTable writes the plain table.
func WriteTable(w io.Writer, list []state.Instance, now time.Time, width int, excerpts bool) {
	fmt.Fprintln(w, TableHeader(width))
	for _, in := range list {
		fmt.Fprintln(w, TableRow(in, now, width, excerpts))
	}
	for _, in := range list {
		if in.State != state.Ended && in.Liveness == state.Unknown {
			fmt.Fprintln(w, "\n? = the owning process could not be identified, so cwatch cannot confirm that it runs.")
			break
		}
	}
}

// ListOutput is the JSON document of "cwatch list --json".
type ListOutput struct {
	SchemaVersion int              `json:"schema_version"`
	GeneratedAt   time.Time        `json:"generated_at"`
	Status        Status           `json:"status"`
	Instances     []state.Instance `json:"instances"`
}

// WriteJSON writes the JSON listing.
func WriteJSON(w io.Writer, snap Snapshot, now time.Time, excerpts bool) error {
	list := make([]state.Instance, 0, len(snap.Instances))
	for _, in := range snap.Instances {
		if !excerpts {
			in.PromptExcerpt, in.AssistantExcerpt = "", ""
		}
		list = append(list, in)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(ListOutput{SchemaVersion: state.SchemaVersion, GeneratedAt: now.UTC(), Status: snap.Status, Instances: list})
}
