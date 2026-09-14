package main

import (
	"errors"
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
		if !alreadyReported(err) {
			fmt.Fprintln(os.Stderr, "error:", err)
		}
		os.Exit(statusCode(err))
	}
}

type exitCoder interface{ ExitCode() int }
type reportedError interface{ AlreadyReported() bool }

func alreadyReported(err error) bool {
	var reported reportedError
	return errors.As(err, &reported) && reported.AlreadyReported()
}

func statusCode(err error) int {
	var coded exitCoder
	if errors.As(err, &coded) {
		code := coded.ExitCode()
		if code > 0 && code <= 255 {
			return code
		}
	}
	return 1
}
