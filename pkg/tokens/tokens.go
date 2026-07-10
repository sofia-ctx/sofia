// Package tokens estimates how many LLM tokens a given string costs.
//
// We deliberately avoid a real tokenizer dependency (tiktoken-go, BPE
// vocab download, ~5MB binary bloat, init cost) because the call log
// only needs *trends* — comparing one invocation to another, finding
// heavy outputs, plotting cost over time. A small heuristic is enough
// for that and stays sub-microsecond.
//
// The heuristic:
//   - ASCII bytes (< 0x80) cost ≈ 1/4 token each (matches the
//     well-known "≈4 characters per token" rule for English/code).
//   - Non-ASCII runes (Cyrillic, CJK, emoji, ...) cost ≈ 1 token each
//     (cl100k and similar BPEs split most Cyrillic chars into 1-2
//     subwords; using 1.0 gives an honest mid-range estimate that
//     errs slightly conservative for English-only text and slightly
//     low for CJK).
//
// If you ever need exact counts for billing, swap this for a real
// tokenizer. The call sites take an int64, so the signature is stable.
package tokens

import "unicode/utf8"

// asciiPerToken is how many ASCII bytes we charge per token. 4 matches
// the OpenAI rule of thumb for English text; TOON output (CSV-like
// rows with frequent punctuation) is a hair denser, but for analytics
// we want a single round factor.
const asciiPerToken = 4.0

// nonAsciiPerToken is tokens charged per non-ASCII rune.
const nonAsciiPerToken = 1.0

// Estimate returns an approximate token count for s. Returns 0 for the
// empty string.
func Estimate(s string) int64 {
	if s == "" {
		return 0
	}
	ascii, runes := scan(s)
	tokens := float64(ascii)/asciiPerToken + float64(runes)*nonAsciiPerToken
	return int64(tokens + 0.5) // round-half-up
}

// scan returns the number of ASCII bytes and the number of non-ASCII
// runes in s. Cheap single pass.
func scan(s string) (ascii int, nonAsciiRunes int) {
	for i := 0; i < len(s); {
		b := s[i]
		if b < 0x80 {
			ascii++
			i++
			continue
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		if size <= 0 {
			// Malformed UTF-8: advance one byte and treat as ASCII to make
			// progress; matches Go conventions on invalid input.
			ascii++
			i++
			continue
		}
		nonAsciiRunes++
		i += size
	}
	return ascii, nonAsciiRunes
}
