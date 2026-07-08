package adapter

import (
	"os"
	"path/filepath"
	"testing"
)

// writeMarker creates dir and drops an empty marker file in it.
func writeMarker(t *testing.T, dir, marker string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, marker), nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolveRoot_WalkUpMarker(t *testing.T) {
	root := t.TempDir()
	writeMarker(t, root, "composer.json")
	start := filepath.Join(root, "src", "Domain")
	if err := os.MkdirAll(start, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := Config{RootMarkers: []string{"composer.json"}}
	got, err := ResolveRoot(cfg, start)
	if err != nil {
		t.Fatalf("ResolveRoot: %v", err)
	}
	if got != root {
		t.Errorf("walk-up root = %q, want %q", got, root)
	}
}

func TestResolveRoot_EnvPin(t *testing.T) {
	pinned := t.TempDir() // no marker here at all — the env var alone pins it
	other := t.TempDir()

	t.Setenv("APP_ROOT", pinned)
	cfg := Config{RootKey: "APP_ROOT", RootMarkers: []string{"composer.json"}}
	got, err := ResolveRoot(cfg, other)
	if err != nil {
		t.Fatalf("ResolveRoot: %v", err)
	}
	if got != pinned {
		t.Errorf("env-pinned root = %q, want %q", got, pinned)
	}
}

func TestResolveRoot_EnvBeatsWalkUp(t *testing.T) {
	pinned := t.TempDir()
	walkup := t.TempDir()
	writeMarker(t, walkup, "composer.json")

	t.Setenv("APP_ROOT", pinned)
	cfg := Config{RootKey: "APP_ROOT", RootMarkers: []string{"composer.json"}}
	got, err := ResolveRoot(cfg, walkup)
	if err != nil {
		t.Fatalf("ResolveRoot: %v", err)
	}
	if got != pinned {
		t.Errorf("env should beat walk-up: got %q, want %q", got, pinned)
	}
}

// An env var that is unset or names a non-directory falls through to the walk-up.
func TestResolveRoot_EnvIgnoredWhenInvalid(t *testing.T) {
	walkup := t.TempDir()
	writeMarker(t, walkup, "composer.json")

	t.Setenv("APP_ROOT", filepath.Join(walkup, "composer.json")) // a file, not a dir
	cfg := Config{RootKey: "APP_ROOT", RootMarkers: []string{"composer.json"}}
	got, err := ResolveRoot(cfg, walkup)
	if err != nil {
		t.Fatalf("ResolveRoot: %v", err)
	}
	if got != walkup {
		t.Errorf("invalid env should fall through to walk-up: got %q, want %q", got, walkup)
	}
}

func TestResolveRoot_NotFound(t *testing.T) {
	start := t.TempDir() // no marker anywhere up the tree we create
	cfg := Config{RootKey: "APP_ROOT", RootMarkers: []string{"composer.json"}}
	_, err := ResolveRoot(cfg, start)
	if err == nil {
		t.Fatal("expected a not-found error when no marker is present")
	}
}
