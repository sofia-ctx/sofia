# A/B: does `sf` earn its tokens? A live measurement

`sf` gives an AI coding agent compact, structured views of a codebase — a
file's shape, where a symbol is used — instead of raw file dumps, to spend fewer
tokens on the same work. Does it actually pay off? We gave the *same* AI the
*same* three real coding jobs **twice** — once with `sf`, once with only ordinary
tools (open a file, search, grep) — and compared the real API bill. The jobs:
work out how one database entity is shaped and used across the code (a many-file
navigation task), add a field to another entity (a small edit), and add one
method to a small single-file class. **The short answer:** `sf` doesn't spend
*fewer* tokens — it spends *more*, but shifts them into a cheaper token *type*,
which nets out ahead on the navigation job and behind on the single-file edits.
So the right policy is a hybrid — `sf` for many-file work, plain reads for small
files — not "route everything through `sf`."

The rest of this page is the measurement in full. A whole-task A/B: two agents
in fresh, isolated sessions with no shared
memory solve the same problems on a real, production Symfony + Doctrine
codebase (a mature, vertical-slice application with a `src/<Context>/`
layout); one arm is equipped with `sf` (its agent instructions + the
`sf-context` skill + the PreToolUse hook), the other has only Read/Grep/
Glob/Edit. The measurement is **real billed tokens and dollars**, from
`claude -p --output-format json`. Model: sonnet, both arms. Base: a single
frozen commit of the target codebase. 2 arms × 3 tasks × 5 repeats = 30
runs; harness — `scripts/ab/`.

## TL;DR (honest, against expectation)

1. **`sf` does NOT reduce tokens.** By the volume of tokens processed
   (`billed_in`, cache-invariant), `sf` costs **35–91% MORE** on every task —
   more small calls, plus reading its own instructions.
2. **`sf` does have a dollar win, but only on navigation, and only because
   of how token types are priced.** `sf` shifts volume into cheap
   `cache_read` (~$0.30/M) instead of expensive `cache_creation` (~$3.75/M)
   and large raw output. Net $: T1 −17%, T2 −50%, **T3 +21%** (a localized
   task — a loss).
3. **Quality is equal** (all 30 runs passed the judge, scores 92–100). The
   comparison is fair.
4. **`sf` is slower in wall-clock time** on every task (+36% on T1) — more
   turns.
5. **Cost is noisy from cache warm-up** between runs: at an **identical**
   token volume (T2 plain = 71,685 across all 5 runs), cost swings **3.5×**
   ($0.028→$0.098). So the absolute dollar figure is a signal, not a
   verdict — the direction and the large effects carry the weight.

**Conclusion:** `sf` isn't "fewer tokens" — it's a **shift into cheaper
token types plus structured output**, which pays off on multi-file
navigation and hurts on single-file edits. The better strategy is a
**complexity-threshold hybrid**, not a monoculture.

## Why measure this: tokens aren't equivalent

The naive assumption is "a full `Read` costs ≈ the size of the file."
That's wrong twice over. First, a file that enters the context re-enters
`cache_read` on **every** subsequent turn (in heavy production sessions on
this codebase, telemetry showed `cache_read` at 180–547M against `out` at
1.5–3.8M — roughly ≈140×). Second — and this is the key point — tokens of
**different types** are priced differently: `cache_read` ≈ $0.30/M,
`cache_creation` ≈ $3.75/M, `output` ≈ $15/M (sonnet). So "a lot of cheap
tokens" can cost less than "a little of the expensive kind." That's exactly
what happens here.

## Tasks (chosen at the break-even boundary)

- `t1_deal` — comprehension: fields/types/relations of a `Deal` entity (19
  fields) plus where it's used. Multi-file navigation — the territory a
  schema/search tool is built for.
- `t2_product` — a small edit: add a nullable field to a `Product` entity,
  matching the file's existing style.
- `t3_phone` — add one method to a small `PhoneNumber` value object.
  Entirely within one file.

## Results (medians, n=5, passing runs only — all of them passed)

| task | metric | `sf` | `plain` | Δ (`sf` vs `plain`) |
|---|---|---:|---:|---:|
| **T1** nav | cost $ | **0.176** | 0.211 | **−17%** |
| | tokens (`billed_in`) | 250,336 | 131,169 | **+91%** |
| | `cache_read` | 242,428 | 105,258 | +130% |
| | turns | 17 | 10 | +70% |
| | wall, s | 98 | 72 | +36% |
| | quality | 5/5, ~97 | 5/5, ~97 | = |
| **T2** edit | cost $ | **0.047** | 0.095 | **−50%** |
| | tokens | 97,102 | 71,685 | +35% |
| | turns | 4 | 3 | |
| | quality | 5/5, ~96 | 5/5, ~95 | = |
| **T3** method | cost $ | 0.112 | **0.093** | **+21%** |
| | tokens | 119,522 | 71,071 | +68% |
| | turns | 5 | 3 | |
| | quality | 5/5, 100 | 5/5, 100 | = |

Two facts side by side: **on tokens `sf` is more expensive everywhere; on
dollars it's cheaper on T1/T2.** That's not a contradiction, it's the price
of token types — `sf`'s extra tokens are cheap `cache_read`; `plain` has
fewer tokens but more of the expensive `cache_creation` (full `Read`s) and
output.

### Confound: cache warm-up (why the absolute dollar figure is noisy)

Runs execute sequentially; the system-prompt cache lives ~5 minutes and
bleeds across runs. On T2 plain, `billed_in` is **identical** (71,685)
across all 5 repeats, while cost is bimodal:

```
t2 plain: $0.028  $0.036  $0.095  $0.096  $0.098   (the same token volume every time!)
```

Warm repeats pay cheap `cache_read`; cold ones pay expensive
`cache_creation`. So **`billed_in` (volume) is the stable metric, dollars
are noisy**. The direction and the large effects (T2 −50%, T3 +68% on
tokens) survive the noise; the small ones don't. *Future rigor:* alternate
arms and/or reset the cache between runs.

## What to fix in `sf` (feedback from the data)

1. **The hook shouldn't nudge on localized tasks.** T3 (one small file):
   `sf` = $0.112 over 5 turns against `plain`'s $0.093 over 3 — the kit's
   overhead (reading its own instructions, then structural-read-then-
   point-read) doesn't pay for itself. The hook's threshold should skip
   nudging a full `Read` when the file is small (e.g. under ~150 lines, or
   the task only touches one file). Fix → the skill plus
   `docs/measurements/tools/hook.md`.
2. **The agent instructions over-optimise toward splitting things up.** The
   "structural read → narrow search → point-read" rule drives the agent
   into 16–17 small calls on T1 (17 turns against 10). When the **whole**
   small file is actually needed, one `Read` is cheaper than
   `code`-summary-plus-body. The instructions need a caveat: drill-down is
   for large files and tree-wide search, not for a small file you need in
   full anyway.
3. **A project-specific tool resolved its root from an environment
   variable, ignoring the working directory.** In a teammate's session (or
   a fork), the schema lookup silently failed and the agent fell back to
   grep. Not a token bug, but a silent failure — a candidate for
   `error: <what>; try: --root <path>` instead of a silent fallback.

## Answering "which approach is actually more reliable and convenient — for AI and for people"

- **For AI (cost):** not "sf instead of Read," but **sf where it shifts
  volume into cheap `cache_read` with a real payoff — multi-file navigation
  and comprehension**; for single-file work, plain `Read` wins. The benefit
  is real but modest, and depends on the cache — not "always −30%."
- **For people:** `sf`'s structured TOON output is auditable and diffable
  (you can see what the agent actually read) — a separate value outside of
  token cost. But the `sf` arm is **slower** (more turns) — a downside for
  anything interactive.
- **Bottom line — a complexity-threshold hybrid**, and that threshold
  belongs in the nudges/instructions, not left to the agent's judgment. A
  dogmatic "route everything through sf" isn't supported by this data.

## Caveats

- N=5/cell, one domain (a single production codebase), one operator,
  sonnet only → a trend, not a law.
- "Treatment" = sf-availability plus the nudges, together; a third arm
  (pre-registered) will separate them.
- Cache warm-up between runs wasn't controlled for (see the confound
  above). Opus, with a larger context, would likely shift the balance
  toward `sf` (more `cache_read` to amortise) — a hypothesis, not a result.
- Runs were guarded (a tool allowlist, disposable one-shot worktrees pinned
  to a frozen base commit).

## Reproduce

```bash
bash scripts/ab/run.sh full        # 30 runs → runs/*.json
bash scripts/ab/judge.sh           # rubric scoring → *.verdict
bash scripts/ab/aggregate.sh       # medians (passing-only) + runs/_records.jsonl
```
Every number traces back to `scripts/ab/runs/_records.jsonl`.
