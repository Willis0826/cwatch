// Package summary collects the work of past Claude Code sessions from the
// transcripts into a compact digest. It reads the transcripts as data only.
// It makes no model calls.
package summary

import (
	"fmt"
	"time"
)

// Range kinds.
const (
	KindYesterday = "yesterday"
	KindWeek      = "week"
	KindThisWeek  = "this-week"
)

// Range is a time range: Start is included, End is not. A range of kind
// KindThisWeek ends at the time of its creation. All other ranges are in the
// past.
type Range struct {
	Kind  string
	Start time.Time
	End   time.Time
}

// Yesterday returns the local calendar day before the day of now.
func Yesterday(now time.Time) Range {
	y, m, d := now.Date()
	loc := now.Location()
	return Range{
		Kind:  KindYesterday,
		Start: time.Date(y, m, d-1, 0, 0, 0, 0, loc),
		End:   time.Date(y, m, d, 0, 0, 0, 0, loc),
	}
}

// LastWeek returns the calendar week before the week of now. A week starts
// on Monday at 00:00 local time.
func LastWeek(now time.Time) Range {
	y, m, d := now.Date()
	loc := now.Location()
	sinceMonday := (int(now.Weekday()) + 6) % 7
	return Range{
		Kind:  KindWeek,
		Start: time.Date(y, m, d-sinceMonday-7, 0, 0, 0, 0, loc),
		End:   time.Date(y, m, d-sinceMonday, 0, 0, 0, 0, loc),
	}
}

// ThisWeek returns the calendar week of now, from Monday at 00:00 local time
// until now.
func ThisWeek(now time.Time) Range {
	y, m, d := now.Date()
	sinceMonday := (int(now.Weekday()) + 6) % 7
	return Range{
		Kind:  KindThisWeek,
		Start: time.Date(y, m, d-sinceMonday, 0, 0, 0, 0, now.Location()),
		End:   now,
	}
}

// Parse returns the range of a kind name.
func Parse(kind string, now time.Time) (Range, error) {
	switch kind {
	case KindYesterday:
		return Yesterday(now), nil
	case KindWeek:
		return LastWeek(now), nil
	case KindThisWeek:
		return ThisWeek(now), nil
	}
	return Range{}, fmt.Errorf("unknown range %q (use %q, %q, or %q)", kind, KindYesterday, KindWeek, KindThisWeek)
}

// Ongoing reports whether the range is not finished. cwatch does not store
// the summary of an ongoing range, because new work changes it.
func (r Range) Ongoing() bool {
	return r.Kind == KindThisWeek
}

// Contains reports whether t is in the range.
func (r Range) Contains(t time.Time) bool {
	return !t.Before(r.Start) && t.Before(r.End)
}

// lastDay returns the start of the last day in the range.
func (r Range) lastDay() time.Time {
	y, m, d := r.End.Date()
	return time.Date(y, m, d-1, 0, 0, 0, 0, r.End.Location())
}

// Label returns a name for people, such as "yesterday (Fri 26 Sep)".
func (r Range) Label() string {
	switch r.Kind {
	case KindWeek:
		return fmt.Sprintf("last week (%s – %s)", r.Start.Format("Mon 2 Jan"), r.lastDay().Format("Mon 2 Jan"))
	case KindThisWeek:
		return fmt.Sprintf("this week (%s – %s)", r.Start.Format("Mon 2 Jan"), r.End.Format("Mon 2 Jan 15:04"))
	}
	return fmt.Sprintf("%s (%s)", r.Kind, r.Start.Format("Mon 2 Jan"))
}

// CacheName returns the file name of the stored summary for the range.
func (r Range) CacheName() string {
	return fmt.Sprintf("%s-%s.v%d.md", r.Kind, r.Start.Format("2006-01-02"), FormatVersion)
}
