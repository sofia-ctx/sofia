// Package php reads a PHP source file and returns a structured summary
// of the first class/interface/trait/enum declaration it finds: namespace,
// FQCN, modifiers, parent/implements, public methods, constructor
// dependencies, and the docblock summary.
//
// The goal is to let downstream tools answer questions about a class
// without making the model cat the whole file.
//
// Backend: github.com/VKCOM/php-parser (pure Go, no CGO). Adapter is the
// package-level Read/ReadString — swap backends here if VKCOM ever stops
// being viable.
package php

import (
	"fmt"
	"os"
	"strings"

	"github.com/VKCOM/php-parser/pkg/ast"
	"github.com/VKCOM/php-parser/pkg/conf"
	"github.com/VKCOM/php-parser/pkg/errors"
	"github.com/VKCOM/php-parser/pkg/parser"
	"github.com/VKCOM/php-parser/pkg/token"
	"github.com/VKCOM/php-parser/pkg/version"
	"github.com/VKCOM/php-parser/pkg/visitor"
	"github.com/VKCOM/php-parser/pkg/visitor/traverser"
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
	File       string
	Namespace  string
	FQCN       string
	Kind       Kind
	Modifiers  []string // final, readonly, abstract — in source order
	Extends    string   // empty for interfaces/traits/enums; FQCN otherwise
	Implements []string // for classes: implements list; for interfaces: extends list; for enums: implements list
	Uses       []string // FQCN of traits composed via `use` in the body (classes and traits)
	DocSummary string   // first non-tag line of the class-level docblock, or ""
	Attributes []Attr   // class-level attributes with arguments (e.g. #[ORM\Table(...)])
	CtorDeps   []CtorDep
	Properties []Property // declared properties (any visibility) with type + attributes
	Cases      []EnumCase // enum cases (empty for non-enums)
	Methods    []Method   // public only; __construct excluded (captured as CtorDeps)
	Partial    bool       // recovered by the regex fallback, not the AST: names are unresolved and members are absent
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
	Name       string
	Type       string // PHP type as written, resolved like params; "" if untyped
	Visibility string // public | protected | private
	Attributes []Attr
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

// Read parses path and returns the first type declaration.
// Returns an error if no such declaration exists, or if the file cannot
// be parsed.
func Read(path string) (*Symbol, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("php.Read %s: %w", path, err)
	}
	return ReadString(string(src), path)
}

// ReadString parses src (which must begin with "<?php") and uses
// virtualPath as the Symbol.File field and as context in error messages.
// Useful for unit tests where the source lives in a string literal.
func ReadString(src, virtualPath string) (*Symbol, error) {
	raw := []byte(src)
	root, parseErrs, err := parsePHP(raw)
	if err != nil {
		return nil, fmt.Errorf("php.ReadString %s: %w", virtualPath, err)
	}
	sym := extract(root, virtualPath)

	// VKCOM tops out at PHP 8.1. On any parse error, retry once on a
	// normalized copy that downgrades PHP 8.2–8.5 declaration syntax (see
	// normalize.go) and keep whichever parse recovered MORE member
	// declarations. A normalization can rescue a whole class while leaving
	// (or even waking) deeper body errors, so a plain "fewer errors" rule
	// would wrongly discard the better parse.
	if len(parseErrs) > 0 {
		if r2, pe2, e2 := parsePHP(normalizeModern(raw)); e2 == nil {
			sym2 := extract(r2, virtualPath)
			if betterSymbol(sym2, sym, len(pe2), len(parseErrs)) {
				sym, parseErrs = sym2, pe2
			}
		}
	}

	// Degrade-to-partial: neither parse recovered a declaration, but the
	// bytes clearly contain one. Hand back a regex-extracted skeleton instead
	// of a hard error, so the caller gets partial structure (and can
	// self-correct) rather than nothing.
	if sym == nil {
		if p := extractPartial(raw, virtualPath); p != nil {
			return p, nil
		}
	}

	if sym == nil {
		if len(parseErrs) > 0 {
			return nil, fmt.Errorf("php.ReadString %s: %d parse error(s): %s",
				virtualPath, len(parseErrs), parseErrs[0].String())
		}
		return nil, fmt.Errorf("php.ReadString %s: no class/interface/trait/enum found", virtualPath)
	}
	return sym, nil
}

// extract walks a (possibly partial) AST and returns the first recovered type
// declaration, or nil. Residual parse errors are tolerated — they are
// typically deep in method bodies and do not affect a class's declared shape.
func extract(root ast.Vertex, virtualPath string) *Symbol {
	if root == nil {
		return nil
	}
	ex := &extractor{file: virtualPath, imports: map[string]string{}}
	traverser.NewTraverser(ex).Traverse(root)
	return ex.sym
}

// betterSymbol reports whether candidate cand should replace the current best
// cur: more recovered members win, ties break on fewer parse errors, and any
// non-nil symbol beats nil.
func betterSymbol(cand, cur *Symbol, candErrs, curErrs int) bool {
	switch {
	case cand == nil:
		return false
	case cur == nil:
		return true
	}
	if cm, curm := memberCount(cand), memberCount(cur); cm != curm {
		return cm > curm
	}
	return candErrs < curErrs
}

func memberCount(s *Symbol) int {
	return len(s.Methods) + len(s.Properties) + len(s.CtorDeps) + len(s.Cases)
}

// parsePHP runs the VKCOM parser at PHP 8.1 (its maximum), collecting any
// recoverable parse errors rather than failing hard.
func parsePHP(src []byte) (ast.Vertex, []*errors.Error, error) {
	var parseErrs []*errors.Error
	v, _ := version.New("8.1")
	root, err := parser.Parse(src, conf.Config{
		Version:          v,
		ErrorHandlerFunc: func(e *errors.Error) { parseErrs = append(parseErrs, e) },
	})
	return root, parseErrs, err
}

// extractor is a Visitor that walks the AST and fills a Symbol. Only
// the FIRST class-like declaration is captured (one-class-per-file is
// the common case; multi-class files are out of scope for v1).
type extractor struct {
	visitor.Null
	file    string
	ns      string
	imports map[string]string // short alias -> FQCN (no leading "\")
	sym     *Symbol
}

func (e *extractor) StmtNamespace(n *ast.StmtNamespace) {
	e.ns = nameToString(n.Name)
}

func (e *extractor) StmtUseDeclaration(n *ast.StmtUse) {
	fqcn := nameToString(n.Use)
	var alias string
	if n.Alias != nil {
		alias = identifierValue(n.Alias)
	} else {
		segs := strings.Split(fqcn, `\`)
		alias = segs[len(segs)-1]
	}
	if alias != "" && fqcn != "" {
		e.imports[alias] = fqcn
	}
}

func (e *extractor) StmtClass(n *ast.StmtClass) {
	if e.sym != nil {
		return
	}
	sym := &Symbol{
		File:       e.file,
		Namespace:  e.ns,
		Kind:       KindClass,
		Modifiers:  identifierList(n.Modifiers),
		Implements: e.resolveNames(n.Implements),
		FQCN:       joinFQCN(e.ns, identifierValue(n.Name)),
		DocSummary: docSummary(leadToken(n.AttrGroups, n.Modifiers, n.ClassTkn)),
		Attributes: e.attributes(n.AttrGroups),
	}
	if n.Extends != nil {
		sym.Extends = e.resolveName(n.Extends)
	}
	e.fillMembers(sym, n.Stmts)
	e.sym = sym
}

func (e *extractor) StmtInterface(n *ast.StmtInterface) {
	if e.sym != nil {
		return
	}
	sym := &Symbol{
		File:       e.file,
		Namespace:  e.ns,
		Kind:       KindInterface,
		FQCN:       joinFQCN(e.ns, identifierValue(n.Name)),
		Implements: e.resolveNames(n.Extends), // `interface A extends B, C` -> Implements=[B,C]
		DocSummary: docSummary(leadToken(n.AttrGroups, nil, n.InterfaceTkn)),
	}
	e.fillMembers(sym, n.Stmts)
	e.sym = sym
}

func (e *extractor) StmtTrait(n *ast.StmtTrait) {
	if e.sym != nil {
		return
	}
	sym := &Symbol{
		File:       e.file,
		Namespace:  e.ns,
		Kind:       KindTrait,
		FQCN:       joinFQCN(e.ns, identifierValue(n.Name)),
		DocSummary: docSummary(leadToken(n.AttrGroups, nil, n.TraitTkn)),
	}
	e.fillMembers(sym, n.Stmts)
	e.sym = sym
}

func (e *extractor) StmtEnum(n *ast.StmtEnum) {
	if e.sym != nil {
		return
	}
	sym := &Symbol{
		File:       e.file,
		Namespace:  e.ns,
		Kind:       KindEnum,
		FQCN:       joinFQCN(e.ns, identifierValue(n.Name)),
		Implements: e.resolveNames(n.Implements),
		DocSummary: docSummary(leadToken(n.AttrGroups, nil, n.EnumTkn)),
	}
	e.fillMembers(sym, n.Stmts)
	e.sym = sym
}

// fillMembers extracts methods, ctor deps, and properties from the class
// body. Skips non-public methods. Skips __construct as a method — its
// params become CtorDeps. Properties are captured at any visibility (a
// Doctrine column is typically private).
func (e *extractor) fillMembers(sym *Symbol, stmts []ast.Vertex) {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ast.StmtClassMethod:
			e.addMethod(sym, n)
		case *ast.StmtPropertyList:
			e.addProperties(sym, n)
		case *ast.StmtTraitUse:
			e.addTraitUses(sym, n)
		case *ast.EnumCase:
			e.addEnumCase(sym, n)
		}
	}
}

// addEnumCase records one enum case. Value is the backing value of a backed
// enum (e.g. 'active'); attrArgValue returns "" for a pure enum (nil Expr).
func (e *extractor) addEnumCase(sym *Symbol, c *ast.EnumCase) {
	sym.Cases = append(sym.Cases, EnumCase{
		Name:  identifierValue(c.Name),
		Value: e.attrArgValue(c.Expr),
	})
}

// addTraitUses records the traits composed into the body via `use Foo, Bar;`.
// Names resolve to FQCN through the file's use-imports (or the current
// namespace), mirroring extends/implements. Trait adaptations
// (insteadof/`as`) are not modelled in v1 — only the source trait set is
// captured, which is what the effective-API surface needs.
func (e *extractor) addTraitUses(sym *Symbol, n *ast.StmtTraitUse) {
	for _, t := range n.Traits {
		if fqcn := e.resolveName(t); fqcn != "" {
			sym.Uses = append(sym.Uses, fqcn)
		}
	}
}

func (e *extractor) addMethod(sym *Symbol, mth *ast.StmtClassMethod) {
	name := identifierValue(mth.Name)
	if !isPublic(mth.Modifiers) {
		return
	}
	if name == "__construct" {
		for _, p := range mth.Params {
			param, ok := p.(*ast.Parameter)
			if !ok {
				continue
			}
			sym.CtorDeps = append(sym.CtorDeps, CtorDep{
				Name:     paramName(param),
				Type:     e.resolveType(param.Type),
				Promoted: len(param.Modifiers) > 0,
			})
		}
		return
	}
	m := Method{
		Name:       name,
		ReturnType: e.resolveType(mth.ReturnType),
		Attributes: attributeNames(mth.AttrGroups),
		Attrs:      e.attributes(mth.AttrGroups),
	}
	for _, p := range mth.Params {
		param, ok := p.(*ast.Parameter)
		if !ok {
			continue
		}
		m.Params = append(m.Params, Param{
			Name: paramName(param),
			Type: e.resolveType(param.Type),
		})
	}
	sym.Methods = append(sym.Methods, m)
}

func (e *extractor) addProperties(sym *Symbol, list *ast.StmtPropertyList) {
	typ := e.resolveType(list.Type)
	vis := visibility(list.Modifiers)
	attrs := e.attributes(list.AttrGroups)
	for _, p := range list.Props {
		prop, ok := p.(*ast.StmtProperty)
		if !ok {
			continue
		}
		v, ok := prop.Var.(*ast.ExprVariable)
		if !ok {
			continue
		}
		sym.Properties = append(sym.Properties, Property{
			Name:       strings.TrimPrefix(identifierValue(v.Name), "$"),
			Type:       typ,
			Visibility: vis,
			Attributes: attrs,
		})
	}
}

// resolveType handles parameter/return type Vertex shapes: builtin
// identifiers (int, void, mixed, self, static), names (resolved via
// use-imports), Nullable, Union, Intersection. Returns "" for nil.
func (e *extractor) resolveType(v ast.Vertex) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case *ast.Nullable:
		return "?" + e.resolveType(t.Expr)
	case *ast.Union:
		parts := make([]string, 0, len(t.Types))
		for _, x := range t.Types {
			parts = append(parts, e.resolveType(x))
		}
		return strings.Join(parts, "|")
	case *ast.Intersection:
		parts := make([]string, 0, len(t.Types))
		for _, x := range t.Types {
			parts = append(parts, e.resolveType(x))
		}
		return strings.Join(parts, "&")
	case *ast.Identifier:
		return string(t.Value)
	default:
		return e.resolveName(v)
	}
}

// resolveName turns a Name/NameFullyQualified Vertex into a FQCN string.
// Plain names (`Foo`, `Foo\Bar`) are looked up in the use-imports map;
// unrecognized names fall back to the current namespace. Fully-qualified
// names (leading "\") are returned without leading "\".
func (e *extractor) resolveName(v ast.Vertex) string {
	if v == nil {
		return ""
	}
	if _, fq := v.(*ast.NameFullyQualified); fq {
		return nameToString(v)
	}
	raw := nameToString(v)
	if raw == "" {
		return ""
	}
	if isBuiltinType(raw) {
		return raw
	}
	segs := strings.SplitN(raw, `\`, 2)
	if fqcn, ok := e.imports[segs[0]]; ok {
		if len(segs) == 1 {
			return fqcn
		}
		return fqcn + `\` + segs[1]
	}
	if e.ns != "" {
		return e.ns + `\` + raw
	}
	return raw
}

func (e *extractor) resolveNames(vs []ast.Vertex) []string {
	if len(vs) == 0 {
		return nil
	}
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		out = append(out, e.resolveName(v))
	}
	return out
}

func nameToString(v ast.Vertex) string {
	if v == nil {
		return ""
	}
	switch n := v.(type) {
	case *ast.Name:
		return joinNameParts(n.Parts)
	case *ast.NameFullyQualified:
		return joinNameParts(n.Parts)
	case *ast.NameRelative:
		return joinNameParts(n.Parts)
	case *ast.Identifier:
		return string(n.Value)
	case *ast.NamePart:
		return string(n.Value)
	}
	return ""
}

func joinNameParts(parts []ast.Vertex) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if np, ok := p.(*ast.NamePart); ok {
			out = append(out, string(np.Value))
		}
	}
	return strings.Join(out, `\`)
}

func identifierValue(v ast.Vertex) string {
	if id, ok := v.(*ast.Identifier); ok {
		return string(id.Value)
	}
	return ""
}

func identifierList(vs []ast.Vertex) []string {
	if len(vs) == 0 {
		return nil
	}
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		out = append(out, identifierValue(v))
	}
	return out
}

// isPublic returns true if the method modifier list contains "public"
// or no visibility modifier at all (PHP defaults methods to public).
func isPublic(mods []ast.Vertex) bool {
	if len(mods) == 0 {
		return true
	}
	for _, m := range mods {
		switch strings.ToLower(identifierValue(m)) {
		case "private", "protected":
			return false
		}
	}
	return true
}

func paramName(p *ast.Parameter) string {
	v, ok := p.Var.(*ast.ExprVariable)
	if !ok {
		return ""
	}
	return strings.TrimPrefix(identifierValue(v.Name), "$")
}

// isBuiltinType returns true for PHP scalar types and pseudo-types that
// must not be FQCN-resolved as if they were class references.
func isBuiltinType(s string) bool {
	switch strings.ToLower(s) {
	case "int", "float", "string", "bool", "void", "mixed", "never",
		"self", "static", "parent", "object", "callable", "iterable",
		"array", "true", "false", "null":
		return true
	}
	return false
}

// leadToken returns the first token of a type declaration, considering
// attribute groups and modifiers that may precede the keyword. The
// FreeFloating list on this token holds the preceding docblock.
func leadToken(attrGroups []ast.Vertex, modifiers []ast.Vertex, keyword *token.Token) *token.Token {
	if len(attrGroups) > 0 {
		if ag, ok := attrGroups[0].(*ast.AttributeGroup); ok {
			return ag.OpenAttributeTkn
		}
	}
	if len(modifiers) > 0 {
		if id, ok := modifiers[0].(*ast.Identifier); ok {
			return id.IdentifierTkn
		}
	}
	return keyword
}

// attributes extracts attributes with their arguments from attribute
// groups. Names are resolved to FQCN via use-imports so consumers can
// match on a stable suffix (e.g. "\Column").
func (e *extractor) attributes(groups []ast.Vertex) []Attr {
	var out []Attr
	for _, g := range groups {
		ag, ok := g.(*ast.AttributeGroup)
		if !ok {
			continue
		}
		for _, a := range ag.Attrs {
			attr, ok := a.(*ast.Attribute)
			if !ok {
				continue
			}
			name := e.resolveName(attr.Name)
			if name == "" {
				continue
			}
			out = append(out, Attr{Name: name, Args: e.attrArgs(attr.Args)})
		}
	}
	return out
}

func (e *extractor) attrArgs(args []ast.Vertex) []AttrArg {
	var out []AttrArg
	for _, a := range args {
		arg, ok := a.(*ast.Argument)
		if !ok {
			continue
		}
		out = append(out, AttrArg{
			Name:  identifierValue(arg.Name),
			Value: e.attrArgValue(arg.Expr),
		})
	}
	return out
}

// attrArgValue stringifies an attribute argument expression. Unknown node
// shapes return "" rather than failing — schema/route consumers tolerate a
// missing value and fall back to defaults.
func (e *extractor) attrArgValue(v ast.Vertex) string {
	switch t := v.(type) {
	case *ast.ScalarString:
		return unquotePHP(string(t.Value))
	case *ast.ScalarLnumber:
		return string(t.Value)
	case *ast.ScalarDnumber:
		return string(t.Value)
	case *ast.ExprConstFetch:
		return nameToString(t.Const) // true | false | null
	case *ast.ExprClassConstFetch:
		return e.resolveName(t.Class) + "::" + identifierValue(t.Const)
	case *ast.ExprArray:
		parts := make([]string, 0, len(t.Items))
		for _, it := range t.Items {
			item, ok := it.(*ast.ExprArrayItem)
			if !ok || item.Val == nil {
				continue
			}
			parts = append(parts, e.attrArgValue(item.Val))
		}
		return "[" + strings.Join(parts, ",") + "]"
	default:
		return ""
	}
}

// unquotePHP strips surrounding single/double quotes from a PHP string
// literal and unescapes the quote + backslash escapes that matter.
func unquotePHP(s string) string {
	if len(s) < 2 {
		return s
	}
	q := s[0]
	if (q == '\'' || q == '"') && s[len(s)-1] == q {
		inner := s[1 : len(s)-1]
		inner = strings.ReplaceAll(inner, `\`+string(q), string(q))
		inner = strings.ReplaceAll(inner, `\\`, `\`)
		return inner
	}
	return s
}

// visibility returns the visibility keyword from a modifier list, or
// "public" when none is present (PHP's default).
func visibility(mods []ast.Vertex) string {
	for _, m := range mods {
		switch v := strings.ToLower(identifierValue(m)); v {
		case "public", "protected", "private":
			return v
		}
	}
	return "public"
}

func attributeNames(groups []ast.Vertex) []string {
	var out []string
	for _, g := range groups {
		ag, ok := g.(*ast.AttributeGroup)
		if !ok {
			continue
		}
		for _, a := range ag.Attrs {
			attr, ok := a.(*ast.Attribute)
			if !ok {
				continue
			}
			if n := nameToString(attr.Name); n != "" {
				out = append(out, n)
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

// docSummary returns the first non-tag line of the docblock that
// immediately precedes the given introducer token (the `class`,
// `interface`, `trait`, or `enum` keyword). Returns "" if none.
func docSummary(t *token.Token) string {
	if t == nil {
		return ""
	}
	var last *token.Token
	for _, ft := range t.FreeFloating {
		if ft.ID == token.T_DOC_COMMENT {
			last = ft
		}
	}
	if last == nil {
		return ""
	}
	for _, ln := range strings.Split(string(last.Value), "\n") {
		ln = strings.TrimSpace(ln)
		ln = strings.TrimPrefix(ln, "/**")
		ln = strings.TrimSuffix(ln, "*/")
		ln = strings.TrimPrefix(ln, "*")
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "@") {
			continue
		}
		return ln
	}
	return ""
}
