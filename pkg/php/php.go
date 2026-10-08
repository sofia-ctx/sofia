// Package php summarizes PHP types and slices declarations from original source.
// The native Go parser supports PHP 8.5. Read tolerates explicitly marked recovery;
// ReadStrict and Slice require an error-free parse of the original bytes.
package php

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/dimasma0305/php-parser-go/ast"
	"github.com/dimasma0305/php-parser-go/parser"
	"github.com/dimasma0305/php-parser-go/phpparser"
)

// Kind classifies the top-level declaration found in the file.
type Kind string

const (
	KindClass     Kind = "class"
	KindInterface Kind = "interface"
	KindTrait     Kind = "trait"
	KindEnum      Kind = "enum"
)

// Symbol is the structured summary of one PHP type declaration.
// All names are FQCN where applicable (parser resolves short names via
// `use`-imports).
type Symbol struct {
	File        string
	Namespace   string
	FQCN        string
	Kind        Kind
	Modifiers   []string // final, readonly, abstract
	Extends     string   // empty for interfaces/traits/enums; FQCN otherwise
	Implements  []string // for classes: implements list; for interfaces: extends list; for enums: implements list
	Uses        []string // FQCN of traits composed via `use` in the body (classes and traits)
	DocSummary  string   // first non-tag line of the class-level docblock, or ""
	Attributes  []Attr   // class-level attributes with arguments (e.g. #[ORM\Table(...)])
	CtorDeps    []CtorDep
	Properties  []Property // declared and promoted properties (any visibility)
	Cases       []EnumCase // enum cases (empty for non-enums)
	Methods     []Method   // public only; __construct excluded (captured as CtorDeps)
	Partial     bool       // source had parse errors; recovered members may be incomplete
	Recovery    string     `json:",omitempty"` // partial_ast | normalized_ast | source
	Diagnostics []string   `json:",omitempty"` // original-source parse errors
}

// Attr is a PHP attribute together with its arguments, in source order.
// Name is the resolved FQCN (e.g. "Doctrine\ORM\Mapping\Column"), so
// consumers can match on a stable suffix regardless of the `use` alias.
type Attr struct {
	Name string
	Args []AttrArg
}

// AttrArg is one attribute argument. Name is the label for named args
// ("length", "type", ...) or "" for positional. Value is a best-effort
// stringification: strings unquoted, ints/floats verbatim, bools as
// "true"/"false", arrays as "[a,b]", class consts as "Foo::BAR".
type AttrArg struct {
	Name  string
	Value string
}

// Get returns the value of the named argument and whether it was present.
func (a Attr) Get(name string) (string, bool) {
	for _, arg := range a.Args {
		if arg.Name == name {
			return arg.Value, true
		}
	}
	return "", false
}

// Property is a class property with its declared type and attributes.
type Property struct {
	Name            string
	Type            string         // PHP type as written, resolved like params; "" if untyped
	Visibility      string         // public | protected | private
	WriteVisibility string         `json:",omitempty"` // explicitly declared public(set), protected(set), private(set)
	Modifiers       []string       `json:",omitempty"`
	Promoted        bool           `json:",omitempty"`
	Hooks           []PropertyHook `json:",omitempty"`
	Attributes      []Attr
}

// EnumCase is one case of a PHP enum. Value is the backing value for a
// backed enum (string/int); "" for a pure enum.
type EnumCase struct {
	Name  string
	Value string
}

// CtorDep is one constructor argument. Promoted=true means it's also
// declared as a property (PHP 8 promoted-property syntax).
type CtorDep struct {
	Name     string
	Type     string
	Promoted bool
}

// Method is a public method signature. Attributes hold the bare names
// (#[Override] -> "Override"); Attrs additionally carries the resolved
// names with arguments (e.g. #[Route('/x', methods: ['GET'])]).
type Method struct {
	Name       string
	Params     []Param
	ReturnType string
	Attributes []string
	Attrs      []Attr
}

// Param is a single method parameter.
type Param struct {
	Name string
	Type string
}

// PropertyHook describes a get/set declaration. BodyKind is abstract, expression,
// or block. Use Slice to obtain the original body, including comments.
type PropertyHook struct {
	Name       string
	ByRef      bool
	Modifiers  []string
	Params     []Param
	Attributes []Attr
	BodyKind   string
}

// Read returns the first named type. Partial results carry original diagnostics.
func Read(path string) (*Symbol, error) { return readFile(path, false) }

// ReadStrict rejects source requiring any error recovery or normalization.
// Successful parsing checks syntax, not PHP runtime or type correctness.
func ReadStrict(path string) (*Symbol, error) { return readFile(path, true) }

func readFile(path string, strict bool) (*Symbol, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("php.Read %s: %w", path, err)
	}
	return readString(string(src), path, strict)
}

// ReadString uses virtualPath in the summary and diagnostics.
func ReadString(src, virtualPath string) (*Symbol, error) { return readString(src, virtualPath, false) }

// ReadStringStrict never uses normalized source or a recovered AST.
func ReadStringStrict(src, virtualPath string) (*Symbol, error) {
	return readString(src, virtualPath, true)
}

func readString(src, path string, strict bool) (*Symbol, error) {
	raw := []byte(src)
	nodes, diagnostics, err := parsePHP(raw)
	if err != nil {
		return nil, fmt.Errorf("php.Read %s: %w", path, err)
	}
	if strict && len(diagnostics) > 0 {
		return nil, parseError(path, diagnostics)
	}
	sym := extract(nodes, path)
	recovery := "partial_ast"
	if len(diagnostics) > 0 && !strict {
		// Compatibility transforms remain a separate, lossy recovery path. Never
		// run them for supported syntax or use their byte offsets for source edits.
		normalized := normalizeModern(raw)
		if !bytes.Equal(normalized, raw) {
			if recovered, errors, err := parsePHP(normalized); err == nil {
				candidate := extract(recovered, path)
				if betterSymbol(candidate, sym, len(errors), len(diagnostics)) {
					sym, recovery = candidate, "normalized_ast"
				}
			}
		}
		if sym == nil {
			sym, recovery = extractPartial(raw, path), "source"
		}
		if sym != nil {
			sym.Partial, sym.Recovery, sym.Diagnostics = true, recovery, diagnostics
		}
	}
	if sym == nil {
		if len(diagnostics) > 0 {
			return nil, parseError(path, diagnostics)
		}
		return nil, fmt.Errorf("php.Read %s: no class/interface/trait/enum found", path)
	}
	return sym, nil
}

func parseError(path string, diagnostics []string) error {
	return fmt.Errorf("php %s: %d parse error(s): %s", path, len(diagnostics), diagnostics[0])
}

func parsePHP(src []byte) ([]ast.Node, []string, error) {
	p, err := (parser.ParserFactory{}).CreateForNewestSupportedVersion()
	if err != nil {
		return nil, nil, err
	}
	handler := &phpparser.CollectingErrorHandler{}
	nodes, err := p.Parse(string(src), handler)
	var diagnostics []string
	for _, e := range handler.Errors() {
		diagnostics = append(diagnostics, e.Error())
	}
	if err != nil && len(diagnostics) == 0 {
		return nil, nil, err
	}
	return nodes, diagnostics, nil
}

func betterSymbol(cand, cur *Symbol, candErrs, curErrs int) bool {
	if cand == nil {
		return false
	}
	if cur == nil {
		return true
	}
	if cm, curm := memberCount(cand), memberCount(cur); cm != curm {
		return cm > curm
	}
	return candErrs < curErrs
}

func memberCount(s *Symbol) int {
	count := len(s.Methods) + len(s.Properties) + len(s.CtorDeps) + len(s.Cases)
	for _, p := range s.Properties {
		count += len(p.Hooks)
	}
	return count
}

type extractor struct {
	file, ns string
	imports  map[string]string
	sym      *Symbol
}

func extract(nodes []ast.Node, path string) *Symbol {
	e := &extractor{file: path, imports: map[string]string{}}
	for _, n := range nodes {
		ast.Walk(n, e.visit)
	}
	return e.sym
}

func (e *extractor) visit(n ast.Node) bool {
	if e.sym != nil {
		return false
	}
	switch t := n.(type) {
	case *ast.StmtNamespace:
		e.ns, e.imports = nameToString(t.Name), map[string]string{}
	case *ast.StmtUse:
		typ, _ := t.Type.(int)
		e.addImports(t.Uses, typ, "")
		return false
	case *ast.StmtGroupUse:
		e.addImports(t.Uses, t.Type, nameToString(t.Prefix))
		return false
	case *ast.StmtClass:
		if nameToString(t.Name) == "" {
			return false
		}
		e.setType(n, t.Name, KindClass, t.Flags, t.AttrGroups, t.Stmts, t.Extends, t.Implements)
		return false
	case *ast.StmtInterface:
		e.setType(n, t.Name, KindInterface, 0, t.AttrGroups, t.Stmts, nil, t.Extends)
		return false
	case *ast.StmtTrait:
		e.setType(n, t.Name, KindTrait, 0, t.AttrGroups, t.Stmts, nil, nil)
		return false
	case *ast.StmtEnum:
		e.setType(n, t.Name, KindEnum, 0, t.AttrGroups, t.Stmts, nil, t.Implements)
		return false
	}
	return true
}

func (e *extractor) addImports(uses []ast.Node, typ int, prefix string) {
	for _, n := range uses {
		u, ok := n.(*ast.UseItem)
		if !ok {
			continue
		}
		kind := typ
		if u.Type != 0 {
			kind = u.Type
		}
		if kind != 0 && kind != ast.StmtUseTypeNormal {
			continue
		}
		fqcn := joinFQCN(prefix, nameToString(u.Name))
		alias := nameToString(u.Alias)
		if alias == "" {
			parts := strings.Split(fqcn, `\`)
			alias = parts[len(parts)-1]
		}
		e.imports[strings.ToLower(alias)] = fqcn
	}
}

func (e *extractor) setType(node, name ast.Node, kind Kind, flags int, attrs, stmts []ast.Node, extends ast.Node, implements []ast.Node) {
	sym := &Symbol{File: e.file, Namespace: e.ns, FQCN: joinFQCN(e.ns, nameToString(name)), Kind: kind,
		Modifiers: modifiers(flags), Extends: e.resolveName(extends), Implements: e.resolveNames(implements),
		Attributes: e.attributes(attrs), DocSummary: docSummary(node)}
	for _, n := range stmts {
		switch m := n.(type) {
		case *ast.StmtClassMethod:
			e.addMethod(sym, m)
		case *ast.StmtProperty:
			for _, item := range m.Props {
				if p, ok := item.(*ast.PropertyItem); ok {
					sym.Properties = append(sym.Properties, e.property(nameToString(p.Name), m.Type, m.Flags, m.AttrGroups, m.Hooks, false))
				}
			}
		case *ast.StmtTraitUse:
			sym.Uses = append(sym.Uses, e.resolveNames(m.Traits)...)
		case *ast.StmtEnumCase:
			sym.Cases = append(sym.Cases, EnumCase{Name: nameToString(m.Name), Value: e.attrArgValue(m.Expr)})
		}
	}
	e.sym = sym
}

func (e *extractor) addMethod(sym *Symbol, m *ast.StmtClassMethod) {
	name := nameToString(m.Name)
	if strings.EqualFold(name, "__construct") {
		for _, n := range m.Params {
			p, ok := n.(*ast.Param)
			if !ok {
				continue
			}
			promoted := p.Flags != 0
			if promoted {
				sym.Properties = append(sym.Properties, e.property(paramName(p), p.Type, p.Flags, p.AttrGroups, p.Hooks, true))
			}
			if visibility(m.Flags) == "public" {
				sym.CtorDeps = append(sym.CtorDeps, CtorDep{Name: paramName(p), Type: e.resolveType(p.Type), Promoted: promoted})
			}
		}
		return
	}
	if visibility(m.Flags) != "public" {
		return
	}
	sym.Methods = append(sym.Methods, Method{Name: name, ReturnType: e.resolveType(m.ReturnType), Params: e.params(m.Params), Attributes: attributeNames(m.AttrGroups), Attrs: e.attributes(m.AttrGroups)})
}

func (e *extractor) property(name string, typ ast.Node, flags int, attrs, hooks []ast.Node, promoted bool) Property {
	p := Property{Name: name, Type: e.resolveType(typ), Visibility: visibility(flags), Modifiers: modifiers(flags), Attributes: e.attributes(attrs), Promoted: promoted}
	switch {
	case flags&phpparser.ModifierPrivateSet != 0:
		p.WriteVisibility = "private"
	case flags&phpparser.ModifierProtectedSet != 0:
		p.WriteVisibility = "protected"
	case flags&phpparser.ModifierPublicSet != 0:
		p.WriteVisibility = "public"
	}
	for _, n := range hooks {
		h, ok := n.(*ast.PropertyHook)
		if !ok {
			continue
		}
		body := "abstract"
		switch h.Body.(type) {
		case []ast.Node, []any:
			body = "block"
		case ast.Node:
			body = "expression"
		}
		p.Hooks = append(p.Hooks, PropertyHook{Name: nameToString(h.Name), ByRef: h.ByRef, Modifiers: modifiers(h.Flags), Params: e.params(h.Params), Attributes: e.attributes(h.AttrGroups), BodyKind: body})
	}
	return p
}

func (e *extractor) params(nodes []ast.Node) []Param {
	var out []Param
	for _, n := range nodes {
		if p, ok := n.(*ast.Param); ok {
			out = append(out, Param{Name: paramName(p), Type: e.resolveType(p.Type)})
		}
	}
	return out
}

func (e *extractor) resolveType(n ast.Node) string {
	switch t := n.(type) {
	case *ast.NullableType:
		return "?" + e.resolveType(t.Type)
	case *ast.UnionType:
		var parts []string
		for _, child := range t.Types {
			node, _ := child.(ast.Node)
			value := e.resolveType(node)
			if _, ok := child.(*ast.IntersectionType); ok {
				value = "(" + value + ")"
			}
			parts = append(parts, value)
		}
		return strings.Join(parts, "|")
	case *ast.IntersectionType:
		var parts []string
		for _, child := range t.Types {
			node, _ := child.(ast.Node)
			parts = append(parts, e.resolveType(node))
		}
		return strings.Join(parts, "&")
	case *ast.Identifier:
		return t.Name
	default:
		return e.resolveName(n)
	}
}

func (e *extractor) resolveName(n ast.Node) string {
	raw := nameToString(n)
	if raw == "" {
		return ""
	}
	if _, ok := n.(*ast.NameFullyQualified); ok {
		return raw
	}
	if _, ok := n.(*ast.NameRelative); ok {
		return joinFQCN(e.ns, raw)
	}
	if isBuiltinType(raw) {
		return raw
	}
	parts := strings.SplitN(raw, `\`, 2)
	if fqcn, ok := e.imports[strings.ToLower(parts[0])]; ok {
		if len(parts) == 2 {
			return fqcn + `\` + parts[1]
		}
		return fqcn
	}
	return joinFQCN(e.ns, raw)
}

func (e *extractor) resolveNames(nodes []ast.Node) []string {
	var out []string
	for _, n := range nodes {
		out = append(out, e.resolveName(n))
	}
	return out
}

func nameToString(n ast.Node) string {
	switch t := n.(type) {
	case *ast.Name:
		return t.Name
	case *ast.NameFullyQualified:
		return t.Name
	case *ast.NameRelative:
		return t.Name
	case *ast.Identifier:
		return t.Name
	case *ast.VarLikeIdentifier:
		return t.Name
	}
	return ""
}

func paramName(p *ast.Param) string {
	if v, ok := p.Var.(*ast.ExprVariable); ok {
		if name, ok := v.Name.(string); ok {
			return name
		}
	}
	return ""
}

func isBuiltinType(s string) bool {
	switch strings.ToLower(s) {
	case "int", "float", "string", "bool", "void", "mixed", "never", "self", "static", "parent", "object", "callable", "iterable", "array", "true", "false", "null":
		return true
	}
	return false
}

func visibility(flags int) string {
	if flags&phpparser.ModifierPrivate != 0 {
		return "private"
	}
	if flags&phpparser.ModifierProtected != 0 {
		return "protected"
	}
	return "public"
}

func modifiers(flags int) []string {
	var out []string
	for _, flag := range []int{phpparser.ModifierPublic, phpparser.ModifierProtected, phpparser.ModifierPrivate, phpparser.ModifierStatic, phpparser.ModifierAbstract, phpparser.ModifierFinal, phpparser.ModifierReadonly, phpparser.ModifierPublicSet, phpparser.ModifierProtectedSet, phpparser.ModifierPrivateSet} {
		if flags&flag != 0 {
			name, _ := phpparser.ModifierString(flag)
			out = append(out, name)
		}
	}
	return out
}

func (e *extractor) attributes(groups []ast.Node) []Attr {
	var out []Attr
	for _, g := range groups {
		if group, ok := g.(*ast.AttributeGroup); ok {
			for _, n := range group.Attrs {
				if a, ok := n.(*ast.Attribute); ok {
					out = append(out, Attr{Name: e.resolveName(a.Name), Args: e.attrArgs(a.Args)})
				}
			}
		}
	}
	return out
}

func (e *extractor) attrArgs(args any) []AttrArg {
	var nodes []ast.Node
	switch a := args.(type) {
	case []ast.Node:
		nodes = a
	case []any:
		for _, n := range a {
			if node, ok := n.(ast.Node); ok {
				nodes = append(nodes, node)
			}
		}
	}
	var out []AttrArg
	for _, n := range nodes {
		if a, ok := n.(*ast.Arg); ok {
			out = append(out, AttrArg{Name: nameToString(a.Name), Value: e.attrArgValue(a.Value)})
		}
	}
	return out
}

func (e *extractor) attrArgValue(n ast.Node) string {
	switch t := n.(type) {
	case *ast.ScalarString:
		return t.Value
	case *ast.ScalarInt:
		if raw, ok := t.GetAttribute("rawValue", nil).(string); ok {
			return raw
		}
		return strconv.Itoa(t.Value)
	case *ast.ScalarFloat:
		if raw, ok := t.GetAttribute("rawValue", nil).(string); ok {
			return raw
		}
		return strconv.FormatFloat(t.Value, 'g', -1, 64)
	case *ast.ExprConstFetch:
		return nameToString(t.Name)
	case *ast.ExprClassConstFetch:
		return e.resolveName(t.Class) + "::" + nameToString(t.Name)
	case *ast.ExprArray:
		var parts []string
		for _, n := range t.Items {
			if item, ok := n.(*ast.ArrayItem); ok {
				parts = append(parts, e.attrArgValue(item.Value))
			}
		}
		return "[" + strings.Join(parts, ",") + "]"
	}
	return ""
}

func attributeNames(groups []ast.Node) []string {
	var out []string
	for _, g := range groups {
		if group, ok := g.(*ast.AttributeGroup); ok {
			for _, n := range group.Attrs {
				if a, ok := n.(*ast.Attribute); ok {
					out = append(out, nameToString(a.Name))
				}
			}
		}
	}
	return out
}

func joinFQCN(ns, name string) string {
	if ns == "" {
		return name
	}
	return ns + `\` + name
}

func docSummary(n ast.Node) string {
	doc := n.DocComment()
	if doc == nil {
		return ""
	}
	for _, line := range strings.Split(doc.Text(), "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSuffix(strings.TrimPrefix(line, "/**"), "*/"), "*"))
		if line != "" && !strings.HasPrefix(line, "@") {
			return line
		}
	}
	return ""
}
