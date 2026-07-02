package main

import (
	"fmt"
	"os"

	"github.com/sofia-ctx/sofia/internal/calllog"
	"github.com/sofia-ctx/sofia/internal/cli"
)

func main() {
	// Graft discovered plugins onto the command tree before dispatch. Reads
	// the cached metadata index, so this doesn't fork any plugin.
	cli.AttachPlugins()
	if err := calllog.Run(cli.RootCmd, ""); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
