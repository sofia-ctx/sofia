// Package gitclone is the first git-clone plumbing in this repo: a small
// shallow-clone helper for `sf plugin install <git-url>` (and, later, `sf pack
// install`). It shells out to the user's own `git`, so authentication is
// ambient — whatever ssh-agent, a credential helper, or ~/.netrc already
// trusts — sofia never reads, stores, or asks for a token itself.
package gitclone

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// IsURL reports whether s names a git remote rather than a local path, by
// prefix alone (no regex, no network round-trip). Anything else — including
// relative and absolute filesystem paths — is treated as a local directory by
// the caller.
func IsURL(s string) bool {
	for _, prefix := range []string{"https://", "http://", "ssh://", "git://", "file://", "git@"} {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

// RepoName derives a plugin/pack name from a git URL: the last path segment,
// with a trailing ".git" and any trailing slash stripped. It understands both
// URL form (scheme://host/path) and the scp-like shorthand git@host:path.
func RepoName(rawurl string) (string, error) {
	s := strings.TrimRight(rawurl, "/")
	if s == "" {
		return "", fmt.Errorf("cannot derive a repo name from %q", rawurl)
	}
	// scp-like shorthand (git@host:path) has no "://" — the part after the
	// last ":" is the path, not a port.
	if !strings.Contains(s, "://") {
		if i := strings.LastIndexByte(s, ':'); i >= 0 {
			s = s[i+1:]
		}
	}
	name := s
	if i := strings.LastIndexByte(s, '/'); i >= 0 {
		name = s[i+1:]
	}
	name = strings.TrimSuffix(name, ".git")
	if name == "" {
		return "", fmt.Errorf("cannot derive a repo name from %q", rawurl)
	}
	return name, nil
}

// CloneShallow clones url at ref into dst (which must not already exist) and
// returns the commit it landed on. ref is a branch or tag name; "" clones the
// remote's default branch. Commit SHAs are not supported — `git clone
// --branch` doesn't accept one, and resolving one would need a full clone.
func CloneShallow(url, ref, dst string) (string, error) {
	args := []string{"clone", "--depth", "1", "--quiet"}
	if ref != "" {
		args = append(args, "--branch", ref)
	}
	args = append(args, "--", url, dst)
	if _, err := git("", args...); err != nil {
		return "", err
	}
	out, err := git(dst, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// git runs a git subcommand and returns stdout, wrapping any failure with
// git's own stderr (so the user sees git's auth/404 message, not just an exit
// code). Mirrors changed.git (internal/common/changed/changed.go).
func git(root string, args ...string) (string, error) {
	full := args
	if root != "" {
		full = append([]string{"-C", root}, args...)
	}
	cmd := exec.Command("git", full...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}
