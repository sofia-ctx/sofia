// Standalone grep binary. Wraps the same Cobra command that the master
// `sf` CLI registers under `sf grep`, so flags and args behave identically
// regardless of how the tool is invoked.
package main

import (
	"fmt"
	"os"

	"github.com/sofia-ctx/sofia/internal/calllog"
	"github.com/sofia-ctx/sofia/internal/common/grep"
)

func main() {
	if err := calllog.Run(grep.NewCommand(), "grep"); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
