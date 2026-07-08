package adapter

import (
	"fmt"
	"path/filepath"
	"strings"
)

// safeRel rejects a config path that could escape its project root: an absolute
// path, or one whose cleaned form still climbs above its start (a leading
// ".."). Every root marker and layer glob in an adapter block goes through this
// before it's joined to a real directory or matched against a real path.
//
// This is a deliberate copy of pack.safeRel (internal/pack/manifest.go): the
// two guard the same class of manifest-supplied path, but the adapter package
// must not import internal/pack, and a ten-line rule isn't worth a shared
// package with its own import surface. Keep the two in sync if the rule changes.
func safeRel(p string) error {
	if p == "" {
		return fmt.Errorf("path is empty")
	}
	if filepath.IsAbs(p) {
		return fmt.Errorf("%q: must be relative", p)
	}
	clean := filepath.Clean(p)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%q: escapes its root", p)
	}
	return nil
}
