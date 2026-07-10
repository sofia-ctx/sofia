//go:build windows

package launch

import "errors"

// interactiveExec has no windows equivalent of unix's exec(2) process-image
// replacement (syscall.Exec is unix-only), so interactive `sf claude` isn't
// supported there yet — use --task instead, which runs claude as a normal
// child process via os/exec and works on every platform.
func interactiveExec(_ string, _, _ []string) error {
	return errors.New("interactive launch unsupported on windows; use --task")
}
