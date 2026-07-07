package pack

import (
	"fmt"
	"os"
	"path/filepath"
)

// Scaffold creates a new pack skeleton at filepath.Join(parentDir, name): a
// pack.yaml with one active instructions entry plus a commented example of
// every other section, a sample instructions/AGENTS.md the active entry
// resolves against, and a README. It is the `sf pack new` implementation.
// parentDir defaults to "." when empty. The target directory must already be
// absent — Scaffold never overwrites.
//
// The generated pack.yaml validates and installs as-is: `sf pack install
// <dir>` lays out AGENTS.md without any edits required first.
func Scaffold(name, parentDir string) (string, error) {
	if !nameRe.MatchString(name) {
		return "", fmt.Errorf("pack.yaml: invalid name %q (must match %s)", name, nameRe.String())
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

	if err := os.MkdirAll(filepath.Join(dst, "instructions"), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dst, manifestFile), []byte(scaffoldManifest(name)), 0o644); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dst, "instructions", "AGENTS.md"), []byte(scaffoldAgents(name)), 0o644); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dst, "README.md"), []byte(scaffoldReadme(name)), 0o644); err != nil {
		return "", err
	}
	return dst, nil
}

// scaffoldManifest is the pack.yaml written for a new pack: one active
// instructions entry (resolving against scaffoldAgents below) plus a
// commented example of every other section, so an author sees the whole
// shape without pack.yaml failing to parse.
func scaffoldManifest(name string) string {
	return fmt.Sprintf(`# pack.yaml -- sofia's pack manifest. Full authoring contract (every
# section, the install/uninstall contract, path safety rules): the
# internal/pack package doc in https://github.com/sofia-ctx/sofia.
schema: %d
name: %s
description: "TODO: describe %s"

# plugins are sf plugins this pack installs globally (exactly one of path/git
# per entry):
# plugins:
#   - path: plugins/my-plugin
#   - git: https://github.com/o/r.git
#     ref: v1.0.0

# instructions land at the target project's root (default dest:
# filepath.Base(src); a directory copies recursively).
instructions:
  - src: instructions/AGENTS.md
    dest: AGENTS.md
#  - src: instructions/backend
#    dest: .agents/backend

# claude groups Claude Code skills/commands -- dest is never settable: a
# skill lands at skills/<basename(src)>/, a command at
# commands/<basename(src)>.
# claude:
#   skills:
#     - src: skills/my-skill
#   commands:
#     - src: commands/my-command.md

# templates land at the target project's root, shaped exactly like
# instructions.
# templates:
#   - src: templates/example.md
#     dest: example.md
`, ManifestSchema, name, name)
}

// scaffoldAgents is the sample instruction file the manifest's active
// instructions entry resolves against.
func scaffoldAgents(name string) string {
	return fmt.Sprintf(`# %s

TODO: replace this with the instructions this pack should install at a
target project's AGENTS.md.
`, name)
}

// scaffoldReadme is the README.md written alongside a new pack: just enough
// to test it locally and where to take it from here.
func scaffoldReadme(name string) string {
	return fmt.Sprintf("# %s\n\n"+
		"A pack: a git repo or local directory holding a pack.yaml that bundles\n"+
		"sf plugins, Claude skills/commands, and project instructions/templates\n"+
		"for `sf pack install` to lay out in one shot.\n\n"+
		"Test it: `sf pack install ./%s --project /some/project`\n\n"+
		"Edit `pack.yaml` to add plugins, skills/commands, or templates -- every\n"+
		"section is sketched (commented) in the manifest already. Distribute by\n"+
		"pushing this directory to a git repo; others install with\n"+
		"`sf pack install <git-url>`.\n\n"+
		"Full authoring contract: the internal/pack package doc in\n"+
		"https://github.com/sofia-ctx/sofia.\n",
		name, name)
}
