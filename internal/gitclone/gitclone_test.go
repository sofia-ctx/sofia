package gitclone

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sofia-ctx/sofia/internal/gitexec"
)

func TestIsURL(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"https://github.com/o/r.git", true},
		{"http://github.com/o/r.git", true},
		{"ssh://git@github.com/o/r.git", true},
		{"git://github.com/o/r.git", true},
		{"file:///tmp/r", true},
		{"git@github.com:o/r.git", true},
		{"./dir", false},
		{"/abs", false},
		{"../x", false},
		{"name", false},
	}
	for _, c := range cases {
		if got := IsURL(c.in); got != c.want {
			t.Errorf("IsURL(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestRepoName(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"https://github.com/o/repo.git", "repo", false},
		{"https://github.com/o/repo", "repo", false},
		{"https://github.com/o/repo/", "repo", false},
		{"git@github.com:o/repo.git", "repo", false},
		{"file:///tmp/repo", "repo", false},
		{"", "", true},
	}
	for _, c := range cases {
		got, err := RepoName(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("RepoName(%q) = %q, want error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("RepoName(%q): unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("RepoName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// requireGit skips the test when git isn't on PATH — these tests exercise
// real clones, not a mock.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
}

// initRepo creates a real git repo in t.TempDir() with one committed file,
// returning its path.
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "--quiet")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "file.txt")
	runGit(t, dir, "commit", "--quiet", "-m", "init")
	return dir
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	if _, err := gitexec.Run(dir, args...); err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
}

func TestCloneShallow(t *testing.T) {
	requireGit(t)
	src := initRepo(t)
	dst := filepath.Join(t.TempDir(), "clone")

	commit, err := CloneShallow("file://"+src, "", dst)
	if err != nil {
		t.Fatalf("CloneShallow: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "file.txt")); err != nil {
		t.Errorf("cloned tree missing file.txt: %v", err)
	}
	want, err := gitexec.Run(dst, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if commit != strings.TrimSpace(want) {
		t.Errorf("commit = %q, want %q", commit, strings.TrimSpace(want))
	}
}

func TestCloneShallow_Ref(t *testing.T) {
	requireGit(t)
	src := initRepo(t)
	runGit(t, src, "tag", "v1")
	tagCommit, err := gitexec.Run(src, "rev-parse", "v1")
	if err != nil {
		t.Fatal(err)
	}
	tagCommit = strings.TrimSpace(tagCommit)

	runGit(t, src, "checkout", "--quiet", "-b", "feature")
	if err := os.WriteFile(filepath.Join(src, "feature.txt"), []byte("feat\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, src, "add", "feature.txt")
	runGit(t, src, "commit", "--quiet", "-m", "feature")
	branchCommit, err := gitexec.Run(src, "rev-parse", "feature")
	if err != nil {
		t.Fatal(err)
	}
	branchCommit = strings.TrimSpace(branchCommit)

	t.Run("tag", func(t *testing.T) {
		dst := filepath.Join(t.TempDir(), "clone-tag")
		commit, err := CloneShallow("file://"+src, "v1", dst)
		if err != nil {
			t.Fatalf("CloneShallow: %v", err)
		}
		if commit != tagCommit {
			t.Errorf("commit = %q, want tag commit %q", commit, tagCommit)
		}
		if _, err := os.Stat(filepath.Join(dst, "feature.txt")); err == nil {
			t.Error("tag clone should not contain feature.txt")
		}
	})

	t.Run("branch", func(t *testing.T) {
		dst := filepath.Join(t.TempDir(), "clone-branch")
		commit, err := CloneShallow("file://"+src, "feature", dst)
		if err != nil {
			t.Fatalf("CloneShallow: %v", err)
		}
		if commit != branchCommit {
			t.Errorf("commit = %q, want branch commit %q", commit, branchCommit)
		}
		if _, err := os.Stat(filepath.Join(dst, "feature.txt")); err != nil {
			t.Errorf("branch clone missing feature.txt: %v", err)
		}
	})
}

func TestCloneShallow_BadURL(t *testing.T) {
	requireGit(t)
	dst := filepath.Join(t.TempDir(), "clone")
	_, err := CloneShallow(filepath.Join(t.TempDir(), "does-not-exist"), "", dst)
	if err == nil {
		t.Fatal("expected an error cloning a nonexistent repo")
	}
	if !strings.Contains(err.Error(), "fatal") {
		t.Errorf("error should carry git's own stderr: %v", err)
	}
}
