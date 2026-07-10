// Package grep implements a generic, language-agnostic TOON-emitting
// search across a project tree, built on the same walker + matcher +
// enclosing infrastructure as this toolkit's other structural tools.
// Useful when the AI needs any substring or regex hit (class names, SQL
// fragments, magic strings) surfaced in a token-cheap, structured form.
package grep

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/sofia-ctx/sofia/internal/calllog"
	"github.com/sofia-ctx/sofia/pkg/codectx"
	"github.com/sofia-ctx/sofia/pkg/emit"
	"github.com/sofia-ctx/sofia/pkg/matcher"
	"github.com/sofia-ctx/sofia/pkg/tokens"
	"github.com/sofia-ctx/sofia/pkg/walker"
)

type Options struct {
	Root          string
	Patterns      []string
	Format        string // "toon" | "md" | "json"
	CaseSensitive bool
	WordBound     bool
	Regex         bool
	Exts          []string // file extensions to include (".php" etc.); empty = all
	ExtraIgnore   []string // extra directory names to skip in addition to defaults
	MaxPerPattern int      // 0 = unlimited
}

type Hit struct {
	File      string `json:"file"`
	Line      int    `json:"line"`
	Col       int    `json:"col"`
	Enclosing string `json:"enclosing,omitempty"`
	Text      string `json:"text"`
}

type PatternResult struct {
	Pattern string `json:"pattern"`
	Files   int    `json:"files"`
	Hits    []Hit  `json:"hits"`
}

type Result struct {
	Patterns []*PatternResult `json:"patterns"`
	Empty    []string         `json:"empty,omitempty"`
	Skipped  int              `json:"skipped,omitempty"` // files skipped: binary or over-long lines
}

// DefaultIgnoreDirs are the directories every project's grep should skip
// by default (heavy, machine-managed). Extra entries can be added via
// Options.ExtraIgnore. Exported so `sf code`'s directory expansion walks the
// same tree grep does, rather than drifting with a second copy.
var DefaultIgnoreDirs = []string{
	"vendor", "node_modules", "var", ".git", ".idea", ".vscode",
	".svn", ".hg", "dist", "build", "target", "__pycache__",
}

// Scan runs the search and returns the structured result without rendering or
// logging anything — the building block a caller that wants to post-process
// hits (group them by layer, feed another tool) uses instead of Run, which
// renders and writes telemetry. It shares the same unexported scan Run drives,
// so the two never drift.
func Scan(opts Options) (*Result, error) {
	if len(opts.Patterns) == 0 {
		return nil, fmt.Errorf("no patterns to search")
	}
	if opts.Root == "" {
		opts.Root = "."
	}
	absRoot, err := filepath.Abs(opts.Root)
	if err != nil {
		return nil, err
	}
	return scan(opts, absRoot)
}

// Run executes the scan and renders the result to w.
func Run(opts Options, w io.Writer) error {
	tracker := calllog.Start("grep", append([]string{"--format=" + opts.Format}, opts.Patterns...))
	if len(opts.Patterns) == 0 {
		err := fmt.Errorf("no patterns to search")
		tracker.Finish(err)
		return err
	}
	if opts.Root == "" {
		opts.Root = "."
	}
	absRoot, err := filepath.Abs(opts.Root)
	if err != nil {
		tracker.Finish(err)
		return err
	}

	result, err := scan(opts, absRoot)
	if err != nil {
		tracker.SetSummary(map[string]any{"patterns": opts.Patterns})
		tracker.Finish(err)
		return err
	}
	tracker.SetSummary(summary(opts, result))

	cw := &calllog.Counter{W: w}
	var renderErr error
	switch opts.Format {
	case "", "toon":
		renderTOON(cw, result, opts.MaxPerPattern)
	case "md":
		renderMarkdown(cw, result, opts.MaxPerPattern)
	case "json":
		renderErr = renderJSON(cw, result)
	default:
		renderErr = fmt.Errorf("unknown format %q (use toon|md|json)", opts.Format)
	}
	if renderErr == nil {
		// No single raw baseline to compare a tree search against — the
		// footer reports this call's own cost only.
		emit.Footer(cw, cw.Tokens, 0)
	}
	tracker.RecordOutput(cw)
	tracker.Finish(renderErr)
	return renderErr
}

func summary(opts Options, r *Result) map[string]any {
	total := 0
	for _, p := range r.Patterns {
		total += len(p.Hits)
	}
	return map[string]any{
		"patterns":   opts.Patterns,
		"format":     opts.Format,
		"empty":      r.Empty,
		"total_hits": total,
		"regex":      opts.Regex,
		"skipped":    r.Skipped,
		"raw_tokens": plainTokens(r), // bare `grep -rn` equivalent, vs the TOON out_tokens
	}
}

// plainTokens estimates the cost of the bare `grep -rn` equivalent
// (file:line:text per hit). Logged alongside the actual TOON out_tokens so
// `sf history` can track whether sf grep's enclosing is paying off vs raw
// grep over real usage.
func plainTokens(r *Result) int64 {
	var b strings.Builder
	for _, pr := range r.Patterns {
		for _, h := range pr.Hits {
			fmt.Fprintf(&b, "%s:%d:%s\n", h.File, h.Line, h.Text)
		}
	}
	return tokens.Estimate(b.String())
}

func scan(opts Options, root string) (*Result, error) {
	ignoreDirs := make(map[string]bool, len(DefaultIgnoreDirs)+len(opts.ExtraIgnore))
	for _, d := range DefaultIgnoreDirs {
		ignoreDirs[d] = true
	}
	for _, d := range opts.ExtraIgnore {
		d = strings.TrimSpace(d)
		if d != "" {
			ignoreDirs[d] = true
		}
	}

	var exts map[string]bool
	if len(opts.Exts) > 0 {
		exts = make(map[string]bool, len(opts.Exts))
		for _, e := range opts.Exts {
			e = strings.TrimSpace(e)
			if e == "" {
				continue
			}
			if !strings.HasPrefix(e, ".") {
				e = "." + e
			}
			exts[strings.ToLower(e)] = true
		}
	}

	files, walkErrs := walker.Files(walker.Options{
		Root:       root,
		IgnoreDirs: ignoreDirs,
		Exts:       exts,
	})

	type fileResult struct {
		path  string
		hits  []matcher.Hit
		lines []string
	}
	results := make(chan fileResult, runtime.NumCPU()*2)
	mopts := matcher.Options{
		Patterns:  opts.Patterns,
		Case:      opts.CaseSensitive,
		WordBound: opts.WordBound,
		Regex:     opts.Regex,
	}

	var wg sync.WaitGroup
	var scanErr error
	var scanErrOnce sync.Once
	var skipped int64
	for i := 0; i < runtime.NumCPU(); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range files {
				hits, lines, err := matcher.ScanFile(path, mopts)
				if err != nil {
					// Binary / over-long-line files are skipped, not fatal —
					// one minified bundle must not kill the whole search.
					if errors.Is(err, matcher.ErrSkip) {
						atomic.AddInt64(&skipped, 1)
						continue
					}
					// regex compile errors surface here too — fail the whole run.
					scanErrOnce.Do(func() { scanErr = err })
					continue
				}
				if len(hits) == 0 {
					continue
				}
				results <- fileResult{path: path, hits: hits, lines: lines}
			}
		}()
	}
	go func() {
		wg.Wait()
		close(results)
	}()

	byPattern := make(map[string]*PatternResult, len(opts.Patterns))
	for _, p := range opts.Patterns {
		byPattern[p] = &PatternResult{Pattern: p}
	}
	fileSetByPattern := make(map[string]map[string]bool, len(opts.Patterns))
	for _, p := range opts.Patterns {
		fileSetByPattern[p] = make(map[string]bool)
	}

	for fr := range results {
		rel, _ := filepath.Rel(root, fr.path)
		rel = filepath.ToSlash(rel)
		ext := strings.ToLower(filepath.Ext(fr.path))
		for _, h := range fr.hits {
			pr := byPattern[h.Pattern]
			if pr == nil {
				continue
			}
			pr.Hits = append(pr.Hits, Hit{
				File:      rel,
				Line:      h.Line,
				Col:       h.Col,
				Enclosing: codectx.Enclosing(fr.lines, h.Line-1, ext),
				Text:      h.Text,
			})
			fileSetByPattern[h.Pattern][rel] = true
		}
	}
	for range walkErrs {
		// best-effort scan; drain silently
	}
	if scanErr != nil {
		return nil, scanErr
	}

	final := &Result{Skipped: int(skipped)}
	for _, p := range opts.Patterns {
		pr := byPattern[p]
		if len(pr.Hits) == 0 {
			final.Empty = append(final.Empty, p)
			continue
		}
		pr.Files = len(fileSetByPattern[p])
		sort.SliceStable(pr.Hits, func(i, j int) bool {
			if pr.Hits[i].File != pr.Hits[j].File {
				return pr.Hits[i].File < pr.Hits[j].File
			}
			return pr.Hits[i].Line < pr.Hits[j].Line
		})
		final.Patterns = append(final.Patterns, pr)
	}
	return final, nil
}
