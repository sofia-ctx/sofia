package php

import (
	"fmt"

	"github.com/dimasma0305/php-parser-go/ast"
)

// Slice returns original source for a type, function, method, property or hook.
// Examples: Example, Example::run, $value, Example::$value, Example::$value::get.
// Bare names must be unambiguous. Parse errors are never normalized or ignored.
func Slice(src []byte, symbol string) (text string, names []string, err error) {
	nodes, diagnostics, err := parsePHP(src)
	if err != nil {
		return "", nil, err
	}
	if len(diagnostics) > 0 {
		return "", nil, parseError("source", diagnostics)
	}
	s := &slicer{src: src, want: symbol}
	for _, n := range nodes {
		ast.Walk(n, s.visit)
	}
	if len(s.matches) > 1 {
		return "", s.names, fmt.Errorf("symbol %q is ambiguous; use a qualified name", symbol)
	}
	if len(s.matches) == 1 {
		return s.matches[0], nil, nil
	}
	return "", s.names, fmt.Errorf("symbol %q not found", symbol)
}

type slicer struct {
	src     []byte
	want    string
	matches []string
	names   []string
}

func (s *slicer) visit(n ast.Node) bool {
	switch t := n.(type) {
	case *ast.StmtFunction:
		s.consider(nameToString(t.Name), nameToString(t.Name), n)
	case *ast.StmtClass:
		if t.Name == nil {
			return false
		}
		s.typeNode(nameToString(t.Name), n, t.Stmts)
	case *ast.StmtInterface:
		s.typeNode(nameToString(t.Name), n, t.Stmts)
	case *ast.StmtTrait:
		s.typeNode(nameToString(t.Name), n, t.Stmts)
	case *ast.StmtEnum:
		s.typeNode(nameToString(t.Name), n, t.Stmts)
	}
	return true
}

func (s *slicer) typeNode(class string, n ast.Node, stmts []ast.Node) {
	s.consider(class, class, n)
	for _, n := range stmts {
		switch m := n.(type) {
		case *ast.StmtClassMethod:
			name := nameToString(m.Name)
			s.consider(name, class+"::"+name, m)
			if name == "__construct" {
				for _, n := range m.Params {
					if p, ok := n.(*ast.Param); ok && p.Flags != 0 {
						s.property(class, paramName(p), p, p.Hooks)
					}
				}
			}
		case *ast.StmtProperty:
			for _, n := range m.Props {
				if p, ok := n.(*ast.PropertyItem); ok {
					s.property(class, nameToString(p.Name), m, m.Hooks)
				}
			}
		}
	}
}

func (s *slicer) property(class, name string, n ast.Node, hooks []ast.Node) {
	bare := "$" + name
	s.consider(bare, class+"::"+bare, n)
	for _, n := range hooks {
		if h, ok := n.(*ast.PropertyHook); ok {
			hook := bare + "::" + nameToString(h.Name)
			s.consider(hook, class+"::"+hook, h)
		}
	}
}

func (s *slicer) consider(bare, qualified string, n ast.Node) {
	s.names = append(s.names, qualified)
	if s.want != bare && s.want != qualified {
		return
	}
	start, end := n.StartFilePos(), n.EndFilePos()+1
	if start >= 0 && start < end && end <= len(s.src) {
		s.matches = append(s.matches, string(s.src[start:end]))
	}
}
