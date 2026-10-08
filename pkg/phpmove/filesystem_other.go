//go:build !linux

package phpmove

import "os"

func validateFilesystem(_ *os.Root, _ *Plan) error { return nil }
