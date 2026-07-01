package phpcode

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/sofia-ctx/sofia/internal/common/php"
)

// methodVia pairs a method with the short name of the trait or parent class
// that defines it ("" for the root class's own methods).
type methodVia struct {
	M   php.Method
	Via string
}

// effectiveSurface resolves the full public method surface of a class or
// trait: its own methods, then methods from each composed trait (recursively),
// then inherited methods up the parent chain. PHP precedence (own > trait >
// parent) is honoured by first-wins dedup on method name. Traits or parents
// whose source file can't be located are reported in notes instead of failing,
// matching `sf code`'s graceful, never-error-out contract.
func effectiveSurface(root *php.Symbol, path string) (methods []methodVia, notes []string) {
	r := newResolver(path, root)
	seen := map[string]bool{}    // method name already taken (precedence)
	visited := map[string]bool{} // FQCN already expanded (cycle guard)
	if root.FQCN != "" {
		visited[root.FQCN] = true
	}

	var walk func(sym *php.Symbol, via string, isRoot bool)
	walk = func(sym *php.Symbol, via string, isRoot bool) {
		for _, m := range sym.Methods { // php.Symbol.Methods is public-only already
			if seen[m.Name] {
				continue
			}
			seen[m.Name] = true
			v := via
			if isRoot {
				v = ""
			}
			methods = append(methods, methodVia{M: m, Via: v})
		}
		// Traits outrank the parent class, so expand them first.
		for _, t := range sym.Uses {
			if visited[t] {
				continue
			}
			visited[t] = true
			ts, ok := r.read(t)
			if !ok {
				notes = append(notes, "trait "+lastSeg(t)+" (unresolved)")
				continue
			}
			walk(ts, lastSeg(t), false)
		}
		if sym.Extends == "" || visited[sym.Extends] {
			return
		}
		visited[sym.Extends] = true
		ps, ok := r.read(sym.Extends)
		if !ok {
			notes = append(notes, "extends "+lastSeg(sym.Extends)+" (unresolved)")
			return
		}
		walk(ps, lastSeg(sym.Extends), false)
	}
	walk(root, "", true)
	return methods, notes
}

// resolver maps a fully-qualified class/trait name to its source file using
// PSR-4 rules. Two sources, tried in order:
//   - derived: the root symbol's own namespace → its directory, which covers
//     sibling traits in the same package without reading anything else;
//   - composer: vendor/composer/autoload_psr4.php (lazy), which covers parents
//     and traits living in other (vendor) packages.
type resolver struct {
	derived  []psr4Entry
	startDir string
	composer []psr4Entry
	loaded   bool
}

type psr4Entry struct {
	prefix string // namespace prefix with trailing backslash, e.g. `Foo\Bar\`
	dirs   []string
}

func newResolver(path string, root *php.Symbol) *resolver {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	dir := filepath.Dir(abs)
	r := &resolver{startDir: dir}
	// PSR-4 guarantees the file's namespace maps to the file's directory, so
	// `<namespace>\ => <dir>` resolves any sibling in the same namespace subtree.
	if root.Namespace != "" {
		r.derived = []psr4Entry{{prefix: root.Namespace + `\`, dirs: []string{dir}}}
	}
	return r
}

func (r *resolver) read(fqcn string) (*php.Symbol, bool) {
	if p, ok := resolveFromEntries(fqcn, r.derived); ok {
		if s, err := php.Read(p); err == nil {
			return s, true
		}
	}
	if !r.loaded {
		r.composer = loadComposerPSR4(r.startDir)
		r.loaded = true
	}
	if p, ok := resolveFromEntries(fqcn, r.composer); ok {
		if s, err := php.Read(p); err == nil {
			return s, true
		}
	}
	return nil, false
}

// resolveFromEntries finds the file for fqcn under the longest matching PSR-4
// prefix, returning the first candidate that exists on disk.
func resolveFromEntries(fqcn string, entries []psr4Entry) (string, bool) {
	var bestDirs []string
	bestLen := -1
	var rel string
	for _, e := range entries {
		if !strings.HasPrefix(fqcn+`\`, e.prefix) {
			continue
		}
		if len(e.prefix) > bestLen {
			bestLen = len(e.prefix)
			bestDirs = e.dirs
			rel = strings.TrimPrefix(fqcn, e.prefix)
		}
	}
	if bestLen < 0 {
		return "", false
	}
	relPath := filepath.FromSlash(strings.ReplaceAll(rel, `\`, "/")) + ".php"
	for _, d := range bestDirs {
		cand := filepath.Join(d, relPath)
		if fileExists(cand) {
			return cand, true
		}
	}
	return "", false
}

var (
	psr4EntryRe = regexp.MustCompile(`'((?:[^'\\]|\\.)*)'\s*=>\s*array\(([^)]*)\)`)
	psr4DirRe   = regexp.MustCompile(`(\$vendorDir|\$baseDir)\s*\.\s*'([^']*)'`)
)

// loadComposerPSR4 walks up from startDir to the nearest
// vendor/composer/autoload_psr4.php and parses its prefix → directory map.
// Returns nil when no installed Composer autoloader is found.
func loadComposerPSR4(startDir string) []psr4Entry {
	dir := startDir
	for {
		al := filepath.Join(dir, "vendor", "composer", "autoload_psr4.php")
		if fileExists(al) {
			return parsePSR4File(al)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil
		}
		dir = parent
	}
}

func parsePSR4File(autoloadFile string) []psr4Entry {
	data, err := os.ReadFile(autoloadFile)
	if err != nil {
		return nil
	}
	// $vendorDir = dirname(__DIR__); $baseDir = dirname($vendorDir), where
	// __DIR__ is the directory holding autoload_psr4.php.
	vendorDir := filepath.Dir(filepath.Dir(autoloadFile))
	baseDir := filepath.Dir(vendorDir)
	var out []psr4Entry
	for _, m := range psr4EntryRe.FindAllStringSubmatch(string(data), -1) {
		prefix := unescapePHP(m[1])
		if !strings.HasSuffix(prefix, `\`) {
			continue
		}
		var dirs []string
		for _, d := range psr4DirRe.FindAllStringSubmatch(m[2], -1) {
			base := baseDir
			if d[1] == "$vendorDir" {
				base = vendorDir
			}
			dirs = append(dirs, filepath.Join(base, filepath.FromSlash(d[2])))
		}
		if len(dirs) > 0 {
			out = append(out, psr4Entry{prefix: prefix, dirs: dirs})
		}
	}
	return out
}

// unescapePHP undoes the escaping in a PHP single-quoted literal (only `\\`
// and `\'` are special there), which is how Composer writes namespace prefixes.
func unescapePHP(s string) string {
	s = strings.ReplaceAll(s, `\\`, `\`)
	return strings.ReplaceAll(s, `\'`, `'`)
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
