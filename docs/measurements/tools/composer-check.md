# Token economy: `sf composer check`

**Status: measured** (on a personal collection of PHP packages, package
`array-reader`).

## Methodology

- Heuristic — `internal/tokens.Estimate`.
- Baseline — `composer check` directly: the full phpunit + phpstan +
  php-cs-fixer output, which a model reads in full to learn "green, or
  where did it fail."
- `sf composer check` — runs the same script in each package and collapses
  the output to a single pass/fail row plus the first error line.

## Scenario: "run the gate and find out the result"

| | `composer check` (raw, 1 package) | `sf composer check <pkg>` |
|---|---|---|
| tokens | 436 | **~25** |
| pass/fail | has to be read out | ✅ explicit |
| first error | has to be searched for | ✅ surfaced |

Against the raw output — **~17×** per package. Scales linearly: across all 9
packages, the baseline is ~3,900 tokens (9 verbose runs) against ~120 tokens
for the tool (one row per package) → **~30×**.

## What it returns

Per line: `{pkg, status, exit, dur_ms, fail}`; `status` = ok | FAIL | skip
(no `check` script). A header line: `# failed=N`. With an argument — one
package; without — every package under the root.

## Boundary of applicability

- It actually runs `composer check` (test+phpstan+cs) — this is a **live
  run**, not static analysis; requires `composer` and an installed vendor
  directory. Time = the real time the gate takes (~5s per package in the
  example).
- On failure it shows the first line that looks like an error (a heuristic
  on `fail/error/exception/✗/…`), otherwise the last non-empty line. For
  the full output, run `composer check` inside the package directory
  yourself.

## Reproduce

```
sf composer check array-reader --root /path/to/your/packages
sf composer check --root /path/to/your/packages          # all 9
sf history --tool "composer check" --stats
```
