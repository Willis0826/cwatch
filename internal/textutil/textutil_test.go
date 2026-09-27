package textutil

import (
	"strings"
	"testing"
)

// Part of acceptance test 10.
func TestSanitizeRemovesEscapes(t *testing.T) {
	cases := map[string]string{
		"plain":                          "plain",
		"\x1b[31mred\x1b[0m":             "red",
		"a\x1b]0;title\x07b":             "ab",
		"a\x1b]8;;http://x\x1b\\link":    "alink",
		"a\x1bPdcs data\x1b\\b":          "ab",
		"c1\u009b31mcsi":                 "c1csi",
		"c1\u009dosc\u009cz":             "c1z",
		"bell\x07 back\x08 del\x7f":      "bell back del",
		"over\rwrite":                    "overwrite",
		"bidi\u202egnp.exe":              "bidignp.exe",
		"bad\xffutf8":                    "badutf8",
		"keep ünïcode ✓ 日本":              "keep ünïcode ✓ 日本",
		"unterminated \x1b]0;never ends": "unterminated ",
		"line1\nline2\ttab":              "line1 line2 tab",
		"\x1b[?1049h\x1b[2J\x1b[Hclear":  "clear",
	}
	for in, want := range cases {
		if got := Sanitize(in, false); got != want {
			t.Errorf("Sanitize(%q) = %q, want %q", in, got, want)
		}
	}
	if got := Sanitize("a\nb", true); got != "a\nb" {
		t.Errorf("keepNewlines: %q", got)
	}
}

func TestTruncate(t *testing.T) {
	if got := Truncate("abcdef", 4); got != "abc…" {
		t.Fatalf("got %q", got)
	}
	if got := Truncate("日本語", 3); got != "日本語" {
		t.Fatalf("got %q", got)
	}
	if got := OneLine("  a \n\n b  ", 10); got != "a b" {
		t.Fatalf("got %q", got)
	}
	if strings.Count(Truncate(strings.Repeat("x", 100), 10), "x") != 9 {
		t.Fatal("wrong length")
	}
}
