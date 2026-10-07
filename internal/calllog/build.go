package calllog

import (
	"runtime/debug"
	"strings"
)

// Build identity is recorded once per process without running Git.
func buildIdentity() string {
	b, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	v := b.Main.Version
	if v == "" || v == "(devel)" {
		v = "dev"
	}
	var revision string
	dirty := false
	for _, s := range b.Settings {
		if s.Key == "vcs.revision" {
			revision = s.Value
		}
		if s.Key == "vcs.modified" && s.Value == "true" {
			dirty = true
		}
	}
	if len(revision) > 12 {
		revision = revision[:12]
	}
	if revision != "" {
		v += "+" + revision
	}
	if dirty {
		v += ".dirty"
	}
	return strings.TrimSpace(v)
}
