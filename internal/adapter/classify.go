package adapter

import (
	"path/filepath"
	"strings"
)

// Classify returns the name of the first layer whose any Match glob hits the
// given path, or "" when no layer claims it. The path is taken relative to the
// resolved project root; it is normalized to forward slashes and stripped of a
// leading "./" so a caller can pass either an OS path or a slash path. Layers
// are tried in declared order, so an adapter author controls precedence by
// ordering the block — and the classification is byte-stable.
func Classify(cfg Config, rel string) string {
	rel = strings.TrimPrefix(filepath.ToSlash(rel), "./")
	for _, l := range cfg.Layers {
		for _, g := range l.Match {
			if Match(g, rel) {
				return l.Name
			}
		}
	}
	return ""
}
