// Package vue implements `sf vue` — helpers over a Vue frontend. `routes`
// parses a vue-router config (`createRouter({ routes: [...] })`) into a flat
// route map (full path, name, component, meta), so the agent reads one compact
// table instead of the whole router file. Extraction is heuristic (no
// pure-Go TS parser) but the router DSL is regular: objects with
// path/name/component/meta and nested children.
package vue

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/sofia-ctx/sofia/internal/calllog"
	"github.com/sofia-ctx/sofia/pkg/toon"
	"github.com/sofia-ctx/sofia/pkg/walker"
)

// Options controls a `vue routes` run.
type Options struct {
	Root   string // tree to search for router/index.ts (default: cwd)
	File   string // explicit router file (overrides the search)
	Format string
}

// Route is one resolved route.
type Route struct {
	Path      string `json:"path"`
	Name      string `json:"name,omitempty"`
	Component string `json:"component,omitempty"`
	Meta      string `json:"meta,omitempty"`
}

var (
	reRoutes = regexp.MustCompile(`\broutes\s*:\s*\[`)
	rePath   = regexp.MustCompile(`(?m)^\s*path\s*:\s*['"]([^'"]*)['"]`)
	reName   = regexp.MustCompile(`(?m)^\s*name\s*:\s*['"]([^'"]+)['"]`)
	reComp   = regexp.MustCompile(`component\s*:\s*\(\)\s*=>\s*import\(\s*['"]([^'"]+)['"]`)
	reCompID = regexp.MustCompile(`component\s*:\s*([A-Za-z_$][\w$]*)`)
	reMeta   = regexp.MustCompile(`meta\s*:\s*\{([^}]*)\}`)
)

// Run finds/loads the router file, parses its routes, renders them, and logs.
func Run(opts Options, w io.Writer) error {
	tracker := calllog.Start("vue routes", []string{"--format=" + opts.Format, opts.File})

	file, err := resolveFile(opts)
	if err != nil {
		tracker.Finish(err)
		return err
	}
	src, err := os.ReadFile(file)
	if err != nil {
		tracker.Finish(err)
		return err
	}
	routes := Parse(string(src))
	tracker.SetSummary(map[string]any{"file": filepath.Base(file), "routes": len(routes)})

	cw := &calllog.Counter{W: w}
	renderErr := render(cw, opts.Format, routes)
	tracker.RecordOutput(cw)
	tracker.Finish(renderErr)
	return renderErr
}

// resolveFile returns the explicit --file, or searches the tree for a
// router/index.ts (then router.ts).
func resolveFile(opts Options) (string, error) {
	if opts.File != "" {
		return opts.File, nil
	}
	root := opts.Root
	if root == "" {
		root = "."
	}
	files, errs := walker.Files(walker.Options{
		Root:       root,
		IgnoreDirs: map[string]bool{"node_modules": true, "vendor": true, ".git": true, "dist": true},
		Exts:       map[string]bool{".ts": true},
	})
	var best string
	for p := range files {
		base := filepath.Base(p)
		if filepath.Base(filepath.Dir(p)) == "router" && base == "index.ts" {
			best = p
			break
		}
		if base == "router.ts" && best == "" {
			best = p
		}
	}
	if err := <-errs; err != nil {
		return "", err
	}
	if best == "" {
		return "", fmt.Errorf("vue routes: no router/index.ts found under %s (pass a file)", root)
	}
	return best, nil
}

// Parse extracts a flat, depth-resolved route list from router source.
func Parse(src string) []Route {
	loc := reRoutes.FindStringIndex(src)
	if loc == nil {
		return nil
	}
	body, ok := bracketBody(src, loc[1]-1, '[', ']') // loc[1]-1 points at '['
	if !ok {
		return nil
	}
	var out []Route
	walkRoutes(body, "", &out)
	return out
}

// walkRoutes parses the top-level route objects in an array body, resolving
// child paths against parentPath and recursing into `children`.
func walkRoutes(arrBody, parentPath string, out *[]Route) {
	for _, obj := range topObjects(arrBody) {
		own := obj
		childArr := ""
		if i := strings.Index(obj, "children"); i >= 0 {
			own = obj[:i] // fields before children belong to this route
			if b, ok2 := afterBracket(obj, i, '[', ']'); ok2 {
				childArr = b
			}
		}

		path := firstSubmatch(rePath, own)
		full := joinPath(parentPath, path)

		r := Route{Path: full, Name: firstSubmatch(reName, own), Meta: metaText(own)}
		if c := firstSubmatch(reComp, own); c != "" {
			r.Component = compName(c)
		} else if c := firstSubmatch(reCompID, own); c != "" {
			r.Component = c
		}
		// A pure layout wrapper (no name, no component-less leaf) still lists if
		// it has a path; index children resolve to the parent path.
		*out = append(*out, r)

		if childArr != "" {
			walkRoutes(childArr, full, out)
		}
	}
}

// topObjects splits an array body into its top-level `{...}` object texts.
func topObjects(arrBody string) []string {
	var objs []string
	depthCurly, depthSquare := 0, 0
	start := -1
	for i, r := range arrBody {
		switch r {
		case '{':
			if depthCurly == 0 && depthSquare == 0 {
				start = i
			}
			depthCurly++
		case '}':
			depthCurly--
			if depthCurly == 0 && depthSquare == 0 && start >= 0 {
				objs = append(objs, arrBody[start:i+1])
				start = -1
			}
		case '[':
			depthSquare++
		case ']':
			depthSquare--
		}
	}
	return objs
}

// bracketBody returns the text between the open bracket at index openIdx and
// its matching close.
func bracketBody(s string, openIdx int, open, close rune) (string, bool) {
	if openIdx < 0 || openIdx >= len(s) || rune(s[openIdx]) != open {
		return "", false
	}
	depth := 0
	for i := openIdx; i < len(s); i++ {
		switch rune(s[i]) {
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return s[openIdx+1 : i], true
			}
		}
	}
	return "", false
}

// afterBracket finds the first `open` at/after index from in s and returns its
// matching bracket body.
func afterBracket(s string, from int, open, close rune) (string, bool) {
	idx := strings.IndexRune(s[from:], open)
	if idx < 0 {
		return "", false
	}
	return bracketBody(s, from+idx, open, close)
}

func firstSubmatch(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}

// metaText compacts a `meta: { ... }` block into a single trimmed line.
func metaText(s string) string {
	m := reMeta.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	return strings.Join(strings.Fields(m[1]), " ")
}

// compName reduces an import specifier to the component name: "../views/
// LoginView.vue" → "LoginView".
func compName(spec string) string {
	base := filepath.Base(spec)
	base = strings.TrimSuffix(base, ".vue")
	base = strings.TrimSuffix(base, ".ts")
	return base
}

// joinPath resolves a child route path against its parent (vue-router rules:
// absolute child wins, empty child = parent index route).
func joinPath(parent, child string) string {
	switch {
	case strings.HasPrefix(child, "/"):
		return child
	case child == "":
		if parent == "" {
			return "/"
		}
		return parent
	case parent == "" || parent == "/":
		return "/" + child
	default:
		return strings.TrimRight(parent, "/") + "/" + child
	}
}

var fields = []string{"path", "name", "component", "meta"}

func render(w io.Writer, format string, routes []Route) error {
	switch format {
	case "", "toon":
		fmt.Fprintf(w, "routes[%d]{%s}:\n", len(routes), strings.Join(fields, ","))
		for _, r := range routes {
			fmt.Fprintf(w, "%s%s,%s,%s,%s\n", toon.Indent,
				toon.Scalar(r.Path), toon.Scalar(orDash(r.Name)), toon.Scalar(orDash(r.Component)), toon.Scalar(orDash(r.Meta)))
		}
		return nil
	case "md":
		fmt.Fprintf(w, "# vue routes (%d)\n\n", len(routes))
		fmt.Fprintln(w, "| Path | Name | Component | Meta |")
		fmt.Fprintln(w, "| --- | --- | --- | --- |")
		for _, r := range routes {
			fmt.Fprintf(w, "| %s | %s | %s | %s |\n", r.Path, orDash(r.Name), orDash(r.Component), orDash(r.Meta))
		}
		return nil
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		return enc.Encode(routes)
	default:
		return fmt.Errorf("unknown format %q (use toon|md|json)", format)
	}
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
