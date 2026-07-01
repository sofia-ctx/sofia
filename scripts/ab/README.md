# A/B: `sf` kit vs. a plain agent

Proves (or honestly bounds) the token economy of `sf` at the level of a
whole task: two agents in fresh, isolated sessions with no shared memory
solve the same tasks on the same real codebase; one arm is equipped with
`sf`, the other isn't. The measurement is **real billed tokens/cost** from
the result of `claude -p --output-format json`, not a heuristic. Output —
[`docs/measurements/evaluation/micro.md`](../../docs/measurements/evaluation/micro.md).

## Design

- **2 arms** (a 3rd is planned — see the roadmap):
  - `sf` (treatment): set up the way `sf` is actually launched for a
    project — the project's own agent instructions plus the sf-specific
    rules, the `sf hook pre` hook in suggest mode, the `sf-context` skill,
    `sf` on `$PATH`.
  - `plain` (control): the same domain (reads the project's own generic
    agent-instructions file), but **only** the standard Read/Grep/Glob/
    Bash; the preamble explicitly forbids `sf`; `SOFIA_HOOK_MODE=off`
    silences the nudges.
- **3 tasks**, chosen at the break-even boundary:
  - `t1_deal` — multi-file comprehension (favours `sf`).
  - `t2_product` — a small, single-file edit matching existing style
    (neutral).
  - `t3_phone` — add a method to one small value object (favours `sf`
    *not* winning: `cat` = one read, against `sf code` plus the body).
- **5 repeats** per (arm×task) → median + spread (LLMs aren't
  deterministic).
- Isolation: every run is a fresh, detached `git worktree` off a frozen
  `BASE_SHA` (a fixed default commit), removed afterward. The model is
  fixed across both arms (`MODEL`, sonnet by default) — otherwise the
  comparison isn't valid.

## Running it

```bash
# smoke (cheap): one task, 1 repeat per arm — sanity-check the pipeline and the price
REPS=1 TASKS=t1_deal bash scripts/ab/run.sh
bash scripts/ab/judge.sh
bash scripts/ab/aggregate.sh

# full run: 2 arms × 3 tasks × 5 = 30 sessions (real spend)
bash scripts/ab/run.sh && bash scripts/ab/judge.sh && bash scripts/ab/aggregate.sh
```

Artifacts land under
`runs/<arm>/<task>/<rep>.{json,diff,phplint,meta,verdict}` (gitignored).
`aggregate.sh` writes `runs/_records.jsonl` and prints a CSV summary.

## Metrics

- **Primary:** `cost_usd` and `billed_in` (= input + cache_read +
  cache_creation) up to task completion; medians are computed **only over
  runs that passed the judge**.
- Secondary: `cache_read`, `out`, `num_turns`, `wall_ms`.
- Quality gate: a judge (`judge.sh`) scores each run against a frozen
  rubric → `{pass,score}`. A cheap run that fails the rubric isn't a win.

## Caveats (for the write-up — honestly)

- Small N, one domain (one production codebase), one operator → a trend,
  not a law.
- "Treatment" = sf-availability **plus** the nudges, together; only a
  planned 3rd arm would separate them.
- The model is fixed (sonnet) to control cost; opus, with a larger context,
  would likely widen the gap (more `cache_read` to amortise). That's a
  hypothesis, not a result.
- Runs are **guarded by default**: tools are restricted to an allowlist (no
  arbitrary shell), edits happen only inside a disposable worktree.
  `PERM=bypass` enables `--dangerously-skip-permissions` (faster, no
  guardrail) — opt-in only.
- Treatment runs write real `sf` calls into the shared `calls.jsonl` (as
  expected); this doesn't affect the measurement, which comes from
  `claude`'s own result, not from `sf`'s own telemetry.
