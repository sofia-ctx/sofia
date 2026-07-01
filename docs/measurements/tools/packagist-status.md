# Token economy: `sf packagist status`

**Status: measured** (on a personal collection of 9 PHP packages, real
network calls).

## Methodology

- Heuristic — `internal/tokens.Estimate`.
- Baseline — a manual release-status check: per package, `git describe` +
  `git ls-remote` + `curl` against Packagist's `p2/<pkg>.json` (each p2
  document is 300+ tokens) + parsing out "what's the latest version." Plus,
  in practice, re-reading a multi-KB publishing runbook from memory.
- `sf packagist status` — the same check, deterministically → one row per
  package.

## Scenario: "what still needs releasing / updating on Packagist"

| | manual check (9 packages) | `sf packagist status` |
|---|---|---|
| tokens (p2 documents only) | ~2,700 | **118** |
| local tag vs Packagist | by hand | ✅ |
| tag pushed? | a separate `ls-remote` | ✅ `pushed` |
| verdict | by hand | ✅ `state` |

Against parsing 9 p2 documents — **~23×**, and that's without counting the
`git` calls or re-reading the publishing runbook.

## What it returns

Per line: `{pkg, local_tag, pushed, packagist, state}`; a header `# drift=N`.
`state` = in-sync | needs-update (Packagist is behind) | unpublished |
no-tags | local-stale (Packagist is ahead of the local tag — needs a
`git pull`). `--offline` — local tags only, no network.

## Boundary of applicability

- Network: an HTTP GET to `repo.packagist.org/p2` (404 = unpublished) plus
  `git ls-remote origin` for `pushed` (best-effort, `GIT_TERMINAL_PROMPT=0`,
  timeout; unreachable → `pushed=?`). Read-only, publishes nothing.
- Version comparison is numeric on dotted parts (a leading `v` is stripped,
  pre-release suffixes are ignored), stable versions only (dev/alpha/beta/rc
  discarded).
- The mutating release flow (`packagist release` = tag+push+update+verify)
  is **not** covered here — see
  [packagist-release.md](packagist-release.md).

## Reproduce

```
sf packagist status /path/to/your/packages
sf packagist status --offline /path/to/your/packages
sf history --tool "packagist status" --stats
```
