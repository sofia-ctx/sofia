# Token economy: `sf changed`

**Status: measured** (on sofia's own repository history).

## Methodology

- Heuristic — `internal/tokens.Estimate`.
- Baseline — `git diff <range>`: what a model reads in full to understand
  "what changed and where." The alternative `git diff --stat` is compact,
  but only gives files+churn, with no classification and no touched
  symbols.
- `sf changed` — git plumbing (`--name-status` + `--numstat` + `-U0` for hunk
  headers) turned into a classified TOON summary. Touched functions/classes
  come from git's **own** funcname context (`@@ … @@ <func>`), without
  parsing any files.

## Scenario: "what changed in this range"

| | `git diff HEAD~3` | `git diff --stat` | `sf changed HEAD~3` |
|---|---|---|---|
| tokens | 22,706 | 442 | **469** |
| classification (cat/lang) | — | — | ✅ |
| touched symbols | (the whole diff) | — | ✅ |
| per-category totals | — | — | ✅ |

Against the full `git diff` — **~48×**. At roughly the size of `--stat`, it
gives what `--stat` doesn't: category (source/test/config/docs/build/
migration) plus language, the functions/classes touched per file, and
per-category totals.

## What it returns

Per line: `{status, cat, +adds, -dels, path, symbols}`, plus `totals` and
`by_category`. Modes: working tree vs HEAD (+ untracked), `--staged`, an
arbitrary `[range]`. `--no-symbols` — files/churn only.

## Boundary of applicability

- **Symbols come from git's funcname context** (its built-in drivers for
  Go/PHP/…): a heuristic — "nearest declaration before the hunk" — not an
  exact AST; it occasionally picks up a line inside a multi-line literal.
  Only shown for recognised code files; suppressed as noise for docs/config.
- Untracked files (in working-tree mode) are listed with status `untracked`
  and a line count, without a diff or symbols.
- Source is git; needs a git repository (`--root` or the current directory).

## Reproduce

```
sf changed                 # working tree vs HEAD
sf changed HEAD~3          # last 3 commits
sf changed main..HEAD --md
sf history --tool changed --stats
```
