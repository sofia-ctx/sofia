# Token economy: `sf grep`

**Status: measured** — systematically across query archetypes on real
trees (a production PHP/Symfony codebase, and sofia's own Go tree), plus one
point case on a separate codebase.

## Methodology

- Heuristic — `internal/tokens.Estimate`.
- Two baselines over the same scope (same extensions and the same
  `vendor/var/.git/…` ignores):
  - **`grep -rn`** — bare `file:line:text`, the cheapest possible raw
    search;
  - **`grep -rn -C2`** — with ±2 lines of context: a more realistic
    baseline, since a bare hit usually still needs context to make sense of
    it (otherwise you're reading the rest of the file anyway).
- Matching is aligned: substrings use `sf grep --word=false` against
  `grep -F`; regex uses both tools in regex mode.

## Systematic measurement (`sf grep` vs `grep`)

| query | sf | `grep -rn` | `grep -C2` | sf/grep | sf/grep-C2 |
|---|--:|--:|--:|--:|--:|
| app `CompleteTask` | 922 | 785 | 2638 | 1.17 | **0.35** |
| app `AsMessageHandler` | 3127 | 3362 | 12715 | 0.93 | **0.25** |
| app `final class` | 1645 | 1826 | 8258 | 0.90 | **0.20** |
| app `public function` | 13921 | 13578 | 53391 | 1.03 | **0.26** |
| app `#[Route(` (regex) | 2137 | 2108 | 7707 | 1.01 | **0.28** |
| sofia `func Parse` (1 hit) | 39 | 21 | 90 | 1.86 | 0.43 |
| sofia `Tracker` | 270 | 247 | 945 | 1.09 | **0.29** |
| sofia `calllog` | 1792 | 1744 | 6172 | 1.03 | **0.29** |

"app" rows are from a production Symfony + Doctrine codebase; "sofia" rows
are from this repository's own Go tree.

## Conclusion

- **Roughly at parity with bare `grep -rn`** (0.90–1.17×) on multi-hit
  queries: the enclosing function/class attached to every hit costs
  approximately nothing in tokens, while giving strictly more information.
- **2.3–5× cheaper than `grep -C2`** — which is the realistic alternative:
  one enclosing line (a function/class signature) replaces ±2 lines of
  context per hit without duplicating them.
- **The default for searching code.** Bare `rg`/`grep` still make sense for
  trivial one-off "does this string even exist" checks with few hits, where
  the fixed TOON overhead is a slight net loss (e.g. one hit: 39 vs 21
  tokens — negligible either way).

## Default cap `--max-per-pattern 30`

The table above is about the economics **per hit** (parity with `rg`,
cheaper than `-C2`). But the actual agent-side cost wasn't the price per
hit, it was **how many hits**: 15 calls accounted for 79% of all grep tokens
in the log (`public function` alone logged 13,952 tokens). So the default
`--max-per-pattern` is now **30** (it used to be `0`, unlimited); the rest is
marked `# +N more truncated`, and `0` remains the escape hatch for "every
hit."

**Measurement (same call, HEAD binary vs working tree):**

| query | before (cap 0) | after (cap 30) | ratio |
|---|--:|--:|--:|
| `public function --ext php` | 30,265 | **991** | **30.5×** |
| `AsMessageHandler --ext php` | 5,220 | **1,116** | **4.7×** |
| ≤30 hits / no match | = HEAD | = HEAD | 1.0× (cap doesn't fire) |

`--max-per-pattern 0` returns the full output byte-for-byte (the escape
hatch is verified). The cap doesn't touch enclosing context — the hits that
are shown keep their context (a deliberate choice: `emit.SmallerOf` against
the raw projection was **not** applied here, since TOON is already near
parity with `rg`; a blind size guard would degrade `grep` into `rg`, cutting
the enclosing context exactly where it matters most on low-hit queries;
`raw_tokens` stays available as a metric in `summary`).

## Point case: enclosing context instead of manual windows

"Where is `gravitySettings` defined/used, and in which functions" — on
another, separate codebase:

| Step (without the tool) | tokens |
|---|---|
| `rg -n gravitySettings src/` | 118 |
| targeted reads around 4 hits (enclosing fn) | 777 |
| **Baseline total** | **895** |

**With the tool**: `sf grep gravitySettings --ext php` = **161 tokens** →
**5.6×**.

## Boundary of applicability

- For "does this just occur anywhere," with no need for enclosing context,
  bare `rg` is slightly cheaper — use it for trivial checks.
- `enclosing` is a heuristic (`internal/codectx`, regex-based detection of
  the surrounding construct), not an AST; it can miss on unusual formatting
  (especially for Go — it's tuned for PHP/TS/Twig/INI).
- Binary files or files over 4 MB are skipped (`skipped=N`), not treated as
  a search failure.

## Reproduce

```
sf grep <pattern> --ext php | <a token counter using internal/tokens.Estimate>
sf history --tool grep --source agent   # raw_tokens (the grep equivalent) is written to summary
```
