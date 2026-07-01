// Standalone composer binary. Wraps the same Cobra command the master `sf`
// CLI registers under `sf composer`.
package main

import (
	"fmt"
	"os"

	"github.com/sofia-ctx/sofia/internal/calllog"
	"github.com/sofia-ctx/sofia/internal/common/composer"
)

func main() {
	if err := calllog.Run(composer.NewCommand(), "composer"); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
