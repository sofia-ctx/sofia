package php

import (
	"fmt"
	"sort"
	"strings"

	"github.com/dimasma0305/php-parser-go/ast"
	"github.com/dimasma0305/php-parser-go/phpparser"
)

// SourceEdit replaces an exact byte span in the original source.
// End is exclusive. Before must still match when applying an edit.
type SourceEdit struct {
	Start  int    `json:"start"`
	End    int    `json:"end"`
	Line   int    `json:"line"`
	Before string `json:"before"`
	After  string `json:"after"`
	Reason string `json:"reason"`
}

// RelocationWarning identifies source requiring human review beyond class names.
type RelocationWarning struct {
	Line   int    `json:"line"`
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
}

// Relocation describes class renames and an optional namespace change in one file.
// Keys and values are fully qualified names without a leading backslash.
type Relocation struct {
	Classes      map[string]string
	Move         bool
	Namespace    string
	NewNamespace string
}

type RelocationResult struct {
	Source       string              `json:"source"`
	Edits        []SourceEdit        `json:"edits"`
	Declarations []string            `json:"declarations"`
	Warnings     []RelocationWarning `json:"warnings"`
}

// Relocate rewrites resolved class names, imports and declarations using original
// AST offsets. It never normalizes source or reprints unrelated code. Warnings
// cover dynamic/string/doc references and namespace-dependent runtime behavior.
func Relocate(src, path string, options Relocation) (result *RelocationResult, err error) {
	// The upstream name resolver can panic on malformed aliases. Such input must
	// fail a plan, never produce an incomplete set of edits.
	defer func() {
		if failure := recover(); failure != nil {
			result = nil
			err = fmt.Errorf("php relocation %s: resolver failure: %v", path, failure)
		}
	}()
	nodes, diagnostics, err := parsePHP([]byte(src))
	if err != nil {
		return nil, err
	}
	if len(diagnostics) > 0 {
		return nil, parseError(path, diagnostics)
	}
	handler := &phpparser.CollectingErrorHandler{}
	resolver := ast.NewNameResolver(handler, map[string]bool{"replaceNodes": false})
	if _, err = ast.NewTraverser(&relocationResolver{NameResolver: resolver}).Traverse(nodes); err != nil {
		return nil, err
	}
	if len(handler.Errors()) > 0 {
		return nil, fmt.Errorf("php relocation %s: %s", path, handler.Errors()[0].Error())
	}
	r := &relocator{source: src, options: options, classes: map[string]string{}, result: &RelocationResult{Source: src}, context: ast.NewNameContext(handler), kinds: map[ast.Node]int{}, warned: map[string]bool{}}
	for from, to := range options.Classes {
		if from == "" || to == "" || strings.HasPrefix(from, `\`) || strings.HasPrefix(to, `\`) {
			return nil, fmt.Errorf("class mappings require nonempty FQCNs without leading backslashes")
		}
		key := strings.ToLower(from)
		if previous, ok := r.classes[key]; ok && previous != to {
			return nil, fmt.Errorf("conflicting class mapping: %s", from)
		}
		r.classes[key] = to
	}
	r.context.StartNamespace(nil)
	for _, n := range nodes {
		ast.Walk(n, r.visit)
	}
	if r.err != nil {
		return nil, fmt.Errorf("php relocation %s: %w", path, r.err)
	}
	if len(handler.Errors()) > 0 {
		return nil, fmt.Errorf("php relocation %s: %s", path, handler.Errors()[0].Error())
	}
	if options.Move && (r.namespaces != 1 || len(r.result.Declarations) != 1) {
		return nil, fmt.Errorf("php relocation %s: moved files require one explicit namespace and one named type", path)
	}
	sort.Slice(r.result.Edits, func(i, j int) bool { return r.result.Edits[i].Start < r.result.Edits[j].Start })
	end := 0
	for _, e := range r.result.Edits {
		if e.Start < end {
			return nil, fmt.Errorf("php relocation %s: overlapping source edits", path)
		}
		end = e.End
	}
	output := src
	for i := len(r.result.Edits) - 1; i >= 0; i-- {
		e := r.result.Edits[i]
		output = output[:e.Start] + e.After + output[e.End:]
	}
	_, diagnostics, err = parsePHP([]byte(output))
	if err != nil {
		return nil, err
	}
	if len(diagnostics) > 0 {
		return nil, fmt.Errorf("rewritten source is invalid: %w", parseError(path, diagnostics))
	}
	r.result.Source = output
	sort.Slice(r.result.Warnings, func(i, j int) bool {
		a, b := r.result.Warnings[i], r.result.Warnings[j]
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.Detail < b.Detail
	})
	return r.result, nil
}

type relocator struct {
	source     string
	options    Relocation
	classes    map[string]string
	result     *RelocationResult
	context    *ast.NameContext
	kinds      map[ast.Node]int
	warned     map[string]bool
	namespaces int
	err        error
}

// Upstream v0.1.1 attempts name resolution on variable/expression calls, which
// are not names. Skip only that operation, retaining traversal of their children.
type relocationResolver struct{ *ast.NameResolver }

func (r *relocationResolver) EnterNode(n ast.Node) any {
	if call, ok := n.(*ast.ExprFuncCall); ok && !isName(call.Name) {
		return nil
	}
	return r.NameResolver.EnterNode(n)
}

func (r *relocator) visit(n ast.Node) bool {
	if r.err != nil {
		return false
	}
	for _, c := range n.Comments() {
		if comment, ok := c.(interface {
			Text() string
			StartLine() int
		}); ok && strings.HasPrefix(comment.Text(), "/**") {
			if r.options.Move || r.mentionsClass(comment.Text()) {
				r.warn(comment.StartLine(), "phpdoc", "Review PHPDoc types and references; comments are preserved verbatim")
			}
		}
	}
	switch t := n.(type) {
	case *ast.StmtNamespace:
		r.namespaces++
		namespace := nameToString(t.Name)
		if r.options.Move {
			if namespace != r.options.Namespace || namespace == "" || r.options.NewNamespace == "" {
				r.err = fmt.Errorf("namespace does not match the supported PSR-4 relocation")
				return false
			}
			namespace = r.options.NewNamespace
			r.edit(t.Name, namespace, "namespace")
		}
		name, _ := ast.NewName(namespace)
		if namespace == "" {
			r.context.StartNamespace(nil)
		} else {
			r.context.StartNamespace(name)
		}
	case *ast.StmtUse:
		typ, _ := t.Type.(int)
		r.imports(n, t.Uses, typ, nil)
		return false
	case *ast.StmtGroupUse:
		r.imports(n, t.Uses, t.Type, t.Prefix)
		return false
	case *ast.StmtClass:
		if t.Name != nil {
			r.declaration(t.Name, t.NamespacedName)
		}
	case *ast.StmtInterface:
		r.declaration(t.Name, t.NamespacedName)
	case *ast.StmtTrait:
		r.declaration(t.Name, t.NamespacedName)
	case *ast.StmtEnum:
		r.declaration(t.Name, t.NamespacedName)
	case *ast.StmtFunction, *ast.StmtConst:
		if r.options.Move {
			r.err = fmt.Errorf("moving files with named functions or namespace constants is unsupported")
			return false
		}
	case *ast.ExprFuncCall:
		if isName(t.Name) {
			r.kinds[t.Name] = ast.StmtUseTypeFunction
		}
	case *ast.ExprConstFetch:
		r.kinds[t.Name] = ast.StmtUseTypeConstant
	case *ast.ScalarString:
		if r.mentionsClass(t.Value) {
			r.warn(n.StartLine(), "string_reference", "Possible class reference in a string; value is unchanged")
		}
	}
	if r.options.Move {
		switch n.NodeType() {
		case "Scalar_MagicConst_Dir", "Scalar_MagicConst_File", "Expr_Include":
			r.warn(n.StartLine(), "file_location", "Review path-dependent expression after moving this file")
		case "Scalar_MagicConst_Namespace":
			if r.options.Namespace != r.options.NewNamespace {
				r.warn(n.StartLine(), "namespace_value", "Review dynamically constructed names using __NAMESPACE__")
			}
		}
	}
	if isName(n) {
		r.reference(n)
	}
	return true
}

func (r *relocator) declaration(name, fqcn ast.Node) {
	original := nameToString(fqcn)
	r.result.Declarations = append(r.result.Declarations, original)
	if target, ok := r.classes[strings.ToLower(original)]; ok {
		if !r.options.Move {
			r.err = fmt.Errorf("mapped declaration %s found outside moved files", original)
			return
		}
		ns, short := splitClass(target)
		if ns != r.options.NewNamespace {
			r.err = fmt.Errorf("declaration target does not match destination namespace")
			return
		}
		r.edit(name, short, "type declaration")
	}
}

func (r *relocator) reference(n ast.Node) {
	kind := r.kinds[n]
	if kind == 0 {
		kind = ast.StmtUseTypeNormal
	}
	value, _ := n.Attribute("resolvedName")
	resolved, _ := value.(ast.Node)
	if resolved == nil {
		// Unqualified functions/constants fall back from the current namespace to
		// the global namespace. A class-only move cannot prove runtime resolution.
		if r.options.Move && r.options.Namespace != r.options.NewNamespace && kind != ast.StmtUseTypeNormal {
			if _, ok := n.Attribute("namespacedName"); ok {
				r.warn(n.StartLine(), "namespace_fallback", nameToString(n))
			}
		}
		return
	}
	desired := nameToString(resolved)
	if kind == ast.StmtUseTypeNormal {
		if isBuiltinType(desired) {
			return
		}
		if target, ok := r.classes[strings.ToLower(desired)]; ok {
			desired = target
		}
	}
	candidate, err := r.context.GetResolvedName(n, kind)
	if err != nil {
		r.err = err
		return
	}
	if nameToString(candidate) != desired {
		r.edit(n, `\`+desired, "resolved reference")
	}
}

type relocationImport struct {
	item                    *ast.UseItem
	kind                    int
	original, target, alias string
}

func (r *relocator) imports(statement ast.Node, nodes []ast.Node, typ int, prefix ast.Node) {
	if typ == 0 {
		typ = ast.StmtUseTypeNormal
	}
	var items []relocationImport
	changed := false
	for _, n := range nodes {
		item, ok := n.(*ast.UseItem)
		if !ok {
			r.err = fmt.Errorf("unsupported import node")
			return
		}
		kind := typ
		if item.Type != 0 {
			kind = item.Type
		}
		original := joinFQCN(nameToString(prefix), nameToString(item.Name))
		target := original
		if kind == ast.StmtUseTypeNormal {
			if mapped, ok := r.classes[strings.ToLower(original)]; ok {
				target = mapped
			}
		}
		alias := item.GetAlias().Name
		fq, _ := ast.NewNameFullyQualified(target)
		if err := r.context.AddAlias(fq, alias, kind, nil); err != nil {
			r.err = err
			return
		}
		items = append(items, relocationImport{item, kind, original, target, alias})
		changed = changed || target != original
	}
	if !changed {
		return
	}
	if prefix == nil {
		for _, item := range items {
			r.importName(item, item.target)
		}
		return
	}
	common := commonImportPrefix(items)
	if common != "" {
		r.edit(prefix, common, "group import prefix")
		for _, item := range items {
			r.importName(item, strings.TrimPrefix(item.target, common+`\`))
		}
		return
	}
	raw := r.nodeSource(statement)
	if strings.Contains(raw, "/*") || strings.Contains(raw, "//") || strings.Contains(raw, "#") {
		r.err = fmt.Errorf("cannot split a group import with comments across unrelated namespace roots")
		return
	}
	var declarations []string
	for _, item := range items {
		keyword := "use "
		if item.kind == ast.StmtUseTypeFunction {
			keyword += "function "
		} else if item.kind == ast.StmtUseTypeConstant {
			keyword += "const "
		}
		declaration := keyword + item.target
		_, short := splitClass(item.target)
		if item.item.Alias != nil || short != item.alias {
			declaration += " as " + item.alias
		}
		declarations = append(declarations, declaration+";")
	}
	newline := "\n"
	if strings.Contains(r.source, "\r\n") {
		newline = "\r\n"
	}
	r.edit(statement, strings.Join(declarations, newline), "split group import")
}

func (r *relocator) importName(item relocationImport, target string) {
	_, short := splitClass(item.target)
	if item.item.Alias == nil && short != item.alias {
		target += " as " + item.alias
	}
	r.edit(item.item.Name, target, "import")
}

func commonImportPrefix(items []relocationImport) string {
	if len(items) == 0 {
		return ""
	}
	ns, _ := splitClass(items[0].target)
	parts := strings.Split(ns, `\`)
	if ns == "" {
		return ""
	}
	for _, item := range items[1:] {
		namespace, _ := splitClass(item.target)
		other := strings.Split(namespace, `\`)
		count := 0
		for count < len(parts) && count < len(other) && parts[count] == other[count] {
			count++
		}
		parts = parts[:count]
	}
	return strings.Join(parts, `\`)
}

func (r *relocator) nodeSource(n ast.Node) string {
	start, end := n.StartFilePos(), n.EndFilePos()+1
	if start < 0 || end < start || end > len(r.source) {
		r.err = fmt.Errorf("invalid source span")
		return ""
	}
	return r.source[start:end]
}

func (r *relocator) edit(n ast.Node, after, reason string) {
	before := r.nodeSource(n)
	if r.err != nil || before == after {
		return
	}
	r.result.Edits = append(r.result.Edits, SourceEdit{Start: n.StartFilePos(), End: n.EndFilePos() + 1, Line: n.StartLine(), Before: before, After: after, Reason: reason})
}

func (r *relocator) mentionsClass(text string) bool {
	text = strings.ToLower(text)
	for from := range r.classes {
		if strings.Contains(text, from) || strings.Contains(text, strings.ReplaceAll(from, `\`, `\\`)) {
			return true
		}
		_, short := splitClass(from)
		for offset := 0; offset < len(text); {
			index := strings.Index(text[offset:], short)
			if index < 0 {
				break
			}
			index += offset
			end := index + len(short)
			if (index == 0 || !phpIdentifierByte(text[index-1])) && (end == len(text) || !phpIdentifierByte(text[end])) {
				return true
			}
			offset = end
		}
	}
	return false
}

func phpIdentifierByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c >= 128
}

func (r *relocator) warn(line int, kind, detail string) {
	key := fmt.Sprintf("%d:%s:%s", line, kind, detail)
	if !r.warned[key] {
		r.warned[key] = true
		r.result.Warnings = append(r.result.Warnings, RelocationWarning{Line: line, Kind: kind, Detail: detail})
	}
}

func splitClass(name string) (string, string) {
	if i := strings.LastIndexByte(name, '\\'); i >= 0 {
		return name[:i], name[i+1:]
	}
	return "", name
}

func isName(n ast.Node) bool {
	switch n.(type) {
	case *ast.Name, *ast.NameFullyQualified, *ast.NameRelative:
		return true
	}
	return false
}
