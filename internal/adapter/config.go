// Package adapter is sofia's Tier-1, host-interpreted plugin mechanism: a
// plugin.yaml `adapter:` block declares project conventions (a root key, root
// markers, file extensions, and named layers with path globs), and the host —
// not a subprocess — turns those into project-aware `layers`/`grep`/`refs`
// commands grouped by layer. It is the generalization of the private
// "crm usages" pattern; a pure-adapter plugin ships no executable at all.
//
// This package holds the typed schema (Config), its validation, the glob
// matcher, the root resolver, the layer classifier, and the synthesized cobra
// commands. It deliberately does NOT import internal/plugin (the manifest tier
// depends on this one; the reverse would be a cycle).
package adapter

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the typed, validated form of a plugin's `adapter:` block. Kind
// names the adapter family (informational); RootKey is the environment
// variable that pins the project root when set; RootMarkers are the files a
// walk-up looks for otherwise; Ext scopes grep/refs to matching extensions;
// Layers classify a project-relative path into a named layer by glob.
type Config struct {
	Kind        string
	RootKey     string
	RootMarkers []string
	Ext         []string
	Layers      []Layer
}

// Layer is one named slice of a project: a path matches the layer when any of
// its Match globs hits (see Match, which understands `**` across segments).
type Layer struct {
	Name  string
	Match []string
}

// rawConfig mirrors the adapter block's on-disk shape (snake_case keys). Parse
// re-marshals the spec map to YAML and decodes it here, which makes the parse
// robust to the JSON round-trip the discovery cache (plugins.json) puts the
// spec through — where a nested map arrives as map[string]interface{} and a
// number as float64. Decoding through a typed struct sidesteps hand-walking
// those any-shaped values.
type rawConfig struct {
	RootKey     string     `yaml:"root_key"`
	RootMarkers []string   `yaml:"root_markers"`
	Ext         []string   `yaml:"ext"`
	Layers      []rawLayer `yaml:"layers"`
}

type rawLayer struct {
	Name  string   `yaml:"name"`
	Match []string `yaml:"match"`
}

// Parse turns an adapter block's kind + spec map into a typed Config. It does
// not validate — call Config.Validate on the result before trusting it. The
// spec is re-marshalled to YAML and decoded into rawConfig so the same code
// path handles a freshly-parsed manifest and one that survived the plugins.json
// JSON cache (where map/number shapes differ).
func Parse(kind string, spec map[string]any) (Config, error) {
	data, err := yaml.Marshal(spec)
	if err != nil {
		return Config{}, fmt.Errorf("adapter %q: re-marshal spec: %w", kind, err)
	}
	var raw rawConfig
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return Config{}, fmt.Errorf("adapter %q: %w", kind, err)
	}
	cfg := Config{
		Kind:        kind,
		RootKey:     raw.RootKey,
		RootMarkers: raw.RootMarkers,
		Ext:         normalizeExts(raw.Ext),
	}
	for _, l := range raw.Layers {
		cfg.Layers = append(cfg.Layers, Layer(l))
	}
	return cfg, nil
}

// normalizeExts lower-cases each extension and gives it a leading dot, so an
// adapter may write `php` or `.PHP` and both scope grep/refs to `.php`. Empty
// entries are dropped here; Validate rejects a block that ends up with none of
// a stated set (see Validate).
func normalizeExts(exts []string) []string {
	var out []string
	for _, e := range exts {
		e = strings.ToLower(strings.TrimSpace(e))
		if e == "" {
			continue
		}
		if !strings.HasPrefix(e, ".") {
			e = "." + e
		}
		out = append(out, e)
	}
	return out
}

// Validate checks the config's invariants, reporting the first offending field
// (the per-field-error style pack.Manifest.Validate uses). RootMarkers is
// required and every marker must be a safe relative path. Layers are optional,
// but each must have a non-empty, unique name and at least one Match glob, and
// every glob must be a safe relative path. A stated but empty `ext:` (all
// entries blank) is rejected — it reads as "some extensions" but scopes to none.
func (c Config) Validate() error {
	if len(c.RootMarkers) == 0 {
		return fmt.Errorf("adapter: root_markers is required (at least one file that marks a project root)")
	}
	for i, m := range c.RootMarkers {
		if err := safeRel(m); err != nil {
			return fmt.Errorf("adapter: root_markers[%d]: %w", i, err)
		}
	}
	seen := make(map[string]bool, len(c.Layers))
	for i, l := range c.Layers {
		if strings.TrimSpace(l.Name) == "" {
			return fmt.Errorf("adapter: layers[%d].name is required", i)
		}
		if seen[l.Name] {
			return fmt.Errorf("adapter: layers[%d]: duplicate layer name %q", i, l.Name)
		}
		seen[l.Name] = true
		if len(l.Match) == 0 {
			return fmt.Errorf("adapter: layers[%d] (%s): needs at least one match glob", i, l.Name)
		}
		for j, g := range l.Match {
			if err := safeRel(g); err != nil {
				return fmt.Errorf("adapter: layers[%d].match[%d]: %w", i, j, err)
			}
		}
	}
	return nil
}
