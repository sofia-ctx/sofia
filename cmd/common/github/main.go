// Standalone github binary. Wraps the same Cobra command the master `sf`
// CLI registers under `sf github`.
package main

import (
	"fmt"
	"os"

	"github.com/sofia-ctx/sofia/internal/calllog"
	"github.com/sofia-ctx/sofia/internal/common/github"
)

func main() {
	if err := calllog.Run(github.NewCommand(), "github"); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
