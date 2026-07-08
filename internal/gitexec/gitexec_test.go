package gitexec

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

// initRepo creates a throwaway git repo with one commit and returns its dir
// and the commit's full SHA.
func initRepo(t *testing.T) (dir, sha string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir = t.TempDir()
	run := func(args ...string) {
		t.Helper()
		if _, err := Run(dir, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	run("init", "--quiet")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")
	run("commit", "--allow-empty", "--quiet", "-m", "initial")

	out, err := Run(dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}
	return dir, strings.TrimSpace(out)
}

func TestRun_ReturnsCommit(t *testing.T) {
	dir, sha := initRepo(t)
	out, err := Run(dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.TrimSpace(out) != sha {
		t.Fatalf("got %q, want %q", strings.TrimSpace(out), sha)
	}
}

func TestRun_EmptyRootOmitsDashC(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	out, err := Run("", "--version")
	if err != nil {
		t.Fatalf("Run with empty root: %v", err)
	}
	if !strings.Contains(out, "git version") {
		t.Fatalf("got %q, want it to contain \"git version\"", out)
	}
}

func TestRun_FailureIncludesStderr(t *testing.T) {
	dir, _ := initRepo(t)
	_, err := Run(dir, "show", "not-a-real-ref")
	if err == nil {
		t.Fatal("want an error for a nonexistent ref")
	}
	if !strings.Contains(err.Error(), "not-a-real-ref") {
		t.Fatalf("error %q does not surface git's stderr", err)
	}
}

func TestRunCtx_CancelledContextErrors(t *testing.T) {
	dir, _ := initRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := RunCtx(ctx, dir, "status"); err == nil {
		t.Fatal("want an error for an already-cancelled context")
	}
}
