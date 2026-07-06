// Package pack implements sf's pack-authoring contract: a pack is a git
// repository or local directory holding a pack.yaml manifest plus the
// artifacts it bundles. A pack can carry all four kinds of thing a project
// might want to adopt in one shot:
//
//   - plugins       sf plugins (see internal/plugin), bundled in a
//     subdirectory of the pack or referenced by a separate git URL.
//   - claude        Claude Code skills and slash commands.
//   - instructions  agent instructions (AGENTS.md and friends) for the
//     target project's root.
//   - templates     arbitrary project-root files/directories, shaped exactly
//     like instructions.
//
// `sf pack install` reads the manifest and lays each artifact onto the shelf
// it belongs to:
//
//	plugins        → $XDG_DATA_HOME/sofia/plugins/<name>/ (plugin.Install /
//	                 plugin.InstallFromGit — see internal/plugin)
//	claude          → $CLAUDE_DIR (env override; default ~/.claude):
//	                 skills/<basename(src)>/, commands/<basename(src)>
//	instructions,
//	templates       → the target project's root (default: cwd)
//
// instructions and templates are plain files — no Claude-specific hook is
// required to benefit from them, so a pack works unmodified in, say, a
// Codex-driven repo that never touches $CLAUDE_DIR. The claude block is
// entirely optional.
//
// # Manifest
//
// pack.yaml sits at the pack's root (see manifest.go for the exact shape).
// Parsing is non-strict — unknown top-level keys are ignored, the same
// forward-compatibility stance as plugin.ParseManifest — so a pack.yaml
// written for a newer sf still parses on an older one. Every declared path is
// validated through safeRel: an absolute path, or one that still climbs above
// its root after filepath.Clean, is rejected outright rather than silently
// escaping the pack or its target shelf. Manifest paths are always "/"-
// separated (Windows groundwork); Validate converts them with
// filepath.FromSlash before any of them touch the filesystem.
//
// Reading a pack's source files follows symlinks the ordinary way
// (os.ReadFile/filepath.WalkDir don't distinguish them): a symlink inside a
// pack's source tree can pull content from outside it into the install. This
// is documented rather than guarded here; a hard rejection can follow if it
// ever matters in practice.
//
// # State: canon + receipt
//
// Two things persist per installed pack, both under
// $XDG_DATA_HOME/sofia/packs/:
//
//	<name>/                 a canonical copy of the pack's own source tree
//	                        (.git stripped) — what `sf pack list`/`info` read
//	                        the description from, and what a later reinstall
//	                        re-derives its plugin/claude side effects from.
//	.receipts/<name>.json   what Install actually wrote: source provenance,
//	                        the plugins/claude files it placed globally, and
//	                        a per-project map of the files it placed there
//	                        (see receipt.go for the exact shape).
//
// # Install: plan, then check, then write
//
// Install resolves the source (shallow git clone or a local directory),
// parses and validates the manifest, and expands every FileMap into concrete
// file writes *before* touching disk. It then checks every one of those
// writes against the receipt and the filesystem: a destination that doesn't
// exist yet, or already holds exactly the planned content, or holds exactly
// what the receipt last recorded there, is safe to (re)write. Anything else —
// a file the pack doesn't own, or one edited since install — is a conflict,
// and *every* conflict is collected and reported together (with --force as
// the escape hatch) before a single byte is written. That ordering is the
// whole point: a partially-applied install (say, plugins landed but a
// project file conflicted) would be worse than refusing up front.
//
// Only once the plan is clear does Install apply it: plugins first (then
// exactly one plugin.Update() — never one per plugin, or `sf plugin list`
// goes stale mid-batch), then claude files, then project files, then the
// canon copy, then the receipt.
//
// # Uninstall
//
// Uninstall is the mirror image, gated the same way: a file whose on-disk sha
// still matches the receipt is removed (and its now-empty parent directories
// pruned up to the project root); anything else is left in place with a
// warning. Once no project references a pack any more, its global footprint
// (claude files, plugins, canon copy, receipt) is torn down the same way;
// while other projects still reference it, the globals and the receipt stay.
package pack
