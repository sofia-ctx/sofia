# Macro A/B: does the `sf` kit pay off on a *real* feature?

> Status: **partial, but honest.** The full matrix was designed, but the
> opus configs (A, B) ran into **Anthropic's session limit** — exactly the
> failure mode the harness is built to catch (via `limited()`) and resume
> after a reset. So: **config C (sonnet/sonnet) is a complete n=3** (the
> backbone of this write-up), **config A (opus) is an n=1 probe**,
> **config B and the remaining repeats were cut short**, pending resume.
> Numbers come from the harness's own run log (plus the raw plan capture for
> the probe run).

A sequel to the micro A/B (`micro.md`). There, the finding was blunt and
against the pitch: on small, synthetic tasks, `sf` **doesn't reduce
tokens** (+35…91% volume), and the dollar win only shows up on navigation,
purely from shifting volume into cheap `cache_read`. This is a check on a
**real, substantial feature**, under a realistic **"plan (opus, ultrathink)
→ implement (auto)"** flow, with objective, functional judging.

## What's new versus the micro test

1. **A real feature, not a synthetic task:** "live boards over a
   Mercure-based pub/sub channel" — a real merged pull request from the
   target codebase's history (35 files changed, +986/−120).
2. **Two phases, measured separately:** PLAN (no edits, just a plan) and
   IMPLEMENT (edits following the plan). Hypothesis: `sf` should help the
   navigation-heavy **plan phase** more than the small tasks showed.
3. **Objective judging:** not a rubric, but **the PR's own tests as the
   spec**, plus the project's CI gate (style linter + static analysis at
   its strictest level + the unit/integration suite). Pass = everything
   green.
4. **A sweep across models:** measuring model cost alongside tool cost
   (opus vs. sonnet, per phase). *What actually completed:* only config C
   ran to completion; A is a single probe run; B was cut short by the
   session limit. The sweep is partial, but even the probe is informative
   (see below).
5. **The `sf` arm got a new lookup tool**: finding every call site of a
   symbol (e.g. "where does a new broadcaster need to be wired in") in one
   call — 7 handlers in one shot — aimed squarely at this task's plan
   phase.

## Setup

- **Task:** implement live boards over a Mercure-based pub/sub channel, on
  the codebase at the commit right before that feature was merged. Spec —
  the tests attached to the real PR: a broadcaster test, a subscription
  test, and a test support double for the pub/sub hub. Navigation is kept
  real: the agent has to find and instrument the existing command handlers
  itself (assign / close / complete / release / take-next, across the
  `Deal` workflow).
- **2 arms:** `sf` (its instructions plus a usages-lookup tool, a
  structural-code tool, and a schema tool, hook on) vs. `plain` (Read/Grep/
  Glob/Edit, hook off, a generic agent-instructions file).
- **2 phases:** PLAN → IMPLEMENT, measured separately.
- **3 model configs:** A = opus-xhigh for planning / sonnet for
  implementation (the realistic setup); B = opus for both phases; C =
  sonnet for both. A↔B isolates the implementation model, A/B↔C isolates
  the planning model.
- **3 repeats, order-counterbalanced** (odd repeats: sf→plain; even:
  plain→sf) — controls for ordering/cache warm-up (the confound from the
  micro test).
- **Isolation:** one disposable fork (a docker stack: web server + Postgres
  + Mercure), a hard reset to the base commit plus re-applying the test
  files on every run; fresh sessions, no `--resume`.
- **Measurement:** exact billed `usage`/`total_cost_usd` from
  `claude -p --output-format json`, per phase. Quality gate: the CI-gate
  exit code plus the feature tests' exit code; cost is only compared among
  runs that passed.

Harness/protocol: a macro sibling of `scripts/ab/`, dry-run validated.

## Hypotheses (verdicts)

- **H1.** `sf` helps the **plan phase** more. **Split — refuted on
  sonnet.** On C, the plan phase with `sf` is actually *more expensive*
  (+18% $, +15% turns) — the opposite of the hypothesis. On A (opus, n=1),
  the plan phase with `sf` is **−39%**: the win only shows up on a
  large-context model — exactly what the micro test's cache-read-shift
  story predicted.
- **H2.** The effect of `sf` is smaller in the implement phase.
  **Reframed / inconclusive.** On C, the entire modest net win sits in
  **implementation** (−10%), not in planning; on A, implementation isn't
  comparable (the `sf`-arm's cost capture for that phase was lost).
- **H3.** The choice of **model** dominates the choice of **tool**, by
  dollars. **Confirmed (directionally).** Same plan phase: opus is
  **≈5×** more expensive than sonnet; the gap between arms within a config
  is ≤18%.
- **H4.** Both arms reach green → quality parity. **With a caveat.** On C:
  plain **3/3**, `sf` **2/3** (one hard failure). On A: 1/1 for both.
  There's no clean parity — `sf` had a real reliability miss.

## Results

### Pass rate (CI gate + feature tests)

| config | arm | pass | | config | arm | pass |
|---|---|---|---|---|---|---|
| C (sonnet/sonnet) | plain | **3/3** | | A (opus/sonnet) | plain | 1/1 |
| C | sf | **2/3** | | A | sf | 1/1 |
| B (opus/opus) | sf | — cut short (session limit) | | B | plain | — not run |

The `sf` failure is **C/sf-1**: the CI gate came back red (two style-linter
violations in newly added files) and one endpoint returned **500 instead of
200**. Not "passed a different way" — it simply didn't get there.

### Cost by phase (median $, passing runs only)

| config | arm | plan $ | impl $ | total $ | plan turns | impl turns |
|---|---|---:|---:|---:|---:|---:|
| C (sonnet/sonnet) | plain | 1.17 | 2.61 | **3.79** | 58 | 101 |
| C | sf | 1.38 | 2.36 | **3.74** | 66 | 96 |
| A (opus/sonnet) | plain | 5.91 | 3.71 | **9.62** | 69 | 97 |
| A | sf | **3.62** | n/a¹ | ≥3.62 | 64 | — |
| B (opus/opus) | — | cut short by the session limit (`B/sf-1` burned $10.12, errored out, ungraded) | | | | |

> ¹ `A/sf-1` passed (the feature was built, tests passed), but its impl-phase
> cost capture came back empty — a 0-byte file. The plan phase is reliable;
> impl and total are not. So this cell is excluded from the aggregated run
> log (an empty JSON payload breaks the aggregation step).

### Deltas

- **sf vs. plain, by phase.** On **C** (sonnet): plan $ **+18%** (`sf` is
  more expensive), impl $ **−10%**, total **−1.4%** (essentially a wash);
  turns: plan +15%, impl −5%. On **A** (opus, n=1): the plan phase with
  `sf` is **−39%** ($3.62 vs $5.91) — the win only appears on a large
  context.
- **Model sweep.** Same plan phase, opus vs. sonnet: plain is **5.0×**
  more expensive ($5.91 vs $1.17), `sf` is 2.6× ($3.62 vs $1.38). The gap
  between model configs (×2.6–5) is **much larger** than the gap between
  arms (≤18% on C). A↔B (the implementation-model comparison) and the
  remaining repeats weren't completed — cut short by the session limit.

## Smoke test (a confirmed fact ahead of the full matrix)

A calibration run (sonnet, `sf` arm) **built a working feature**: the CI
gate came back green, feature tests passed **8/8 (27 assertions)**, 10
files changed, the correct handlers instrumented. Cost: plan $1.65 (74
turns) plus implementation $1.89 (85 turns) = **$3.54**. In other words, a
sonnet agent following a plan→implement flow can deliver a 35-file feature
against an objective gate — the pipeline and the macro-level thesis both
work; the matrix exists to quantify sf-vs-plain and model choice on top of
that. This calibration figure of $3.54 is now **confirmed by the complete
C/sf n=3** (passing runs: $3.07 and $4.40, median $3.74): the probe wasn't a
fluke.

## Analysis

The main, against-the-pitch finding repeats the micro test
([`micro.md`](micro.md)), now on a **real feature**: **`sf` is not a
"savings" story in the plan phase.** On sonnet (the complete config C), the
plan phase with `sf` is *more expensive* than plain (+18% $, +15% turns):
instructions that push the agent toward splitting things into small calls
cost more when there isn't opus-level context and a large cache to amortise
against. Net across the whole task, it's a **wash** (−1.4%, inside the
cache-warm-up noise), and the modest net gain that does exist comes from the
implementation phase (−10%), not from the navigation work `sf` was built
for.

Where `sf` genuinely pays off is on **opus** (the A probe): the plan phase
is **−39%** ($3.62 vs $5.91). A large context plus extended thinking inflate
volume, and `sf` shifts that volume into cheap `cache_read` — this is where
structural reading finally earns its keep. This is n=1, so it's a
**signal, not proof** — but it points exactly the way the micro test
predicted.

And on top of all of it — **the model choice dominates the tool choice**
(H3): the same plan phase costs ×5 going from sonnet to opus, while
sf-vs-plain moves the needle by ≤18%. Anyone seriously trying to cut cost
should pick the model/effort per phase first, and the tool second. On
quality: not a clean parity — `sf` scored **2/3** on C (a style-lint
failure plus one 500 response), plain scored **3/3**.

## What we're changing in `sf` (feedback from the data)

1. **Plan-phase nudges don't pay off on sonnet.** The "structure → narrow
   search → point-read" split costs +18% $ and +15% turns on a sonnet plan,
   with no offsetting win. The lever `sf` actually needs is **opus-scale
   context**, not the default model — the hook/instruction threshold should
   key off model/context size rather than nudging unconditionally (the same
   direction as the localized-task threshold from the micro test). Fix →
   the hook package plus the skill.
2. **The usages-lookup tool is over-sold for the plan phase.** "7 handlers
   in one call" is a nice pitch, but on a sonnet plan it didn't outweigh the
   overhead of splitting work up. Don't cut it — but the instructions should
   position it as a tool for **navigating many files on large models**, not
   as "always cheaper than grep."
3. **`C/sf-1`: a style-lint failure plus a 500 response is a correctness
   signal, not a token one.** One of three `sf` runs produced code that
   didn't pass the gate. This is about how reliably the instructions guide
   the actual wiring work, and it's a candidate for a closer look at
   regressions.

These are **write-up recommendations**, not code changes: the actual
instruction/skill updates and the roadmap follow-up are tracked separately.

## Caveats (honestly)

- **The matrix is incomplete.** A full n=3 only exists for **C** (sonnet).
  A is an **n=1 probe** (opus); B and the remaining repeats were **cut
  short by Anthropic's session limit** (`B/sf-1`: hit a session reset,
  $10.12 spent, errored out). The opus-based findings (H1's win, H3) are
  directional, from n=1.
- **`A/sf-1`'s implementation-phase cost capture was lost** (an empty
  file): its impl and total costs aren't comparable, only its plan phase
  is trustworthy.
- One feature, one domain → a trend, not a law, even for config C.
- Judging is objective (real tests), but an agent could pass them by a
  different route than the reference PR; an optional LLM judge comparing
  against the reference diff is a possible follow-up.
- "Treatment" = sf-availability **plus** the nudges, together; a third arm
  (sf without nudges) would separate them.
- Cost of a full run: roughly $250–330, docker + opus, multi-hour — and it
  runs into session limits, so completing A↔B only happens in installments
  after resets.

## Reproduce

```bash
<path-to-target-repo>/dev/worktree.sh new abm <base-commit>
CONFIGS="C A B" REPS=3 bash scripts/ab-macro/run-macro.sh
bash scripts/ab-macro/aggregate-macro.sh   # → tables + a run log
```
On hitting the session limit, the harness exits with code 2 ("resume after
reset") — after the reset, rerun with the remaining `CONFIGS`/`REPS` and run
`aggregate-macro.sh` again. Every number traces back to the run log (the
`A/sf` plan probe traces to its own raw plan capture, since its empty impl
capture excludes that cell from the aggregated log).
