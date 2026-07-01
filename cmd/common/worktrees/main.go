// Standalone worktrees binary. Wraps the same Cobra command the master `sf`
// CLI registers under `sf worktrees`.
package main

import (
	"fmt"
	"os"

	"github.com/sofia-ctx/sofia/internal/calllog"
	"github.com/sofia-ctx/sofia/internal/common/worktrees"
)

func main() {
	if err := calllog.Run(worktrees.NewCommand(), "worktrees"); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
