package pack

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// ManifestSchema is the pack.yaml schema version this host understands. Same
// convention as plugin.ManifestSchema: it only bumps on a breaking change to
// the manifest shape.
const ManifestSchema = 1

// manifestFile is the fixed name of a pack's manifest, read from the pack's
// root (bundled or freshly cloned).
const manifestFile = "pack.yaml"

// nameRe is the accepted shape for a pack's name: it becomes a directory name
// (canon copy, receipt file), so it's restricted to what a plugin name
// already allows.
var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// Manifest is the parsed pack.yaml. Unknown top-level keys are ignored for
// forward compatibility — the same non-strict decode plugin.ParseManifest
// uses — so a pack.yaml written for a newer sf still parses on an older one.
type Manifest struct {
	// Schema is the manifest schema version (see ManifestSchema).
	Schema int `yaml:"schema" json:"schema"`
	// Name identifies the pack; see nameRe. It becomes the canon directory
	// name and the receipt file name.
	Name string `yaml:"name" json:"name"`
	// Description is a one-line summary shown by `sf pack list`/`info`.
	Description string `yaml:"description" json:"description,omitempty"`
	// Plugins are sf plugins the pack installs globally; see PluginRef.
	Plugins []PluginRef `yaml:"plugins" json:"plugins,omitempty"`
	// Instructions land at the target project's root (default dest:
	// filepath.Base(Src); directories copy recursively).
	Instructions []FileMap `yaml:"instructions" json:"instructions,omitempty"`
	// Claude groups the two Claude-specific shelves (skills, commands).
	// Entirely optional — a pack with no claude: block never touches
	// $CLAUDE_DIR.
	Claude Claude `yaml:"claude" json:"claude,omitempty"`
	// Templates land at the target project's root, shaped exactly like
	// Instructions.
	Templates []FileMap `yaml:"templates" json:"templates,omitempty"`
}

// PluginRef names one plugin the pack installs globally: Path is a directory
// inside the pack (installed via plugin.Install), Git is an external
// repository (via plugin.InstallFromGit) — exactly one of the two is set.
// Ref is a branch or tag and only makes sense alongside Git (see
// gitclone.CloneShallow — commit shas aren't supported).
type PluginRef struct {
	Path string `yaml:"path" json:"path,omitempty"`
	Git  string `yaml:"git" json:"git,omitempty"`
	Ref  string `yaml:"ref" json:"ref,omitempty"`
}

// FileMap is one source→destination mapping: Src is relative to the pack
// root, Dest relative to the target shelf and defaulting to
// filepath.Base(Src) when empty. Src may name a single file or a directory,
// copied recursively.
type FileMap struct {
	Src  string `yaml:"src" json:"src"`
	Dest string `yaml:"dest" json:"dest,omitempty"`
}

// Claude groups the two Claude-specific shelves. Unlike Instructions/
// Templates, dest is never settable here: a skill always lands at
// skills/<basename(src)>/ and a command at commands/<basename(src)> — the
// shelf's own naming convention decides it, not the manifest author.
type Claude struct {
	Skills   []FileMap `yaml:"skills" json:"skills,omitempty"`
	Commands []FileMap `yaml:"commands" json:"commands,omitempty"`
}

// ParseManifest decodes a pack.yaml. Unknown keys are ignored for forward
// compatibility; a syntactically invalid document is an error. Call Validate
// on the result before trusting it — a syntactically valid manifest can still
// be schema-incomplete (no name, wrong schema, an ambiguous plugin ref, …).
func ParseManifest(data []byte) (Manifest, error) {
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return Manifest{}, fmt.Errorf("parse pack.yaml: %w", err)
	}
	return m, nil
}

// Validate checks the manifest's invariants and, on success, normalizes every
// declared path with filepath.FromSlash (manifest paths are always "/"-
// separated) so every later consumer can filepath.Join them directly without
// re-converting.
func (m *Manifest) Validate() error {
	if m.Schema != ManifestSchema {
		return fmt.Errorf("pack.yaml: unsupported schema %d (want %d)", m.Schema, ManifestSchema)
	}
	if !nameRe.MatchString(m.Name) {
		return fmt.Errorf("pack.yaml: invalid name %q (must match %s)", m.Name, nameRe.String())
	}
	for i, p := range m.Plugins {
		if (p.Path == "") == (p.Git == "") {
			return fmt.Errorf("pack.yaml: plugins[%d] needs exactly one of path or git", i)
		}
		if p.Git == "" && p.Ref != "" {
			return fmt.Errorf("pack.yaml: plugins[%d]: ref only applies alongside git", i)
		}
		if p.Path != "" {
			if err := safeRel(p.Path); err != nil {
				return fmt.Errorf("pack.yaml: plugins[%d].path: %w", i, err)
			}
			m.Plugins[i].Path = filepath.FromSlash(p.Path)
		}
	}
	groups := []struct {
		label string
		fms   []FileMap
	}{
		{"instructions", m.Instructions},
		{"templates", m.Templates},
		{"claude.skills", m.Claude.Skills},
		{"claude.commands", m.Claude.Commands},
	}
	for _, g := range groups {
		for i := range g.fms {
			if err := validateFileMap(g.label, i, &g.fms[i]); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateFileMap checks and (on success) normalizes one FileMap in place.
func validateFileMap(label string, i int, fm *FileMap) error {
	if fm.Src == "" {
		return fmt.Errorf("pack.yaml: %s[%d].src is required", label, i)
	}
	if err := safeRel(fm.Src); err != nil {
		return fmt.Errorf("pack.yaml: %s[%d].src: %w", label, i, err)
	}
	fm.Src = filepath.FromSlash(fm.Src)
	if fm.Dest != "" {
		if err := safeRel(fm.Dest); err != nil {
			return fmt.Errorf("pack.yaml: %s[%d].dest: %w", label, i, err)
		}
		fm.Dest = filepath.FromSlash(fm.Dest)
	}
	return nil
}

// safeRel rejects a manifest path that could escape its root: an absolute
// path, or one whose cleaned form still climbs above its start (a leading
// ".."). Every src/dest in pack.yaml goes through this before it's ever
// joined to a real directory.
func safeRel(p string) error {
	if p == "" {
		return fmt.Errorf("path is empty")
	}
	if filepath.IsAbs(p) {
		return fmt.Errorf("%q: must be relative", p)
	}
	clean := filepath.Clean(p)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%q: escapes its root", p)
	}
	return nil
}
