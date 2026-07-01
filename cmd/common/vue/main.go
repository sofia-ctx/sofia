// Standalone vue binary. Wraps the same Cobra command the master `sf` CLI
// registers under `sf vue`.
package main

import (
	"fmt"
	"os"

	"github.com/sofia-ctx/sofia/internal/calllog"
	"github.com/sofia-ctx/sofia/internal/common/vue"
)

func main() {
	if err := calllog.Run(vue.NewCommand(), "vue"); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
