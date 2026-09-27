package transcript

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sync"

	"cwatch/internal/textutil"
)

// MaxUsageRead bounds the bytes that one Update reads from one transcript.
// A longer backlog is read on later calls.
const MaxUsageRead = 64 << 20

// Usage is the token usage of the main thread of a session. It counts each
// API response once. It does not include subagents that write their own
// transcript files.
type Usage struct {
	// Context is the number of input tokens of the latest response:
	// uncached input plus cache writes plus cache reads. It is the size of
	// the context that the model received.
	Context    int64  `json:"context_tokens"`
	Input      int64  `json:"input_tokens"`
	CacheWrite int64  `json:"cache_creation_input_tokens"`
	CacheRead  int64  `json:"cache_read_input_tokens"`
	Output     int64  `json:"output_tokens"`
	Responses  int    `json:"responses"`
	Model      string `json:"model,omitempty"`
	// Partial is true while unread transcript data remains.
	Partial bool `json:"partial,omitempty"`
}

// Total returns all input and output tokens.
func (u Usage) Total() int64 { return u.Input + u.CacheWrite + u.CacheRead + u.Output }

type usageFile struct {
	offset int64
	size   int64
	ino    uint64
	seen   map[string]struct{}
	usage  Usage
}

// UsageTracker reads token usage from transcripts incrementally. It keeps
// the read offset and the counted message IDs of each file, so a refresh
// reads only new lines. It is safe for concurrent use.
type UsageTracker struct {
	mu    sync.Mutex
	files map[string]*usageFile
}

// NewUsageTracker returns an empty tracker.
func NewUsageTracker() *UsageTracker { return &UsageTracker{files: map[string]*usageFile{}} }

// Update reads new lines of path and returns the usage so far.
func (t *UsageTracker) Update(path string) (Usage, error) {
	if path == "" {
		return Usage{}, errors.New("no transcript path")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	f, err := os.Open(path)
	if err != nil {
		delete(t.files, path)
		return Usage{}, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return Usage{}, err
	}
	if !fi.Mode().IsRegular() {
		return Usage{}, errors.New("transcript is not a regular file")
	}
	st := t.files[path]
	ino := inode(fi)
	if st == nil || fi.Size() < st.offset || ino != st.ino {
		// A new, truncated, or replaced file. Start again.
		st = &usageFile{seen: map[string]struct{}{}, ino: ino}
		t.files[path] = st
	}
	st.size = fi.Size()
	if st.offset < st.size {
		n := st.size - st.offset
		if n > MaxUsageRead {
			n = MaxUsageRead
		}
		buf := make([]byte, n)
		read, err := f.ReadAt(buf, st.offset)
		if err != nil && !errors.Is(err, io.EOF) {
			return st.usage, err
		}
		buf = buf[:read]
		// Consume only complete lines. A partial final line is read again
		// when the writer finishes it.
		end := bytes.LastIndexByte(buf, '\n')
		if end < 0 {
			if int64(read) == MaxUsageRead {
				// One line is longer than the read limit. Skip it.
				st.offset += int64(read)
			}
		} else {
			st.addLines(buf[:end+1])
			st.offset += int64(end + 1)
		}
	}
	st.usage.Partial = st.offset < st.size
	return st.usage, nil
}

// Forget drops the state of the paths that are not in keep.
func (t *UsageTracker) Forget(keep map[string]bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for p := range t.files {
		if !keep[p] {
			delete(t.files, p)
		}
	}
}

var usageKey = []byte(`"usage"`)

type usageRecord struct {
	Type        string `json:"type"`
	IsSidechain bool   `json:"isSidechain"`
	Message     struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage *struct {
			Input      int64 `json:"input_tokens"`
			CacheWrite int64 `json:"cache_creation_input_tokens"`
			CacheRead  int64 `json:"cache_read_input_tokens"`
			Output     int64 `json:"output_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

func (st *usageFile) addLines(buf []byte) {
	for len(buf) > 0 {
		i := bytes.IndexByte(buf, '\n')
		line := buf
		if i >= 0 {
			line, buf = buf[:i], buf[i+1:]
		} else {
			buf = nil
		}
		// Most records hold no usage. Skip them before the JSON decode.
		if !bytes.Contains(line, usageKey) {
			continue
		}
		var r usageRecord
		if err := json.Unmarshal(line, &r); err != nil {
			continue
		}
		m := r.Message
		if r.Type != "assistant" || r.IsSidechain || m.Usage == nil || m.Model == "<synthetic>" {
			continue
		}
		// Claude Code can write one response as several records, one for
		// each content block. The records share the message ID and the
		// usage, so count each ID once.
		if m.ID != "" {
			if _, dup := st.seen[m.ID]; dup {
				continue
			}
			st.seen[m.ID] = struct{}{}
		}
		u := m.Usage
		st.usage.Input += u.Input
		st.usage.CacheWrite += u.CacheWrite
		st.usage.CacheRead += u.CacheRead
		st.usage.Output += u.Output
		st.usage.Context = u.Input + u.CacheWrite + u.CacheRead
		st.usage.Responses++
		if m.Model != "" {
			st.usage.Model = textutil.OneLine(m.Model, 60)
		}
	}
}
