package plugin

import (
	"fmt"
	"os"
	"path/filepath"
)

// exampleCommand is the single command path a scaffolded plugin exposes. It
// is shared between the generated manifest and the generated executable so
// the two stay in lockstep, and it is the command docs/plugins.md's
// walkthrough runs.
const exampleCommand = "greet"

// Scaffold creates a new plugin skeleton at filepath.Join(parentDir, name). It
// is the `sf plugin new` implementation. parentDir defaults to "." when empty;
// the target directory must already be absent — Scaffold never overwrites.
//
// Two shapes, selected by adapter:
//   - a subprocess plugin (adapter=false): a plugin.yaml declaring protocol +
//     one example command, an executable POSIX-sh stub, and a README.
//   - a Tier-1 adapter (adapter=true): a plugin.yaml with an adapter block (root
//     markers, extensions, one example layer) and no executable — the host runs
//     its synthesized layers/grep/refs commands in-process.
//
// Either shape is installable as-is: `sf plugin install <dir>` parses and
// enables it without any edits required first.
func Scaffold(name, parentDir string, adapter bool) (string, error) {
	if err := validScaffoldName(name); err != nil {
		return "", err
	}
	if parentDir == "" {
		parentDir = "."
	}
	dst := filepath.Join(parentDir, name)
	if _, err := os.Stat(dst); err == nil {
		return "", fmt.Errorf("%s already exists", dst)
	} else if !os.IsNotExist(err) {
		return "", err
	}

	if err := os.MkdirAll(dst, 0o755); err != nil {
		return "", err
	}

	manifest, readme := scaffoldManifest(name), scaffoldReadme(name)
	if adapter {
		manifest, readme = scaffoldAdapterManifest(name), scaffoldAdapterReadme(name)
	}
	if err := os.WriteFile(filepath.Join(dst, manifestFile), []byte(manifest), 0o644); err != nil {
		return "", err
	}
	// A pure adapter ships no executable — the host synthesizes its commands.
	if !adapter {
		if err := os.WriteFile(filepath.Join(dst, name), []byte(scaffoldExec(name)), 0o755); err != nil {
			return "", err
		}
	}
	if err := os.WriteFile(filepath.Join(dst, "README.md"), []byte(readme), 0o644); err != nil {
		return "", err
	}
	return dst, nil
}

// validScaffoldName mirrors the shape Install requires of a plugin name (see
// Install: it derives one from a source path and rejects an empty result, a
// bare "." or a bare separator). Given as a name directly here rather than
// derived from a path, the same shape means: non-empty, not "." or "..", and
// a single path component — no embedded separator.
func validScaffoldName(name string) error {
	if name == "" {
		return fmt.Errorf("plugin name cannot be empty")
	}
	if name == "." || name == ".." {
		return fmt.Errorf("%q is not a valid plugin name", name)
	}
	if filepath.Base(name) != name {
		return fmt.Errorf("%q is not a valid plugin name (must be a single path component, no separators)", name)
	}
	return nil
}

// scaffoldManifest is the plugin.yaml written for a new plugin. It declares
// the current HostProtocol so the scaffold installs and enables without
// edits, one example command, and a commented-out settings example.
func scaffoldManifest(name string) string {
	return fmt.Sprintf(`# plugin.yaml -- sofia's plugin manifest. Full authoring contract (every
# field, the invocation contract, protocol versioning): the internal/plugin
# package doc, or docs/plugins.md in https://github.com/sofia-ctx/sofia.
schema: %d
protocol: %q
version: "0.1.0"
description: "TODO: describe what %s does"
exec: %q
commands:
  - path: %s
    short: "Print a greeting"
# settings:
#   - key: GREETING
#     prompt: "Greeting word"
#     description: "word used to greet"
#     default: "Hello"
#     required: false
`, ManifestSchema, HostProtocol, name, name, exampleCommand)
}

// scaffoldAdapterManifest is the plugin.yaml written for `sf plugin new
// --adapter`: a pure Tier-1 adapter with no exec. It declares the current
// HostProtocol so it installs and enables without edits, required root_markers,
// one active example layer, and commented root_key/ext the author fills in.
func scaffoldAdapterManifest(name string) string {
	return fmt.Sprintf(`# plugin.yaml -- a Tier-1 adapter. The host interprets the adapter block
# below (no executable required) and synthesizes project-aware layers/grep/refs
# commands under "sf %[1]s". Full reference: docs/adapters.md in
# https://github.com/sofia-ctx/sofia.
schema: %[2]d
protocol: %[3]q
version: "0.1.0"
description: "TODO: describe the project %[1]s classifies"
adapter:
  kind: %[1]s
  # root_key pins the project root from an env var when set; otherwise the host
  # walks up from the cwd for one of root_markers.
  # root_key: PROJECT_ROOT
  root_markers: [composer.json]
  # ext scopes grep/refs to these extensions (leading dot optional).
  # ext: [php]
  layers:
    - name: Domain
      match: ["src/Domain/**"]
`, name, ManifestSchema, HostProtocol)
}

// scaffoldExec is the executable stub written for a new plugin: a POSIX sh
// script that handles the one example command, echoes the two env vars
// every plugin gets (SOFIA_FORMAT, SOFIA_PROJECT_ROOT), and exits 0.
func scaffoldExec(name string) string {
	return fmt.Sprintf(`#!/bin/sh
# %[1]s -- scaffolded by "sf plugin new".
#
# sofia execs this file with the resolved command path as argv (see this
# manifest's "commands") followed by the user's own args, and the SOFIA_*
# variables below in the environment -- the full contract is the
# internal/plugin package doc's "Invocation contract".
#
# Windows note: sofia execs this file directly, so a POSIX sh script only
# runs where "sh" is on PATH (Git Bash, WSL). Ship a .exe or .cmd shim
# instead for native Windows support.
cmd="$1"
[ $# -gt 0 ] && shift

case "$cmd" in
  %[2]s)
    echo "Hello, $*!"
    echo "format=${SOFIA_FORMAT} root=${SOFIA_PROJECT_ROOT}"
    ;;
  *)
    echo "%[1]s: unknown command '${cmd}'" 1>&2
    exit 1
    ;;
esac
`, name, exampleCommand)
}

// scaffoldReadme is the README.md written alongside a new plugin: just
// enough to test it locally and a pointer to the full authoring contract.
func scaffoldReadme(name string) string {
	return fmt.Sprintf("# %s\n\n"+
		"An sf plugin, scaffolded by `sf plugin new`.\n\n"+
		"## Try it locally\n\n"+
		"    sf plugin install ./%s\n"+
		"    sf %s %s world\n\n"+
		"Edit `%s` (the executable) and `plugin.yaml` (the manifest). `sf plugin\n"+
		"update` picks up a manifest change; re-run `sf plugin install ./%s` if you\n"+
		"rename the executable or otherwise change what's on disk.\n\n"+
		"## Distribute it\n\n"+
		"Push this directory to a git repo. Others install it with\n"+
		"`sf plugin install <git-url>` and upgrade with `sf plugin upgrade %s`.\n\n"+
		"Full authoring contract (invocation env, protocol versioning, capabilities):\n"+
		"docs/plugins.md in https://github.com/sofia-ctx/sofia, or the internal/plugin\n"+
		"package doc in that repo.\n",
		name, name, name, exampleCommand, name, name, name)
}

// scaffoldAdapterReadme is the README.md written alongside a new Tier-1 adapter:
// how to install it and what the host synthesizes, plus a pointer to the concept
// page.
func scaffoldAdapterReadme(name string) string {
	return fmt.Sprintf("# %[1]s\n\n"+
		"A Tier-1 sofia adapter, scaffolded by `sf plugin new --adapter`. It ships no\n"+
		"executable: the host reads the `adapter:` block in `plugin.yaml` and\n"+
		"synthesizes project-aware commands from it.\n\n"+
		"## Try it locally\n\n"+
		"    sf plugin install ./%[1]s\n"+
		"    sf %[1]s layers                 # list the declared layers\n"+
		"    sf %[1]s layers src/Domain/X    # classify a path into a layer\n"+
		"    sf %[1]s grep <pattern>         # search, grouped by layer\n"+
		"    sf %[1]s refs <symbol>          # defs/uses, grouped by layer\n\n"+
		"Run these from inside a project the adapter's `root_markers` identify (or set\n"+
		"its `root_key`, or pass `--root`).\n\n"+
		"## Edit the block\n\n"+
		"Open `./%[1]s/plugin.yaml` and edit the `adapter:` block — the root markers,\n"+
		"the extensions grep/refs scope to, and the layer globs. Re-run `sf plugin\n"+
		"update` after a change so dispatch sees it.\n\n"+
		"Full reference: docs/adapters.md in https://github.com/sofia-ctx/sofia.\n",
		name)
}
