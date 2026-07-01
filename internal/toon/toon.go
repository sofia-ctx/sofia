// Package toon implements low-level helpers for the TOON
// (Token-Oriented Object Notation) text format. Concrete renderers in
// individual tools (xref, history, ...) compose these primitives
// to emit their own schema. The format itself is documented at
// https://github.com/toon-format/toon.
package toon

import "strings"

const Indent = "  "

// Scalar returns a TOON-safe representation of s — bare when possible,
// otherwise wrapped in double quotes with backslash escapes for the
// control characters and structural punctuation that TOON cares about.
func Scalar(s string) string {
	if s == "" {
		return `""`
	}
	if !NeedsQuote(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\\', '"':
			b.WriteByte('\\')
			b.WriteByte(c)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// NeedsQuote reports whether s contains any byte that would clash with
// TOON's structural characters (or leading/trailing whitespace).
func NeedsQuote(s string) bool {
	if s == "" {
		return true
	}
	if s[0] == ' ' || s[0] == '\t' || s[len(s)-1] == ' ' || s[len(s)-1] == '\t' {
		return true
	}
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case ',', ':', '"', '\\', '\n', '\r', '\t', '[', ']', '{', '}':
			return true
		}
	}
	return false
}

// JoinList renders []string as a comma-separated TOON inline list with
// per-item quoting. Empty slices return an empty string.
func JoinList(items []string) string {
	parts := make([]string, len(items))
	for i, s := range items {
		parts[i] = Scalar(s)
	}
	return strings.Join(parts, ",")
}
