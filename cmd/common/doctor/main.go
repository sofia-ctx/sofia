// Standalone doctor binary. Wraps the same Cobra command the master `sf`
// CLI registers under `sf doctor`.
package main

import (
	"fmt"
	"os"

	"github.com/sofia-ctx/sofia/internal/calllog"
	"github.com/sofia-ctx/sofia/internal/common/doctor"
)

func main() {
	if err := calllog.Run(doctor.NewCommand(), "doctor"); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
