// Package gitexec is the one place in the repo that shells out to `git`.
// Every caller used to carry its own copy of this ~15-line helper (changed,
// worktrees, doctor, gitclone, packagist, composer); this collapses them into
// one leaf package (stdlib only, so nothing can end up depending on it
// cyclically) that they all import instead.
package gitexec

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Run runs `git [-C root] args...` and returns raw, untrimmed stdout. root
// is omitted from the command line (no `-C`) when empty — load-bearing for
// gitclone's `clone`, which has no directory to `-C` into yet. On failure the
// returned error wraps git's own stderr, so callers surface git's actual
// complaint (auth, 404, not-a-repo) rather than a bare exit status.
func Run(root string, args ...string) (string, error) {
	return RunCtx(context.Background(), root, args...)
}

// RunCtx is Run with a caller-supplied context and GIT_TERMINAL_PROMPT=0, so
// a stale credential or unreachable remote fails fast instead of blocking on
// a prompt nothing will ever answer.
func RunCtx(ctx context.Context, root string, args ...string) (string, error) {
	full := args
	if root != "" {
		full = append([]string{"-C", root}, args...)
	}
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = append(cmd.Environ(), "GIT_TERMINAL_PROMPT=0")
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}
