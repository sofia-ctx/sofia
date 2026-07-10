// Package plugin implements sofia's subprocess-first plugin protocol: the
// host-side mechanics that let a third-party executable extend the `sf`
// command tree without being compiled into the binary. It is Tier 2 of a
// three-tier design (Tier 1 = declarative YAML adapters, Tier 3 = a first-party
// Go SDK, now living under pkg/ — see docs/sdk.md); the subprocess tier
// lives here.
//
// Tier 1 lives in internal/adapter and is wired in here: a manifest's
// `adapter:` block (see manifest.go's Adapter.Config) turns into host-
// synthesized layers/grep/refs commands for a plugin that ships no executable
// (see shims.go's attachAdapter and discover.go's isAdapterOnly). The concept
// is documented in docs/adapters.md; the subprocess contract below is Tier 2.
//
// # Discovery
//
// A plugin is found one of two ways (see discover.go):
//
//   - Convention — an executable named `sf-<name>` anywhere on $PATH, the
//     git-subcommand convention. Zero config: it becomes a single passthrough
//     command `sf <name> …` that execs the binary with the user's args. It is
//     not protocol-gated (there is no manifest to negotiate against), mirroring
//     how git never introspects `git-foo`.
//   - Managed — a directory `$XDG_DATA_HOME/sofia/plugins/<name>/` holding an
//     executable plus a `plugin.yaml` manifest (see manifest.go). The manifest
//     declares the commands, protocol version, config settings and capabilities
//     the plugin exposes, so the host can build `sf --help` and gate on
//     compatibility without ever executing the binary.
//
// Discovered metadata is cached in `$XDG_DATA_HOME/sofia/plugins.json` so the
// hot path (`sf --help`, command dispatch) reads a single JSON file instead of
// forking every plugin on every invocation — the Docker plugin fork-bomb
// lesson. The cache is rebuilt only on `sf plugin update` or when it is missing
// or stale (the managed directory changed since the cache was written).
// Crucially, discovery never executes a plugin: managed metadata comes from the
// manifest file and convention metadata is synthesized from the file name.
//
// # Invocation contract
//
// The host passes context to a plugin two ways (see invoke.go):
//
//	argv                       the resolved command path + the user's args
//	SOFIA_PROJECT_ROOT         repo root of the cwd (or an inherited override)
//	SOFIA_FORMAT               desired output format (toon|md|json), default toon
//	SOFIA_TAG                  project tag, as calllog attributes it
//	SOFIA_SESSION_ID           the calling agent session, as calllog sees it
//	SOFIA_SOURCE               agent | manual | test, as calllog classifies it
//
// The SOFIA_* names mirror exactly what internal/calllog already reads, so a
// plugin and the host agree on identity without a parallel vocabulary. A plugin
// writes its output (in whatever format it chose) to stdout — the host does not
// reparse it — and diagnostics to stderr. A "rich" plugin can additionally
// declare the "stdin-json" capability to receive a structured JSON request on
// stdin; the argv-only path is the default and needs no opt-in.
//
// Telemetry is free to the plugin author: the host wraps the plugin's stdout in
// a calllog.Counter and writes exactly one calls.jsonl line per invocation —
// tool name dotted `<plugin>.<command>`, the real subprocess exit code, output
// bytes/tokens — even when the plugin prints nothing or exits non-zero.
//
// # Protocol versioning
//
// The protocol is semver'd independently of sf's own release version (see
// version.go). A managed plugin declares the protocol version it speaks and,
// optionally, the minimum host protocol it needs (`min_sf`). The host supports
// an N-1 major window: a plugin outside it, or one requiring a newer host, is
// reported disabled with a stated reason via `sf plugin list`/`info` — never a
// crash, never a silent skip.
package plugin

// HostProtocol is the plugin-protocol version this build of sf speaks. It is
// semver and deliberately unrelated to sf's own release version (internal/
// version): the two evolve on separate clocks, so a plugin negotiates against
// the protocol, not against "which sf tag am I running". The N-1 support window
// (see Compatible) admits plugins declaring a protocol major of HostProtocol's
// major or one below it.
//
// 1.1.0: release-fetch (see release.go) — a URL install downloads a prebuilt
// exec from the repo's GitHub release when the clone ships no binary and the
// manifest declares a `release:` block. Additive: a 1.0.0 plugin still
// negotiates fine (same major); a plugin that *needs* release-fetch declares
// `min_sf: "1.1.0"` so an older host reports "requires host protocol >= 1.1"
// instead of a bare "no runnable executable".
//
// 1.2.0: authenticated (private) release-fetch — releaseGet sends a bearer
// token from GH_TOKEN/GITHUB_TOKEN when one is set, so the release assets of a
// private repo are reachable. Additive for public plugins (no token → the
// request is byte-for-byte the same); a plugin whose release lives in a
// private repo declares `min_sf: "1.2.0"` so an older host reports "requires
// host protocol >= 1.2" instead of failing the download with a bare 404.
const HostProtocol = "1.2.0"

// Kind distinguishes the two discovery mechanisms. It changes how a plugin is
// gated: Managed plugins negotiate protocol compatibility from their manifest;
// Convention plugins are trusted passthroughs (git-subcommand style) with no
// manifest to negotiate against.
type Kind string

const (
	// Managed is a plugin installed under $XDG_DATA_HOME/sofia/plugins/<name>/
	// with a plugin.yaml manifest.
	Managed Kind = "managed"
	// Convention is a bare `sf-<name>` executable found on $PATH.
	Convention Kind = "convention"
)

// Descriptor is one resolved plugin: where its executable lives, the manifest
// that describes it, and whether the host will run it (Enabled) or refuse to
// (with Reason stating why). It is the unit `sf plugin list` renders and the
// unit the command tree builds shims from.
type Descriptor struct {
	Name         string   `json:"name"`
	Kind         Kind     `json:"kind"`
	Exec         string   `json:"exec"`             // absolute path to the executable
	Dir          string   `json:"dir,omitempty"`    // managed install dir; empty for convention plugins
	Manifest     Manifest `json:"manifest"`         // declared (managed) or synthesized (convention)
	Enabled      bool     `json:"enabled"`          // false → the host will not dispatch to it
	UserDisabled bool     `json:"user_disabled"`    // disabled by `sf plugin disable`, distinct from a compat failure
	Reason       string   `json:"reason,omitempty"` // why Enabled is false; empty when enabled
}

// IsGroup reports whether the plugin exposes named subcommands (so `sf <name>`
// is a help-only group) rather than a single passthrough command. A plugin is a
// group if it declares commands or carries an adapter block (the host
// synthesizes layers/grep/refs under it). Group names must be kept out of the
// central call-log fallback, the same way `sf cc` and `sf gripe` are (see
// calllog.RegisterPluginGroups).
func (d Descriptor) IsGroup() bool {
	return len(d.Manifest.Commands) > 0 || d.Manifest.HasAdapter()
}
