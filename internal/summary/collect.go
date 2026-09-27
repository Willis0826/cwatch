package summary

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"cwatch/internal/gitinfo"
	"cwatch/internal/transcript"
)

// MaxLineBytes is the longest transcript line that Collect parses. Collect
// skips longer lines. Large tool results make most of these lines.
const MaxLineBytes = 8 << 20

// Session is the work of one transcript in a range.
type Session struct {
	ID        string
	Title     string
	Cwd       string
	Branch    string
	Prompts   []string
	LastReply string   // the last assistant text in the range
	Files     []string // edited files, in the order of the first edit
	ToolCalls int
	First     time.Time
	Last      time.Time
}

// Project groups the sessions of one repository or directory.
type Project struct {
	Dir      string
	Sessions []*Session
	Commits  []string
}

// Name returns the last element of the project directory.
func (p Project) Name() string { return filepath.Base(p.Dir) }

// Digest is the collected work of a range.
type Digest struct {
	Range    Range
	Projects []Project
	Skipped  int // lines that did not parse or were too long
}

// Empty reports whether the digest has no session.
func (d Digest) Empty() bool { return len(d.Projects) == 0 }

// CommitFunc returns the subjects of the commits of the user in a
// repository and a range.
type CommitFunc func(ctx context.Context, root string, r Range) []string

// Collect reads the transcripts in projectsDir/*/*.jsonl and returns the work
// in r. It does not read subagent transcripts in subdirectories. commits can
// be nil.
func Collect(ctx context.Context, projectsDir string, r Range, commits CommitFunc) (Digest, error) {
	d := Digest{Range: r}
	files, err := filepath.Glob(filepath.Join(projectsDir, "*", "*.jsonl"))
	if err != nil {
		return d, err
	}
	var sessions []*Session
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return d, err
		}
		fi, err := os.Stat(f)
		if err != nil || !fi.Mode().IsRegular() || fi.ModTime().Before(r.Start) {
			continue
		}
		s, skipped, err := readSession(f, r)
		d.Skipped += skipped
		if err != nil || s == nil {
			continue
		}
		sessions = append(sessions, s)
	}

	byDir := map[string]*Project{}
	for _, s := range sessions {
		dir := s.Cwd
		if root := gitinfo.Root(dir); root != "" {
			dir = root
		}
		p := byDir[dir]
		if p == nil {
			p = &Project{Dir: dir}
			byDir[dir] = p
		}
		p.Sessions = append(p.Sessions, s)
	}
	for _, p := range byDir {
		sort.Slice(p.Sessions, func(i, j int) bool { return p.Sessions[i].First.Before(p.Sessions[j].First) })
		if commits != nil && gitinfo.Root(p.Dir) == p.Dir {
			p.Commits = commits(ctx, p.Dir, r)
		}
		d.Projects = append(d.Projects, *p)
	}
	sort.Slice(d.Projects, func(i, j int) bool { return d.Projects[i].Dir < d.Projects[j].Dir })
	return d, nil
}

type record struct {
	Type        string    `json:"type"`
	Timestamp   time.Time `json:"timestamp"`
	Cwd         string    `json:"cwd"`
	GitBranch   string    `json:"gitBranch"`
	IsSidechain bool      `json:"isSidechain"`
	IsMeta      bool      `json:"isMeta"`
	AITitle     string    `json:"aiTitle"`
	Message     struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type toolUse struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
	Input struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
	} `json:"input"`
}

// readSession returns the work of one transcript in r, or nil when the
// transcript has no record in r.
func readSession(path string, r Range) (*Session, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	s := &Session{ID: strings.TrimSuffix(filepath.Base(path), ".jsonl")}
	seenFile := map[string]bool{}
	title := ""
	active, skipped := false, 0
	br := bufio.NewReaderSize(f, 64<<10)
	for {
		line, tooLong, err := readLine(br, MaxLineBytes)
		if tooLong {
			skipped++
		} else if line = bytes.TrimSpace(line); len(line) > 0 {
			var rec record
			if json.Unmarshal(line, &rec) != nil {
				skipped++
			} else if rec.Type == "ai-title" {
				// The title record has no timestamp. The last one wins.
				if rec.AITitle != "" {
					title = rec.AITitle
				}
			} else if !rec.IsSidechain && !rec.IsMeta && r.Contains(rec.Timestamp) {
				if s.add(rec, seenFile) {
					active = true
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, skipped, err
		}
	}
	if !active {
		return nil, skipped, nil
	}
	s.Title = title
	return s, skipped, nil
}

// add adds a user or assistant record. It reports whether the record counts
// as activity.
func (s *Session) add(rec record, seenFile map[string]bool) bool {
	role := rec.Message.Role
	if role == "" {
		role = rec.Type
	}
	if role != "user" && role != "assistant" {
		return false
	}
	if s.First.IsZero() {
		s.First = rec.Timestamp
	}
	s.Last = rec.Timestamp
	if rec.Cwd != "" {
		s.Cwd = rec.Cwd
	}
	if rec.GitBranch != "" && rec.GitBranch != "HEAD" {
		s.Branch = rec.GitBranch
	}
	switch role {
	case "user":
		if text := strings.TrimSpace(transcript.Text(rec.Message.Content)); text != "" && !transcript.LooksSynthetic(text) {
			s.Prompts = append(s.Prompts, text)
		}
	case "assistant":
		if text := strings.TrimSpace(transcript.Text(rec.Message.Content)); text != "" {
			s.LastReply = text
		}
		var blocks []toolUse
		if json.Unmarshal(rec.Message.Content, &blocks) != nil {
			return true
		}
		for _, b := range blocks {
			if b.Type != "tool_use" {
				continue
			}
			s.ToolCalls++
			switch b.Name {
			case "Edit", "Write", "MultiEdit", "NotebookEdit":
				p := b.Input.FilePath
				if p == "" {
					p = b.Input.NotebookPath
				}
				if p != "" && !seenFile[p] {
					seenFile[p] = true
					s.Files = append(s.Files, p)
				}
			}
		}
	}
	return true
}

// readLine returns the next line without the newline. When the line is
// longer than max, it discards the line and reports tooLong.
func readLine(br *bufio.Reader, max int) (line []byte, tooLong bool, err error) {
	for {
		chunk, err := br.ReadSlice('\n')
		if !tooLong {
			if len(line)+len(chunk) > max {
				tooLong, line = true, nil
			} else {
				line = append(line, chunk...)
			}
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return line, tooLong, err
	}
}
