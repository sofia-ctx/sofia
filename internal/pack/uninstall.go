package pack

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/sofia-ctx/sofia/internal/plugin"
)

// UninstallResult reports what Uninstall did beyond the happy path: files it
// left in place because they'd been edited, and whether the pack's global
// footprint (claude files, plugins, canon copy, receipt) was torn down too.
type UninstallResult struct {
	Warnings []string
	Global   bool
}

// Uninstall removes name's footprint from project (project defaults to cwd
// when empty). Per file the receipt recorded there: a matching sha256 means
// it's untouched, so it's removed (and its now-empty parent directories
// pruned up to the project root); a mismatch means it was edited since
// install, so it's left in place with a warning; already-missing is a silent
// skip. Once no project references the pack any more, its global footprint is
// torn down the same way; while other projects still reference it, the
// globals and the receipt are left as they are.
func Uninstall(name, project string) (UninstallResult, error) {
	project, err := resolveProject(project)
	if err != nil {
		return UninstallResult{}, err
	}

	r, found, err := loadReceipt(name)
	if err != nil {
		return UninstallResult{}, err
	}
	pi, installedHere := r.Projects[project]
	if !found || !installedHere {
		return UninstallResult{}, fmt.Errorf("pack %q is not installed in %s", name, project)
	}

	var res UninstallResult
	if _, err := os.Stat(project); err != nil {
		if !os.IsNotExist(err) {
			return res, err
		}
		// The project root itself is gone from disk — nothing to remove,
		// just drop the record.
		res.Warnings = append(res.Warnings, "project root no longer exists, dropping record: "+project)
	} else {
		for _, f := range pi.Files {
			abs := filepath.Join(project, filepath.FromSlash(f.Dest))
			info, err := os.Stat(abs)
			if err != nil {
				if os.IsNotExist(err) {
					continue // already gone
				}
				return res, err
			}
			if info.IsDir() {
				res.Warnings = append(res.Warnings, "modified, left in place: "+f.Dest)
				continue
			}
			currentSHA, err := sha256File(abs)
			if err != nil {
				return res, err
			}
			if currentSHA != f.SHA256 {
				res.Warnings = append(res.Warnings, "modified, left in place: "+f.Dest)
				continue
			}
			if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
				return res, err
			}
			pruneEmptyDirs(filepath.Dir(abs), project)
		}
	}

	delete(r.Projects, project)
	if len(r.Projects) > 0 {
		return res, saveReceipt(r)
	}

	// No project references this pack any more — tear down its global
	// footprint too.
	res.Global = true
	for _, f := range r.Claude {
		info, err := os.Stat(f.Dest)
		if err != nil {
			continue // already gone
		}
		if info.IsDir() {
			res.Warnings = append(res.Warnings, "modified, left in place: "+f.Dest)
			continue
		}
		sha, err := sha256File(f.Dest)
		if err != nil || sha != f.SHA256 {
			res.Warnings = append(res.Warnings, "modified, left in place: "+f.Dest)
			continue
		}
		if err := os.Remove(f.Dest); err != nil && !os.IsNotExist(err) {
			return res, err
		}
	}
	for _, pname := range r.Plugins {
		if err := plugin.Uninstall(pname); err != nil {
			// A plugin already removed by hand shouldn't block the rest of
			// the teardown.
			res.Warnings = append(res.Warnings, fmt.Sprintf("plugin %s: %v", pname, err))
		}
	}
	if len(r.Plugins) > 0 {
		if _, err := plugin.Update(); err != nil {
			return res, err
		}
	}
	if err := os.RemoveAll(canonDir(r.Name)); err != nil {
		return res, err
	}
	if err := deleteReceipt(r.Name); err != nil {
		return res, err
	}
	return res, nil
}

// pruneEmptyDirs removes dir and walks upward removing each newly-empty
// parent, stopping at (and never removing) root. Best-effort: a non-empty
// directory just ends the walk — other files the user or another pack put
// there must survive, so an ENOTEMPTY-shaped error from os.Remove is not
// treated as a failure.
func pruneEmptyDirs(dir, root string) {
	root = filepath.Clean(root)
	for {
		dir = filepath.Clean(dir)
		if dir == root || dir == "." || dir == string(filepath.Separator) {
			return
		}
		if err := os.Remove(dir); err != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}
