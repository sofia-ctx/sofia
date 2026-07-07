// Package gocode is the Go backend for `sf code`: a structural summary of a
// Go source file (package, imports, types, func/method signatures, consts,
// vars) and single-symbol slicing — via the stdlib go/parser (syntax only, no
// type-checking), so it works on any file regardless of whether it compiles.
package gocode

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io"
	"path/filepath"
	"strings"

	"github.com/sofia-ctx/sofia/internal/toon"
)

// Summarize writes the structural summary of the Go file at path to w in the
// given format (toon|md|json) and returns a call-log summary map. api (PHP's
// effective-surface flag) is not meaningful for Go and is ignored. brief
// requests the signature-only cut — see Brief for exactly what it drops.
func Summarize(w io.Writer, path, format string, exported, _, brief bool) (map[string]any, error) {
	g, err := ReadGo(path)
	if err != nil {
		return nil, err
	}
	if exported {
		g.FilterExported()
	}
	if brief {
		g.Brief()
	}
	switch format {
	case "", "toon":
		renderTOON(w, g)
	case "md":
		renderMarkdown(w, g)
	case "json":
		if err := renderJSON(w, g); err != nil {
			return nil, err
		}
	}
	return map[string]any{"lang": "go", "package": g.Package, "types": len(g.Types), "funcs": len(g.Funcs)}, nil
}

// Slice returns the source of one named symbol (signature + body + doc) from
// src, or the available names when not found.
func Slice(src []byte, symbol string) (string, []string, error) {
	return sliceGo(src, symbol)
}

// GoFile is the structural summary of one Go source file.
type GoFile struct {
	File    string    `json:"file"`
	Package string    `json:"package"`
	Imports []string  `json:"imports"`
	Types   []GoType  `json:"types"`
	Funcs   []GoFunc  `json:"funcs"` // free funcs and methods (Recv set)
	Consts  []GoValue `json:"consts,omitempty"`
	Vars    []GoValue `json:"vars,omitempty"`
}

type GoType struct {
	Kind     string `json:"kind"` // struct | interface | alias | defined
	Name     string `json:"name"`
	Detail   string `json:"detail,omitempty"` // struct fields | interface methods | underlying type — blank in Brief mode for structs
	Exported bool   `json:"exported"`
}

type GoFunc struct {
	Recv     string `json:"recv,omitempty"` // "" for free funcs, else "*Server" / "Server"
	Name     string `json:"name"`
	Sig      string `json:"sig"` // "(params) results"
	Exported bool   `json:"exported"`
}

type GoValue struct {
	Name     string `json:"name"`
	Type     string `json:"type,omitempty"`
	Exported bool   `json:"exported"`
}

// ReadGo parses a single Go file (syntax only — no type-checking, no build),
// so it works on any file regardless of whether its package compiles.
func ReadGo(path string) (*GoFile, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	g := &GoFile{File: path, Package: f.Name.Name}

	for _, imp := range f.Imports {
		p := strings.Trim(imp.Path.Value, `"`)
		if imp.Name != nil {
			p = imp.Name.Name + " " + p
		}
		g.Imports = append(g.Imports, p)
	}

	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			switch d.Tok {
			case token.TYPE:
				for _, spec := range d.Specs {
					if ts, ok := spec.(*ast.TypeSpec); ok {
						g.Types = append(g.Types, goType(fset, ts))
					}
				}
			case token.CONST:
				g.Consts = append(g.Consts, goValues(fset, d)...)
			case token.VAR:
				g.Vars = append(g.Vars, goValues(fset, d)...)
			}
		case *ast.FuncDecl:
			g.Funcs = append(g.Funcs, goFunc(fset, d))
		}
	}
	return g, nil
}

// FilterExported drops unexported (lowercase) symbols, leaving the API view.
func (g *GoFile) FilterExported() {
	types := g.Types[:0]
	for _, t := range g.Types {
		if t.Exported {
			types = append(types, t)
		}
	}
	g.Types = types
	funcs := g.Funcs[:0]
	for _, fn := range g.Funcs {
		if fn.Exported {
			funcs = append(funcs, fn)
		}
	}
	g.Funcs = funcs
	g.Consts = exportedValues(g.Consts)
	g.Vars = exportedValues(g.Vars)
}

// Brief collapses field/value-level detail for a signature-only view: struct
// field lists (and their tags) disappear — kind and name stay — while
// interface Detail is left alone (it's already just method signatures, the
// same level of detail as a free func's Sig). Consts and vars drop their
// type column, leaving bare names — the const/var equivalent of a struct's
// fields, and no more useful in a whole-package map. Composes with
// FilterExported in either order.
func (g *GoFile) Brief() {
	for i := range g.Types {
		if g.Types[i].Kind == "struct" {
			g.Types[i].Detail = ""
		}
	}
	for i := range g.Consts {
		g.Consts[i].Type = ""
	}
	for i := range g.Vars {
		g.Vars[i].Type = ""
	}
}

func exportedValues(vs []GoValue) []GoValue {
	out := vs[:0]
	for _, v := range vs {
		if v.Exported {
			out = append(out, v)
		}
	}
	return out
}

func goType(fset *token.FileSet, ts *ast.TypeSpec) GoType {
	t := GoType{Name: ts.Name.Name, Exported: ts.Name.IsExported()}
	switch u := ts.Type.(type) {
	case *ast.StructType:
		t.Kind = "struct"
		t.Detail = structDetail(fset, u)
	case *ast.InterfaceType:
		t.Kind = "interface"
		t.Detail = interfaceDetail(fset, u)
	default:
		if ts.Assign.IsValid() {
			t.Kind = "alias" // type X = Y
		} else {
			t.Kind = "defined" // type X Y
		}
		t.Detail = typeString(fset, ts.Type)
	}
	return t
}

func goFunc(fset *token.FileSet, d *ast.FuncDecl) GoFunc {
	fn := GoFunc{Name: d.Name.Name, Sig: funcSig(fset, d.Type), Exported: d.Name.IsExported()}
	if d.Recv != nil && len(d.Recv.List) > 0 {
		fn.Recv = typeString(fset, d.Recv.List[0].Type)
	}
	return fn
}

func goValues(fset *token.FileSet, d *ast.GenDecl) []GoValue {
	var out []GoValue
	for _, spec := range d.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		typ := ""
		if vs.Type != nil {
			typ = typeString(fset, vs.Type)
		}
		for _, n := range vs.Names {
			if n.Name == "_" {
				continue
			}
			out = append(out, GoValue{Name: n.Name, Type: typ, Exported: n.IsExported()})
		}
	}
	return out
}

// structDetail renders fields as "name type `tag`; ..." (embedded fields
// appear as the bare type).
func structDetail(fset *token.FileSet, st *ast.StructType) string {
	var parts []string
	for _, f := range st.Fields.List {
		typ := typeString(fset, f.Type)
		if f.Tag != nil {
			typ += " " + f.Tag.Value
		}
		if len(f.Names) == 0 {
			parts = append(parts, typ) // embedded
			continue
		}
		parts = append(parts, fieldNames(f.Names)+" "+typ)
	}
	return strings.Join(parts, "; ")
}

// interfaceDetail renders method signatures "Name(params) results; ..."
// (embedded interfaces appear as the bare type name).
func interfaceDetail(fset *token.FileSet, it *ast.InterfaceType) string {
	var parts []string
	for _, m := range it.Methods.List {
		if len(m.Names) > 0 {
			if ft, ok := m.Type.(*ast.FuncType); ok {
				parts = append(parts, m.Names[0].Name+funcSig(fset, ft))
			}
			continue
		}
		parts = append(parts, typeString(fset, m.Type)) // embedded
	}
	return strings.Join(parts, "; ")
}

// funcSig renders "(params) results" — results parenthesized when there is
// more than one or any is named.
func funcSig(fset *token.FileSet, ft *ast.FuncType) string {
	sig := "(" + fieldList(fset, ft.Params) + ")"
	if ft.Results != nil && len(ft.Results.List) > 0 {
		r := fieldList(fset, ft.Results)
		if len(ft.Results.List) > 1 || len(ft.Results.List[0].Names) > 0 {
			sig += " (" + r + ")"
		} else {
			sig += " " + r
		}
	}
	return sig
}

// fieldList renders a parameter/result list: "a, b int, c string".
func fieldList(fset *token.FileSet, fl *ast.FieldList) string {
	if fl == nil {
		return ""
	}
	var parts []string
	for _, f := range fl.List {
		typ := typeString(fset, f.Type)
		if len(f.Names) == 0 {
			parts = append(parts, typ)
			continue
		}
		parts = append(parts, fieldNames(f.Names)+" "+typ)
	}
	return strings.Join(parts, ", ")
}

func fieldNames(names []*ast.Ident) string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = n.Name
	}
	return strings.Join(out, ", ")
}

// typeString renders a type expression on a single line via go/printer.
func typeString(fset *token.FileSet, e ast.Expr) string {
	var b strings.Builder
	if err := printer.Fprint(&b, fset, e); err != nil {
		return "?"
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func renderTOON(w io.Writer, g *GoFile) {
	// Basename only: the caller already knows the path it passed, and echoing
	// the full path would scale the summary with directory depth — enough, for
	// a small file in a deep path, to push the compact output past the raw file
	// and needlessly trip the compact-or-raw fallback (see internal/emit).
	fmt.Fprintf(w, "file: %s\n", filepath.Base(g.File))
	fmt.Fprintf(w, "package: %s\n", g.Package)
	if len(g.Imports) > 0 {
		fmt.Fprintf(w, "imports[%d]: %s\n", len(g.Imports), toon.JoinList(g.Imports))
	}

	fmt.Fprintf(w, "types[%d]{kind,name,detail}:\n", len(g.Types))
	for _, t := range g.Types {
		fmt.Fprintf(w, "%s%s,%s,%s\n", toon.Indent, t.Kind, toon.Scalar(t.Name), toon.Scalar(t.Detail))
	}

	fmt.Fprintf(w, "funcs[%d]{recv,name,sig}:\n", len(g.Funcs))
	for _, fn := range g.Funcs {
		fmt.Fprintf(w, "%s%s,%s,%s\n", toon.Indent, toon.Scalar(fn.Recv), toon.Scalar(fn.Name), toon.Scalar(fn.Sig))
	}

	if len(g.Consts) > 0 {
		fmt.Fprintf(w, "consts[%d]{name,type}:\n", len(g.Consts))
		for _, c := range g.Consts {
			fmt.Fprintf(w, "%s%s,%s\n", toon.Indent, toon.Scalar(c.Name), toon.Scalar(c.Type))
		}
	}
	if len(g.Vars) > 0 {
		fmt.Fprintf(w, "vars[%d]{name,type}:\n", len(g.Vars))
		for _, v := range g.Vars {
			fmt.Fprintf(w, "%s%s,%s\n", toon.Indent, toon.Scalar(v.Name), toon.Scalar(v.Type))
		}
	}
}

func renderMarkdown(w io.Writer, g *GoFile) {
	fmt.Fprintf(w, "# %s — package `%s`\n\n", filepath.Base(g.File), g.Package)
	if len(g.Imports) > 0 {
		fmt.Fprintf(w, "**imports:** %s\n\n", strings.Join(g.Imports, ", "))
	}
	if len(g.Types) > 0 {
		fmt.Fprintln(w, "## types")
		for _, t := range g.Types {
			fmt.Fprintf(w, "- `%s %s` — %s\n", t.Kind, t.Name, t.Detail)
		}
		fmt.Fprintln(w)
	}
	if len(g.Funcs) > 0 {
		fmt.Fprintln(w, "## funcs")
		for _, fn := range g.Funcs {
			recv := ""
			if fn.Recv != "" {
				recv = "(" + fn.Recv + ") "
			}
			fmt.Fprintf(w, "- `%s%s%s`\n", recv, fn.Name, fn.Sig)
		}
		fmt.Fprintln(w)
	}
	if len(g.Consts) > 0 {
		fmt.Fprintln(w, "## consts")
		for _, c := range g.Consts {
			fmt.Fprintf(w, "- `%s` %s\n", c.Name, c.Type)
		}
		fmt.Fprintln(w)
	}
	if len(g.Vars) > 0 {
		fmt.Fprintln(w, "## vars")
		for _, v := range g.Vars {
			fmt.Fprintf(w, "- `%s` %s\n", v.Name, v.Type)
		}
	}
}

func renderJSON(w io.Writer, g *GoFile) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(g)
}

// sliceGo returns the source text of one named symbol (func/method/type/
// const/var) including its doc comment.
func sliceGo(src []byte, symbol string) (string, []string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return "", nil, err
	}
	off := func(p token.Pos) int { return fset.Position(p).Offset }
	cut := func(start, end token.Pos) string {
		return strings.TrimRight(string(src[off(start):off(end)]), " \t\n")
	}
	// genRange picks the slice bounds for a type/const/var: the whole decl
	// (keyword + doc) when it holds a single spec, else just the matched spec.
	genRange := func(d *ast.GenDecl, sp ast.Spec) (token.Pos, token.Pos) {
		if len(d.Specs) == 1 {
			start := d.Pos()
			if d.Doc != nil {
				start = d.Doc.Pos()
			}
			return start, d.End()
		}
		return sp.Pos(), sp.End()
	}

	var names []string
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			bare := d.Name.Name
			full := bare
			if d.Recv != nil && len(d.Recv.List) > 0 {
				if r := recvName(d.Recv.List[0].Type); r != "" {
					full = r + "." + bare
				}
			}
			names = append(names, full)
			if symbol == bare || symbol == full {
				start := d.Pos()
				if d.Doc != nil {
					start = d.Doc.Pos()
				}
				return cut(start, d.End()), nil, nil
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch sp := spec.(type) {
				case *ast.TypeSpec:
					names = append(names, sp.Name.Name)
					if sp.Name.Name == symbol {
						st, en := genRange(d, sp)
						return cut(st, en), nil, nil
					}
				case *ast.ValueSpec:
					for _, nm := range sp.Names {
						if nm.Name == "_" {
							continue
						}
						names = append(names, nm.Name)
						if nm.Name == symbol {
							st, en := genRange(d, sp)
							return cut(st, en), nil, nil
						}
					}
				}
			}
		}
	}
	return "", names, fmt.Errorf("symbol %q not found", symbol)
}

// recvName extracts the receiver base type name: *Server → Server,
// Server → Server, Cache[T] → Cache.
func recvName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return recvName(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr:
		return recvName(t.X)
	case *ast.IndexListExpr:
		return recvName(t.X)
	}
	return ""
}
