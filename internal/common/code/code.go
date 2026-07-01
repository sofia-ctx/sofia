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
// accounting, and single-symbol slice mode.
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
	API          bool   // PHP only: effective public surface (own + traits + inherited)
	Symbol       string // optional: slice just this func/method/type (single file)
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

	if opts.Symbol != "" {
		err := runSlice(cw, opts.Inputs[0], opts.Symbol)
		tracker.SetSummary(map[string]any{"slice": opts.Symbol, "file": filepath.Base(opts.Inputs[0])})
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

// runSlice emits one symbol's source from a single file (compact-or-raw).
func runSlice(w io.Writer, path, symbol string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("cannot read %s", path)
	}
	be, _ := backendFor(path)
	if be.slice == nil {
		return fmt.Errorf("symbol slice supports Go (.go) and PHP (.php), not %s", path)
	}
	text, names, sErr := be.slice(raw, symbol)
	if sErr != nil {
		if len(names) > 0 {
			sErr = fmt.Errorf("%w; available: %s", sErr, strings.Join(names, ", "))
		}
		return sErr
	}
	_, _ = emit.SmallerOf(w, []byte(ensureNL(text)), raw)
	return nil
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
