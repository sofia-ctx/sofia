// Package strdist provides small string-distance helpers for typo-tolerant
// CLI suggestions ("did you mean X?"). Cross-cutting and project-agnostic:
// consumers are tools that take a name argument and want to self-correct a
// near-miss instead of failing silently (e.g. `sf composer show <pkg>`,
// flag-typo hints).
package strdist

import (
	"strings"
	"unicode/utf8"
)

// Levenshtein returns the edit distance between a and b (insert/delete/substitute,
// cost 1 each). Operates on runes, so it is correct for non-ASCII input.
func Levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 {
		return len(rb)
	}
	if len(rb) == 0 {
		return len(ra)
	}
	// Single-row DP: prev[j] holds the distance for the previous row.
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		diag := prev[0] // prev[i-1][j-1]
		prev[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur := min3(prev[j]+1, prev[j-1]+1, diag+cost)
			diag = prev[j]
			prev[j] = cur
		}
	}
	return prev[len(rb)]
}

// Nearest returns the candidate closest to target under a tolerance scaled to
// target length (≤ ⌈len/3⌉, min 1) — close enough to be a likely typo, not an
// unrelated word. Comparison is case-insensitive. ok is false when no candidate
// is within tolerance (or candidates is empty), so callers can fall back to a
// generic hint. Ties resolve to the first candidate in iteration order.
func Nearest(target string, candidates []string) (best string, ok bool) {
	target = strings.TrimSpace(target)
	if target == "" || len(candidates) == 0 {
		return "", false
	}
	lt := strings.ToLower(target)
	tol := (utf8.RuneCountInString(target) + 2) / 3 // ceil(len/3)
	if tol < 1 {
		tol = 1
	}
	bestD := tol + 1
	for _, c := range candidates {
		d := Levenshtein(lt, strings.ToLower(c))
		if d < bestD {
			bestD, best, ok = d, c, true
		}
	}
	return best, ok
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}
