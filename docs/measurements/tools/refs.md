# Token economy: `sf refs`

**Status: measured (tool-output token counts only)** — deterministic,
reproducible numbers from `sf refs`'s own footer and call log, on real
symbols in this repository. These are **not** agent-$ or turn measurements
(that needs a paid pilot — out of scope here); they're the same free,
deterministic `internal/tokens.Estimate` heuristic every other tool in
`docs/measurements/tools/` is measured with.

## What it does

`sf refs <symbol>` answers "who defines/uses this symbol across the tree,
and from where" in one call: a word-boundary literal scan (Go/PHP/TS/TSX/Vue
by default), every hit labeled `def`/`use` and its enclosing function/type.

## The baseline it replaces

`grep -rn <symbol>` gives bare `file:line:text` — no idea which function a
call site is in. The realistic manual alternative is `grep -rn` **plus**
opening every caller to read the enclosing function, which is a fan of
reads (exactly the pattern `sf grep` already displaces for the general
search case — see [grep.md](./grep.md)). `sf refs` folds both steps into
one call and additionally classifies each hit as a definition or a use.

## Reproducible local measurement

Built `sf`, then ran it against two real symbols in this repo's own Go
tree, capturing the footer line `sf refs` prints and the `tok_raw` field it
logs (the `grep -rn`-equivalent baseline `internal/tokens.Estimate` puts on
`file:line:text` for every hit, uncapped):

```
go build -o /tmp/sf ./cmd/sf
SOFIA_LOG_DIR=/tmp/sf-refs-measure /tmp/sf refs Footer
SOFIA_LOG_DIR=/tmp/sf-refs-measure /tmp/sf refs Enclosing
sf history --tool refs   # tok_raw lives in summary
```

| symbol | sf refs (footer) | tok_raw (`grep -rn` equiv.) | `grep -rn -w <sym>` line count | sf/raw |
|---|--:|--:|--:|--:|
| `Footer` | 490 | 336 | 16 | 1.46× |
| `Enclosing` | 1060 (capped at 30 of 34 hits) | 778 (all 34, uncapped) | 33 | 1.36× |

Both calls print `# sf ≈N tok · raw passthrough` — the honest branch of
`internal/emit.Footer`: `refs` didn't beat the raw baseline on these two
symbols, so it says so rather than claiming a save that isn't there.

## Why `sf refs` costs more than bare `grep -rn` here

Unlike `sf grep` (which compares against `grep -C2` and wins by eliminating
manual context lines — see grep.md), `sf refs`'s baseline here is the
*bare*, contextless `grep -rn`. `refs` always adds two columns bare grep
doesn't have — `kind` and `enclosing` — so on symbols with many short hits
it typically costs a bit more than the bare line-per-hit baseline. What it
buys instead is the elimination of the *next* step: opening each of those
16–34 call sites by hand to see what function they're in. That fan-of-reads
comparison is the one that shows the real win, and a paid agent-turn A/B now
measures it. On a task that needs every call site's enclosing context (map all
uses of `calllog.Counter`: pattern + deviations), an `sf`-equipped agent vs a
plain grep+read agent, sonnet, n=3 median: **18 vs 42 tool-call turns (−57%),
$0.56 vs $0.97 (−43%), and 587K vs 1.23M cache-read tokens (−52%)** — the
plain arm's grep-then-open fan re-reads its whole context each turn, which the
per-call footer here structurally cannot see. So the footer is honest about
one call's bytes but *undersells* `refs`; the value is the collapsed read-fan.
(High variance — the win is a median, not every run; both arms scored < 50 on
the hard rubric, so refs made the map cheaper and slightly more complete, not
correct. Full pre-registration + numbers: sofia-ctx/evaluation
`results/2026-07-11-refs-turn-collapse.md`.)

## A note on hit counts: occurrences, not lines

`sf refs Enclosing` reports 34 hits where `grep -rn -w Enclosing` reports 33
*lines* — one line (`internal/common/grep/scan.go:246`) uses the identifier
twice (a struct field named `Enclosing` assigned from a call to
`codectx.Enclosing(...)`), and `refs`, like `grep -on`, counts each
occurrence. Plain `grep -rn` counts matching lines once regardless of how
many hits are on it — a real discrepancy to know about when comparing counts
by hand.

## Boundary of applicability / honesty about the heuristic

- **Def/use classification is textual**, not type-checked: a line matching
  a language-appropriate declaration pattern for the symbol (`func Foo`,
  `class Foo`, `const Foo`, …) counts as a `def`; everything else is a
  `use`. A symbol declared in several places (or a common short name reused
  across packages) shows several defs — `refs` doesn't resolve which one a
  given call site actually binds to.
- **Enclosing labels are AST-derived for Go** (`internal/common/code/gocode.
  EnclosingDecls` — exact function/type signature, receiver included) but
  **regex heuristics for PHP/TS/TSX/Vue** (`internal/codectx`, the same code
  `sf grep` uses) — they can miss on unusual formatting, and TS/JS method
  shorthand (`foo() { ... }` inside a class, no `function` keyword) isn't
  recognised, so such a hit's enclosing falls back to the nearest outer
  `class`/`function`/`const` line instead.
- A comment line containing the symbol is indistinguishable from code to
  the matcher — it's counted as a `use` like any other textual hit.
- Binary files and lines over the scanner's buffer are skipped
  (`skipped=N`), not treated as a failure.
- Output is capped at 30 hits by default, defs first, then file, then line;
  `Defs`/`Uses` in the header are always the true totals over every hit
  found, even when the list below is capped — pass `--max 0` to widen or a
  negative `--max` to remove the cap.

## Reproduce

```
sf refs <symbol> [--root DIR] [--ext go,php,ts,tsx,vue]
sf history --tool refs --source agent   # tok_raw (the grep -rn equivalent) is written to summary
```
