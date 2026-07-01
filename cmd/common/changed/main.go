// Standalone changed binary. Wraps the same Cobra command the master `sf`
// CLI registers under `sf changed`.
package main

import (
	"fmt"
	"os"

	"github.com/sofia-ctx/sofia/internal/calllog"
	"github.com/sofia-ctx/sofia/internal/common/changed"
)

func main() {
	if err := calllog.Run(changed.NewCommand(), "changed"); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
