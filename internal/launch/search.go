package launch

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// maxSearchDepth is how many directory levels below a root SearchProjects
// descends to find a project by name — a direct child is depth 1. The default
// (2) finds a project and a one-level-nested area under it (e.g. a monorepo's
// `packages/`) while staying shallow enough to skip framework noise like
// Symfony's `config/packages`. Override with $SF_CLAUDE_SEARCH_DEPTH.
const maxSearchDepth = 2

// SearchProjects finds directories named `name` a few levels under the
// configured projects root ($SF_CLAUDE_DIR) and the current directory. Results
// are absolute, deduplicated, and sorted. It's cheap: the depth cap plus the
// VCS/dependency skips mean it reads only a handful of directories, not the
// whole tree.
func SearchProjects(name string) []string {
	var roots []string
	if r := os.Getenv("SF_CLAUDE_DIR"); r != "" {
		roots = append(roots, r)
	}
	if wd, err := os.Getwd(); err == nil {
		roots = append(roots, wd)
	}
	return searchIn(name, roots, searchDepth())
}

func searchDepth() int {
	if v := os.Getenv("SF_CLAUDE_SEARCH_DEPTH"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return maxSearchDepth
}

func searchIn(name string, roots []string, max int) []string {
	seen := map[string]bool{}
	var out []string
	for _, root := range roots {
		abs, err := filepath.Abs(root)
		if err != nil || !dirExists(abs) {
			continue
		}
		walkFor(abs, name, 0, max, seen, &out)
	}
	sort.Strings(out)
	return out
}

// walkFor records directories named `name` found up to `max` levels below the
// root (the dir passed with depth 0). It never descends into a match (a project
// rarely nests another of the same name) nor into VCS/dependency dirs, and
// stops recursing once a deeper match couldn't be within `max`.
func walkFor(dir, name string, depth, max int, seen map[string]bool, out *[]string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		b := e.Name()
		child := filepath.Join(dir, b)
		if b == name {
			if !seen[child] {
				seen[child] = true
				*out = append(*out, child)
			}
			continue
		}
		if skipDescend(b) || depth+1 >= max {
			continue
		}
		walkFor(child, name, depth+1, max, seen, out)
	}
}

func skipDescend(b string) bool {
	switch b {
	case ".git", "node_modules", "vendor", ".svn", ".hg":
		return true
	}
	return strings.HasPrefix(b, ".")
}
