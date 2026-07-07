// Package code implements `sf code` — a compact structural summary of source
// files, without function bodies, so the agent needn't cat a whole file just
// to see its shape/API (where read tokens go in practice).
//
// This package is a thin ROUTER: it dispatches each file by extension to a
// per-language backend library (gocode, phpcode, tscode), runs multiple files
// in parallel, and aggregates the results. Each backend is small and tested in
// isolation. The router owns the cross-cutting concerns: the compact-or-raw
// token-saver invariant (internal/emit — emit the summary only when it is
// actually cheaper than the raw file, else hand back the raw bytes), the
// below-threshold raw passthrough ("never worse than cat" — see rawBelow),
// call-log accounting, and symbol-slice mode (one file, one or more symbols).
package code

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/sofia-ctx/sofia/internal/calllog"
	"github.com/sofia-ctx/sofia/internal/common/code/gocode"
	"github.com/sofia-ctx/sofia/internal/common/code/phpcode"
	"github.com/sofia-ctx/sofia/internal/common/code/tscode"
	"github.com/sofia-ctx/sofia/internal/common/grep"
	"github.com/sofia-ctx/sofia/internal/dedup"
	"github.com/sofia-ctx/sofia/internal/emit"
	"github.com/sofia-ctx/sofia/internal/tokens"
	"github.com/sofia-ctx/sofia/internal/walker"
)

type Options struct {
	Inputs       []string // one or more source files, directories, or glob patterns
	Format       string   // toon | md | json
	ExportedOnly bool
	API          bool     // PHP only: effective public surface (own + traits + inherited)
	Symbols      []string // optional: slice these func/method/type/etc. from Inputs[0] (single file)
	Force        bool     // bypass the dedup stub for a repeated call (still records itself, see internal/dedup)
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

	// Directory/glob expansion is a multi-file-mode concept; slice mode's
	// single real file is already enforced by validate above, so it skips
	// this and keeps Inputs untouched.
	if len(opts.Symbols) == 0 {
		expanded, err := expandInputs(opts.Inputs)
		if err != nil {
			tracker.Finish(err)
			return err
		}
		opts.Inputs = expanded
	}
	if err := checkExts(opts.Inputs); err != nil {
		tracker.Finish(err)
		return err
	}

	g := dedup.Begin("code", opts.Force, keyParts(opts)...)
	if h := g.Hit(); h != nil {
		dedup.WriteStub(cw, opts.Format, h)
		emit.Footer(cw, cw.Tokens, h.Tok)
		g.CommitStub()
		// tok_rep: what the identical full answer cost when first produced —
		// the quota report's savings baseline for a stub (see `sf cc value
		// --quota`), same role tok_raw plays for a full call below.
		tracker.SetSummary(map[string]any{"dedup": true, "dup_of": h.N, "tok_rep": h.Tok})
		tracker.RecordOutput(cw)
		tracker.Finish(nil)
		return nil
	}

	below := rawBelow()

	if len(opts.Symbols) > 0 {
		found, rawN, rawTok, err := runSlices(cw, opts.Inputs[0], opts.Symbols, below)
		tracker.SetSummary(map[string]any{
			"file":    filepath.Base(opts.Inputs[0]),
			"symbols": len(opts.Symbols),
			"found":   found,
			"raw":     rawN,
			// tok_raw: the whole-file estimate this slice was measured
			// against — the quota report's savings baseline (see `sf cc
			// value --quota`), not persisted until now (gap #1).
			"tok_raw": rawTok,
		})
		if err == nil {
			emit.Footer(cw, cw.Tokens, rawTok)
			g.CommitFull(cw.Tokens)
		}
		tracker.RecordOutput(cw)
		tracker.Finish(err)
		return err
	}

	blocks := renderAll(opts, below)
	rawN, rawTok := 0, int64(0)
	for i, b := range blocks {
		if i > 0 {
			_, _ = cw.Write([]byte("\n"))
		}
		_, _ = cw.Write(b.out)
		if b.raw {
			rawN++
		}
		rawTok += b.rawTok
	}
	emit.Footer(cw, cw.Tokens, rawTok)
	g.CommitFull(cw.Tokens)
	// tok_raw: the combined raw-file estimate the footer already compared
	// against — recorded here so it survives past the process (gap #1: the
	// footer prints it but the log never kept it).
	tracker.SetSummary(map[string]any{"files": len(opts.Inputs), "raw": rawN, "tok_raw": rawTok})
	tracker.RecordOutput(cw)
	tracker.Finish(nil)
	return nil
}

// keyParts builds the dedup key for one `sf code` call: the working
// directory, the effective format, the exported/api flags, each requested
// symbol, and — the part that matters most — each input's size+mtime, so
// editing a file between two otherwise-identical calls busts the dedup and
// the re-read goes through in full.
func keyParts(opts Options) []string {
	format := opts.Format
	if format == "" {
		format = "toon"
	}
	cwd, _ := os.Getwd()
	parts := []string{
		"cwd=" + cwd,
		"fmt=" + format,
		fmt.Sprintf("exported=%v", opts.ExportedOnly),
		fmt.Sprintf("api=%v", opts.API),
	}
	for _, s := range opts.Symbols {
		parts = append(parts, "sym="+s)
	}
	for _, p := range opts.Inputs {
		if fi, err := os.Stat(p); err == nil {
			parts = append(parts, fmt.Sprintf("in=%s@%d:%d", p, fi.Size(), fi.ModTime().UnixNano()))
		} else {
			parts = append(parts, "in="+p+"@!")
		}
	}
	return parts
}

// defaultRawBelow is the raw-passthrough floor: a file smaller than this
// is returned raw (behind a one-line marker header) instead of summarised
// or sliced. Numerically the same measured floor as the Read-hook's
// defaultMinBytes (internal/common/hook) — the project's own A/B showed a
// structural round-trip losing to a plain read below ≈8 KB — but kept a
// separate constant on purpose: the hook gates other tools' Reads, this
// gates sf code's own output, and the two knobs can move independently.
const defaultRawBelow = 8192

// rawBelow reads SOFIA_CODE_RAW_BELOW: integer bytes, 0 disables the
// passthrough entirely, unset/invalid → defaultRawBelow. Same Sscanf
// parsing as hook.MinBytes(), except 0 is meaningful here (the off
// switch), so only negatives are rejected.
func rawBelow() int64 {
	var n int64
	if _, err := fmt.Sscanf(os.Getenv("SOFIA_CODE_RAW_BELOW"), "%d", &n); err == nil && n >= 0 {
		return n
	}
	return defaultRawBelow
}

// passthroughBlock renders a below-threshold file as its raw content behind
// a one-line marker header, so the agent sees WHY there is no structure
// without paying for one. For --format json the marker becomes an object
// with an explicit raw flag (a bare content dump inside a JSON stream would
// be indistinguishable from a broken summary). symbols, when non-empty,
// marks slice mode: the header notes the requested symbols ride along in
// the full file.
func passthroughBlock(path string, raw []byte, format string, below int64, symbols []string) []byte {
	note := fmt.Sprintf("%s < %s — %s", sizeK(int64(len(raw))), sizeK(below), rawReason(symbols))
	if format == "json" {
		return rawFileJSON(path, note, raw)
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "# raw: %s (%s)\n", path, note)
	b.Write(raw)
	return b.Bytes()
}

func rawReason(symbols []string) string {
	if len(symbols) > 0 {
		return "full file (includes " + strings.Join(symbols, ", ") + ")"
	}
	return "full file is cheaper than structure"
}

// rawFile is the JSON shape of a passthrough block: the raw marker plus the
// same human-readable note the toon/md header carries.
type rawFile struct {
	File    string `json:"file"`
	Raw     bool   `json:"raw"`
	Note    string `json:"note"`
	Content string `json:"content"`
}

func rawFileJSON(path, note string, raw []byte) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	_ = enc.Encode(rawFile{File: path, Raw: true, Note: note, Content: string(raw)})
	return b.Bytes()
}

// sizeK renders a byte count the way the hook's nudge text does (%.1fK),
// except whole multiples of 1024 drop the decimal (8192 → "8K").
func sizeK(n int64) string {
	if n%1024 == 0 {
		return fmt.Sprintf("%dK", n/1024)
	}
	return fmt.Sprintf("%.1fK", float64(n)/1024)
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
	if len(opts.Symbols) > 0 {
		// classifyArgs (the CLI path) never builds this shape, but Options
		// can be constructed directly (MCP), so guard it here too.
		if len(opts.Inputs) != 1 {
			return fmt.Errorf("sf code: symbol slicing takes exactly one file, got %d", len(opts.Inputs))
		}
		if looksExpandable(opts.Inputs[0]) {
			return fmt.Errorf("sf code: symbol slicing needs one real file, not a directory or glob pattern: %s", opts.Inputs[0])
		}
	}
	return nil
}

// checkExts is the ext-support check, run AFTER directory/glob expansion so
// it sees only what will actually be summarised. Expansion itself only ever
// emits supported-ext files, so a rejection here can only come from a
// literal file argument the caller named directly.
func checkExts(inputs []string) error {
	for _, p := range inputs {
		if !supportedExt(p) {
			return fmt.Errorf("sf code supports Go (.go), PHP (.php), TS/Vue (.ts/.tsx/.vue); got %s", p)
		}
	}
	return nil
}

// looksExpandable reports whether in would be expanded by expandInputs
// rather than treated as a single literal file: a directory, or a glob
// pattern that doesn't itself name an existing file.
func looksExpandable(in string) bool {
	if fi, err := os.Stat(in); err == nil {
		return fi.IsDir()
	}
	return isGlobPattern(in)
}

func isGlobPattern(s string) bool {
	return strings.ContainsAny(s, "*?[")
}

// maxExpandedFiles caps one call's expanded file count: past this, the
// output would be big enough that a narrower path (or --brief) is the
// honest answer, not a silent truncation. Fixed on purpose — no env knob.
const maxExpandedFiles = 250

// supportedExts is the extension allow-list directory/glob expansion filters
// to — the same languages backendFor dispatches (kept as a map here so
// walker.Options.Exts can use it directly).
var supportedExts = map[string]bool{".go": true, ".php": true, ".ts": true, ".tsx": true, ".vue": true}

// expandInputs turns each input into one or more supported-extension files:
//   - a directory expands recursively (internal/walker, same default ignores
//     `sf grep` uses — vendor/, node_modules/, .git/, and friends);
//   - a glob pattern (contains *?[) that doesn't itself name an existing file
//     expands via filepath.Glob — matched directories recurse the same way,
//     matched files are filtered to supported extensions;
//   - anything else (including a nonexistent literal path) passes through
//     unchanged, so a typo'd filename still gets Run's own per-file error
//     instead of a confusing expansion failure.
//
// Every file is normalised to its absolute path (backends only ever print
// the basename, so this is invisible in the output) and duplicates across
// inputs are dropped, keeping the first occurrence — the same file reached
// two ways contributes once. A directory or glob that expands to nothing is
// a named error, not a silent no-op; the merged result over 250 files is a
// named error too, rather than a silent truncation.
func expandInputs(inputs []string) ([]string, error) {
	var out []string
	seen := make(map[string]bool, len(inputs))
	for _, in := range inputs {
		files, err := expandOne(in)
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			af, err := filepath.Abs(f)
			if err != nil {
				af = f
			}
			if seen[af] {
				continue
			}
			seen[af] = true
			out = append(out, af)
		}
	}
	if len(out) > maxExpandedFiles {
		return nil, fmt.Errorf("sf code: %d files matched — too many for one call (limit %d); narrow the path or add --brief", len(out), maxExpandedFiles)
	}
	return out, nil
}

// expandOne expands a single input arg; see expandInputs for the rules.
func expandOne(in string) ([]string, error) {
	if fi, err := os.Stat(in); err == nil {
		if !fi.IsDir() {
			return []string{in}, nil // literal file, whatever its extension — checkExts reports a precise error
		}
		files, err := walkSupported(in)
		if err != nil {
			return nil, err
		}
		if len(files) == 0 {
			return nil, fmt.Errorf("%s: no supported files (.go/.php/.ts/.tsx/.vue) under this directory", in)
		}
		return files, nil
	}
	if !isGlobPattern(in) {
		return []string{in}, nil // nonexistent literal path — Run's per-file error handles it
	}
	matches, err := filepath.Glob(in)
	if err != nil {
		return nil, fmt.Errorf("%s: bad glob pattern: %w", in, err)
	}
	var files []string
	for _, m := range matches {
		fi, err := os.Stat(m)
		if err != nil {
			continue
		}
		if fi.IsDir() {
			sub, err := walkSupported(m)
			if err != nil {
				return nil, err
			}
			files = append(files, sub...)
			continue
		}
		if supportedExt(m) {
			files = append(files, m)
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("%s: glob matched no supported files (.go/.php/.ts/.tsx/.vue)", in)
	}
	sort.Strings(files)
	return files, nil
}

// walkSupported recursively lists supported-extension files under dir, using
// the same default ignore dirs as `sf grep` (internal/common/grep). Sorted
// for determinism — filesystem iteration order isn't guaranteed stable, and
// byte-stable output is a cache feature (repeated identical calls dedup).
func walkSupported(dir string) ([]string, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	ignoreDirs := make(map[string]bool, len(grep.DefaultIgnoreDirs))
	for _, d := range grep.DefaultIgnoreDirs {
		ignoreDirs[d] = true
	}
	files, errs := walker.Files(walker.Options{Root: root, IgnoreDirs: ignoreDirs, Exts: supportedExts})
	var out []string
	for f := range files {
		out = append(out, f)
	}
	if err := <-errs; err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
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
//
// Below the passthrough threshold the file isn't sliced at all: slicing a
// tiny file is pure ceremony, so the whole raw file comes back with a
// header noting the requested symbols are included in full. rawN reports
// that (0 or 1) for call-log accounting; found then counts the symbols as
// delivered-by-containment — nothing was parsed to verify them, which is
// the point. rawTok is the estimated token cost of the raw file, for the
// cost footer.
func runSlices(w io.Writer, path string, symbols []string, below int64) (found, rawN int, rawTok int64, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("cannot read %s", path)
	}
	rawTok = tokens.Estimate(string(raw))
	be, _ := backendFor(path)
	if be.slice == nil {
		return 0, 0, rawTok, fmt.Errorf("symbol slice supports Go (.go) and PHP (.php), not %s", path)
	}
	if below > 0 && int64(len(raw)) < below {
		// Slice output ignores opts.Format today (it is always plain
		// source); the passthrough header follows suit.
		_, _ = w.Write(passthroughBlock(path, raw, "", below, symbols))
		return len(symbols), 1, rawTok, nil
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
		return 0, 0, rawTok, missErr
	}

	_, _ = emit.SmallerOf(w, out.Bytes(), raw)
	return found, 0, rawTok, nil
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

// rendered is one file's output block plus the router's bookkeeping about
// how it was produced.
type rendered struct {
	out    []byte
	raw    bool  // below-threshold passthrough (not the compact-or-raw fallback)
	rawTok int64 // estimated token cost of the raw file, for the cost footer
}

// renderAll summarises every input file concurrently, returning one output
// block per file in input order.
func renderAll(opts Options, below int64) []rendered {
	blocks := make([]rendered, len(opts.Inputs))
	sem := make(chan struct{}, maxParallel())
	var wg sync.WaitGroup
	for i, path := range opts.Inputs {
		wg.Add(1)
		go func(i int, path string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			blocks[i] = renderOne(path, opts.Format, opts.ExportedOnly, opts.API, below)
		}(i, path)
	}
	wg.Wait()
	return blocks
}

// renderOne produces the output for one file: the structural summary, or — when
// it can't be built or wouldn't save tokens — the raw file (so the agent always
// gets the content and never has to fall back to a manual cat). JSON is exempt
// from the size comparison (machine output).
//
// Below the passthrough threshold the raw file IS the cheap form, so the
// structural detour is skipped entirely ("never worse than cat"). --api is
// exempt for the same reason it skips the size guard below: its output
// includes trait/parent methods the raw file doesn't contain.
func renderOne(path, format string, exported, api bool, below int64) rendered {
	raw, _ := os.ReadFile(path)
	rt := tokens.Estimate(string(raw))
	if raw != nil && !api && below > 0 && int64(len(raw)) < below {
		return rendered{out: passthroughBlock(path, raw, format, below, nil), raw: true, rawTok: rt}
	}
	be, _ := backendFor(path)

	var compact bytes.Buffer
	if _, err := be.summarize(&compact, path, format, exported, api); err != nil {
		if raw != nil { // graceful fallback to the raw file
			return rendered{out: raw, rawTok: rt}
		}
		return rendered{out: []byte(fmt.Sprintf("file: %s\n# %v\n", filepath.Base(path), err))}
	}
	// --api deliberately surfaces methods the raw file doesn't contain (they
	// live in traits/parents), so the compact-or-raw size guard — which would
	// prefer the smaller raw file — must not apply. JSON is exempt likewise.
	if format == "json" || api {
		return rendered{out: compact.Bytes(), rawTok: rt}
	}
	var out bytes.Buffer
	_, _ = emit.SmallerOf(&out, compact.Bytes(), raw)
	return rendered{out: out.Bytes(), rawTok: rt}
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
