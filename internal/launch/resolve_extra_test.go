package launch

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// isolateAliases points the alias store at an empty temp file so a developer's
// real ~/.config/sofia/projects.yaml can't influence resolution under test.
func isolateAliases(t *testing.T) {
	t.Helper()
	t.Setenv("SF_CLAUDE_ALIASES", filepath.Join(t.TempDir(), "aliases.yaml"))
}

func TestResolveDirAliasWins(t *testing.T) {
	isolateAliases(t)
	// A saved alias resolves regardless of $SF_CLAUDE_DIR and cwd.
	proj := t.TempDir()
	if err := SaveAlias("myproj", proj); err != nil {
		t.Fatalf("SaveAlias: %v", err)
	}
	t.Setenv("SF_CLAUDE_DIR", "")
	t.Chdir(t.TempDir()) // a clean, unrelated cwd

	got, err := ResolveDir("", "myproj")
	if err != nil {
		t.Fatalf("ResolveDir: %v", err)
	}
	if want, _ := filepath.Abs(proj); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveDirStaleAliasIgnored(t *testing.T) {
	isolateAliases(t)
	// An alias whose dir no longer exists is skipped, not fatal — resolution
	// falls through to the other sources (here: a ./myproj child).
	gone := filepath.Join(t.TempDir(), "removed")
	if err := SaveAlias("myproj", gone); err != nil {
		t.Fatalf("SaveAlias: %v", err)
	}
	cwd := t.TempDir()
	child := filepath.Join(cwd, "myproj")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SF_CLAUDE_DIR", "")
	t.Chdir(cwd)

	got, err := ResolveDir("", "myproj")
	if err != nil {
		t.Fatalf("ResolveDir: %v", err)
	}
	if want, _ := filepath.Abs(child); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveDirSFClaudeDirMissFallsThrough(t *testing.T) {
	isolateAliases(t)
	// $SF_CLAUDE_DIR is set but has no <project> child; resolution must fall
	// through to ./<project> instead of hard-erroring "not a directory" —
	// the reported `sf claude packages` case.
	root := t.TempDir() // projects root, no "myproj" under it
	cwd := t.TempDir()
	child := filepath.Join(cwd, "myproj")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SF_CLAUDE_DIR", root)
	t.Chdir(cwd)

	got, err := ResolveDir("", "myproj")
	if err != nil {
		t.Fatalf("ResolveDir: %v", err)
	}
	if want, _ := filepath.Abs(child); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveDirNotFoundType(t *testing.T) {
	isolateAliases(t)
	root := t.TempDir() // set but empty
	t.Setenv("SF_CLAUDE_DIR", root)
	t.Chdir(t.TempDir()) // clean cwd, no ./ghost or ../ghost

	_, err := ResolveDir("", "ghost")
	var nf *NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("want *NotFoundError, got %T: %v", err, err)
	}
	if nf.Name != "ghost" || !strings.Contains(err.Error(), "--dir") {
		t.Errorf("unhelpful NotFoundError: %v", err)
	}
}

func TestSearchProjectsFindsAndFilters(t *testing.T) {
	// Layout under the projects root:
	//   a/pkg          (depth 2 — found)
	//   b/pkg          (depth 2 — found)
	//   c/config/pkg   (depth 3 — beyond default depth, skipped)
	//   node_modules/pkg (a skipped dir — not descended)
	root := t.TempDir()
	for _, p := range []string{
		filepath.Join("a", "pkg"),
		filepath.Join("b", "pkg"),
		filepath.Join("c", "config", "pkg"),
		filepath.Join("node_modules", "pkg"),
	} {
		if err := os.MkdirAll(filepath.Join(root, p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("SF_CLAUDE_DIR", root)
	t.Setenv("SF_CLAUDE_SEARCH_DEPTH", "") // default (2)
	t.Chdir(t.TempDir())                   // clean cwd adds nothing

	got := SearchProjects("pkg")
	want := []string{filepath.Join(root, "a", "pkg"), filepath.Join(root, "b", "pkg")}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SearchProjects:\n got %q\nwant %q", got, want)
	}
}

func TestSearchProjectsDepthOverride(t *testing.T) {
	// With a deeper cap the depth-3 match becomes visible.
	root := t.TempDir()
	deep := filepath.Join(root, "c", "sub", "pkg")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SF_CLAUDE_DIR", root)
	t.Setenv("SF_CLAUDE_SEARCH_DEPTH", "3")
	t.Chdir(t.TempDir())

	got := SearchProjects("pkg")
	if len(got) != 1 || got[0] != deep {
		t.Errorf("depth-3 search got %q, want [%q]", got, deep)
	}
}

func TestPromptPick(t *testing.T) {
	cands := []string{"/a/pkg", "/b/pkg"}
	orig := pickFrom
	defer func() { pickFrom = orig }()

	pickFrom = strings.NewReader("2\n")
	if got, err := promptPick("pkg", cands, io.Discard); err != nil || got != "/b/pkg" {
		t.Fatalf("valid pick: got %q, err %v", got, err)
	}
	// Immediate EOF (non-interactive stdin) → an actionable --dir hint.
	pickFrom = strings.NewReader("")
	if _, err := promptPick("pkg", cands, io.Discard); err == nil || !strings.Contains(err.Error(), "--dir") {
		t.Errorf("EOF should hint --dir, got %v", err)
	}
	pickFrom = strings.NewReader("9\n")
	if _, err := promptPick("pkg", cands, io.Discard); err == nil {
		t.Errorf("out-of-range selection should error")
	}
	pickFrom = strings.NewReader("q\n")
	if _, err := promptPick("pkg", cands, io.Discard); err == nil {
		t.Errorf("q should cancel")
	}
}

func TestSaveAndLoadAlias(t *testing.T) {
	isolateAliases(t)
	proj := t.TempDir()
	if err := SaveAlias("thing", proj); err != nil {
		t.Fatalf("SaveAlias: %v", err)
	}
	m := loadAliases()
	if want, _ := filepath.Abs(proj); m["thing"] != want {
		t.Errorf("loadAliases[thing] = %q, want %q", m["thing"], want)
	}
	// Idempotent: saving the same mapping again is a no-op, not an error.
	if err := SaveAlias("thing", proj); err != nil {
		t.Errorf("re-save: %v", err)
	}
}
