package adapter

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ResolveRoot finds the project root an adapter's commands operate on, trying,
// in order:
//
//  1. $<RootKey> — when the config names a root key and that environment
//     variable is set and points at an existing directory, it wins outright.
//     This is the explicit escape hatch (e.g. APP_ROOT) for working outside the
//     tree, or from a checkout whose markers live elsewhere.
//  2. a walk up from startDir to the nearest ancestor containing any RootMarker
//     — the ordinary case, modelled on phpcode.loadComposerPSR4 and
//     plugin.projectRoot.
//
// It returns an error naming the markers when neither locates a root, so the
// synthesized command can say precisely why it couldn't run. The command layer
// adds the higher-precedence explicit `--root` on top of this.
func ResolveRoot(cfg Config, startDir string) (string, error) {
	if cfg.RootKey != "" {
		if v := os.Getenv(cfg.RootKey); v != "" {
			if info, err := os.Stat(v); err == nil && info.IsDir() {
				return v, nil
			}
		}
	}
	if root, ok := walkUpForMarker(cfg.RootMarkers, startDir); ok {
		return root, nil
	}
	return "", fmt.Errorf("no project root found: none of %s in %s or its parents (set %s or pass --root)",
		strings.Join(cfg.RootMarkers, ", "), startDir, markerHint(cfg.RootKey))
}

// walkUpForMarker climbs from startDir toward the filesystem root, returning the
// first directory that directly contains any of the markers.
func walkUpForMarker(markers []string, startDir string) (string, bool) {
	for dir := startDir; ; {
		for _, m := range markers {
			if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(m))); err == nil {
				return dir, true
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// markerHint names the root-key env var in the not-found error, or a neutral
// phrase when the adapter declared no root key.
func markerHint(rootKey string) string {
	if rootKey == "" {
		return "a root key"
	}
	return "$" + rootKey
}
