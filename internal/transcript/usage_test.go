package transcript

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func asst(id string, in, cw, cr, out int64, extra string) string {
	return fmt.Sprintf(`{"type":"assistant"%s,"message":{"id":%q,"model":"claude-opus-5-5","role":"assistant","content":[],"usage":{"input_tokens":%d,"cache_creation_input_tokens":%d,"cache_read_input_tokens":%d,"output_tokens":%d}}}`+"\n", extra, id, in, cw, cr, out)
}

func appendFile(t *testing.T, p, s string) {
	t.Helper()
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(s)
	f.Close()
}

func TestUsageCountsEachResponseOnce(t *testing.T) {
	p := filepath.Join(t.TempDir(), "t.jsonl")
	appendFile(t, p, `{"type":"user","message":{"role":"user","content":"hi"}}`+"\n")
	// One response written as three records (one per content block).
	appendFile(t, p, asst("msg_1", 10, 100, 1000, 50, "")+asst("msg_1", 10, 100, 1000, 50, "")+asst("msg_1", 10, 100, 1000, 50, ""))
	appendFile(t, p, asst("msg_2", 2, 20, 1150, 30, ""))
	// Records that do not count.
	appendFile(t, p, asst("msg_side", 9, 9, 9, 9, `,"isSidechain":true`))
	appendFile(t, p, `{"type":"assistant","message":{"id":"x","model":"<synthetic>","usage":{"input_tokens":5,"output_tokens":5}}}`+"\n")
	appendFile(t, p, "not json but has \"usage\"\n")

	tr := NewUsageTracker()
	u, err := tr.Update(p)
	if err != nil {
		t.Fatal(err)
	}
	want := Usage{Context: 2 + 20 + 1150, Input: 12, CacheWrite: 120, CacheRead: 2150, Output: 80, Responses: 2, Model: "claude-opus-5-5"}
	if u != want {
		t.Fatalf("usage %+v\nwant  %+v", u, want)
	}
	if u.Total() != 12+120+2150+80 {
		t.Fatalf("total %d", u.Total())
	}
}

func TestUsageIsIncremental(t *testing.T) {
	p := filepath.Join(t.TempDir(), "t.jsonl")
	appendFile(t, p, asst("a", 1, 0, 100, 10, ""))
	tr := NewUsageTracker()
	u, _ := tr.Update(p)
	if u.Responses != 1 {
		t.Fatalf("first read %+v", u)
	}
	// A partial line is not consumed until the writer finishes it.
	line := asst("b", 1, 0, 200, 20, "")
	appendFile(t, p, line[:40])
	if u, _ = tr.Update(p); u.Responses != 1 {
		t.Fatalf("partial line counted: %+v", u)
	}
	appendFile(t, p, line[40:])
	if u, _ = tr.Update(p); u.Responses != 2 || u.Context != 201 || u.Output != 30 {
		t.Fatalf("after completion %+v", u)
	}
	// The same message ID again (a repeated block) does not count.
	appendFile(t, p, asst("b", 1, 0, 200, 20, ""))
	if u, _ = tr.Update(p); u.Responses != 2 {
		t.Fatalf("repeat counted: %+v", u)
	}
}

func TestUsageRestartsOnTruncationOrReplacement(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "t.jsonl")
	appendFile(t, p, asst("a", 1, 0, 100, 10, "")+asst("b", 1, 0, 100, 10, ""))
	tr := NewUsageTracker()
	tr.Update(p)
	os.WriteFile(p, []byte(asst("c", 1, 0, 5, 1, "")), 0o600) // shorter file
	if u, _ := tr.Update(p); u.Responses != 1 || u.Context != 6 {
		t.Fatalf("after truncation %+v", u)
	}
	// A new file with the same name and a longer content.
	q := filepath.Join(dir, "new.jsonl")
	appendFile(t, q, asst("d", 1, 0, 7, 1, "")+asst("e", 1, 0, 8, 1, "")+asst("f", 1, 0, 9, 1, ""))
	os.Rename(q, p)
	if u, _ := tr.Update(p); u.Responses != 3 || u.Context != 10 {
		t.Fatalf("after replacement %+v", u)
	}
}

func TestUsageErrors(t *testing.T) {
	tr := NewUsageTracker()
	if _, err := tr.Update(""); err == nil {
		t.Fatal("empty path")
	}
	if _, err := tr.Update(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing file")
	}
	if _, err := tr.Update(t.TempDir()); err == nil {
		t.Fatal("directory")
	}
}
