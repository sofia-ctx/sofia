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

// Scaffold creates a new plugin skeleton at filepath.Join(parentDir, name): a
// plugin.yaml manifest, an executable POSIX-sh stub, and a README. It is the
// `sf plugin new` implementation. parentDir defaults to "." when empty. The
// target directory must already be absent — Scaffold never overwrites.
//
// The generated manifest declares protocol: HostProtocol and a runnable
// executable, so it is installable as-is: `sf plugin install <dir>` parses
// and enables it without any edits required first.
func Scaffold(name, parentDir string) (string, error) {
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
	if err := os.WriteFile(filepath.Join(dst, manifestFile), []byte(scaffoldManifest(name)), 0o644); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dst, name), []byte(scaffoldExec(name)), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dst, "README.md"), []byte(scaffoldReadme(name)), 0o644); err != nil {
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
