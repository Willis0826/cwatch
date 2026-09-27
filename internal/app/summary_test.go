package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cwatch/internal/process"
	"cwatch/internal/state"
	"cwatch/internal/summary"
)

type fakeSummarizer struct {
	calls int
	got   string
	out   string
	err   error
}

func (f *fakeSummarizer) Summarize(_ context.Context, digest string) (string, error) {
	f.calls++
	f.got = digest
	return f.out, f.err
}

func summaryEnv(t *testing.T, transcript string) (*Env, *fakeSummarizer, summary.Range) {
	t.Helper()
	cfg := t.TempDir()
	if transcript != "" {
		dir := filepath.Join(cfg, "projects", "p")
		os.MkdirAll(dir, 0o700)
		writeFile(t, filepath.Join(dir, "s.jsonl"), []byte(transcript+"\n"))
	}
	fake := &fakeSummarizer{out: "- **p**\n  - did \x1b[2Jwork\n"}
	clock := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	env := &Env{
		StateDir:   t.TempDir(),
		Now:        func() time.Time { return clock },
		Getenv:     func(k string) string { return map[string]string{"CLAUDE_CONFIG_DIR": cfg}[k] },
		Summarizer: fake,
	}
	return env, fake, summary.Yesterday(clock)
}

const yesterdayPrompt = `{"type":"user","timestamp":"2026-09-26T09:00:00Z","cwd":"/w","message":{"role":"user","content":"write the docs"}}`

func TestSummaryCaches(t *testing.T) {
	env, fake, r := summaryEnv(t, yesterdayPrompt)
	text, cached, err := env.Summary(context.Background(), r, false)
	if err != nil || cached || fake.calls != 1 {
		t.Fatalf("first: %q %v %v calls %d", text, cached, err, fake.calls)
	}
	if !strings.Contains(fake.got, "- write the docs") {
		t.Fatalf("digest:\n%s", fake.got)
	}
	if strings.Contains(text, "\x1b") || !strings.Contains(text, "did work") {
		t.Fatalf("text %q", text)
	}
	path := filepath.Join(env.StateDir, SummaryDir, r.CacheName())
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("cache file: %v %v", fi, err)
	}

	again, cached, err := env.Summary(context.Background(), r, false)
	if err != nil || !cached || again != text || fake.calls != 1 {
		t.Fatalf("second: %v %v calls %d", cached, err, fake.calls)
	}
	fake.out = "- new"
	fresh, cached, err := env.Summary(context.Background(), r, true)
	if err != nil || cached || fresh != "- new\n" || fake.calls != 2 {
		t.Fatalf("refresh: %q %v %v calls %d", fresh, cached, err, fake.calls)
	}
}

func TestSummaryNoActivity(t *testing.T) {
	env, fake, r := summaryEnv(t, `{"type":"user","timestamp":"2026-09-20T09:00:00Z","cwd":"/w","message":{"role":"user","content":"old"}}`)
	if _, _, err := env.Summary(context.Background(), r, false); !errors.Is(err, ErrNoActivity) {
		t.Fatalf("err %v", err)
	}
	if fake.calls != 0 {
		t.Fatal("the model was called without activity")
	}
}

func TestSummaryModelError(t *testing.T) {
	env, fake, r := summaryEnv(t, yesterdayPrompt)
	fake.err = errors.New("not logged in")
	if _, _, err := env.Summary(context.Background(), r, false); err == nil || !strings.Contains(err.Error(), "not logged in") {
		t.Fatalf("err %v", err)
	}
	fake.err, fake.out = nil, "  \n"
	if _, _, err := env.Summary(context.Background(), r, false); err == nil {
		t.Fatal("no error for an empty summary")
	}
	if _, err := os.Stat(filepath.Join(env.StateDir, SummaryDir, r.CacheName())); err == nil {
		t.Fatal("a failed summary was stored")
	}
}

func TestPruneSummaries(t *testing.T) {
	dir := t.TempDir()
	clock := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	old, fresh := filepath.Join(dir, "week-2026-06-01.md"), filepath.Join(dir, "week-2026-09-14.md")
	writeFile(t, old, []byte("x"))
	writeFile(t, fresh, []byte("x"))
	os.Chtimes(old, clock.Add(-SummaryRetention-time.Hour), clock.Add(-SummaryRetention-time.Hour))
	pruneSummaries(dir, clock)
	if _, err := os.Stat(old); err == nil {
		t.Fatal("old summary kept")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatal("new summary deleted")
	}
}

func TestRunHookDisabled(t *testing.T) {
	dir := t.TempDir()
	getenv := func(k string) string { return map[string]string{DisableEnv: "1"}[k] }
	in := strings.NewReader(`{"session_id":"s","hook_event_name":"UserPromptSubmit","cwd":"/w"}`)
	if code := RunHook([]string{"--state-dir", dir}, in, os.Stderr, getenv, &procs{m: map[int]process.Proc{}}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if _, err := os.Stat(filepath.Join(dir, state.DBFile)); err == nil {
		t.Fatal("the disabled hook opened the store")
	}
}
