//go:build !windows

package launch

import "syscall"

// interactiveExec replaces the current process image with bin/argv,
// handing the terminal to claude directly (no wrapper process, no lost
// signals/job control).
func interactiveExec(bin string, argv, env []string) error {
	return syscall.Exec(bin, argv, env)
}
