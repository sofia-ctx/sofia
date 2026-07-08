// Package changed implements `sf changed` — a compact, classified summary
// of a git diff: per file its status, churn (+/-), category, language, and
// the enclosing functions/classes touched (read from git's own hunk-header
// function context, no file parsing). Replaces reading a full `git diff`
// just to learn what changed and where.
package changed

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/sofia-ctx/sofia/internal/calllog"
	"github.com/sofia-ctx/sofia/internal/emit"
	"github.com/sofia-ctx/sofia/internal/gitexec"
)

type Options struct {
	Root    string // git repo dir (default: cwd)
	Range   string // optional revision/range (e.g. HEAD~3, main..HEAD)
	Staged  bool   // staged changes only
	Symbols bool   // include touched symbols (from hunk headers); default true
	Format  string
}

// Change is one changed file.
type Change struct {
	Status   string   `json:"status"` // M, A, D, R, untracked
	Path     string   `json:"path"`
	Category string   `json:"category"`
	Lang     string   `json:"lang,omitempty"`
	Adds     int      `json:"adds"`
	Dels     int      `json:"dels"`
	Symbols  []string `json:"symbols,omitempty"` // enclosing funcs/classes touched
}

// Result is the classified diff.
type Result struct {
	Spec    string   `json:"spec"` // what was diffed (range/staged/working tree)
	Changes []Change `json:"changes"`
}

// Run collects the classified diff, renders it, and logs the call.
func Run(opts Options, w io.Writer) error {
	tracker := calllog.Start("changed", []string{"--format=" + opts.Format, opts.Range})
	res, err := Collect(opts)
	if err != nil {
		tracker.Finish(err)
		return err
	}
	tracker.SetSummary(map[string]any{"files": len(res.Changes), "spec": res.Spec})

	cw := &calllog.Counter{W: w}
	var renderErr error
	switch opts.Format {
	case "", "toon":
		renderTOON(cw, res)
	case "md":
		renderMarkdown(cw, res)
	case "json":
		renderErr = renderJSON(cw, res)
	default:
		renderErr = fmt.Errorf("unknown format %q (use toon|md|json)", opts.Format)
	}
	if renderErr == nil {
		// No single raw baseline to compare a classified diff against — the
		// footer reports this call's own cost only.
		emit.Footer(cw, cw.Tokens, 0)
	}
	tracker.RecordOutput(cw)
	tracker.Finish(renderErr)
	return renderErr
}

// Collect runs git and assembles the classified diff.
func Collect(opts Options) (*Result, error) {
	base, spec := diffBase(opts)
	res := &Result{Spec: spec}

	nameStatus, err := gitexec.Run(opts.Root, append([]string{"diff", "--name-status"}, base...)...)
	if err != nil {
		return nil, err
	}
	numstat, err := gitexec.Run(opts.Root, append([]string{"diff", "--numstat"}, base...)...)
	if err != nil {
		return nil, err
	}
	churn := parseNumstat(numstat)

	byPath := map[string]*Change{}
	order := []string{}
	for _, st := range parseNameStatus(nameStatus) {
		c := &Change{Status: st.status, Path: st.path}
		c.Category, c.Lang = classify(st.path)
		if ch, ok := churn[st.path]; ok {
			c.Adds, c.Dels = ch.adds, ch.dels
		}
		byPath[st.path] = c
		order = append(order, st.path)
	}

	if opts.Symbols {
		u0, err := gitexec.Run(opts.Root, append([]string{"diff", "-U0"}, base...)...)
		if err != nil {
			return nil, err
		}
		for path, syms := range parseHunkSymbols(u0) {
			// git's hunk-header context is only meaningful for code (it has
			// funcname drivers for Go/PHP/… ); for docs/config it's noise.
			if c, ok := byPath[path]; ok && c.Lang != "" {
				c.Symbols = syms
			}
		}
	}

	// Untracked files (only meaningful for the working-tree default).
	if !opts.Staged && opts.Range == "" {
		porcelain, err := gitexec.Run(opts.Root, "status", "--porcelain")
		if err == nil {
			for _, p := range parseUntracked(porcelain) {
				if _, seen := byPath[p]; seen {
					continue
				}
				c := &Change{Status: "untracked", Path: p, Adds: countLines(filepath.Join(opts.Root, p))}
				c.Category, c.Lang = classify(p)
				byPath[p] = c
				order = append(order, p)
			}
		}
	}

	sort.Strings(order)
	for _, p := range order {
		res.Changes = append(res.Changes, *byPath[p])
	}
	return res, nil
}

// diffBase returns the git diff revision args and a human spec string.
func diffBase(opts Options) ([]string, string) {
	switch {
	case opts.Staged:
		return []string{"--cached"}, "staged (vs HEAD)"
	case opts.Range != "":
		return []string{opts.Range}, opts.Range
	default:
		return []string{"HEAD"}, "working tree (vs HEAD)"
	}
}

type statusLine struct{ status, path string }

// parseNameStatus parses `git diff --name-status` lines (M/A/D/Rxxx/Cxxx).
func parseNameStatus(out string) []statusLine {
	var rows []statusLine
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		f := strings.Split(sc.Text(), "\t")
		if len(f) < 2 || f[0] == "" {
			continue
		}
		st := f[0][:1] // R100 -> R, C75 -> C
		path := f[len(f)-1]
		rows = append(rows, statusLine{status: st, path: path})
	}
	return rows
}

type churnEntry struct{ adds, dels int }

// parseNumstat parses `git diff --numstat` lines (adds\tdels\tpath; "-" for
// binary). Renames `old => new` are keyed by the new path.
func parseNumstat(out string) map[string]churnEntry {
	m := map[string]churnEntry{}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		f := strings.Split(sc.Text(), "\t")
		if len(f) < 3 {
			continue
		}
		path := f[2]
		if i := strings.Index(path, " => "); i >= 0 {
			path = renamedNew(path)
		}
		m[path] = churnEntry{adds: atoiDash(f[0]), dels: atoiDash(f[1])}
	}
	return m
}

// renamedNew extracts the new path from a numstat rename token, handling
// both `old => new` and `dir/{old => new}/x` brace forms.
func renamedNew(p string) string {
	if l := strings.Index(p, "{"); l >= 0 {
		if r := strings.Index(p, "}"); r > l {
			inner := p[l+1 : r]
			_, newPart, _ := strings.Cut(inner, " => ")
			return p[:l] + newPart + p[r+1:]
		}
	}
	_, newPart, _ := strings.Cut(p, " => ")
	return newPart
}

// parseHunkSymbols parses `git diff -U0` and returns, per file, the deduped
// enclosing-function/class hints git puts after the `@@ ... @@` of each hunk.
func parseHunkSymbols(out string) map[string][]string {
	res := map[string][]string{}
	seen := map[string]map[string]bool{}
	cur := ""
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "+++ b/"):
			cur = strings.TrimPrefix(line, "+++ b/")
		case strings.HasPrefix(line, "+++ "): // /dev/null (deleted)
			cur = ""
		case strings.HasPrefix(line, "@@") && cur != "":
			sym := hunkSection(line)
			if sym == "" {
				continue
			}
			if seen[cur] == nil {
				seen[cur] = map[string]bool{}
			}
			if !seen[cur][sym] {
				seen[cur][sym] = true
				res[cur] = append(res[cur], sym)
			}
		}
	}
	return res
}

// hunkSection returns the text after the second `@@` of a hunk header.
func hunkSection(line string) string {
	i := strings.Index(line, "@@")
	if i < 0 {
		return ""
	}
	j := strings.Index(line[i+2:], "@@")
	if j < 0 {
		return ""
	}
	return strings.TrimSpace(line[i+2+j+2:])
}

// parseUntracked pulls `?? path` entries from `git status --porcelain`.
func parseUntracked(out string) []string {
	var paths []string
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "?? ") {
			paths = append(paths, strings.TrimSpace(line[3:]))
		}
	}
	return paths
}

func atoiDash(s string) int {
	if s == "-" {
		return 0
	}
	n, _ := strconv.Atoi(s)
	return n
}

func countLines(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	return bytes.Count(data, []byte{'\n'})
}
