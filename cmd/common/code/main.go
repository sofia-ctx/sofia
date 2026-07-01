// Standalone code binary. Wraps the same Cobra command the master `sf` CLI
// registers under `sf code`.
package main

import (
	"fmt"
	"os"

	"github.com/sofia-ctx/sofia/internal/calllog"
	"github.com/sofia-ctx/sofia/internal/common/code"
)

func main() {
	if err := calllog.Run(code.NewCommand(), "code"); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
