package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// cacheVersion is the on-disk schema version of plugins.json. A mismatch forces
// a rescan, so an older cache written by a previous sf can't feed a newer one a
// shape it doesn't understand.
const cacheVersion = 1

// manifestFile is the fixed name of a managed plugin's manifest.
const manifestFile = "plugin.yaml"

// DataDir returns sofia's XDG data directory (the parent of both the managed
// plugins tree and the discovery cache). Resolution mirrors the XDG Base
// Directory precedence internal/calllog uses for its own paths:
//
//  1. $XDG_DATA_HOME/sofia            — XDG-conformant override.
//  2. ~/.local/share/sofia            — the spec's default when unset.
//  3. ./.sofia                        — last resort if HOME is undiscoverable.
func DataDir() string {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "sofia")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".local", "share", "sofia")
	}
	return ".sofia"
}

// PluginsDir is where managed plugins are installed: one subdirectory per
// plugin, each holding an executable and a plugin.yaml.
func PluginsDir() string { return filepath.Join(DataDir(), "plugins") }

func cachePath() string    { return filepath.Join(DataDir(), "plugins.json") }
func disabledPath() string { return filepath.Join(DataDir(), "plugins-disabled.json") }

// cachedPlugin is the discovery result persisted in plugins.json. It holds only
// what discovery found on disk (never the live enabled/disabled decision, which
// is recomputed against HostProtocol and the user's disable set on every load).
type cachedPlugin struct {
	Name      string   `json:"name"`
	Kind      Kind     `json:"kind"`
	Exec      string   `json:"exec"`
	Dir       string   `json:"dir,omitempty"`
	Manifest  Manifest `json:"manifest"`
	LoadError string   `json:"load_error,omitempty"` // e.g. an invalid manifest or a missing executable
}

type diskCache struct {
	Version int            `json:"version"`
	Built   time.Time      `json:"built"`
	DirMod  time.Time      `json:"dir_mod"` // managed dir ModTime at scan; zero if the dir was absent
	Plugins []cachedPlugin `json:"plugins"`
}

// Load returns the current plugin descriptors, reading the discovery cache when
// it is present and fresh and rescanning (then rewriting the cache) otherwise.
// It never executes a plugin — the whole point of the cache is that `sf --help`
// and command dispatch cost one file read, not one fork per plugin.
func Load() []Descriptor {
	return decorate(loadOrScan())
}

// Update forces a rescan, rewrites the cache, and returns the fresh
// descriptors. This is the `sf plugin update` path.
func Update() ([]Descriptor, error) {
	plugins := scan()
	if err := saveCache(plugins); err != nil {
		return nil, err
	}
	return decorate(plugins), nil
}

func loadOrScan() []cachedPlugin {
	if c, ok := loadCache(); ok && !cacheStale(c) {
		return c.Plugins
	}
	plugins := scan()
	_ = saveCache(plugins) // best-effort: a read-only data dir must not break dispatch
	return plugins
}

func loadCache() (*diskCache, bool) {
	data, err := os.ReadFile(cachePath())
	if err != nil {
		return nil, false
	}
	var c diskCache
	if err := json.Unmarshal(data, &c); err != nil || c.Version != cacheVersion {
		return nil, false
	}
	return &c, true
}

// cacheStale reports whether the managed plugins directory changed since the
// cache was built. It compares the directory's current mtime against the mtime
// recorded at scan time — two filesystem timestamps of the same inode, so the
// comparison is exact (no wall-clock-vs-fs granularity gap), and adding or
// removing a plugin directory, which always bumps the parent's mtime, reliably
// invalidates the cache. Convention plugins on $PATH are not tracked this way —
// `sf plugin update` refreshes them explicitly — because stat'ing every $PATH
// entry on the hot path would defeat the point of the cache.
func cacheStale(c *diskCache) bool {
	info, err := os.Stat(PluginsDir())
	if err != nil {
		// The managed dir is gone now; stale only if the cache recorded one.
		return !c.DirMod.IsZero()
	}
	return !info.ModTime().Equal(c.DirMod)
}

func saveCache(plugins []cachedPlugin) error {
	if err := os.MkdirAll(DataDir(), 0o755); err != nil {
		return err
	}
	var dirMod time.Time
	if info, err := os.Stat(PluginsDir()); err == nil {
		dirMod = info.ModTime()
	}
	c := diskCache{Version: cacheVersion, Built: time.Now().UTC(), DirMod: dirMod, Plugins: plugins}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(cachePath(), data, 0o644)
}

// scan discovers plugins from disk. Managed plugins take precedence over a
// convention plugin of the same name (the manifest carries strictly more
// information than a bare $PATH executable). The result is sorted by name so
// the cache and every rendering are stable.
func scan() []cachedPlugin {
	byName := map[string]cachedPlugin{}
	for _, cp := range scanManaged() {
		byName[cp.Name] = cp
	}
	for _, cp := range scanConvention() {
		if _, ok := byName[cp.Name]; !ok {
			byName[cp.Name] = cp
		}
	}
	out := make([]cachedPlugin, 0, len(byName))
	for _, cp := range byName {
		out = append(out, cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// scanManaged reads $XDG_DATA_HOME/sofia/plugins/<name>/plugin.yaml for every
// managed plugin. A subdirectory without a manifest is skipped; a subdirectory
// with an unparseable manifest or no runnable executable is kept but tagged with
// a LoadError so it surfaces disabled-with-reason rather than silently vanishing.
func scanManaged() []cachedPlugin {
	entries, err := os.ReadDir(PluginsDir())
	if err != nil {
		return nil
	}
	var out []cachedPlugin
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		dir := filepath.Join(PluginsDir(), name)
		manifestPath := filepath.Join(dir, manifestFile)
		data, err := os.ReadFile(manifestPath)
		if err != nil {
			continue // a stray directory, not a plugin
		}
		cp := cachedPlugin{Name: name, Kind: Managed, Dir: dir}
		m, err := ParseManifest(data)
		if err != nil {
			cp.LoadError = fmt.Sprintf("invalid %s: %v", manifestFile, err)
			out = append(out, cp)
			continue
		}
		cp.Manifest = m
		if m.HasAdapter() {
			// A broken adapter block disables the plugin with a precise reason,
			// the same as a broken manifest — validated once here so dispatch
			// and `sf plugin list` agree.
			if cfg, cerr := m.Adapter.Config(); cerr != nil {
				cp.LoadError = "invalid adapter: " + cerr.Error()
			} else if verr := cfg.Validate(); verr != nil {
				cp.LoadError = "invalid adapter: " + verr.Error()
			}
		}
		exe, err := managedExec(dir, m)
		if err != nil {
			// A pure-adapter plugin (adapter block, no exec, no declared
			// commands) legitimately ships no executable — the host runs its
			// synthesized commands in-process — so a missing binary isn't fatal.
			if isAdapterOnly(m) {
				exe = ""
			} else if cp.LoadError == "" {
				cp.LoadError = err.Error()
			}
		}
		cp.Exec = exe
		out = append(out, cp)
	}
	return out
}

// scanConvention finds `sf-<name>` executables on $PATH (the git-subcommand
// convention). The first match for a given name wins, matching how the shell
// resolves a command against $PATH. Convention plugins carry a synthesized
// manifest — a single passthrough command, no protocol gating.
func scanConvention() []cachedPlugin {
	seen := map[string]bool{}
	var out []cachedPlugin
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			base := e.Name()
			name, ok := strings.CutPrefix(base, "sf-")
			if !ok || name == "" || seen[name] {
				continue
			}
			path := filepath.Join(dir, base)
			if !isExecutable(path) {
				continue
			}
			seen[name] = true
			out = append(out, cachedPlugin{
				Name: name,
				Kind: Convention,
				Exec: path,
				Manifest: Manifest{
					Schema:      ManifestSchema,
					Description: fmt.Sprintf("external sf-%s plugin (found on $PATH)", name),
				},
			})
		}
	}
	return out
}

// managedExec resolves a managed plugin's executable: the manifest's `exec`
// field, or the plugin directory's own name by default. The file must exist and
// be executable, else the plugin is disabled with this error as its reason.
func managedExec(dir string, m Manifest) (string, error) {
	name := m.Exec
	if name == "" {
		name = filepath.Base(dir)
	}
	path := filepath.Join(dir, filepath.FromSlash(name))
	if !isExecutable(path) {
		return "", fmt.Errorf("no runnable executable %q in %s", name, dir)
	}
	return path, nil
}

// isAdapterOnly reports whether a manifest is a pure Tier-1 adapter: an adapter
// block, no declared executable, and no declared subprocess commands. Such a
// plugin runs entirely in-process (its host-synthesized commands), so it needs
// no binary on disk.
func isAdapterOnly(m Manifest) bool {
	return m.HasAdapter() && m.Exec == "" && len(m.Commands) == 0
}

// isExecutable reports whether path is a regular file with an execute bit set.
func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode().Perm()&0o111 != 0
}

// decorate turns raw discovery results into Descriptors, computing the live
// enabled/disabled decision for each: a load error wins first (nothing else
// matters if we can't run it), then protocol compatibility for managed plugins
// (a mismatch `sf plugin enable` can't fix), then the user's own disable set.
// Convention plugins are trusted passthroughs and are only ever disabled by the
// user.
func decorate(cps []cachedPlugin) []Descriptor {
	disabled := loadDisabled()
	out := make([]Descriptor, 0, len(cps))
	for _, cp := range cps {
		d := Descriptor{
			Name:     cp.Name,
			Kind:     cp.Kind,
			Exec:     cp.Exec,
			Dir:      cp.Dir,
			Manifest: cp.Manifest,
		}
		switch {
		case cp.LoadError != "":
			d.Enabled, d.Reason = false, cp.LoadError
		case cp.Kind == Managed:
			if ok, reason := Compatible(cp.Manifest.Protocol, cp.Manifest.MinSF, HostProtocol); !ok {
				d.Enabled, d.Reason = false, reason
			} else if disabled[cp.Name] {
				d.Enabled, d.UserDisabled, d.Reason = false, true, userDisabledReason(cp.Name)
			} else {
				d.Enabled = true
			}
		default: // Convention
			if disabled[cp.Name] {
				d.Enabled, d.UserDisabled, d.Reason = false, true, userDisabledReason(cp.Name)
			} else {
				d.Enabled = true
			}
		}
		out = append(out, d)
	}
	return out
}

func userDisabledReason(name string) string {
	return fmt.Sprintf("disabled by user (run `sf plugin enable %s` to re-enable)", name)
}

// loadDisabled reads the set of user-disabled plugin names. It is kept separate
// from the discovery cache so `enable`/`disable` never trigger a rescan and the
// choice survives a cache rebuild.
func loadDisabled() map[string]bool {
	set := map[string]bool{}
	data, err := os.ReadFile(disabledPath())
	if err != nil {
		return set
	}
	var names []string
	if json.Unmarshal(data, &names) != nil {
		return set
	}
	for _, n := range names {
		set[n] = true
	}
	return set
}

func saveDisabled(set map[string]bool) error {
	names := make([]string, 0, len(set))
	for n := range set {
		names = append(names, n)
	}
	sort.Strings(names)
	if err := os.MkdirAll(DataDir(), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(names, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(disabledPath(), data, 0o644)
}

// Disable records name in the user-disabled set (idempotent). The plugin then
// reports disabled-by-user and is dropped from the command tree until enabled.
func Disable(name string) error {
	set := loadDisabled()
	set[name] = true
	return saveDisabled(set)
}

// Enable clears a prior Disable for name (idempotent). It does not override a
// compatibility failure — an incompatible plugin stays disabled with its
// protocol reason.
func Enable(name string) error {
	set := loadDisabled()
	delete(set, name)
	return saveDisabled(set)
}

// GroupNames returns the names of enabled plugins that expose subcommands (so
// `sf <name>` is a help-only group). These must be registered with calllog so
// the central fallback doesn't log a junk entry for the bare help view — the
// same treatment `cc` and `gripe` get.
func GroupNames(ds []Descriptor) []string {
	var names []string
	for _, d := range ds {
		if d.Enabled && d.IsGroup() {
			names = append(names, d.Name)
		}
	}
	return names
}

// Find returns the descriptor named name, or false. Used by `sf plugin info`
// and enable/disable to give a precise "no such plugin" error.
func Find(ds []Descriptor, name string) (Descriptor, bool) {
	for _, d := range ds {
		if d.Name == name {
			return d, true
		}
	}
	return Descriptor{}, false
}
