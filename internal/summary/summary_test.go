package summary

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestYesterday(t *testing.T) {
	loc := time.FixedZone("X", 8*3600)
	r := Yesterday(time.Date(2026, 9, 1, 0, 30, 0, 0, loc))
	if !r.Start.Equal(time.Date(2026, 8, 31, 0, 0, 0, 0, loc)) || !r.End.Equal(time.Date(2026, 9, 1, 0, 0, 0, 0, loc)) {
		t.Fatalf("range %v – %v", r.Start, r.End)
	}
	if got := r.Label(); got != "yesterday (Mon 31 Aug)" {
		t.Fatalf("label %q", got)
	}
	if got := r.CacheName(); got != "yesterday-2026-08-31.v2.md" {
		t.Fatalf("cache name %q", got)
	}
}

func TestLastWeek(t *testing.T) {
	loc := time.UTC
	for _, c := range []struct {
		now   time.Time
		start string
	}{
		{time.Date(2026, 9, 21, 9, 0, 0, 0, loc), "2026-09-14"},  // Monday
		{time.Date(2026, 9, 27, 23, 0, 0, 0, loc), "2026-09-14"}, // Sunday
		{time.Date(2026, 9, 23, 12, 0, 0, 0, loc), "2026-09-14"}, // Wednesday
	} {
		r := LastWeek(c.now)
		if got := r.Start.Format("2006-01-02"); got != c.start {
			t.Errorf("%v: start %s, want %s", c.now, got, c.start)
		}
		if r.Start.Weekday() != time.Monday || r.End.Sub(r.Start) != 7*24*time.Hour {
			t.Errorf("%v: range %v – %v", c.now, r.Start, r.End)
		}
	}
	if got := LastWeek(time.Date(2026, 9, 23, 0, 0, 0, 0, loc)).Label(); got != "last week (Mon 14 Sep – Sun 20 Sep)" {
		t.Fatalf("label %q", got)
	}
}

func TestLastWeekDST(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skip("no time zone data")
	}
	// The clocks go back on Sun 25 Oct 2026.
	r := LastWeek(time.Date(2026, 10, 28, 12, 0, 0, 0, loc))
	if r.Start.Hour() != 0 || r.End.Hour() != 0 || r.Start.Day() != 19 || r.End.Day() != 26 {
		t.Fatalf("range %v – %v", r.Start, r.End)
	}
	if r.End.Sub(r.Start) != 7*24*time.Hour+time.Hour {
		t.Fatalf("length %v", r.End.Sub(r.Start))
	}
}

func TestParse(t *testing.T) {
	if _, err := Parse("month", time.Now()); err == nil {
		t.Fatal("no error for an unknown range")
	}
	if r, err := Parse("week", time.Now()); err != nil || r.Kind != KindWeek {
		t.Fatalf("week: %v %v", r, err)
	}
}

type rec map[string]any

func line(t *testing.T, r rec) string {
	t.Helper()
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func user(ts, cwd, text string) rec {
	return rec{"type": "user", "timestamp": ts, "cwd": cwd, "gitBranch": "main",
		"message": rec{"role": "user", "content": text}}
}

func assistantTool(ts, cwd, name, path string) rec {
	return rec{"type": "assistant", "timestamp": ts, "cwd": cwd,
		"message": rec{"role": "assistant", "content": []rec{
			{"type": "text", "text": "ok"},
			{"type": "tool_use", "name": name, "input": rec{"file_path": path}},
		}}}
}

func writeTranscript(t *testing.T, dir, name string, lines ...string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCollect(t *testing.T) {
	projects := t.TempDir()
	repo := t.TempDir()
	os.MkdirAll(filepath.Join(repo, ".git"), 0o700)
	sub := filepath.Join(repo, "cmd")
	other := t.TempDir()
	r := Yesterday(time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC))

	writeTranscript(t, projects, "p1/a.jsonl",
		line(t, user("2026-09-25T10:00:00Z", repo, "before the range")),
		line(t, user("2026-09-26T09:00:00Z", repo, "add the summary command")),
		line(t, user("2026-09-26T09:00:01Z", repo, "<command-name>/clear</command-name>")),
		line(t, rec{"type": "user", "timestamp": "2026-09-26T09:00:02Z", "isMeta": true, "message": rec{"role": "user", "content": "meta"}}),
		line(t, rec{"type": "user", "timestamp": "2026-09-26T09:00:03Z", "isSidechain": true, "message": rec{"role": "user", "content": "side"}}),
		`{"type": "user", broken`,
		line(t, assistantTool("2026-09-26T09:05:00Z", sub, "Edit", filepath.Join(repo, "cmd", "main.go"))),
		line(t, assistantTool("2026-09-26T09:06:00Z", sub, "Edit", filepath.Join(repo, "cmd", "main.go"))),
		line(t, assistantTool("2026-09-26T09:07:00Z", sub, "Read", filepath.Join(repo, "README.md"))),
		line(t, rec{"type": "ai-title", "aiTitle": "Summary command"}),
		line(t, user("2026-09-27T01:00:00Z", repo, "after the range")),
	)
	writeTranscript(t, projects, "p2/b.jsonl", line(t, user("2026-09-26T20:00:00Z", other, "fix the docs")))
	// A subagent transcript in a subdirectory is not read.
	writeTranscript(t, projects, "p2/b/subagents/c.jsonl", line(t, user("2026-09-26T20:00:00Z", other, "subagent")))
	// A file with an old modification time is not read.
	old := writeTranscript(t, projects, "p3/d.jsonl", line(t, user("2026-09-26T20:00:00Z", other, "old file")))
	os.Chtimes(old, r.Start.Add(-time.Hour), r.Start.Add(-time.Hour))
	// A file without records in the range gives no session.
	writeTranscript(t, projects, "p4/e.jsonl", line(t, user("2026-09-20T20:00:00Z", other, "last week")))

	var commitCalls []string
	commits := func(_ context.Context, root string, _ Range) []string {
		commitCalls = append(commitCalls, root)
		return []string{"Add the summary command"}
	}
	d, err := Collect(context.Background(), projects, r, commits)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Projects) != 2 {
		t.Fatalf("projects %+v", d.Projects)
	}
	var pr, po Project
	for _, p := range d.Projects {
		if p.Dir == repo {
			pr = p
		} else {
			po = p
		}
	}
	if len(pr.Sessions) != 1 || len(po.Sessions) != 1 || po.Dir != other {
		t.Fatalf("sessions %+v %+v", pr, po)
	}
	s := pr.Sessions[0]
	if strings.Join(s.Prompts, "|") != "add the summary command" {
		t.Fatalf("prompts %q", s.Prompts)
	}
	if s.LastReply != "ok" {
		t.Fatalf("last reply %q", s.LastReply)
	}
	if s.Title != "Summary command" || s.Branch != "main" || s.ToolCalls != 3 {
		t.Fatalf("session %+v", s)
	}
	if len(s.Files) != 1 || s.Files[0] != filepath.Join(repo, "cmd", "main.go") {
		t.Fatalf("files %q", s.Files)
	}
	if d.Skipped != 1 {
		t.Fatalf("skipped %d", d.Skipped)
	}
	if strings.Join(commitCalls, "|") != repo || len(pr.Commits) != 1 || po.Commits != nil {
		t.Fatalf("commit calls %q", commitCalls)
	}
	if strings.Join(po.Sessions[0].Prompts, "|") != "fix the docs" {
		t.Fatalf("other prompts %q", po.Sessions[0].Prompts)
	}

	out := d.Render()
	for _, want := range []string{"Range: yesterday (Sat 26 Sep)", "## Project " + filepath.Base(repo), "Summary command", "- add the summary command", "- cmd/main.go", "Last Claude reply: ok", "Commits:\n- Add the summary command"} {
		if !strings.Contains(out, want) {
			t.Errorf("render has no %q:\n%s", want, out)
		}
	}
}

func TestCollectLongLine(t *testing.T) {
	projects := t.TempDir()
	cwd := t.TempDir()
	r := Yesterday(time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC))
	big := line(t, user("2026-09-26T09:00:00Z", cwd, strings.Repeat("x", MaxLineBytes)))
	writeTranscript(t, projects, "p/a.jsonl", big, line(t, user("2026-09-26T10:00:00Z", cwd, "short")))
	d, err := Collect(context.Background(), projects, r, nil)
	if err != nil {
		t.Fatal(err)
	}
	if d.Skipped != 1 || len(d.Projects) != 1 || strings.Join(d.Projects[0].Sessions[0].Prompts, "|") != "short" {
		t.Fatalf("digest %+v skipped %d", d.Projects, d.Skipped)
	}
}

func TestRenderLimits(t *testing.T) {
	r := Yesterday(time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC))
	s := &Session{First: r.Start, Last: r.Start}
	for i := 0; i < MaxPromptsPerSess+5; i++ {
		s.Prompts = append(s.Prompts, strings.Repeat("p", MaxPromptRunes*2)+"\x1b[2J")
	}
	for i := 0; i < MaxFilesPerProject+3; i++ {
		s.Files = append(s.Files, filepath.Join("/repo", strings.Repeat("f", i+1)))
	}
	d := Digest{Range: r, Projects: []Project{{Dir: "/repo", Sessions: []*Session{s}}}}
	out := d.Render()
	if !strings.Contains(out, "- … (5 more)") || !strings.Contains(out, "- … (3 more)") {
		t.Fatalf("no limit markers:\n%s", out)
	}
	if strings.Contains(out, "\x1b") {
		t.Fatal("escape sequence in the digest")
	}

	var many []Project
	for i := 0; i < 200; i++ {
		many = append(many, d.Projects[0])
	}
	d.Projects = many
	if out := d.Render(); len(out) > MaxDigestBytes || !strings.Contains(out, "more projects omitted") {
		t.Fatalf("digest size %d", len(out))
	}
}
