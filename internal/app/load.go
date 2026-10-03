package app

import (
	"context"
	"errors"
	"os"
	"sort"

	"cwatch/internal/gitinfo"
	"cwatch/internal/setup"
	"cwatch/internal/state"
	"cwatch/internal/transcript"
)

// Status explains an empty or partial listing.
type Status string

// Status values.
const (
	StatusOK            Status = "ok"
	StatusHooksAbsent   Status = "hooks_absent"
	StatusNoEvents      Status = "no_events"
	StatusNoLive        Status = "no_live_sessions"
	StatusUnavailable   Status = "state_unavailable"
	StatusUnsupportedOS Status = "unsupported_platform"
)

// Snapshot is one reconciled view of the store.
type Snapshot struct {
	Instances      []state.Instance
	Status         Status
	HooksInstalled bool
	Err            error
}

// LoadOptions select what Load returns.
type LoadOptions struct {
	All      bool // include ended instances
	Prune    bool // delete old history
	NoTokens bool // do not read token usage from the transcripts
}

// Load reads, reconciles, and sorts the instances. It creates no files
// when the state directory does not exist.
func (e *Env) Load(ctx context.Context, o LoadOptions) Snapshot {
	var snap Snapshot
	snap.HooksInstalled = e.hooksInstalled()
	st, err := state.OpenExisting(ctx, e.StateDir)
	if errors.Is(err, os.ErrNotExist) {
		snap.Status = StatusNoEvents
		if !snap.HooksInstalled {
			snap.Status = StatusHooksAbsent
		}
		return snap
	}
	if err != nil {
		snap.Status, snap.Err = StatusUnavailable, err
		return snap
	}
	defer st.Close()
	if o.Prune {
		_ = st.Prune(ctx, e.Now())
	}
	list, err := st.List(ctx)
	if err != nil {
		snap.Status, snap.Err = StatusUnavailable, err
		return snap
	}
	list = state.Reconcile(ctx, st, e.Inspector, list, e.Now())
	var branches gitinfo.Cache
	usage := e.Usage
	if usage == nil {
		usage = transcript.NewUsageTracker()
	}
	keep := map[string]bool{}
	var out []state.Instance
	for _, in := range list {
		if !o.All && !in.Live() {
			continue
		}
		if in.Cwd != "" {
			in.Branch = branches.Get(in.Cwd)
		}
		if in.TranscriptPath != "" && !o.NoTokens {
			keep[in.TranscriptPath] = true
			if u, err := usage.Update(in.TranscriptPath); err == nil && u.Responses > 0 {
				in.Tokens = &u
			}
		}
		out = append(out, in)
	}
	usage.Forget(keep)
	SortInstances(out)
	snap.Instances = out
	switch {
	case len(out) > 0:
		snap.Status = StatusOK
	case len(list) == 0 && !snap.HooksInstalled:
		snap.Status = StatusHooksAbsent
	case len(list) == 0:
		snap.Status = StatusNoEvents
	default:
		snap.Status = StatusNoLive
	}
	return snap
}

func (e *Env) hooksInstalled() bool {
	data, _, err := setup.ReadSettings(e.SettingsFile)
	if err != nil {
		return false
	}
	inst, err := setup.Installed(data)
	return err == nil && len(inst) > 0
}

// SortInstances groups attention-needed instances first, then working, then
// idle, then ended. Inside a group, the creation time decides the order, so
// rows do not move on every tool event.
func SortInstances(list []state.Instance) {
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if ra, rb := a.AttentionRank(), b.AttentionRank(); ra != rb {
			return ra < rb
		}
		if !a.CreatedAt.Equal(b.CreatedAt) {
			return a.CreatedAt.Before(b.CreatedAt)
		}
		return a.InstanceID < b.InstanceID
	})
}

// StatusMessage explains a status to the user.
func StatusMessage(s Status, err error) string {
	switch s {
	case StatusHooksAbsent:
		return "cwatch hooks are not installed. Run \"cwatch setup --dry-run\" to see the changes, then \"cwatch setup\"."
	case StatusNoEvents:
		return "Hooks are installed, but no events were recorded yet. Restart running Claude Code sessions, then submit a prompt."
	case StatusNoLive:
		return "No live Claude Code sessions. Use \"cwatch list --all\" to show ended instances."
	case StatusUnavailable:
		if err != nil {
			return "The state is unavailable: " + err.Error()
		}
		return "The state is unavailable."
	case StatusUnsupportedOS:
		return "This platform is not supported. cwatch supports macOS with iTerm2."
	}
	return ""
}
