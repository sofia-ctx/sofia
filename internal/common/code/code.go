// Package code implements `sf code` — a compact structural summary of source
// files, without function bodies, so the agent needn't cat a whole file just
// to see its shape/API (where read tokens go in practice).
//
// This package is a thin ROUTER: it dispatches each file by extension to a
// per-language backend library (gocode, phpcode, tscode), runs multiple files
// in parallel, and aggregates the results. Each backend is small and tested in
// isolation. The router owns the cross-cutting concerns: the compact-or-raw
// token-saver invariant (internal/emit — emit the summary only when it is
// actually cheaper than the raw file, else hand back the raw bytes), call-log
// accounting, and symbol-slice mode (one file, one or more symbols).
package code

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/sofia-ctx/sofia/internal/calllog"
	"github.com/sofia-ctx/sofia/internal/common/code/gocode"
	"github.com/sofia-ctx/sofia/internal/common/code/phpcode"
	"github.com/sofia-ctx/sofia/internal/common/code/tscode"
	"github.com/sofia-ctx/sofia/internal/emit"
)

type Options struct {
	Inputs       []string // one or more source files
	Format       string   // toon | md | json
	ExportedOnly bool
	API          bool     // PHP only: effective public surface (own + traits + inherited)
	Symbols      []string // optional: slice these func/method/type/etc. from Inputs[0] (single file)
}

// backend is a per-language implementation the router dispatches to.
type backend struct {
	// summarize renders the structural summary of path to w (toon|md|json).
	// api requests PHP's effective public surface; non-PHP backends ignore it.
	summarize func(w io.Writer, path, format string, exported, api bool) (map[string]any, error)
	// slice returns one symbol's source from src; nil when unsupported.
	slice func(src []byte, symbol string) (string, []string, error)
}

func backendFor(path string) (backend, bool) {
	switch {
	case strings.HasSuffix(path, ".go"):
		return backend{summarize: gocode.Summarize, slice: gocode.Slice}, true
	case strings.HasSuffix(path, ".php"):
		return backend{summarize: phpcode.Summarize, slice: phpcode.Slice}, true
	case hasSuffixAny(path, ".ts", ".tsx", ".vue"):
		return backend{summarize: tscode.Summarize}, true
	}
	return backend{}, false
}

// Run dispatches the requested files to their backends and writes the result.
// Multiple files are summarised in parallel and aggregated in input order.
func Run(opts Options, w io.Writer) error {
	tracker := calllog.Start("code", append([]string{"--format=" + opts.Format}, opts.Inputs...))
	cw := &calllog.Counter{W: w}

	if err := validate(opts); err != nil {
		tracker.Finish(err)
		return err
	}

	if len(opts.Symbols) > 0 {
		found, err := runSlices(cw, opts.Inputs[0], opts.Symbols)
		tracker.SetSummary(map[string]any{
			"file":    filepath.Base(opts.Inputs[0]),
			"symbols": len(opts.Symbols),
			"found":   found,
		})
		tracker.RecordOutput(cw)
		tracker.Finish(err)
		return err
	}

	blocks := renderAll(opts)
	for i, b := range blocks {
		if i > 0 {
			_, _ = cw.Write([]byte("\n"))
		}
		_, _ = cw.Write(b)
	}
	tracker.SetSummary(map[string]any{"files": len(opts.Inputs)})
	tracker.RecordOutput(cw)
	tracker.Finish(nil)
	return nil
}

func validate(opts Options) error {
	switch opts.Format {
	case "", "toon", "md", "json":
	default:
		return fmt.Errorf("unknown format %q (use toon|md|json)", opts.Format)
	}
	if len(opts.Inputs) == 0 {
		return fmt.Errorf("sf code: need at least one file")
	}
	// An unsupported extension is a usage error, not a fallback case.
	for _, p := range opts.Inputs {
		if !supportedExt(p) {
			return fmt.Errorf("sf code supports Go (.go), PHP (.php), TS/Vue (.ts/.tsx/.vue); got %s", p)
		}
	}
	return nil
}

// runSlices emits one or more symbols' source from a single file, in input
// order, honoring compact-or-raw over the COMBINED output (never worth more
// tokens than the file itself, however many symbols are requested).
//
// Partial success over hard-fail: a symbol that isn't found doesn't sink the
// whole call — whatever is found still comes back, and each miss gets a
// marked comment line with the same "available: …" suggestion the
// single-symbol hard-fail below uses, so the agent can retry the miss alone
// without re-fetching what it already has. Only when NONE of the requested
// symbols exist does the call fail outright. found reports how many were
// actually located, for call-log accounting.
//
// A single requested symbol behaves exactly as before (no header comment,
// same error on a miss) — this is the multi-symbol generalisation of the
// original single-symbol slice, not a parallel code path.
func runSlices(w io.Writer, path string, symbols []string) (found int, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("cannot read %s", path)
	}
	be, _ := backendFor(path)
	if be.slice == nil {
		return 0, fmt.Errorf("symbol slice supports Go (.go) and PHP (.php), not %s", path)
	}

	multi := len(symbols) > 1
	var out bytes.Buffer
	var available []string // names available in the file, refreshed on every miss
	var missErr error      // the backend's own not-found error, for the all-missed case
	for _, symbol := range symbols {
		text, names, sErr := be.slice(raw, symbol)
		if sErr != nil {
			missErr, available = sErr, names
			if multi {
				writeSep(&out)
				out.WriteString(notFoundComment(symbol, names))
			}
			continue
		}
		found++
		writeSep(&out)
		if multi {
			out.WriteString(symbolHeader(symbol))
		}
		out.WriteString(ensureNL(text))
	}

	if found == 0 {
		if multi {
			missErr = fmt.Errorf("none of the requested symbols were found: %s", strings.Join(symbols, ", "))
		}
		if len(available) > 0 {
			missErr = fmt.Errorf("%w; available: %s", missErr, strings.Join(available, ", "))
		}
		return 0, missErr
	}

	_, _ = emit.SmallerOf(w, out.Bytes(), raw)
	return found, nil
}

// writeSep separates consecutive symbol blocks with a blank line; a no-op
// before the first block.
func writeSep(out *bytes.Buffer) {
	if out.Len() > 0 {
		out.WriteString("\n")
	}
}

// symbolHeader marks the start of one symbol's source within a multi-symbol
// slice — plain comment syntax, so the concatenated output still reads as
// valid-ish source rather than an unmarked jumble of disjoint snippets.
func symbolHeader(symbol string) string {
	return fmt.Sprintf("// --- %s ---\n", symbol)
}

// notFoundComment marks a requested symbol that wasn't found, inline with
// whatever else the call did find. Same "available: …" wording as runSlices'
// own hard-fail error — the suggestion mechanics are shared, only the
// severity differs.
func notFoundComment(symbol string, available []string) string {
	if len(available) == 0 {
		return fmt.Sprintf("// --- %s: not found ---\n", symbol)
	}
	return fmt.Sprintf("// --- %s: not found; available: %s ---\n", symbol, strings.Join(available, ", "))
}

// renderAll summarises every input file concurrently, returning one output
// block per file in input order.
func renderAll(opts Options) [][]byte {
	blocks := make([][]byte, len(opts.Inputs))
	sem := make(chan struct{}, maxParallel())
	var wg sync.WaitGroup
	for i, path := range opts.Inputs {
		wg.Add(1)
		go func(i int, path string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			blocks[i] = renderOne(path, opts.Format, opts.ExportedOnly, opts.API)
		}(i, path)
	}
	wg.Wait()
	return blocks
}

// renderOne produces the output for one file: the structural summary, or — when
// it can't be built or wouldn't save tokens — the raw file (so the agent always
// gets the content and never has to fall back to a manual cat). JSON is exempt
// from the size comparison (machine output).
func renderOne(path, format string, exported, api bool) []byte {
	raw, _ := os.ReadFile(path)
	be, _ := backendFor(path)

	var compact bytes.Buffer
	if _, err := be.summarize(&compact, path, format, exported, api); err != nil {
		if raw != nil { // graceful fallback to the raw file
			return raw
		}
		return []byte(fmt.Sprintf("file: %s\n# %v\n", filepath.Base(path), err))
	}
	// --api deliberately surfaces methods the raw file doesn't contain (they
	// live in traits/parents), so the compact-or-raw size guard — which would
	// prefer the smaller raw file — must not apply. JSON is exempt likewise.
	if format == "json" || api {
		return compact.Bytes()
	}
	var out bytes.Buffer
	_, _ = emit.SmallerOf(&out, compact.Bytes(), raw)
	return out.Bytes()
}

func maxParallel() int {
	n := runtime.NumCPU()
	if n > 8 {
		n = 8
	}
	if n < 1 {
		n = 1
	}
	return n
}

func supportedExt(path string) bool {
	return hasSuffixAny(path, ".go", ".php", ".ts", ".tsx", ".vue")
}

func hasSuffixAny(path string, exts ...string) bool {
	for _, e := range exts {
		if strings.HasSuffix(path, e) {
			return true
		}
	}
	return false
}

// ensureNL appends a trailing newline unless the string is empty or already
// ends in one.
func ensureNL(s string) string {
	if s == "" || strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}
