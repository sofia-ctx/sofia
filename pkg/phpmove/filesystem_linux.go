package phpmove

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"syscall"
)

// Staging, atomic replacements and rollback all require the journal filesystem.
// Check existing source files and destination ancestors before any source writes.
func validateFilesystem(root *os.Root, plan *Plan) error {
	base, err := root.Stat(".")
	if err != nil {
		return err
	}
	baseStat, ok := base.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("cannot determine journal filesystem")
	}
	check := func(name string) error {
		for {
			info, err := root.Stat(name)
			if errors.Is(err, fs.ErrNotExist) && name != "." {
				name = path.Dir(name)
				continue
			}
			if err != nil {
				return err
			}
			stat, ok := info.Sys().(*syscall.Stat_t)
			if !ok || stat.Dev != baseStat.Dev {
				return fmt.Errorf("cross-filesystem moves are unsupported: %s", name)
			}
			return nil
		}
	}
	for _, change := range plan.Changes {
		if err := check(change.From); err != nil {
			return err
		}
		if err := check(path.Dir(change.To)); err != nil {
			return err
		}
	}
	for _, dir := range plan.Directories {
		if err := check(dir.From); err != nil {
			return err
		}
		if err := check(path.Dir(dir.To)); err != nil {
			return err
		}
	}
	return check(".sf-move")
}
