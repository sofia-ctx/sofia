# Token economy: `sf composer ls`

**Status: measured** (on a real personal collection of 9 PHP packages).

## Methodology

- Heuristic — `internal/tokens.Estimate` (ASCII bytes/4 + non-ASCII
  runes×1).
- Baseline — what a model does by hand to survey a collection: `cat` every
  `composer.json` (in practice, also `git tag`/`describe` and a
  `phpstan.neon` grep per package — **not** counted here, so the real gap is
  larger than shown).
- `sf composer ls` — walks the tree (`internal/walker`), parses each
  `composer.json` plus its latest git tag and PHPStan level from
  `phpstan.neon` → one TOON row per package.

## Scenario: "survey the whole collection"

| | `cat */composer.json` (9 files) | `sf composer ls` |
|---|---|---|
| tokens | 3,032 | **415** |
| version (git tag) | — | ✅ |
| PHPStan level | — | ✅ |
| scripts / deps / dev | raw JSON | normalised |

Against reading 9 `composer.json` files — **~7.3×**, and that's without
counting the `git tag` and PHPStan greps that would otherwise also be run by
hand. (The row got heavier once require-dev got its own column, but it's
still far cheaper, and now carries dev dependencies the digest didn't have
before at all.)

## What it returns

Per line: `{pkg, version, type, php, phpstan, scripts, deps, dev}`. `deps`
are the real require dependencies, `dev` is require-dev; both exclude
`php`/`ext-*`. `--md`/`--json`. Default root is the current directory or a
positional argument.

## Boundary of applicability

- Version = the latest annotated git tag (`describe --tags`); packages
  don't carry a `version` field in `composer.json` by house rule. No tags →
  `—`.
- PHPStan level — a regex for `level:` in `phpstan.neon[.dist]`; a
  non-standard config (level in an included file) won't be picked up.
- Ignored: `vendor`, `node_modules`, `.git`. An invalid `composer.json` is
  skipped.

## Reproduce

```
sf composer ls /path/to/your/packages
sf history --tool "composer ls" --stats
```
