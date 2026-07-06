package plugin

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/sofia-ctx/sofia/internal/gitclone"
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

// originFile records where a git-installed plugin came from (see
// InstallFromGit). Discovery ignores files it doesn't recognize, so this sits
// alongside plugin.yaml without disturbing scanManaged.
const originFile = ".sf-origin.json"

// origin is the shape written to originFile: enough for a future `sf plugin
// upgrade` (out of scope here) to re-clone and diff against what's installed.
type origin struct {
	URL    string `json:"url"`
	Ref    string `json:"ref,omitempty"`
	Commit string `json:"commit"`
}

// InstallFromGit shallow-clones url (optionally pinned to ref — a branch or
// tag; see gitclone.CloneShallow) into a temporary directory and installs it
// exactly like Install: the repo's name (gitclone.RepoName) drives the plugin
// name through the same basename convention. The clone's .git is stripped
// first so copyTree doesn't drag the object store into the managed dir. It
// records provenance in <PluginsDir>/<name>/.sf-origin.json and returns the
// installed name.
func InstallFromGit(url, ref string) (string, error) {
	name, err := gitclone.RepoName(url)
	if err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp("", "sofia-plugin-install-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)

	dst := filepath.Join(tmp, name)
	commit, err := gitclone.CloneShallow(url, ref, dst)
	if err != nil {
		return "", err
	}
	if err := os.RemoveAll(filepath.Join(dst, ".git")); err != nil {
		return "", err
	}

	installed, err := Install(dst)
	if err != nil {
		return "", err
	}

	data, err := json.MarshalIndent(origin{URL: url, Ref: ref, Commit: commit}, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(PluginsDir(), installed, originFile), data, 0o644); err != nil {
		return "", err
	}
	return installed, nil
}

// readOrigin loads a git-installed plugin's provenance, for cmd.go to report
// the commit it landed on. A plugin installed from a local directory has no
// originFile, so a missing file is a plain error, not a special case here.
func readOrigin(name string) (origin, error) {
	data, err := os.ReadFile(filepath.Join(PluginsDir(), name, originFile))
	if err != nil {
		return origin{}, err
	}
	var o origin
	if err := json.Unmarshal(data, &o); err != nil {
		return origin{}, err
	}
	return o, nil
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
