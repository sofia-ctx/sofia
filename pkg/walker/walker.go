// Package walker enumerates files in a directory tree, applying include/exclude rules.
package walker

import (
	"io/fs"
	"path/filepath"
	"strings"
)

type Options struct {
	Root       string
	IgnoreDirs map[string]bool
	IgnoreRels map[string]bool
	Exts       map[string]bool
}

// Files walks Root and emits absolute paths of files that pass the filters.
// Errors during walk are sent on the returned error channel and the producer
// closes both channels when done.
func Files(opts Options) (<-chan string, <-chan error) {
	out := make(chan string, 64)
	errs := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errs)
		err := filepath.WalkDir(opts.Root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			rel, _ := filepath.Rel(opts.Root, path)
			rel = filepath.ToSlash(rel)
			if d.IsDir() {
				if opts.IgnoreDirs[d.Name()] {
					return filepath.SkipDir
				}
				if opts.IgnoreRels[rel] {
					return filepath.SkipDir
				}
				return nil
			}
			if len(opts.Exts) > 0 {
				ext := strings.ToLower(filepath.Ext(path))
				if !opts.Exts[ext] {
					return nil
				}
			}
			out <- path
			return nil
		})
		if err != nil {
			errs <- err
		}
	}()
	return out, errs
}
