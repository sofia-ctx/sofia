# Token economy: `sf github ci`

**Status: measured** (on a personal collection of PHP packages, repository
`array-reader`).

## Methodology

- Heuristic — `internal/tokens.Estimate`.
- Baseline — `gh run list` (listing) and `gh run watch` (waiting for a run).
- `sf github ci` — a wrapper over `gh run list/view` with TOON output;
  `--watch` polls the latest run to completion and prints **only** the final
  row.

## Scenario A: listing recent runs

| | `gh run list --limit 5` | `sf github ci --limit 5` |
|---|---|---|
| tokens | 166 | ~150 |

Here the token gain is **≈1×** — honestly: `gh run list` is already
compact. The listing's value isn't in tokens, it's in TOON (scriptable, one
format shared with the rest of `sf`) and resolving a package by name under
`--root`.

## Scenario B: waiting for a run (`--watch`) — this is where the ROI is

`gh run watch` streams live status: a spinner plus a line per job, dozens of
lines per run. `sf github ci <pkg> --watch` polls silently and prints one
final row, `{id, …, conclusion}`, plus a non-zero exit code on failure (like
`gh run watch --exit-status`). For a multi-minute run that's dozens of lines
of noisy streaming against **one row** (~30 tokens) — an order-of-magnitude
gain.

## What it returns

Per line: `{id, workflow, status, conclusion, branch, event, created,
title}`. The target is a package (by name/dir under `--root`) or the
current repository. `--limit N`, `--watch`, `--timeout`. When the target is
a tree of packages (the root isn't a git repo itself), the output is a
per-package rollup (a `pkg` column, each package's latest run, `# failing=K`
in the header): one call instead of looping `for d in tree/*; do sf github
ci …` by hand — N per-package `gh run list` calls collapse into one TOON
block.

## Boundary of applicability

- Requires `gh` (authenticated) and network access. An error/timeout is
  returned as a command error.
- `--watch` polls roughly every 8s until `completed` or `--timeout`
  (default 15m); returns a non-zero exit code if the run didn't finish
  `success`.

## Reproduce

```
sf github ci array-reader --root /path/to/your/packages
sf github ci enum --root /path/to/your/packages --watch
sf history --tool "github ci" --stats
```
