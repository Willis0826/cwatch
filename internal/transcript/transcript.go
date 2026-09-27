// Package transcript extracts message excerpts from a Claude Code JSONL
// transcript. It reads a bounded tail of the file, treats every record as
// data, and makes no model calls.
package transcript

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"

	"cwatch/internal/textutil"
)

// DefaultTailBytes is the default number of bytes read from the file end.
const DefaultTailBytes = 256 * 1024

// Excerpts holds the latest message excerpts.
type Excerpts struct {
	Assistant string
	User      string
	Skipped   int  // records that did not parse
	Partial   bool // the read started inside a record
}

// ReadTail reads at most maxBytes from the end of path and returns the
// latest assistant text and the latest user prompt text. Each excerpt has
// at most maxRunes runes.
func ReadTail(path string, maxBytes int64, maxRunes int) (Excerpts, error) {
	var ex Excerpts
	if path == "" {
		return ex, errors.New("no transcript path")
	}
	f, err := os.Open(path)
	if err != nil {
		return ex, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return ex, err
	}
	if !fi.Mode().IsRegular() {
		return ex, errors.New("transcript is not a regular file")
	}
	size := fi.Size()
	start := int64(0)
	if size > maxBytes {
		start = size - maxBytes
	}
	buf := make([]byte, size-start)
	n, err := f.ReadAt(buf, start)
	if err != nil && !errors.Is(err, io.EOF) {
		return ex, err
	}
	buf = buf[:n]
	if start > 0 {
		// The first line can be part of a record. Drop it.
		ex.Partial = true
		if i := bytes.IndexByte(buf, '\n'); i >= 0 {
			buf = buf[i+1:]
		} else {
			buf = nil
		}
	}
	ex.parse(buf, maxRunes)
	return ex, nil
}

func (ex *Excerpts) parse(buf []byte, maxRunes int) {
	lines := bytes.Split(buf, []byte("\n"))
	for i := len(lines) - 1; i >= 0 && (ex.Assistant == "" || ex.User == ""); i-- {
		line := bytes.TrimSpace(lines[i])
		if len(line) == 0 {
			continue
		}
		var rec record
		if err := json.Unmarshal(line, &rec); err != nil {
			// A partial final record or an unknown format.
			ex.Skipped++
			continue
		}
		if rec.IsSidechain || rec.IsMeta {
			continue
		}
		role := rec.Message.Role
		if role == "" {
			role = rec.Type
		}
		text := rec.Message.text()
		if text == "" {
			continue
		}
		switch role {
		case "assistant":
			if ex.Assistant == "" {
				ex.Assistant = textutil.Bounded(text, maxRunes)
			}
		case "user":
			if ex.User == "" && !looksSynthetic(text) {
				ex.User = textutil.Bounded(text, maxRunes)
			}
		}
	}
}

// looksSynthetic reports whether a user record holds text that Claude Code
// generated, such as command output, instead of a typed prompt.
func looksSynthetic(s string) bool {
	s = strings.TrimSpace(s)
	for _, p := range []string{"<command-", "<local-command", "<system-reminder>", "<bash-", "[Request interrupted"} {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

type record struct {
	Type        string  `json:"type"`
	IsSidechain bool    `json:"isSidechain"`
	IsMeta      bool    `json:"isMeta"`
	Message     message `json:"message"`
}

type message struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// text returns the text blocks of a message. It ignores tool calls, tool
// results, images, and thinking blocks.
func (m message) text() string {
	if len(m.Content) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(m.Content, &s); err == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(m.Content, &blocks); err != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}
