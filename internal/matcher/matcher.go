// Package matcher scans a file line-by-line for occurrences of one or
// more patterns, returning hits with line/column information.
//
// Two modes:
//   - literal (default): byte-fast substring search via strings.Index,
//     optionally requiring a UTF-8 word boundary around the match. A
//     search for `технология` doesn't fire inside `вижутехнологияя`
//     thanks to the boundary check using utf8.DecodeRune.
//   - regex (Options.Regex): each pattern is compiled with
//     regexp.Compile; case-insensitive mode prepends `(?i:...)` to the
//     pattern. Word boundary is ignored — the user controls it via `\b`.
package matcher

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// peekSize is how many leading bytes openText inspects to classify a file
// as binary (a NUL byte never appears in UTF-8 text).
const peekSize = 8000

// ErrSkip signals that a file should be silently skipped rather than
// failing the whole scan — it is binary, or has a line longer than the
// scanner's buffer (minified bundles, lock files, source maps). Callers
// (grep, xref) count these and continue.
var ErrSkip = errors.New("matcher: file skipped (binary or over-long line)")

// openText opens path for line scanning and rejects binary files up front
// (a NUL byte in the first peekSize bytes), returning ErrSkip. The returned
// file is rewound to the start.
func openText(path string) (*os.File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	buf := make([]byte, peekSize)
	n, _ := io.ReadFull(f, buf)
	if bytes.IndexByte(buf[:n], 0) >= 0 {
		f.Close()
		return nil, ErrSkip
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

type Hit struct {
	Pattern string // which input pattern produced the hit
	Line    int
	Col     int
	Text    string
}

type Options struct {
	Patterns  []string // one or more patterns to look for
	Case      bool     // true: case-sensitive
	WordBound bool     // true: require non-word boundary (literal mode only)
	Regex     bool     // true: patterns are Go regexp; WordBound is ignored
}

func isWordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// wordBoundaryViolated reports whether a match at [start, start+length)
// in s sits inside a larger word — i.e. the rune just before or just
// after is itself a word rune. Both edges (start == 0, end == len(s))
// are valid boundaries.
func wordBoundaryViolated(s string, start, length int) bool {
	if start > 0 {
		r, _ := utf8.DecodeLastRuneInString(s[:start])
		if r != utf8.RuneError && isWordRune(r) {
			return true
		}
	}
	end := start + length
	if end < len(s) {
		r, _ := utf8.DecodeRuneInString(s[end:])
		if r != utf8.RuneError && isWordRune(r) {
			return true
		}
	}
	return false
}

// ScanFile opens path and returns hits for every configured pattern, plus
// the full set of lines read so callers can extract surrounding context
// without re-reading the file.
func ScanFile(path string, opts Options) ([]Hit, []string, error) {
	if opts.Regex {
		return scanFileRegex(path, opts)
	}
	return scanFileLiteral(path, opts)
}

func scanFileLiteral(path string, opts Options) ([]Hit, []string, error) {
	f, err := openText(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)

	needles := make([]string, len(opts.Patterns))
	for i, p := range opts.Patterns {
		if opts.Case {
			needles[i] = p
		} else {
			needles[i] = strings.ToLower(p)
		}
	}

	var hits []Hit
	var lines []string
	lineNum := 0
	for sc.Scan() {
		lineNum++
		text := sc.Text()
		lines = append(lines, text)

		haystack := text
		if !opts.Case {
			haystack = strings.ToLower(text)
		}
		for i, needle := range needles {
			hits = append(hits, scanLineLiteral(text, haystack, needle, opts.Patterns[i], lineNum, opts.WordBound)...)
		}
	}
	if err := sc.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return nil, nil, ErrSkip
		}
		return nil, nil, err
	}
	return hits, lines, nil
}

func scanLineLiteral(text, haystack, needle, pattern string, lineNum int, wordBound bool) []Hit {
	if needle == "" {
		return nil
	}
	var hits []Hit
	offset := 0
	for offset <= len(haystack) {
		idx := strings.Index(haystack[offset:], needle)
		if idx < 0 {
			break
		}
		col := offset + idx
		if wordBound && wordBoundaryViolated(haystack, col, len(needle)) {
			offset = col + 1
			continue
		}
		hits = append(hits, Hit{Pattern: pattern, Line: lineNum, Col: col, Text: text})
		offset = col + len(needle)
		if offset == col {
			offset++
		}
	}
	return hits
}

func scanFileRegex(path string, opts Options) ([]Hit, []string, error) {
	res := make([]*regexp.Regexp, len(opts.Patterns))
	for i, p := range opts.Patterns {
		expr := p
		if !opts.Case {
			expr = "(?i:" + expr + ")"
		}
		re, err := regexp.Compile(expr)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid pattern %q: %w", p, err)
		}
		res[i] = re
	}

	f, err := openText(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)

	var hits []Hit
	var lines []string
	lineNum := 0
	for sc.Scan() {
		lineNum++
		text := sc.Text()
		lines = append(lines, text)
		for i, re := range res {
			for _, idx := range re.FindAllStringIndex(text, -1) {
				hits = append(hits, Hit{
					Pattern: opts.Patterns[i],
					Line:    lineNum,
					Col:     idx[0],
					Text:    text,
				})
			}
		}
	}
	if err := sc.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return nil, nil, ErrSkip
		}
		return nil, nil, err
	}
	return hits, lines, nil
}
