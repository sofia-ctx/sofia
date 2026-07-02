package plugin

import (
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/sofia-ctx/sofia/internal/envfile"
)

// ManifestSchema is the plugin.yaml schema version this host understands. It
// is bumped only on a breaking change to the manifest *shape*; the plugin
// protocol (HostProtocol) versions the runtime contract separately.
const ManifestSchema = 1

// Manifest is the parsed `plugin.yaml` a managed plugin ships. Unknown keys are
// ignored rather than rejected (yaml.v3's default; we never call KnownFields),
// so a manifest written for a newer sf still parses on an older one — the same
// forward-compatibility LSP relies on for capability negotiation.
type Manifest struct {
	// Schema is the manifest schema version (see ManifestSchema).
	Schema int `yaml:"schema" json:"schema"`
	// Protocol is the plugin-protocol version this binary speaks (semver).
	// Negotiated against HostProtocol; see Compatible.
	Protocol string `yaml:"protocol" json:"protocol"`
	// Version is the plugin's own release version (free-form; semver by
	// convention). Informational — not used for gating.
	Version string `yaml:"version" json:"version,omitempty"`
	// MinSF is the minimum host *protocol* version the plugin needs (semver).
	// Optional; empty means "any host in the supported major window".
	MinSF string `yaml:"min_sf" json:"min_sf,omitempty"`
	// Description is a one-line summary shown by `sf plugin list`/`info`.
	Description string `yaml:"description" json:"description,omitempty"`
	// Exec is the executable's name (or dir-relative path) within the plugin
	// directory. Empty defaults to the directory's own name.
	Exec string `yaml:"exec" json:"exec,omitempty"`
	// Commands are the subcommands the plugin exposes, with short help — enough
	// for `sf --help` to list them without executing the binary. An empty list
	// means the plugin is a single passthrough command (`sf <name> …`).
	Commands []Command `yaml:"commands" json:"commands,omitempty"`
	// Capabilities are optional feature flags (e.g. "stdin-json"). Unknown
	// flags are ignored, so a plugin may advertise capabilities a given host
	// does not act on.
	Capabilities []string `yaml:"capabilities" json:"capabilities,omitempty"`
	// Settings are declared config fields, shaped like envfile.Field, so the
	// host resolves plugin config exactly as it resolves its own project config.
	Settings []Setting `yaml:"settings" json:"settings,omitempty"`
	// Adapter is reserved for Tier 1 (declarative YAML adapters). It is parsed
	// and preserved but not consumed by the subprocess tier.
	Adapter *Adapter `yaml:"adapter" json:"adapter,omitempty"`
}

// Command is one CLI subcommand the plugin exposes under `sf <name>`.
type Command struct {
	// Path is the subcommand path relative to `sf <name>`, space- or
	// slash-separated for nesting (e.g. "greet" → `sf <name> greet`,
	// "cache clear" → `sf <name> cache clear`).
	Path string `yaml:"path" json:"path"`
	// Short is the one-line help shown in `sf --help` / `sf <name> --help`.
	Short string `yaml:"short" json:"short,omitempty"`
}

// Setting mirrors the resolvable fields of envfile.Field (the func-valued
// Validator is not expressible in YAML and is omitted). Field converts it back
// into an envfile.Field so plugin config flows through the same resolver as the
// host's own env-backed config.
type Setting struct {
	Key         string `yaml:"key" json:"key"`
	Prompt      string `yaml:"prompt" json:"prompt,omitempty"`
	Description string `yaml:"description" json:"description,omitempty"`
	Default     string `yaml:"default" json:"default,omitempty"`
	Required    bool   `yaml:"required" json:"required,omitempty"`
}

// Field adapts a declared Setting to an envfile.Field for resolution.
func (s Setting) Field() envfile.Field {
	return envfile.Field{
		Key:         s.Key,
		Prompt:      s.Prompt,
		Description: s.Description,
		Default:     s.Default,
		Required:    s.Required,
	}
}

// Adapter is the reserved Tier-1 declarative block. Kind names the adapter and
// Spec captures the rest verbatim; the subprocess tier does not interpret it.
type Adapter struct {
	Kind string         `yaml:"kind" json:"kind,omitempty"`
	Spec map[string]any `yaml:",inline" json:"spec,omitempty"`
}

// ParseManifest decodes a plugin.yaml. Unknown top-level keys are ignored for
// forward compatibility. A syntactically invalid document is an error; a valid
// but under-specified one (e.g. no protocol) parses and is caught later by
// compatibility gating, so `sf plugin list` can state a precise reason instead
// of the plugin vanishing.
func ParseManifest(data []byte) (Manifest, error) {
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return Manifest{}, fmt.Errorf("parse plugin.yaml: %w", err)
	}
	return m, nil
}

// HasCapability reports whether the manifest advertises the named capability.
func (m Manifest) HasCapability(name string) bool {
	for _, c := range m.Capabilities {
		if c == name {
			return true
		}
	}
	return false
}
