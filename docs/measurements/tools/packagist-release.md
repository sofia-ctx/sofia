# Token economy: `sf packagist release`

**Status: an action tool** (mutating); the savings aren't in a TOON output,
they're in folding a multi-step recipe into one command and removing the
need to re-read it from memory.

## What it is

`sf packagist release <pkg> <version>` publishes one package: an annotated
tag (reusing an existing one) → `git push origin <tag>` → Packagist's
`update-package` webhook (doesn't fire for every package) → polling
`p2/<pkg>.json` until the version shows up. Token: `$PACKAGIST_API_TOKEN`,
falling back to a per-vendor dotfile under `~/.config/<vendor>/packagist.env`.

## Methodology / value

Unlike the read tools (`composer ls`, `packagist status`), this is an
**Agentic Tool**: its value isn't measured as an output ratio, it's measured
by the repeated manual work and risk it removes.

- Baseline: a model re-reads a ~4.6 KB publishing runbook from memory
  (~1,150 tokens), then composes and runs 3–4 commands (tag, push, curl
  update-package, curl p2-verify), parsing their output. Per package,
  that's ~1.2–1.5k tokens just to "recall and assemble," plus the risk of
  confusing `create`/`update`, forgetting the SSH remote, or not waiting
  for the crawl.
- The tool: one call, a compact summary `{tag_created, tag_pushed,
  packagist_updated, verified, packagist}` plus the steps taken (~80–120
  tokens). The runbook no longer needs to live in memory — it's encoded.

## Safety

- **Mutates and publishes**: pushes a tag to a public repository and
  triggers a Packagist publish. Outward-facing, hard to undo.
- `--dry-run` prints every step without changing anything (the token is
  masked as `***`) — **run this first**.
- Preflight: requires a clean working tree (otherwise an error;
  `--allow-dirty` overrides).
- The version is validated as semver; an existing tag is reused, not
  recreated.
- The token is never inlined into logs/output (the call log records only
  `pkg`+`version`).

## Boundary of applicability

- `verified=false` doesn't mean failure: Packagist crawls asynchronously,
  the version can appear after the timeout — recheck with
  `sf packagist status`.
- The GitHub push uses the normal git/SSH remote; the Packagist token is
  only used for `update-package`.

## Reproduce

```
sf packagist release array-reader 2.1.0 --dry-run
sf packagist release array-reader 2.1.0
sf packagist status /path/to/your/packages         # confirm in-sync
```
