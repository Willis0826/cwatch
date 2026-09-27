package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"cwatch/internal/summary"
	"cwatch/internal/textutil"
)

// Summary limits.
const (
	SummaryDir       = "summaries"
	SummaryTimeout   = 3 * time.Minute
	SummaryRetention = 60 * 24 * time.Hour
	maxModelStderr   = 2000
)

// DisableEnv is the environment variable that turns the hook off. cwatch sets
// it for the Claude Code process of a summary, so that process does not show
// as a session.
const DisableEnv = "CWATCH_DISABLE"

// ErrNoActivity means that no session has records in the range.
var ErrNoActivity = errors.New("no Claude Code activity")

// Summarizer turns a digest into a summary.
type Summarizer interface {
	Summarize(ctx context.Context, digest string) (string, error)
}

// ClaudeCLI runs "claude -p" with no tools and no session persistence. It
// uses the Claude Code login of the user.
type ClaudeCLI struct {
	// Dir is the working directory of the child. A neutral directory keeps
	// the settings and the CLAUDE.md of a project out of the call.
	Dir    string
	Getenv func(string) string
}

// Summarize sends the digest on stdin and returns the text output.
func (c ClaudeCLI) Summarize(ctx context.Context, digest string) (string, error) {
	bin, err := c.find()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, SummaryTimeout)
	defer cancel()
	// The prompt comes directly after -p, because --tools takes a list.
	cmd := exec.CommandContext(ctx, bin, "-p", summary.Instruction,
		"--no-session-persistence", "--tools", "", "--output-format", "text")
	cmd.Dir = c.Dir
	cmd.Env = append(os.Environ(), DisableEnv+"=1")
	cmd.Stdin = strings.NewReader(digest)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("claude: %w", ctx.Err())
		}
		msg := textutil.OneLine(stderr.String(), maxModelStderr)
		if msg == "" {
			msg = textutil.OneLine(stdout.String(), maxModelStderr)
		}
		return "", fmt.Errorf("claude: %v: %s", err, msg)
	}
	return stdout.String(), nil
}

func (c ClaudeCLI) find() (string, error) {
	if p, err := exec.LookPath("claude"); err == nil {
		return p, nil
	}
	getenv := c.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	if home := getenv("HOME"); home != "" {
		for _, p := range []string{
			filepath.Join(home, ".local", "bin", "claude"),
			filepath.Join(home, ".claude", "local", "claude"),
		} {
			if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() && fi.Mode()&0o111 != 0 {
				return p, nil
			}
		}
	}
	return "", errors.New("cannot find the claude program in PATH")
}

// ProjectsDir returns the directory of the Claude Code transcripts. It
// honours CLAUDE_CONFIG_DIR.
func (e *Env) ProjectsDir() (string, error) {
	settings, err := DefaultSettingsFile(e.Getenv)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(settings), "projects"), nil
}

// Summary returns the summary of the work in r. It returns a stored summary
// when one exists, unless refresh is true. It returns ErrNoActivity when no
// session has records in r. It does not start the model in that case.
func (e *Env) Summary(ctx context.Context, r summary.Range, refresh bool) (text string, cached bool, err error) {
	dir := filepath.Join(e.StateDir, SummaryDir)
	path := filepath.Join(dir, r.CacheName())
	if !refresh {
		if text, ok := e.CachedSummary(r); ok {
			return text, true, nil
		}
	}
	projects, err := e.ProjectsDir()
	if err != nil {
		return "", false, err
	}
	d, err := summary.Collect(ctx, projects, r, e.Commits)
	if err != nil {
		return "", false, err
	}
	if d.Empty() {
		return "", false, ErrNoActivity
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", false, err
	}
	sum := e.Summarizer
	if sum == nil {
		sum = ClaudeCLI{Dir: dir, Getenv: e.Getenv}
	}
	out, err := sum.Summarize(ctx, d.Render())
	if err != nil {
		return "", false, err
	}
	out = strings.TrimSpace(textutil.Sanitize(out, true))
	if out == "" {
		return "", false, errors.New("claude returned an empty summary")
	}
	out += "\n"
	if err := writeSummary(dir, path, out); err != nil && e.Stderr != nil {
		fmt.Fprintf(e.Stderr, "cwatch: cannot store the summary: %v\n", err)
	}
	pruneSummaries(dir, e.Now())
	return out, false, nil
}

// CachedSummary returns the stored summary of r, if one exists.
func (e *Env) CachedSummary(r summary.Range) (string, bool) {
	b, err := os.ReadFile(filepath.Join(e.StateDir, SummaryDir, r.CacheName()))
	if err != nil || len(bytes.TrimSpace(b)) == 0 {
		return "", false
	}
	return string(b), true
}

// writeSummary writes the file atomically with mode 0600.
func writeSummary(dir, path, text string) error {
	f, err := os.CreateTemp(dir, ".summary-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(text); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// pruneSummaries deletes stored summaries older than SummaryRetention.
func pruneSummaries(dir string, now time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, en := range entries {
		if !strings.HasSuffix(en.Name(), ".md") {
			continue
		}
		if fi, err := en.Info(); err == nil && now.Sub(fi.ModTime()) > SummaryRetention {
			_ = os.Remove(filepath.Join(dir, en.Name()))
		}
	}
}
