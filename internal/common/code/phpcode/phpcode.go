// Package phpcode is the PHP backend for `sf code`: a structural summary of a
// PHP file (namespace, class/interface/trait/enum, extends/implements,
// attributes, enum cases, constructor deps, properties, method signatures) and
// single-method slicing. It reuses the shared VKCOM-based reader
// (internal/common/php), which normalizes PHP 8.2–8.5 to the 8.1 grammar.
package phpcode

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/sofia-ctx/sofia/pkg/php"
	"github.com/sofia-ctx/sofia/pkg/toon"
)

// Summarize writes the structural summary of the PHP file at path to w. When
// api is set and the file declares a class or trait, the methods block becomes
// the effective public surface — own methods plus those composed via traits
// and inherited from parent classes (see effectiveSurface). api implies the
// public-only filter. brief drops properties and enum cases' backing values
// (see Brief). The json format stays structural (machine output); callers
// wanting the flattened surface use toon/md.
func Summarize(w io.Writer, path, format string, exported, api, brief bool) (map[string]any, error) {
	s, err := php.Read(path)
	if err != nil {
		return nil, err
	}
	if brief {
		Brief(s)
	}
	apiMode := api && (s.Kind == php.KindClass || s.Kind == php.KindTrait)
	var surface []methodVia
	var notes []string
	if apiMode {
		surface, notes = effectiveSurface(s, path)
	}
	exp := exported || api // --api implies public-only
	switch format {
	case "", "toon":
		renderTOON(w, s, exp, apiMode, surface, notes)
	case "md":
		renderMarkdown(w, s, exp, apiMode, surface, notes)
	case "json":
		if err := renderJSON(w, s); err != nil {
			return nil, err
		}
	}
	methods := len(s.Methods)
	if apiMode {
		methods = len(surface)
	}
	return map[string]any{"lang": "php", "kind": s.Kind, "methods": methods, "properties": len(s.Properties)}, nil
}

// Slice returns the source of one named method from src.
func Slice(src []byte, symbol string) (string, []string, error) {
	return php.Slice(src, symbol)
}

// Brief collapses field/value-level detail for a signature-only view:
// properties disappear entirely (they're the class-body equivalent of Go
// struct fields), and enum cases keep their names but drop the backing value
// — class/interface/trait name, extends/implements, constructor and method
// signatures are all left alone, since those already sit at signature level.
// Dropping every case's value also makes enumIsBacked report false, so
// rendering falls through to the existing name-only cases header for free.
func Brief(s *php.Symbol) {
	s.Properties = nil
	for i := range s.Cases {
		s.Cases[i].Value = ""
	}
}

// phpProperties returns the properties, filtered to public when exported.
func phpProperties(s *php.Symbol, exported bool) []php.Property {
	if !exported {
		return s.Properties
	}
	out := make([]php.Property, 0, len(s.Properties))
	for _, p := range s.Properties {
		if p.Visibility == "public" {
			out = append(out, p)
		}
	}
	return out
}

func renderTOON(w io.Writer, s *php.Symbol, exported, apiMode bool, surface []methodVia, notes []string) {
	// Basename only: a full path scales the summary with directory depth and can
	// trip the compact-or-raw fallback on small files in deep trees.
	fmt.Fprintf(w, "file: %s\n", filepath.Base(s.File))
	if s.Namespace != "" {
		fmt.Fprintf(w, "namespace: %s\n", s.Namespace)
	}
	fmt.Fprintf(w, "kind: %s\n", s.Kind)
	fmt.Fprintf(w, "name: %s\n", lastSeg(s.FQCN))
	if len(s.Modifiers) > 0 {
		fmt.Fprintf(w, "modifiers: %s\n", strings.Join(s.Modifiers, " "))
	}
	if s.Extends != "" {
		fmt.Fprintf(w, "extends: %s\n", toon.Scalar(s.Extends))
	}
	if len(s.Implements) > 0 {
		fmt.Fprintf(w, "implements: %s\n", toon.JoinList(s.Implements))
	}
	if len(s.Attributes) > 0 {
		fmt.Fprintf(w, "attributes: %s\n", toon.Scalar(phpAttrs(s.Attributes)))
	}

	if len(s.Cases) > 0 {
		if enumIsBacked(s.Cases) {
			fmt.Fprintf(w, "cases[%d]{name,value}:\n", len(s.Cases))
			for _, c := range s.Cases {
				fmt.Fprintf(w, "%s%s,%s\n", toon.Indent, toon.Scalar(c.Name), toon.Scalar(c.Value))
			}
		} else {
			// Pure enum: every value is empty, so drop the column entirely
			// rather than print a noisy `Name,""` for each case.
			fmt.Fprintf(w, "cases[%d]{name}:\n", len(s.Cases))
			for _, c := range s.Cases {
				fmt.Fprintf(w, "%s%s\n", toon.Indent, toon.Scalar(c.Name))
			}
		}
	}

	if len(s.CtorDeps) > 0 {
		fmt.Fprintf(w, "ctor[%d]{name,type,promoted}:\n", len(s.CtorDeps))
		for _, d := range s.CtorDeps {
			fmt.Fprintf(w, "%s%s,%s,%t\n", toon.Indent, toon.Scalar(d.Name), toon.Scalar(d.Type), d.Promoted)
		}
	}

	props := phpProperties(s, exported)
	if len(props) > 0 {
		fmt.Fprintf(w, "properties[%d]{vis,name,type,attrs}:\n", len(props))
		for _, p := range props {
			fmt.Fprintf(w, "%s%s,%s,%s,%s\n", toon.Indent,
				p.Visibility, toon.Scalar(p.Name), toon.Scalar(p.Type), toon.Scalar(phpAttrs(p.Attributes)))
		}
	}

	if apiMode {
		fmt.Fprintf(w, "methods[%d]{name,sig,attrs,via}:\n", len(surface))
		for _, mv := range surface {
			fmt.Fprintf(w, "%s%s,%s,%s,%s\n", toon.Indent,
				toon.Scalar(mv.M.Name), toon.Scalar(phpSig(mv.M)), toon.Scalar(phpAttrs(mv.M.Attrs)), toon.Scalar(mv.Via))
		}
		for _, n := range notes {
			fmt.Fprintf(w, "# unresolved: %s\n", n)
		}
		return
	}
	fmt.Fprintf(w, "methods[%d]{name,sig,attrs}:\n", len(s.Methods))
	for _, m := range s.Methods {
		fmt.Fprintf(w, "%s%s,%s,%s\n", toon.Indent,
			toon.Scalar(m.Name), toon.Scalar(phpSig(m)), toon.Scalar(phpAttrs(m.Attrs)))
	}
	if exported {
		writeAPIHint(w, s)
	}
}

// writeAPIHint advertises the --api flag when the public view of a class/trait
// hides a fuller callable surface behind traits or a parent class. Names come
// straight from the parsed symbol (no file resolution), so the hint is free.
func writeAPIHint(w io.Writer, s *php.Symbol) {
	if len(s.Uses) == 0 && s.Extends == "" {
		return
	}
	var parts []string
	if len(s.Uses) > 0 {
		short := make([]string, len(s.Uses))
		for i, u := range s.Uses {
			short[i] = lastSeg(u)
		}
		parts = append(parts, "traits("+strings.Join(short, ",")+")")
	}
	if s.Extends != "" {
		parts = append(parts, "extends("+lastSeg(s.Extends)+")")
	}
	fmt.Fprintf(w, "# +api: %s — re-run with --api for the full callable surface\n", strings.Join(parts, " "))
}

func renderMarkdown(w io.Writer, s *php.Symbol, exported, apiMode bool, surface []methodVia, notes []string) {
	fmt.Fprintf(w, "# %s `%s`", s.Kind, lastSeg(s.FQCN))
	if s.Namespace != "" {
		fmt.Fprintf(w, " — `%s`", s.Namespace)
	}
	fmt.Fprint(w, "\n\n")
	if s.Extends != "" {
		fmt.Fprintf(w, "- **extends**: `%s`\n", s.Extends)
	}
	if len(s.Implements) > 0 {
		fmt.Fprintf(w, "- **implements**: %s\n", strings.Join(s.Implements, ", "))
	}
	if len(s.Attributes) > 0 {
		fmt.Fprintf(w, "- **attributes**: %s\n", phpAttrs(s.Attributes))
	}
	if len(s.Cases) > 0 {
		fmt.Fprintln(w, "\n## cases")
		for _, c := range s.Cases {
			if c.Value != "" {
				fmt.Fprintf(w, "- `%s` = `%s`\n", c.Name, c.Value)
			} else {
				fmt.Fprintf(w, "- `%s`\n", c.Name)
			}
		}
	}
	if len(s.CtorDeps) > 0 {
		fmt.Fprintln(w, "\n## constructor")
		for _, d := range s.CtorDeps {
			fmt.Fprintf(w, "- `%s $%s`\n", d.Type, d.Name)
		}
	}
	if props := phpProperties(s, exported); len(props) > 0 {
		fmt.Fprintln(w, "\n## properties")
		for _, p := range props {
			fmt.Fprintf(w, "- `%s %s $%s`%s\n", p.Visibility, p.Type, p.Name, phpAttrSuffix(p.Attributes))
		}
	}
	if apiMode {
		fmt.Fprintln(w, "\n## methods (effective public API)")
		for _, mv := range surface {
			via := ""
			if mv.Via != "" {
				via = " _(via " + mv.Via + ")_"
			}
			fmt.Fprintf(w, "- `%s%s`%s%s\n", mv.M.Name, phpSig(mv.M), phpAttrSuffix(mv.M.Attrs), via)
		}
		for _, n := range notes {
			fmt.Fprintf(w, "- _unresolved: %s_\n", n)
		}
		return
	}
	fmt.Fprintln(w, "\n## methods")
	for _, m := range s.Methods {
		fmt.Fprintf(w, "- `%s%s`%s\n", m.Name, phpSig(m), phpAttrSuffix(m.Attrs))
	}
	if exported && (len(s.Uses) > 0 || s.Extends != "") {
		fmt.Fprint(w, "\n> ")
		writeAPIHintMD(w, s)
	}
}

// writeAPIHintMD writes the --api advert as a markdown blockquote line.
func writeAPIHintMD(w io.Writer, s *php.Symbol) {
	var parts []string
	if len(s.Uses) > 0 {
		short := make([]string, len(s.Uses))
		for i, u := range s.Uses {
			short[i] = lastSeg(u)
		}
		parts = append(parts, "traits("+strings.Join(short, ",")+")")
	}
	if s.Extends != "" {
		parts = append(parts, "extends("+lastSeg(s.Extends)+")")
	}
	fmt.Fprintf(w, "+api: %s — re-run with `--api` for the full callable surface\n", strings.Join(parts, " "))
}

func renderJSON(w io.Writer, s *php.Symbol) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(s)
}

// phpAttr formats an attribute compactly: short name + arguments, e.g.
// `Route(/api/v1/me, name: api_v1_me, methods: [GET])`.
func phpAttr(a php.Attr) string {
	name := lastSeg(a.Name)
	if len(a.Args) == 0 {
		return name
	}
	parts := make([]string, len(a.Args))
	for i, arg := range a.Args {
		if arg.Name != "" {
			parts[i] = arg.Name + ": " + arg.Value
		} else {
			parts[i] = arg.Value
		}
	}
	return name + "(" + strings.Join(parts, ", ") + ")"
}

func phpAttrs(attrs []php.Attr) string {
	out := make([]string, len(attrs))
	for i, a := range attrs {
		out[i] = phpAttr(a)
	}
	return strings.Join(out, "; ")
}

func phpAttrSuffix(attrs []php.Attr) string {
	if len(attrs) == 0 {
		return ""
	}
	return " — " + phpAttrs(attrs)
}

// phpSig renders a method signature, PHP-style: `(string $email, array $roles): JsonResponse`.
func phpSig(m php.Method) string {
	params := make([]string, len(m.Params))
	for i, p := range m.Params {
		if p.Type != "" {
			params[i] = p.Type + " $" + p.Name
		} else {
			params[i] = "$" + p.Name
		}
	}
	sig := "(" + strings.Join(params, ", ") + ")"
	if m.ReturnType != "" {
		sig += ": " + m.ReturnType
	}
	return sig
}

func lastSeg(s string) string {
	if i := strings.LastIndexByte(s, '\\'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// enumIsBacked reports whether any case carries a backing value (string/int).
// A backed enum is all-or-nothing in PHP, so one non-empty value is enough.
func enumIsBacked(cases []php.EnumCase) bool {
	for _, c := range cases {
		if c.Value != "" {
			return true
		}
	}
	return false
}
