package plugin

import (
	"fmt"
	"os"
)

// upgradeResult is the outcome of re-cloning one git-installed plugin: its
// origin commit before and after. OldCommit == NewCommit means the ref's tip
// hadn't moved since the last install/upgrade.
type upgradeResult struct {
	Name      string
	OldCommit string
	NewCommit string
}

// reinstallFromOrigin re-clones name from its recorded .sf-origin.json (see
// InstallFromGit) and reports the commit it moved from/to. name must be a
// managed plugin installed from git; one without an origin file is reported
// as such rather than silently skipped. It does not refresh the discovery
// cache — callers batch that into a single Update() call (see upgradeCmd),
// so upgrading several plugins costs one rescan, not one per plugin.
func reinstallFromOrigin(name string) (upgradeResult, error) {
	o, err := readOrigin(name)
	if err != nil {
		if os.IsNotExist(err) {
			return upgradeResult{}, fmt.Errorf("%s is not a git install (no %s)", name, originFile)
		}
		return upgradeResult{}, fmt.Errorf("%s: %w", name, err)
	}
	if _, err := InstallFromGit(o.URL, o.Ref); err != nil {
		return upgradeResult{}, err
	}
	n, err := readOrigin(name)
	if err != nil {
		return upgradeResult{}, err
	}
	return upgradeResult{Name: name, OldCommit: o.Commit, NewCommit: n.Commit}, nil
}

// gitInstalledPlugins partitions the managed plugins in ds into those
// installed from git (carrying .sf-origin.json) and those installed from a
// local directory — the latter `sf plugin upgrade` (no args) reports and
// skips rather than silently ignoring. Convention plugins aren't managed and
// are excluded from both.
func gitInstalledPlugins(ds []Descriptor) (git, local []string) {
	for _, d := range ds {
		if d.Kind != Managed {
			continue
		}
		if _, err := readOrigin(d.Name); err != nil {
			local = append(local, d.Name)
		} else {
			git = append(git, d.Name)
		}
	}
	return git, local
}

// formatUpgrade renders one upgradeResult the way `sf plugin upgrade` prints
// it: "upgraded <name>: <old> → <new>", or "<name> is up to date (<commit>)"
// when the ref's tip didn't move.
func formatUpgrade(r upgradeResult) string {
	if r.OldCommit == r.NewCommit {
		return fmt.Sprintf("%s is up to date (%.7s)\n", r.Name, r.NewCommit)
	}
	return fmt.Sprintf("upgraded %s: %.7s → %.7s\n", r.Name, r.OldCommit, r.NewCommit)
}
