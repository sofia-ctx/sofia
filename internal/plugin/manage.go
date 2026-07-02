package plugin

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Install copies a local plugin directory into the managed plugins tree
// ($XDG_DATA_HOME/sofia/plugins/<name>/), where <name> is src's base name. src
// must be a directory holding a parseable plugin.yaml. An existing install of
// the same name is replaced (reinstall). It returns the installed name; the
// caller refreshes the cache (via Update) so the plugin is picked up at once.
//
// This is the local half of a krew-style install flow; a remote registry /
// community index is intentionally out of scope for this pass.
func Install(src string) (string, error) {
	info, err := os.Stat(src)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", src)
	}
	manifestPath := filepath.Join(src, manifestFile)
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return "", fmt.Errorf("%s has no %s", src, manifestFile)
	}
	if _, err := ParseManifest(data); err != nil {
		return "", err
	}

	name := filepath.Base(filepath.Clean(src))
	if name == "" || name == "." || name == string(os.PathSeparator) {
		return "", fmt.Errorf("cannot derive a plugin name from %q", src)
	}
	dst := filepath.Join(PluginsDir(), name)
	if err := os.RemoveAll(dst); err != nil {
		return "", err
	}
	if err := copyTree(src, dst); err != nil {
		return "", err
	}
	return name, nil
}

// Uninstall removes a managed plugin's directory. It also clears any stale
// user-disable entry so a later reinstall isn't silently disabled. Convention
// plugins (bare $PATH executables) are not managed and cannot be uninstalled.
func Uninstall(name string) error {
	dst := filepath.Join(PluginsDir(), name)
	if _, err := os.Stat(dst); err != nil {
		return fmt.Errorf("no managed plugin named %q", name)
	}
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	return Enable(name)
}

// copyTree recursively copies the directory src to dst, preserving file
// permission bits (so the plugin's executable stays executable).
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if e.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
}
