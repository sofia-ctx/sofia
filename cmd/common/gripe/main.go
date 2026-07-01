// Standalone gripe binary. Wraps the same Cobra command the master `sf`
// CLI registers under `sf gripe`.
package main

import (
	"fmt"
	"os"

	"github.com/sofia-ctx/sofia/internal/calllog"
	"github.com/sofia-ctx/sofia/internal/common/gripe"
)

func main() {
	if err := calllog.Run(gripe.NewCommand(), "gripe"); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
