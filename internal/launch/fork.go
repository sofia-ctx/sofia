package launch

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ForkSlug normalizes a fork selector for `sf claude <project> <sel>`: a bare
// number N becomes "sN" (the s1..sN convention); any other token is used
// verbatim.
func ForkSlug(sel string) string {
	if isAllDigits(sel) {
		return "s" + sel
	}
	return sel
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// ResolveFork redirects a launch into an isolated worktree copy of the
// project, via the project's own dev/worktree.sh (that script owns the fork
// scheme — paths, ports, stack; the launcher only orchestrates it). Errors
// clearly if the project doesn't ship one. dryRun only resolves the fork's
// dir without creating or starting anything.
func ResolveFork(projectDir, sel string, dryRun bool) (string, error) {
	script := filepath.Join(projectDir, "dev", "worktree.sh")
	if !fileExists(script) {
		return "", fmt.Errorf("forks need a dev/worktree.sh in the project (missing: %s)", script)
	}
	slug := ForkSlug(sel)

	dirCmd := exec.Command(script, "dir", slug)
	dirCmd.Dir = projectDir
	dirCmd.Stderr = os.Stderr
	out, err := dirCmd.Output()
	if err != nil {
		return "", fmt.Errorf("worktree.sh dir %s: %w", slug, err)
	}
	dir := strings.TrimSpace(string(out))
	if dryRun {
		return dir, nil
	}

	action := "up"
	if !dirExists(dir) {
		action = "new"
	}
	cmd := exec.Command(script, action, slug)
	cmd.Dir = projectDir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("worktree.sh %s %s: %w", action, slug, err)
	}
	return dir, nil
}
