package transcript

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "t.jsonl")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// Part of acceptance test 10.
func TestReadTailHandlesBadRecords(t *testing.T) {
	content := strings.Join([]string{
		`{"type":"user","message":{"role":"user","content":"first prompt"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"old answer"}]}}`,
		`not json at all`,
		`{"type":"brand-new-record","payload":{"x":1}}`,
		`{"type":"user","message":{"role":"user","content":"latest \u001b[2J\u001b]0;evil\u0007prompt"}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"ignored"}]}}`,
		`{"type":"assistant","isSidechain":true,"message":{"role":"assistant","content":[{"type":"text","text":"subagent text"}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":"hidden"},{"type":"text","text":"new answer"},{"type":"tool_use","name":"Bash"}]}}`,
		`{"type":"assistant","message":{"role":"assist`, // partial final record
	}, "\n")
	ex, err := ReadTail(write(t, content), DefaultTailBytes, 100)
	if err != nil {
		t.Fatal(err)
	}
	if ex.Assistant != "new answer" {
		t.Errorf("assistant %q", ex.Assistant)
	}
	if ex.User != "latest prompt" {
		t.Errorf("user %q", ex.User)
	}
	// The reader stops when it has both excerpts, so it skips only the
	// partial final record here.
	if ex.Skipped != 1 {
		t.Errorf("skipped %d, want 1", ex.Skipped)
	}
}

func TestReadTailBoundsBytes(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"far away"}]}}` + "\n")
	for i := 0; i < 2000; i++ {
		b.WriteString(`{"type":"attachment","x":"` + strings.Repeat("y", 100) + `"}` + "\n")
	}
	ex, err := ReadTail(write(t, b.String()), 4096, 100)
	if err != nil {
		t.Fatal(err)
	}
	if ex.Assistant != "" || !ex.Partial {
		t.Fatalf("read beyond the bound: %+v", ex)
	}
}

func TestReadTailMissingAndEmpty(t *testing.T) {
	if _, err := ReadTail(filepath.Join(t.TempDir(), "missing.jsonl"), 100, 10); err == nil {
		t.Fatal("no error for a missing file")
	}
	if _, err := ReadTail("", 100, 10); err == nil {
		t.Fatal("no error for an empty path")
	}
	ex, err := ReadTail(write(t, ""), 100, 10)
	if err != nil || ex.Assistant != "" {
		t.Fatalf("empty file: %+v %v", ex, err)
	}
	if _, err := ReadTail(t.TempDir(), 100, 10); err == nil {
		t.Fatal("no error for a directory")
	}
}

func TestReadTailTruncatesExcerpt(t *testing.T) {
	ex, _ := ReadTail(write(t, `{"type":"assistant","message":{"role":"assistant","content":"`+strings.Repeat("z", 500)+`"}}`), 10000, 50)
	if n := len([]rune(ex.Assistant)); n != 50 {
		t.Fatalf("excerpt has %d runes", n)
	}
}
