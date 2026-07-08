package adapter

import (
	"path/filepath"
	"strings"
)

// Match reports whether a slash-separated path matches a layer glob. It is a
// small segmented globber — no go.mod dependency (doublestar isn't vendored) —
// with exactly the one extension filepath.Match lacks: `**`, which matches zero
// or more whole path segments. Both pattern and path are split on "/"; within a
// segment, filepath.Match's metacharacters (`*`, `?`, `[…]`) apply and never
// cross a "/". So `src/*` matches `src/User.php` but not `src/Domain/User.php`,
// while `src/Domain/**` matches `src/Domain` and everything beneath it.
func Match(pattern, path string) bool {
	return matchSegs(strings.Split(pattern, "/"), strings.Split(path, "/"))
}

// matchSegs matches pattern segments pat against path segments seg. `**` is the
// only cross-segment token: it tries to swallow zero or more segments, so it
// recurses over every suffix of seg for the remaining pattern. Every other
// segment matches exactly one path segment via filepath.Match.
func matchSegs(pat, seg []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			rest := pat[1:]
			if len(rest) == 0 {
				return true // trailing ** matches the rest (including nothing)
			}
			for i := 0; i <= len(seg); i++ {
				if matchSegs(rest, seg[i:]) {
					return true
				}
			}
			return false
		}
		if len(seg) == 0 {
			return false
		}
		ok, err := filepath.Match(pat[0], seg[0])
		if err != nil || !ok {
			return false
		}
		pat, seg = pat[1:], seg[1:]
	}
	return len(seg) == 0
}
