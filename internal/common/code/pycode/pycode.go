// Package pycode is the Python backend for `sf code`: a structural summary of
// a Python source file (module imports, top-level classes with their methods,
// module-level functions, and module-level assignments) plus single-symbol
// slicing. Go has no lightweight stdlib Python parser, so — like the PHP and TS
// backends — pycode reads structure with line/indentation heuristics rather
// than a full parser: dependency-light, cgo-free (keeping the Windows build and
// the SDK's zero-cgo story intact), and resilient to a file that doesn't fully
// parse. It tracks scope by indentation, so a nested function isn't mistaken
// for a method and a def inside a docstring isn't seen at all.
package pycode

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/sofia-ctx/sofia/pkg/toon"
)

// Summarize writes the structural summary of the Python file at path to w in
// the given format (toon|md|json) and returns a call-log summary map. The api
// flag (PHP's effective-surface notion) has no Python meaning and is ignored.
// brief requests the signature-only cut — see Brief.
func Summarize(w io.Writer, path, format string, exported, _, brief bool) (map[string]any, error) {
	p, err := ReadPy(path)
	if err != nil {
		return nil, err
	}
	if exported {
		p.FilterExported()
	}
	if brief {
		p.Brief()
	}
	switch format {
	case "", "toon":
		renderTOON(w, p)
	case "md":
		renderMarkdown(w, p)
	case "json":
		if err := renderJSON(w, p); err != nil {
			return nil, err
		}
	}
	return map[string]any{"lang": "python", "module": p.Module, "classes": len(p.Classes), "funcs": len(p.Funcs)}, nil
}

// Slice returns the source of one named symbol — a class or function, including
// its decorators and docstring — from src, or the available names when not
// found. A method is addressable as "Class.method" or by its bare name.
func Slice(src []byte, symbol string) (string, []string, error) {
	return slicePy(src, symbol)
}

// PyFile is the structural summary of one Python source file.
type PyFile struct {
	File    string    `json:"file"`
	Module  string    `json:"module"`
	Imports []string  `json:"imports,omitempty"`
	Classes []PyClass `json:"classes,omitempty"`
	Funcs   []PyFunc  `json:"funcs"` // module-level functions and methods (Class set)
	Vars    []PyVar   `json:"vars,omitempty"`
}

type PyClass struct {
	Name     string `json:"name"`
	Bases    string `json:"bases,omitempty"` // "Base1, Base2" — blank for a plain class
	Exported bool   `json:"exported"`
}

type PyFunc struct {
	Class    string `json:"class,omitempty"` // "" for a module-level function, else the enclosing class
	Name     string `json:"name"`
	Sig      string `json:"sig"` // "(params) -> ret", prefixed "async " for async defs
	Exported bool   `json:"exported"`
}

type PyVar struct {
	Name     string `json:"name"`
	Type     string `json:"type,omitempty"` // annotation when present — dropped in Brief
	Exported bool   `json:"exported"`
}

var (
	reImport = regexp.MustCompile(`^import\s+(.+)$`)
	reFrom   = regexp.MustCompile(`^from\s+(\S+)\s+import\s+(.+)$`)
	reClass  = regexp.MustCompile(`^class\s+([A-Za-z_]\w*)\s*(?:\(([^)]*)\))?\s*:`)
	reDef    = regexp.MustCompile(`^(async\s+)?def\s+([A-Za-z_]\w*)\s*\(`)
	// =([^=]|$) stands in for a negative lookahead (RE2 has none): match a
	// single "=" assignment, not "==" (comparison). Augmented forms like "+="
	// are excluded already — the "+" sits between the name and the "=".
	reVar = regexp.MustCompile(`^([A-Za-z_]\w*)\s*(?::\s*([^=]+?))?\s*=([^=]|$)`)
)

// ReadPy reads and structurally parses a Python file. A read error is
// returned; a syntactically odd file is not — the heuristics degrade to
// whatever they can recognise rather than failing.
func ReadPy(path string) (*PyFile, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	p := parsePy(src)
	p.File = path
	p.Module = strings.TrimSuffix(filepath.Base(path), ".py")
	return p, nil
}

// scope is one open indentation scope (a class or def) on the parse stack.
type scope struct {
	kind     string // "class" | "def"
	name     string
	indent   int
	recorded bool // a top-level class whose methods we attribute; false for nested
}

func parsePy(src []byte) *PyFile {
	p := &PyFile{}
	lines := strings.Split(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n")
	var stack []scope
	var triple string // active triple-quote delimiter, "" when not inside one

	for i := 0; i < len(lines); i++ {
		raw := lines[i]
		trimmed := strings.TrimSpace(raw)

		// Skip the body of a triple-quoted string (docstrings, blobs) so its
		// contents can't be mistaken for code.
		if triple != "" {
			if strings.Contains(raw, triple) {
				triple = ""
			}
			continue
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "@") {
			continue
		}
		if d := opensTriple(trimmed); d != "" {
			triple = d
			continue
		}

		indent := indentOf(raw)
		// Dedent: close every scope at least as indented as this line.
		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}
		enclosing := ""
		enclosingIsClass := false
		if n := len(stack); n > 0 {
			enclosing = stack[n-1].name
			enclosingIsClass = stack[n-1].kind == "class" && stack[n-1].recorded
		}

		switch {
		case indent == 0 && reImport.MatchString(trimmed):
			p.Imports = append(p.Imports, splitImport(trimmed)...)
		case indent == 0 && reFrom.MatchString(trimmed):
			p.Imports = append(p.Imports, splitImport(trimmed)...)
		case reClass.MatchString(trimmed):
			m := reClass.FindStringSubmatch(trimmed)
			name, bases := m[1], normWS(m[2])
			topLevel := len(stack) == 0
			if topLevel {
				p.Classes = append(p.Classes, PyClass{Name: name, Bases: bases, Exported: exportedName(name)})
			}
			stack = append(stack, scope{kind: "class", name: name, indent: indent, recorded: topLevel})
		case reDef.MatchString(trimmed):
			header, end := gatherHeader(lines, i)
			i = end
			m := reDef.FindStringSubmatch(trimmed)
			async, name := m[1] != "", m[2]
			sig := sigFromHeader(header)
			if async {
				sig = "async " + sig
			}
			// A def is a method only when its immediate enclosing scope is a
			// class; a def inside a def (closure/helper) is skipped.
			if enclosingIsClass {
				p.Funcs = append(p.Funcs, PyFunc{Class: enclosing, Name: name, Sig: sig, Exported: exportedName(name)})
			} else if enclosing == "" {
				p.Funcs = append(p.Funcs, PyFunc{Name: name, Sig: sig, Exported: exportedName(name)})
			}
			stack = append(stack, scope{kind: "def", name: name, indent: indent})
		case indent == 0 && reVar.MatchString(trimmed):
			m := reVar.FindStringSubmatch(trimmed)
			p.Vars = append(p.Vars, PyVar{Name: m[1], Type: normWS(m[2]), Exported: exportedName(m[1])})
		}
	}
	return p
}

// gatherHeader returns a def's full header text (joined onto one line) and the
// index of its last line — following a signature that wraps across lines until
// the parentheses balance.
func gatherHeader(lines []string, i int) (string, int) {
	var b strings.Builder
	depth := 0
	for j := i; j < len(lines); j++ {
		b.WriteString(lines[j])
		b.WriteByte(' ')
		for _, r := range lines[j] {
			switch r {
			case '(', '[', '{':
				depth++
			case ')', ']', '}':
				depth--
			}
		}
		if depth <= 0 && strings.Contains(lines[j], ":") {
			return b.String(), j
		}
	}
	return b.String(), len(lines) - 1
}

// sigFromHeader extracts "(params) -> ret" from a def header, collapsing any
// wrapped whitespace onto one line.
func sigFromHeader(header string) string {
	open := strings.Index(header, "(")
	if open < 0 {
		return "()"
	}
	depth, close := 0, -1
	for i := open; i < len(header); i++ {
		switch header[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				close = i
			}
		}
		if close >= 0 {
			break
		}
	}
	if close < 0 {
		return "(" + trimParams(header[open+1:]) + ")"
	}
	sig := "(" + trimParams(header[open+1:close]) + ")"
	if arrow := strings.Index(header[close:], "->"); arrow >= 0 {
		rest := header[close+arrow+2:]
		if colon := strings.LastIndex(rest, ":"); colon >= 0 {
			rest = rest[:colon]
		}
		if ret := normWS(rest); ret != "" {
			sig += " -> " + ret
		}
	}
	return sig
}

// splitImport turns an import statement into comma-free entries: `import a, b`
// → "a","b"; `import a.b as c` → "a.b as c"; `from x import a, b` → "x:a","x:b"
// (module-qualified names, kept comma-free so the TOON imports list stays
// unambiguous).
func splitImport(stmt string) []string {
	if m := reFrom.FindStringSubmatch(stmt); m != nil {
		mod := m[1]
		var out []string
		for _, name := range strings.Split(m[2], ",") {
			name = strings.TrimSpace(strings.Trim(name, "()"))
			if name == "" {
				continue
			}
			out = append(out, mod+":"+name)
		}
		return out
	}
	m := reImport.FindStringSubmatch(stmt)
	if m == nil {
		return nil
	}
	var out []string
	for _, part := range strings.Split(m[1], ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// opensTriple reports the delimiter of a triple-quoted string that a line opens
// but does not close (so its body should be skipped), or "" otherwise.
func opensTriple(trimmed string) string {
	for _, d := range []string{`"""`, `'''`} {
		first := strings.Index(trimmed, d)
		if first < 0 {
			continue
		}
		// Closed on the same line (a one-line docstring) → not an open block.
		if strings.Contains(trimmed[first+3:], d) {
			return ""
		}
		return d
	}
	return ""
}

func indentOf(line string) int {
	n := 0
	for _, r := range line {
		switch r {
		case ' ':
			n++
		case '\t':
			n += 8
		default:
			return n
		}
	}
	return n
}

func normWS(s string) string { return strings.Join(strings.Fields(s), " ") }

// trimParams collapses a parameter list onto one line and drops the trailing
// comma a wrapped signature leaves behind (`def f(\n a,\n b,\n)`).
func trimParams(s string) string {
	return strings.TrimSpace(strings.TrimSuffix(normWS(s), ","))
}

// exportedName reports whether a Python name is part of the public surface:
// anything not starting with "_", plus dunder names (__init__, __repr__…),
// which are API even though they start with "_".
func exportedName(n string) bool {
	if strings.HasPrefix(n, "__") && strings.HasSuffix(n, "__") {
		return true
	}
	return !strings.HasPrefix(n, "_")
}

// FilterExported drops underscored (private) symbols, leaving the public API.
func (p *PyFile) FilterExported() {
	classes := p.Classes[:0]
	for _, c := range p.Classes {
		if c.Exported {
			classes = append(classes, c)
		}
	}
	p.Classes = classes
	funcs := p.Funcs[:0]
	for _, fn := range p.Funcs {
		if fn.Exported {
			funcs = append(funcs, fn)
		}
	}
	p.Funcs = funcs
	vars := p.Vars[:0]
	for _, v := range p.Vars {
		if v.Exported {
			vars = append(vars, v)
		}
	}
	p.Vars = vars
}

// Brief drops module-level annotations for a signature-only view; classes keep
// their (short) base list and funcs keep their sigs, which are already the
// signature-level detail.
func (p *PyFile) Brief() {
	for i := range p.Vars {
		p.Vars[i].Type = ""
	}
}

func renderTOON(w io.Writer, p *PyFile) {
	// Basename only — see gocode.renderTOON for why the full path is elided.
	fmt.Fprintf(w, "file: %s\n", filepath.Base(p.File))
	fmt.Fprintf(w, "module: %s\n", p.Module)
	if len(p.Imports) > 0 {
		fmt.Fprintf(w, "imports[%d]: %s\n", len(p.Imports), toon.JoinList(p.Imports))
	}
	fmt.Fprintf(w, "classes[%d]{name,bases}:\n", len(p.Classes))
	for _, c := range p.Classes {
		fmt.Fprintf(w, "%s%s,%s\n", toon.Indent, toon.Scalar(c.Name), toon.Scalar(c.Bases))
	}
	fmt.Fprintf(w, "funcs[%d]{class,name,sig}:\n", len(p.Funcs))
	for _, fn := range p.Funcs {
		fmt.Fprintf(w, "%s%s,%s,%s\n", toon.Indent, toon.Scalar(fn.Class), toon.Scalar(fn.Name), toon.Scalar(fn.Sig))
	}
	if len(p.Vars) > 0 {
		fmt.Fprintf(w, "vars[%d]{name,type}:\n", len(p.Vars))
		for _, v := range p.Vars {
			fmt.Fprintf(w, "%s%s,%s\n", toon.Indent, toon.Scalar(v.Name), toon.Scalar(v.Type))
		}
	}
}

func renderMarkdown(w io.Writer, p *PyFile) {
	fmt.Fprintf(w, "# %s — module `%s`\n\n", filepath.Base(p.File), p.Module)
	if len(p.Imports) > 0 {
		fmt.Fprintf(w, "**imports:** %s\n\n", strings.Join(p.Imports, ", "))
	}
	if len(p.Classes) > 0 {
		fmt.Fprintln(w, "## classes")
		for _, c := range p.Classes {
			if c.Bases != "" {
				fmt.Fprintf(w, "- `class %s(%s)`\n", c.Name, c.Bases)
			} else {
				fmt.Fprintf(w, "- `class %s`\n", c.Name)
			}
		}
		fmt.Fprintln(w)
	}
	if len(p.Funcs) > 0 {
		fmt.Fprintln(w, "## funcs")
		for _, fn := range p.Funcs {
			cls := ""
			if fn.Class != "" {
				cls = fn.Class + "."
			}
			fmt.Fprintf(w, "- `%s%s%s`\n", cls, fn.Name, fn.Sig)
		}
		fmt.Fprintln(w)
	}
	if len(p.Vars) > 0 {
		fmt.Fprintln(w, "## vars")
		for _, v := range p.Vars {
			fmt.Fprintf(w, "- `%s` %s\n", v.Name, v.Type)
		}
	}
}

func renderJSON(w io.Writer, p *PyFile) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(p)
}

// slicePy returns the source text of one named class or function (with its
// decorators and body) by scanning indentation: the block runs from the def/
// class header — plus any decorator lines immediately above it — to the next
// non-blank line indented no deeper than the header.
func slicePy(src []byte, symbol string) (string, []string, error) {
	lines := strings.Split(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n")
	cls, meth, qualified := strings.Cut(symbol, ".")

	var names []string
	var stack []scope
	var triple string
	for i := 0; i < len(lines); i++ {
		raw := lines[i]
		trimmed := strings.TrimSpace(raw)
		if triple != "" {
			if strings.Contains(raw, triple) {
				triple = ""
			}
			continue
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "@") {
			continue
		}
		if d := opensTriple(trimmed); d != "" {
			triple = d
			continue
		}
		indent := indentOf(raw)
		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}
		enclosing := ""
		if n := len(stack); n > 0 && stack[n-1].kind == "class" {
			enclosing = stack[n-1].name
		}

		var kind, name string
		headerStart, headerEnd := i, i
		if m := reClass.FindStringSubmatch(trimmed); m != nil {
			kind, name = "class", m[1]
		} else if m := reDef.FindStringSubmatch(trimmed); m != nil {
			kind, name = "def", m[2]
			_, headerEnd = gatherHeader(lines, i)
			i = headerEnd
		}
		if kind == "" {
			continue
		}

		full := name
		if enclosing != "" {
			full = enclosing + "." + name
			names = append(names, full)
		} else {
			names = append(names, name)
		}
		match := (qualified && cls == enclosing && meth == name) ||
			(!qualified && name == symbol) ||
			full == symbol
		if match {
			return blockText(lines, withDecorators(lines, headerStart), headerEnd, indent), nil, nil
		}
		stack = append(stack, scope{kind: kind, name: name, indent: indent})
	}
	return "", names, fmt.Errorf("symbol %q not found", symbol)
}

// withDecorators walks upward from a header line over contiguous decorator
// lines (@... at the same-or-greater indent) and returns the first line index
// of the block to slice.
func withDecorators(lines []string, header int) int {
	start := header
	for start > 0 {
		prev := strings.TrimSpace(lines[start-1])
		if strings.HasPrefix(prev, "@") {
			start--
			continue
		}
		break
	}
	return start
}

// blockText returns the source from start (the first decorator/header line)
// through the body: every line after the header down to — but not including —
// the first non-blank line indented no deeper than the header. Trailing blank
// lines are trimmed.
func blockText(lines []string, start, headerEnd, headerIndent int) string {
	end := headerEnd
	for j := headerEnd + 1; j < len(lines); j++ {
		if strings.TrimSpace(lines[j]) == "" {
			continue
		}
		if indentOf(lines[j]) <= headerIndent {
			break
		}
		end = j
	}
	return strings.TrimRight(strings.Join(lines[start:end+1], "\n"), " \t\n")
}
