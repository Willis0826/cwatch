// Package textutil cleans untrusted text before the tool stores or renders it.
package textutil

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Sanitize removes ANSI escape sequences, other control characters, and
// invalid UTF-8 from s. When keepNewlines is false, the function replaces
// newlines and tabs with single spaces. The result is safe to print to a
// terminal.
func Sanitize(s string, keepNewlines bool) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size <= 1 {
			i++
			continue
		}
		switch {
		case r == 0x1b:
			i = skipEscape(s, i+size)
			continue
		case r == 0x9b: // C1 CSI
			i = skipCSI(s, i+size)
			continue
		case r == 0x9d || r == 0x90 || r == 0x9e || r == 0x9f: // C1 OSC, DCS, PM, APC
			i = skipString(s, i+size)
			continue
		case r == '\n':
			if keepNewlines {
				b.WriteByte('\n')
			} else {
				b.WriteByte(' ')
			}
		case r == '\t':
			if keepNewlines {
				b.WriteString("    ")
			} else {
				b.WriteByte(' ')
			}
		case r == '\r':
			// Drop carriage returns. They can overwrite earlier output.
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0):
			// Drop other C0 and C1 control characters.
		case isBidiControl(r):
			// Drop bidirectional overrides. They can disguise text.
		case !unicode.IsPrint(r) && !unicode.IsSpace(r):
			// Drop other non-printable characters.
		default:
			b.WriteRune(r)
		}
		i += size
	}
	return b.String()
}

func isBidiControl(r rune) bool {
	return (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) || r == 0x200e || r == 0x200f
}

// skipEscape skips an escape sequence that starts after ESC at index i.
func skipEscape(s string, i int) int {
	if i >= len(s) {
		return i
	}
	switch s[i] {
	case '[':
		return skipCSI(s, i+1)
	case ']', 'P', '^', '_', 'X':
		return skipString(s, i+1)
	default:
		// Two-character sequence, or an intermediate byte and a final byte.
		for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2f {
			i++
		}
		if i < len(s) {
			i++
		}
		return i
	}
}

// skipCSI skips parameter and intermediate bytes, then the final byte.
func skipCSI(s string, i int) int {
	for i < len(s) {
		c := s[i]
		i++
		if c >= 0x40 && c <= 0x7e {
			return i
		}
		if c < 0x20 || c > 0x7e {
			// A malformed sequence. Stop at this byte.
			return i
		}
	}
	return i
}

// skipString skips an OSC, DCS, PM, or APC string up to BEL or ST.
func skipString(s string, i int) int {
	for i < len(s) {
		c := s[i]
		if c == 0x07 {
			return i + 1
		}
		if c == 0x1b && i+1 < len(s) && s[i+1] == '\\' {
			return i + 2
		}
		if c == 0xc2 && i+1 < len(s) && s[i+1] == 0x9c { // C1 ST in UTF-8
			return i + 2
		}
		i++
	}
	return i
}

// Truncate returns at most max runes of s. It adds an ellipsis when it
// removes text.
func Truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	if max == 1 {
		return "…"
	}
	return string(runes[:max-1]) + "…"
}

// OneLine sanitizes s, collapses whitespace, and truncates the result.
func OneLine(s string, max int) string {
	return Truncate(strings.Join(strings.Fields(Sanitize(s, false)), " "), max)
}

// Bounded sanitizes s, keeps newlines, and truncates the result.
func Bounded(s string, max int) string {
	return Truncate(strings.TrimSpace(Sanitize(s, true)), max)
}
