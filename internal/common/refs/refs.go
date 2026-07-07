// Package refs answers "who defines/uses this symbol across the tree" in
// one call: a deterministic, word-boundary-matched scan (built on the same
// walker + matcher infrastructure as internal/common/grep) that labels every
// hit with its enclosing function/type — the def/use fan `sf grep <symbol>`
// plus opening each caller by hand would otherwise take. Go gets an AST-
// accurate enclosing label (internal/common/code/gocode); PHP/TS/TSX/Vue
// reuse internal/codectx's regex heuristics, the same ones `sf grep` uses.
package refs

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/sofia-ctx/sofia/internal/calllog"
	"github.com/sofia-ctx/sofia/internal/codectx"
	"github.com/sofia-ctx/sofia/internal/common/code/gocode"
	"github.com/sofia-ctx/sofia/internal/common/grep"
	"github.com/sofia-ctx/sofia/internal/emit"
	"github.com/sofia-ctx/sofia/internal/matcher"
	"github.com/sofia-ctx/sofia/internal/tokens"
	"github.com/sofia-ctx/sofia/internal/walker"
)

type Options struct {
	Symbol     string
	Root       string   // default "."
	Exts       []string // default defaultExts
	IgnoreDirs []string // extra, on top of grep.DefaultIgnoreDirs
	Max        int      // 0 = default 30; <0 = unlimited
	Format     string
}

// Ref is one occurrence of Symbol — a definition or a use.
type Ref struct {
	Kind      string `json:"kind"` // "def" | "use"
	File      string `json:"file"`
	Line      int    `json:"line"`
	Col       int    `json:"col"`
	Enclosing string `json:"enclosing,omitempty"`
	Text      string `json:"text"`
}

// Result is the whole answer for one symbol. Refs is already capped (defs
// first, then file, then line); Defs/Uses count the true totals found
// before capping, and Truncated is how many were dropped.
type Result struct {
	Symbol    string   `json:"symbol"`
	Refs      []Ref    `json:"refs"`
	Defs      int      `json:"defs"`
	Uses      int      `json:"uses"`
	Truncated int      `json:"truncated,omitempty"`
	Skipped   []string `json:"skipped,omitempty"`
}

// defaultExts is the parseable set refs understands (matches sf code's
// supportedExts): the languages with either an AST (Go) or a regex
// enclosing heuristic (PHP/TS/TSX/Vue).
var defaultExts = []string{".go", ".php", ".ts", ".tsx", ".vue"}

// identRe accepts a bare identifier: a leading letter/underscore/$, then
// letters/digits/underscore/$. Rejects regex metacharacters and whitespace —
// refs is not a regex tool, and word-boundary matching assumes an identifier.
var identRe = regexp.MustCompile(`^[\p{L}_$][\p{L}\p{N}_$]*$`)

func validateSymbol(sym string) error {
	if sym == "" {
		return errors.New("refs needs a symbol; try: sf refs <name>")
	}
	if !identRe.MatchString(sym) {
		return fmt.Errorf("refs takes a bare identifier, not %q (it's not a regex — word-boundary matching assumes an identifier)", sym)
	}
	return nil
}

// Run executes the scan and renders the result to w.
func Run(opts Options, w io.Writer) error {
	tracker := calllog.Start("refs", []string{"--format=" + opts.Format, opts.Symbol})
	if err := validateSymbol(opts.Symbol); err != nil {
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

	result, rawTok, err := scan(opts, absRoot)
	if err != nil {
		tracker.Finish(err)
		return err
	}

	cw := &calllog.Counter{W: w}
	var renderErr error
	switch opts.Format {
	case "", "toon":
		renderTOON(cw, result)
	case "md":
		renderMarkdown(cw, result)
	case "json":
		renderErr = renderJSON(cw, result)
	default:
		renderErr = fmt.Errorf("unknown format %q (use toon|md|json)", opts.Format)
	}
	if renderErr == nil {
		emit.Footer(cw, cw.Tokens, rawTok)
	}
	tracker.SetSummary(map[string]any{
		"symbol":  result.Symbol,
		"defs":    result.Defs,
		"uses":    result.Uses,
		"tok_raw": rawTok,
	})
	tracker.RecordOutput(cw)
	tracker.Finish(renderErr)
	return renderErr
}

// resolveMax converts the Options.Max convention (0 = default 30, negative =
// unlimited) into the cap to apply; 0 means "no cap".
func resolveMax(max int) int {
	switch {
	case max == 0:
		return 30
	case max < 0:
		return 0
	default:
		return max
	}
}

// applyCap trims all (already sorted) to capN entries, reporting how many
// were dropped. capN <= 0 means unlimited.
func applyCap(all []Ref, capN int) (kept []Ref, truncated int) {
	if capN <= 0 || len(all) <= capN {
		return all, 0
	}
	return all[:capN], len(all) - capN
}

// defRegexes compiles the per-language "this line declares SYM" patterns
// once per run. Anything outside these three families defaults to "use" in
// kindFor.
type declRegexes struct {
	goRe, phpRe, tsRe *regexp.Regexp
}

func compileDeclRegexes(sym string) declRegexes {
	q := regexp.QuoteMeta(sym)
	return declRegexes{
		goRe:  regexp.MustCompile(`^\s*(func\s+(\([^)]*\)\s*)?|type\s+|const\s+|var\s+)` + q + `\b`),
		phpRe: regexp.MustCompile(`\b(function|class|interface|trait|enum|const)\s+` + q + `\b`),
		tsRe:  regexp.MustCompile(`\b(export\s+)?(default\s+)?(async\s+)?(function|class|interface|type|enum|const|let)\s+` + q + `\b`),
	}
}

// kindFor classifies a hit as "def" or "use" textually: a language-
// appropriate declaration pattern on the hit's own line makes it a def.
// This is honest, not type-checked — a symbol declared in N places shows N
// defs.
func kindFor(ext, text string, re declRegexes) string {
	var pattern *regexp.Regexp
	switch ext {
	case ".go":
		pattern = re.goRe
	case ".php":
		pattern = re.phpRe
	case ".ts", ".tsx", ".vue":
		pattern = re.tsRe
	default:
		return "use"
	}
	if pattern.MatchString(text) {
		return "def"
	}
	return "use"
}

// enclosingGo finds the innermost decl (narrowest span) containing line,
// among the top-level decls gocode.EnclosingDecls returned for this file.
func enclosingGo(decls []gocode.Decl, line int) string {
	label, bestSpan := "", -1
	for _, d := range decls {
		if line < d.StartLine || line > d.EndLine {
			continue
		}
		span := d.EndLine - d.StartLine
		if bestSpan == -1 || span < bestSpan {
			bestSpan, label = span, d.Label
		}
	}
	return label
}

func scan(opts Options, root string) (*Result, int64, error) {
	ignoreDirs := make(map[string]bool, len(grep.DefaultIgnoreDirs)+len(opts.IgnoreDirs))
	for _, d := range grep.DefaultIgnoreDirs {
		ignoreDirs[d] = true
	}
	for _, d := range opts.IgnoreDirs {
		d = strings.TrimSpace(d)
		if d != "" {
			ignoreDirs[d] = true
		}
	}

	exts := opts.Exts
	if len(exts) == 0 {
		exts = defaultExts
	}
	extSet := make(map[string]bool, len(exts))
	for _, e := range exts {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if !strings.HasPrefix(e, ".") {
			e = "." + e
		}
		extSet[strings.ToLower(e)] = true
	}

	files, walkErrs := walker.Files(walker.Options{
		Root:       root,
		IgnoreDirs: ignoreDirs,
		Exts:       extSet,
	})

	mopts := matcher.Options{
		Patterns:  []string{opts.Symbol},
		Case:      true,
		WordBound: true,
		Regex:     false,
	}
	declRe := compileDeclRegexes(opts.Symbol)

	type fileResult struct {
		refs    []Ref
		skipped string
	}
	results := make(chan fileResult, runtime.NumCPU()*2)

	var wg sync.WaitGroup
	var scanErr error
	var scanErrOnce sync.Once
	for i := 0; i < runtime.NumCPU(); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range files {
				hits, lines, err := matcher.ScanFile(path, mopts)
				if err != nil {
					if errors.Is(err, matcher.ErrSkip) {
						rel, _ := filepath.Rel(root, path)
						results <- fileResult{skipped: filepath.ToSlash(rel)}
						continue
					}
					scanErrOnce.Do(func() { scanErr = err })
					continue
				}
				if len(hits) == 0 {
					continue
				}

				rel, _ := filepath.Rel(root, path)
				rel = filepath.ToSlash(rel)
				ext := strings.ToLower(filepath.Ext(path))

				var decls []gocode.Decl
				if ext == ".go" {
					decls = gocode.EnclosingDecls([]byte(strings.Join(lines, "\n")))
				}

				refs := make([]Ref, 0, len(hits))
				for _, h := range hits {
					var enclosing string
					if ext == ".go" {
						enclosing = enclosingGo(decls, h.Line)
					} else {
						enclosing = codectx.Enclosing(lines, h.Line-1, ext)
					}
					refs = append(refs, Ref{
						Kind:      kindFor(ext, h.Text, declRe),
						File:      rel,
						Line:      h.Line,
						Col:       h.Col,
						Enclosing: enclosing,
						Text:      h.Text,
					})
				}
				results <- fileResult{refs: refs}
			}
		}()
	}
	go func() {
		wg.Wait()
		close(results)
	}()

	var all []Ref
	var skipped []string
	for fr := range results {
		if fr.skipped != "" {
			skipped = append(skipped, fr.skipped)
			continue
		}
		all = append(all, fr.refs...)
	}
	for range walkErrs {
		// best-effort scan; drain silently
	}
	if scanErr != nil {
		return nil, 0, scanErr
	}

	sort.SliceStable(all, func(i, j int) bool {
		if (all[i].Kind == "def") != (all[j].Kind == "def") {
			return all[i].Kind == "def" // defs first
		}
		if all[i].File != all[j].File {
			return all[i].File < all[j].File
		}
		return all[i].Line < all[j].Line
	})
	sort.Strings(skipped)

	defs, uses := 0, 0
	for _, r := range all {
		if r.Kind == "def" {
			defs++
		} else {
			uses++
		}
	}
	rawTok := plainTokens(all)

	kept, truncated := applyCap(all, resolveMax(opts.Max))
	return &Result{
		Symbol:    opts.Symbol,
		Refs:      kept,
		Defs:      defs,
		Uses:      uses,
		Truncated: truncated,
		Skipped:   skipped,
	}, rawTok, nil
}

// plainTokens estimates the cost of the bare `grep -rn <symbol>` equivalent
// (file:line:text per hit, uncapped) — mirrors grep's own plainTokens so the
// footer's raw baseline is honest, not inflated with imagined code-fan
// savings.
func plainTokens(all []Ref) int64 {
	var b strings.Builder
	for _, r := range all {
		fmt.Fprintf(&b, "%s:%d:%s\n", r.File, r.Line, r.Text)
	}
	return tokens.Estimate(b.String())
}
