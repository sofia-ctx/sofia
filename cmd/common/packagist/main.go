// Standalone packagist binary. Wraps the same Cobra command the master `sf`
// CLI registers under `sf packagist`.
package main

import (
	"fmt"
	"os"

	"github.com/sofia-ctx/sofia/internal/calllog"
	"github.com/sofia-ctx/sofia/internal/common/packagist"
)

func main() {
	if err := calllog.Run(packagist.NewCommand(), "packagist"); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
