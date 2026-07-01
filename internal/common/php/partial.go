package php

import (
	"regexp"
	"strings"
)

var (
	rePartialNamespace  = regexp.MustCompile(`(?m)^\s*namespace\s+([\w\\]+)\s*;`)
	rePartialClassDecl  = regexp.MustCompile(`\b(class|interface|trait|enum)\s+(\w+)([^{;]*)`)
	rePartialExtends    = regexp.MustCompile(`\bextends\s+([\w\\]+(?:\s*,\s*[\w\\]+)*)`)
	rePartialImplements = regexp.MustCompile(`\bimplements\s+([\w\\]+(?:\s*,\s*[\w\\]+)*)`)
)

// extractPartial is the last-resort fallback used by ReadString when neither
// the raw nor the normalized parse recovered a declaration, but the bytes
// clearly contain a class-like type. It pulls a coarse skeleton — namespace,
// kind, FQCN and extends/implements — straight from the source text, so a
// syntactically broken file yields partial structure (flagged Partial)
// instead of a dead-end error.
//
// Members (methods/properties) are deliberately not recovered here — that is
// the AST path's job. Names are as-written (not `use`-resolved). Returns nil
// if no class-like keyword is present.
func extractPartial(src []byte, virtualPath string) *Symbol {
	m := rePartialClassDecl.FindSubmatch(src)
	if m == nil {
		return nil
	}
	kind := Kind(strings.ToLower(string(m[1])))
	name := string(m[2])
	tail := m[3] // text between the name and the body `{` (extends/implements)

	ns := ""
	if nm := rePartialNamespace.FindSubmatch(src); nm != nil {
		ns = string(nm[1])
	}
	sym := &Symbol{
		File:      virtualPath,
		Namespace: ns,
		FQCN:      joinFQCN(ns, name),
		Kind:      kind,
		Partial:   true,
	}
	if em := rePartialExtends.FindSubmatch(tail); em != nil {
		parts := splitPartialNames(string(em[1]))
		if kind == KindInterface {
			sym.Implements = parts // `interface A extends B, C` → Implements
		} else if len(parts) > 0 {
			sym.Extends = parts[0]
		}
	}
	if im := rePartialImplements.FindSubmatch(tail); im != nil {
		sym.Implements = append(sym.Implements, splitPartialNames(string(im[1]))...)
	}
	return sym
}

func splitPartialNames(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimPrefix(strings.TrimSpace(p), `\`)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
