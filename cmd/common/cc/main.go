// Standalone cc binary. Wraps the same Cobra group the master `sf` CLI
// registers under `sf cc` (subcommands: ls, show).
package main

import (
	"fmt"
	"os"

	"github.com/sofia-ctx/sofia/internal/calllog"
	"github.com/sofia-ctx/sofia/internal/cc"
)

func main() {
	if err := calllog.Run(cc.NewCommand(), "cc"); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
