package php

import (
	"fmt"

	"github.com/VKCOM/php-parser/pkg/ast"
	"github.com/VKCOM/php-parser/pkg/visitor"
	"github.com/VKCOM/php-parser/pkg/visitor/traverser"
)

// Slice returns the source text of the named top-level function or class
// method in src. symbol may be a bare name ("completeTask") or qualified
// ("CompleteTaskController::completeTask"). On no match, names lists the
// available function/method identifiers so callers can build a helpful error.
func Slice(src []byte, symbol string) (text string, names []string, err error) {
	root, _, perr := parsePHP(src)
	if perr != nil {
		return "", nil, perr
	}
	if root == nil {
		return "", nil, fmt.Errorf("php: could not parse source")
	}
	s := &slicer{src: src, want: symbol}
	traverser.NewTraverser(s).Traverse(root)
	if s.found != "" {
		return s.found, nil, nil
	}
	return "", s.names, fmt.Errorf("symbol %q not found", symbol)
}

type slicer struct {
	visitor.Null
	src   []byte
	want  string
	found string
	names []string
}

func (s *slicer) StmtFunction(n *ast.StmtFunction) {
	name := identifierValue(n.Name)
	s.consider(name, name, n)
}

func (s *slicer) StmtClass(n *ast.StmtClass)         { s.typeNode(identifierValue(n.Name), n, n.Stmts) }
func (s *slicer) StmtInterface(n *ast.StmtInterface) { s.typeNode(identifierValue(n.Name), n, n.Stmts) }
func (s *slicer) StmtTrait(n *ast.StmtTrait)         { s.typeNode(identifierValue(n.Name), n, n.Stmts) }
func (s *slicer) StmtEnum(n *ast.StmtEnum)           { s.typeNode(identifierValue(n.Name), n, n.Stmts) }

// typeNode makes the type itself sliceable by its bare name (whole
// class/interface/trait/enum source), then exposes its methods as Class::method.
func (s *slicer) typeNode(name string, n ast.Vertex, stmts []ast.Vertex) {
	s.consider(name, name, n)
	s.members(name, stmts)
}

func (s *slicer) members(class string, stmts []ast.Vertex) {
	for _, st := range stmts {
		m, ok := st.(*ast.StmtClassMethod)
		if !ok {
			continue
		}
		name := identifierValue(m.Name)
		s.consider(name, class+"::"+name, m)
	}
}

// consider records the identifier and, if it matches want (bare or qualified)
// and nothing has been found yet, slices the node's source by byte position.
func (s *slicer) consider(bare, qualified string, n ast.Vertex) {
	s.names = append(s.names, qualified)
	if s.found != "" {
		return
	}
	if s.want != bare && s.want != qualified {
		return
	}
	if p := n.GetPosition(); p != nil && p.StartPos >= 0 && p.StartPos < p.EndPos && p.EndPos <= len(s.src) {
		s.found = string(s.src[p.StartPos:p.EndPos])
	}
}
