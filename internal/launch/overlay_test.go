package launch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolateOverlays points the overlays root at an empty temp dir so a
// developer's real ~/.local/share/sofia/overlays can't influence resolution
// under test.
func isolateOverlays(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "overlays")
	t.Setenv("SF_CLAUDE_OVERLAY_DIR", root)
	return root
}

// writeOverlay creates <root>/<repo>/<tag>/AGENTS.md with body.
func writeOverlay(t *testing.T, root, repo, tag, body string) string {
	t.Helper()
	dir := filepath.Join(root, repo, tag)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, overlayFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestResolveOverlayFound(t *testing.T) {
	root := isolateOverlays(t)
	want := writeOverlay(t, root, "myrepo", "packages", "rules")

	dir, file, ok := resolveOverlay("packages")
	if !ok {
		t.Fatal("expected an overlay match for packages")
	}
	if dir != want {
		t.Errorf("dir = %q, want %q", dir, want)
	}
	if file != filepath.Join(want, overlayFile) {
		t.Errorf("file = %q, want %q", file, filepath.Join(want, overlayFile))
	}
}

func TestResolveOverlayMissing(t *testing.T) {
	root := isolateOverlays(t)
	writeOverlay(t, root, "myrepo", "packages", "rules")

	// A repo without a matching tag, and a plain name, both resolve nowhere.
	if _, _, ok := resolveOverlay("other"); ok {
		t.Error("unrelated tag should not match")
	}
	if _, _, ok := resolveOverlay(""); ok {
		t.Error("empty name should not match")
	}
}

func TestResolveOverlayDeterministicOnCollision(t *testing.T) {
	root := isolateOverlays(t)
	// Two repos define the same tag; the alphabetically-first repo wins,
	// deterministically (os.ReadDir sorts).
	first := writeOverlay(t, root, "aaa", "packages", "from-aaa")
	writeOverlay(t, root, "bbb", "packages", "from-bbb")

	dir, _, ok := resolveOverlay("packages")
	if !ok || dir != first {
		t.Errorf("collision should pick %q, got %q (ok=%v)", first, dir, ok)
	}
}

func TestBaseArgsWithOverlay(t *testing.T) {
	root := isolateOverlays(t)
	dir := writeOverlay(t, root, "myrepo", "myproj", "personal rules here")
	target := Target{Name: "myproj", Dir: "/w/myproj"}

	ia := InteractiveArgs(target, Options{})
	js := strings.Join(ia, " ")
	for _, want := range []string{
		"--add-dir /w/myproj",
		"--add-dir " + dir,
		"--append-system-prompt",
		"Personal project overlay (authoritative)",
		"personal rules here",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("args missing %q in: %s", want, js)
		}
	}
}

func TestBaseArgsOverlayDisabled(t *testing.T) {
	root := isolateOverlays(t)
	writeOverlay(t, root, "myrepo", "myproj", "personal rules here")
	target := Target{Name: "myproj", Dir: "/w/myproj"}

	js := strings.Join(InteractiveArgs(target, Options{NoOverlay: true}), " ")
	if strings.Contains(js, "--append-system-prompt") || strings.Contains(js, root) {
		t.Errorf("--no-overlay should inject nothing: %s", js)
	}
}

func TestBaseArgsPromptFileAndOverlayCombine(t *testing.T) {
	root := isolateOverlays(t)
	writeOverlay(t, root, "myrepo", "myproj", "overlay body")
	prompt := filepath.Join(t.TempDir(), "prompt.md")
	if err := os.WriteFile(prompt, []byte("env body"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SF_CLAUDE_PROMPT_FILE", prompt)
	target := Target{Name: "myproj", Dir: "/w/myproj"}

	ia := InteractiveArgs(target, Options{})
	// Exactly one --append-system-prompt, carrying both bodies in order.
	var got string
	for i, a := range ia {
		if a == "--append-system-prompt" {
			if got != "" {
				t.Fatal("expected a single --append-system-prompt")
			}
			got = ia[i+1]
		}
	}
	if !strings.Contains(got, "env body") || !strings.Contains(got, "overlay body") {
		t.Errorf("combined prompt missing a source: %q", got)
	}
	if strings.Index(got, "env body") > strings.Index(got, "overlay body") {
		t.Errorf("env prompt should come before the overlay: %q", got)
	}
}

func TestOverlayRepoName(t *testing.T) {
	cases := map[string]string{
		"git@github.com:sofia-ctx/overlays.git":     "overlays",
		"https://github.com/sofia-ctx/overlays.git": "overlays",
		"https://github.com/sofia-ctx/overlays":     "overlays",
		"git@github.com:sofia-ctx/overlays.git/":    "overlays",
	}
	for in, want := range cases {
		if got := overlayRepoName(in); got != want {
			t.Errorf("overlayRepoName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOverlayTags(t *testing.T) {
	root := isolateOverlays(t)
	writeOverlay(t, root, "myrepo", "packages", "x")
	writeOverlay(t, root, "myrepo", "xcraft", "y")
	// A dir without AGENTS.md is not a tag.
	if err := os.MkdirAll(filepath.Join(root, "myrepo", "notes"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := overlayTags(filepath.Join(root, "myrepo"))
	want := []string{"packages", "xcraft"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("overlayTags = %v, want %v", got, want)
	}
}
