package launch

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// aliasFile is the project-alias store: a name→absolute-dir map that lets
// `sf claude <name>` resolve instantly, skipping the deep search. Location:
// $SF_CLAUDE_ALIASES override (used by tests), else $XDG_CONFIG_HOME/sofia or
// ~/.config/sofia — matching sofia's other config (`~/.config/sofia/*`).
// Empty string if no home can be resolved.
func aliasFile() string {
	if p := os.Getenv("SF_CLAUDE_ALIASES"); p != "" {
		return p
	}
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "sofia", "projects.yaml")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".config", "sofia", "projects.yaml")
	}
	return ""
}

// loadAliases reads the name→dir map. A missing, unreadable, or malformed file
// is not an error — it yields an empty map, so a launch never fails on the
// cache.
func loadAliases() map[string]string {
	m := map[string]string{}
	p := aliasFile()
	if p == "" {
		return m
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return m
	}
	_ = yaml.Unmarshal(data, &m)
	return m
}

// SaveAlias records name→dir in the alias store, creating the file and its
// parent as needed. yaml.v3 marshals map keys sorted, so the file stays stable
// and human-editable. A no-op when the entry is already present.
func SaveAlias(name, dir string) error {
	p := aliasFile()
	if p == "" {
		return fmt.Errorf("no config location for the alias store")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	m := loadAliases()
	if m[name] == abs {
		return nil
	}
	m[name] = abs
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	body, err := yaml.Marshal(m)
	if err != nil {
		return err
	}
	const header = "# sf claude project aliases — name: /abs/path\n" +
		"# Added automatically on first resolve; edit or delete freely.\n"
	return os.WriteFile(p, append([]byte(header), body...), 0o644)
}
