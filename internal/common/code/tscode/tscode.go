// Package tscode is the TypeScript/Vue backend for `sf code`. Extraction is
// line/block-based (there is no good pure-Go TS parser), so it is deliberately
// approximate but covers what a structural read needs: imports, top-level
// declarations, interface/type/enum members, and — for Vue SFCs — props,
// emits, models, the stores and API calls used, and the components rendered.
package tscode

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/sofia-ctx/sofia/internal/toon"
)

// Summarize writes the structural summary of the TS/Vue file at path to w.
// api (PHP's effective-surface flag) is not meaningful for TS/Vue and is
// ignored. brief requests the signature-only cut — see Brief for exactly
// what it drops.
func Summarize(w io.Writer, path, format string, exported, _, brief bool) (map[string]any, error) {
	f, err := ReadTS(path)
	if err != nil {
		return nil, err
	}
	if exported {
		kept := f.Symbols[:0]
		for _, s := range f.Symbols {
			if s.Exported {
				kept = append(kept, s)
			}
		}
		f.Symbols = kept
		types := f.Types[:0]
		for _, t := range f.Types {
			if t.Exported {
				types = append(types, t)
			}
		}
		f.Types = types
	}
	if brief {
		f.Brief()
	}
	switch format {
	case "", "toon":
		renderTOON(w, f)
	case "md":
		renderMarkdown(w, f)
	case "json":
		if err := renderJSON(w, f); err != nil {
			return nil, err
		}
	}
	return map[string]any{"lang": f.Lang, "symbols": len(f.Symbols), "types": len(f.Types), "imports": len(f.Imports)}, nil
}

// TSFile is the structural summary of a TypeScript / Vue source file.
type TSFile struct {
	File       string     `json:"file"`
	Lang       string     `json:"lang"`                 // ts | vue
	Component  string     `json:"component,omitempty"`  // .vue only
	Imports    []string   `json:"imports,omitempty"`    // module specifiers
	Props      []string   `json:"props,omitempty"`      // .vue defineProps keys
	Emits      []string   `json:"emits,omitempty"`      // .vue defineEmits keys
	Models     []string   `json:"models,omitempty"`     // .vue defineModel names
	Stores     []string   `json:"stores,omitempty"`     // .vue composables/stores used (useXStore)
	APICalls   []string   `json:"api_calls,omitempty"`  // .vue client.* / axios.* calls
	Components []string   `json:"components,omitempty"` // .vue components used in <template>
	Types      []TSType   `json:"types,omitempty"`      // interface/type/enum with members
	Symbols    []TSSymbol `json:"symbols"`              // const | function | class
}

type TSSymbol struct {
	Kind     string `json:"kind"` // const | function | class
	Name     string `json:"name"`
	Exported bool   `json:"exported"`
}

type TSType struct {
	Kind     string `json:"kind"` // interface | type | enum
	Name     string `json:"name"`
	Members  string `json:"members,omitempty"` // "id: string; name: string" | "A, B" | union RHS — blank in Brief mode for interface/type
	Exported bool   `json:"exported"`
}

var (
	reTSImport  = regexp.MustCompile(`^\s*import\b.*\bfrom\s+['"]([^'"]+)['"]`)
	reTSImport2 = regexp.MustCompile(`^\s*import\s+['"]([^'"]+)['"]`)
	reTSTypeHd  = regexp.MustCompile(`^\s*(export\s+)?(interface|enum)\s+([A-Za-z_$][\w$]*)`)
	reTSAlias   = regexp.MustCompile(`^\s*(export\s+)?type\s+([A-Za-z_$][\w$]*)\s*=\s*(.*)$`)
	reTSDecl    = regexp.MustCompile(`^\s*export\s+(?:default\s+)?(?:async\s+)?(const|function|class)\s+([A-Za-z_$][\w$]*)`)
	reTSFunc    = regexp.MustCompile(`^\s*(?:async\s+)?function\s+([A-Za-z_$][\w$]*)`)
	reTSConst   = regexp.MustCompile(`^\s*const\s+([A-Za-z_$][\w$]*)\s*[=:]`)
	reMember    = regexp.MustCompile(`^\s*(?:readonly\s+)?([A-Za-z_$][\w$]*)\s*\??\s*:\s*(.+?);?\s*$`)
	reEnumKey   = regexp.MustCompile(`^\s*([A-Za-z_$][\w$]*)\s*(?:=.*)?,?\s*$`)
	reScript    = regexp.MustCompile(`(?s)<script([^>]*)>(.*?)</script>`)
	reTemplate  = regexp.MustCompile(`(?s)<template[^>]*>(.*)</template>`)
	reDefine    = regexp.MustCompile(`\bdefine(Props|Emits)\b`)
	reModel     = regexp.MustCompile(`(?:const\s+([A-Za-z_$][\w$]*)\s*=\s*)?defineModel(?:<[^>]*>)?\s*\(\s*['"]?([A-Za-z_$][\w$]*)?`)
	reStore     = regexp.MustCompile(`\b(use[A-Z][A-Za-z0-9_$]*)\s*\(`)
	reAPICall   = regexp.MustCompile(`\b(?:client|api|axios|http)\.([a-zA-Z][\w$]*)\s*\(`)
	reCompTag   = regexp.MustCompile(`<([A-Z][A-Za-z0-9]*)\b`)
	reKey       = regexp.MustCompile(`['"]?([A-Za-z_$][\w$:-]*)['"]?\s*[?]?\s*:`)
)

// ReadTS reads a .ts/.tsx/.vue file into a structural summary.
func ReadTS(path string) (*TSFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	f := &TSFile{File: path}
	src := string(data)
	if strings.HasSuffix(path, ".vue") {
		f.Lang = "vue"
		f.Component = strings.TrimSuffix(filepath.Base(path), ".vue")
		if tmpl := reTemplate.FindStringSubmatch(src); tmpl != nil {
			f.Components = templateComponents(f.Component, tmpl[1])
		}
		src = vueScript(src)
	} else {
		f.Lang = "ts"
	}
	parseScript(f, src)
	return f, nil
}

// Brief collapses field/value-level detail for a signature-only view:
// interface and type-alias bodies drop their member list — the object-shape
// equivalent of a Go struct's fields — while enum stays (its Members is
// already just the bare case names, no more detail than a Go const block's
// names). Symbols carry no member/signature detail to begin with (tscode is
// line-based and never captured one), so there's nothing left to cut there.
func (f *TSFile) Brief() {
	for i := range f.Types {
		if f.Types[i].Kind == "interface" || f.Types[i].Kind == "type" {
			f.Types[i].Members = ""
		}
	}
}

// vueScript returns the content of the SFC's <script> block, preferring the
// `setup` one when several are present.
func vueScript(src string) string {
	matches := reScript.FindAllStringSubmatch(src, -1)
	if len(matches) == 0 {
		return ""
	}
	for _, m := range matches {
		if strings.Contains(m[1], "setup") {
			return m[2]
		}
	}
	return matches[0][2]
}

func parseScript(f *TSFile, src string) {
	seen := map[string]bool{}
	storeSeen := map[string]bool{}
	apiSeen := map[string]bool{}
	lines := strings.Split(src, "\n")

	for i := 0; i < len(lines); i++ {
		line := lines[i]

		if m := reTSImport.FindStringSubmatch(line); m != nil {
			addUniq(&f.Imports, seen, m[1])
			continue
		}
		if m := reTSImport2.FindStringSubmatch(line); m != nil {
			addUniq(&f.Imports, seen, m[1])
			continue
		}

		// interface / enum with a (possibly multi-line) body.
		if m := reTSTypeHd.FindStringSubmatch(line); m != nil {
			t := TSType{Kind: m[2], Name: m[3], Exported: m[1] != ""}
			body, next := gatherBlock(lines, i)
			t.Members = members(t.Kind, body)
			f.Types = append(f.Types, t)
			i = next
			continue
		}
		// type alias: export type X = <rhs> (rhs may be one line or an object).
		if m := reTSAlias.FindStringSubmatch(line); m != nil {
			t := TSType{Kind: "type", Name: m[2], Exported: m[1] != ""}
			rhs := strings.TrimSpace(m[3])
			if strings.HasPrefix(rhs, "{") && !strings.Contains(rhs, "}") {
				body, next := gatherBlock(lines, i)
				t.Members = members("interface", body)
				i = next
			} else {
				t.Members = strings.TrimRight(rhs, ";")
			}
			f.Types = append(f.Types, t)
			continue
		}

		// Vue-only signals (cheap to scan everywhere; only populated for .vue).
		if f.Lang == "vue" {
			scanVue(f, line, storeSeen, apiSeen)
		}

		if m := reTSDecl.FindStringSubmatch(line); m != nil {
			f.Symbols = append(f.Symbols, TSSymbol{Kind: m[1], Name: m[2], Exported: true})
			continue
		}
		if m := reTSFunc.FindStringSubmatch(line); m != nil {
			f.Symbols = append(f.Symbols, TSSymbol{Kind: "function", Name: m[1]})
			continue
		}
		if m := reTSConst.FindStringSubmatch(line); m != nil {
			f.Symbols = append(f.Symbols, TSSymbol{Kind: "const", Name: m[1]})
		}
	}
}

// scanVue pulls Vue-specific signals out of one script line.
func scanVue(f *TSFile, line string, storeSeen, apiSeen map[string]bool) {
	if reDefine.MatchString(line) {
		if strings.Contains(line, "defineProps") {
			f.Props = append(f.Props, braceKeys(line)...)
		}
		if strings.Contains(line, "defineEmits") {
			f.Emits = append(f.Emits, braceKeys(line)...)
		}
	}
	if strings.Contains(line, "defineModel") {
		if m := reModel.FindStringSubmatch(line); m != nil {
			name := m[2]
			if name == "" {
				name = m[1]
			}
			if name == "" {
				name = "modelValue"
			}
			f.Models = append(f.Models, name)
		}
	}
	for _, m := range reStore.FindAllStringSubmatch(line, -1) {
		addUniq(&f.Stores, storeSeen, m[1])
	}
	for _, m := range reAPICall.FindAllStringSubmatch(line, -1) {
		addUniq(&f.APICalls, apiSeen, m[1])
	}
}

// gatherBlock returns the text between the first `{` at/after line i and its
// matching `}` (by brace depth), plus the index of the line holding the close.
func gatherBlock(lines []string, i int) (string, int) {
	depth := 0
	started := false
	var b strings.Builder
	for j := i; j < len(lines); j++ {
		for _, r := range lines[j] {
			switch r {
			case '{':
				depth++
				started = true
				continue
			case '}':
				depth--
			}
			if started && depth >= 1 {
				b.WriteRune(r)
			}
		}
		if started && depth <= 0 {
			return b.String(), j
		}
		if started {
			b.WriteByte('\n')
		}
	}
	return b.String(), len(lines) - 1
}

// members renders an interface body as "name: type; ..." or an enum body as
// "A, B, C". Members are delimited by newlines and, inline, by ';'
// (interface/type) or ',' (enum) — so both multi-line and single-line bodies
// parse.
func members(kind, body string) string {
	delims := "\n;"
	if kind == "enum" {
		delims = "\n,"
	}
	toks := strings.FieldsFunc(body, func(r rune) bool { return strings.ContainsRune(delims, r) })

	var parts []string
	for _, tok := range toks {
		tok = strings.TrimSpace(tok)
		if tok == "" || strings.HasPrefix(tok, "//") {
			continue
		}
		if kind == "enum" {
			if m := reEnumKey.FindStringSubmatch(tok); m != nil {
				parts = append(parts, m[1])
			}
			continue
		}
		if m := reMember.FindStringSubmatch(tok); m != nil {
			parts = append(parts, m[1]+": "+strings.TrimSpace(m[2]))
		}
	}
	if kind == "enum" {
		return strings.Join(parts, ", ")
	}
	return strings.Join(parts, "; ")
}

// templateComponents returns the distinct PascalCase component tags used in a
// Vue <template>, excluding the component's own name.
func templateComponents(self, tmpl string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range reCompTag.FindAllStringSubmatch(tmpl, -1) {
		name := m[1]
		if name == self || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

func braceKeys(line string) []string {
	l := strings.IndexByte(line, '{')
	r := strings.LastIndexByte(line, '}')
	if l < 0 || r <= l {
		return nil
	}
	var keys []string
	for _, m := range reKey.FindAllStringSubmatch(line[l+1:r], -1) {
		keys = append(keys, m[1])
	}
	return keys
}

func addUniq(dst *[]string, seen map[string]bool, v string) {
	if v == "" || seen[v] {
		return
	}
	seen[v] = true
	*dst = append(*dst, v)
}

func renderTOON(w io.Writer, f *TSFile) {
	fmt.Fprintf(w, "file: %s\n", filepath.Base(f.File))
	fmt.Fprintf(w, "lang: %s\n", f.Lang)
	if f.Component != "" {
		fmt.Fprintf(w, "component: %s\n", f.Component)
	}
	if len(f.Imports) > 0 {
		fmt.Fprintf(w, "imports[%d]: %s\n", len(f.Imports), toon.JoinList(f.Imports))
	}
	if len(f.Props) > 0 {
		fmt.Fprintf(w, "props[%d]: %s\n", len(f.Props), toon.JoinList(f.Props))
	}
	if len(f.Emits) > 0 {
		fmt.Fprintf(w, "emits[%d]: %s\n", len(f.Emits), toon.JoinList(f.Emits))
	}
	if len(f.Models) > 0 {
		fmt.Fprintf(w, "models[%d]: %s\n", len(f.Models), toon.JoinList(f.Models))
	}
	if len(f.Stores) > 0 {
		fmt.Fprintf(w, "stores[%d]: %s\n", len(f.Stores), toon.JoinList(f.Stores))
	}
	if len(f.APICalls) > 0 {
		fmt.Fprintf(w, "api_calls[%d]: %s\n", len(f.APICalls), toon.JoinList(f.APICalls))
	}
	if len(f.Components) > 0 {
		fmt.Fprintf(w, "components[%d]: %s\n", len(f.Components), toon.JoinList(f.Components))
	}
	if len(f.Types) > 0 {
		fmt.Fprintf(w, "types[%d]{kind,name,exported,members}:\n", len(f.Types))
		for _, t := range f.Types {
			fmt.Fprintf(w, "%s%s,%s,%t,%s\n", toon.Indent, t.Kind, toon.Scalar(t.Name), t.Exported, toon.Scalar(t.Members))
		}
	}
	fmt.Fprintf(w, "symbols[%d]{kind,name,exported}:\n", len(f.Symbols))
	for _, s := range f.Symbols {
		fmt.Fprintf(w, "%s%s,%s,%t\n", toon.Indent, s.Kind, toon.Scalar(s.Name), s.Exported)
	}
}

func renderMarkdown(w io.Writer, f *TSFile) {
	fmt.Fprintf(w, "# %s — %s", filepath.Base(f.File), f.Lang)
	if f.Component != "" {
		fmt.Fprintf(w, " `%s`", f.Component)
	}
	fmt.Fprint(w, "\n\n")
	mdList(w, "imports", f.Imports)
	mdList(w, "props", f.Props)
	mdList(w, "emits", f.Emits)
	mdList(w, "models", f.Models)
	mdList(w, "stores", f.Stores)
	mdList(w, "api_calls", f.APICalls)
	mdList(w, "components", f.Components)
	if len(f.Types) > 0 {
		fmt.Fprintln(w, "\n## types")
		for _, t := range f.Types {
			fmt.Fprintf(w, "- `%s %s` — %s\n", t.Kind, t.Name, t.Members)
		}
	}
	if len(f.Symbols) > 0 {
		fmt.Fprintln(w, "\n## symbols")
		for _, s := range f.Symbols {
			exp := ""
			if s.Exported {
				exp = "export "
			}
			fmt.Fprintf(w, "- `%s%s %s`\n", exp, s.Kind, s.Name)
		}
	}
}

func mdList(w io.Writer, label string, xs []string) {
	if len(xs) > 0 {
		fmt.Fprintf(w, "- **%s**: %s\n", label, strings.Join(xs, ", "))
	}
}

func renderJSON(w io.Writer, f *TSFile) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(f)
}
