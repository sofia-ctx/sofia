# Economy: `sf cc value`

**Status: measured** (real session transcripts from `~/.claude/projects` on
this machine: 315 files, 263 MB).

## Methodology

- **Tokens aren't a new parse.** `internal/cc/cc.go`'s `Parse`/`ingestEntry`
  already extracts `input`/`output`/`cache_read`/`cache_creation` from each
  transcript's `usage` records — the same source `cc show`/`cc ls` use.
  `cc value` just aggregates the already-parsed `Session` fields over two
  time windows (`--days`, default 7) and converts to $.
- **Prices are a snapshot in `internal/cc/pricing.go`, not a live feed.**
  The source is [`../evaluation/micro.md`](../evaluation/micro.md): a live
  A/B against real billing (`claude -p --output-format json`) gives, for
  sonnet, cache_read ≈ $0.30/M, cache_creation ≈ $3.75/M, output ≈ $15/M.
  The base (uncached) input rate isn't stated directly there — it's derived
  from Anthropic's documented cache multipliers (write ≈1.25× input, read
  ≈0.1× input), which are exactly what produce the $0.30/$3.75 figures at
  input = $3.00/M. That's a reverse-derivation from numbers already accepted
  in this project, via a publicly documented formula — not a number measured
  in `micro.md` itself. The boundary is stated honestly here, not passed off
  as a direct measurement. Other models (`opus-4-x`, `fable-5`, `haiku-4-5`)
  use the same multipliers against their own published input/output rates.
- **There's no canonical rate table in the project.** `scripts/ab/` and
  `scripts/ab-macro/` don't recompute $ from rates themselves — both
  harnesses take `total_cost_usd` straight from the API's billing response.
  That's exactly the gap this task was asked not to paper over with a guess:
  the table in `pricing.go` is the first explicit rate table in this repo.

## Scenario: "my weekly $ delta and breakdown by token type"

Without the tool, there's no short path: `sf cc ls` doesn't compute $ or
distinguish `cache_creation` from `input`. The closest substitute is
`sf cc show` per session in the window plus manual arithmetic against rates
the agent either remembers or risks inventing.

| Approach | Cost |
|---|---|
| `sf cc show` × session (measured, `cc-show.md`: 701–1659 tok/session) over 13 sessions (a real `--days 7` window on this machine) | **≈9,100–21,600** tokens — and that's before any $, just the tokens |
| `sf cc value` (one call; measured via `sf history --tool cc.value`) | **130–180** tokens, $ already computed |

Ratio ≈ **50–170×**, and that's a lower bound on the value: the baseline
doesn't even answer the original question ("how much in $") without one more
step that itself risks wrong rates.

## Scaling

The output is an aggregate (2 windows × ≤4 token types, plus one row per
model seen), not a session list, so its size barely grows with the number of
sessions scanned:

| Window | Sessions in window | Output (TOON, tokens, measured) |
|---|---:|---:|
| `--days 3` | ~5 | **136** |
| `--days 7` | 13 | **178** |
| `--days 21` | 173 | **~150–180** |

Wall-clock grows (parsing files), not output: across this machine's whole
corpus (315 transcripts, 263 MB), `sf cc value --days 21` finishes in a few
seconds — the same order as `cc ls`/`cc candidates` over full history.

## Boundary of applicability

- **Prices are a snapshot, not a live feed.** Anthropic can change rates;
  `internal/cc/pricing.go` needs a manual update alongside this file when
  new models/tariffs ship.
- **One model per session.** `Session.Model` is the model of the *last*
  message (existing `cc.go` behavior, not new to this tool); if the model
  changed mid-session (see the different `plan`/`impl` configs in
  [`macro.md`](../evaluation/macro.md)), the whole session is priced at the
  last one — a known simplification inherited from the fact that `Session`
  doesn't split tokens by model for any `cc` command.
- **An unknown model isn't $0, it's `unpriced`.** `<synthetic>` tokens
  (Claude Code housekeeping entries, usually zero usage) and any other
  model-id not in the table are counted in `tokens` but not in `cost_usd` —
  via an explicit `priced=false` field, never a silent zero.
- **This is "how much did I spend," not "how much did `sf` save."** Unlike
  [`micro.md`](../evaluation/micro.md)/[`macro.md`](../evaluation/macro.md)
  (A/B: sf-assisted vs plain), `cc value` doesn't isolate `sf`'s own effect —
  it's an aggregate of all of a user's session activity, exactly what the
  roadmap's measurement goal asks for ("your own weekly $ delta"), not a
  measurement of the tool's own value.
- **Window boundaries use `Session.End`** (the timestamp of the session's
  last record), not each individual message; a session that started in the
  previous window and continued into the current one falls entirely into
  whichever window it ended in.

## `--quota`: the subscription angle

Everything above answers "how much did I spend" — meaningless on a flat-fee
Claude subscription, where the thing that actually runs out is the rolling
5-hour usage window, not $. `--quota` is a different report for that case:
it reads sf's own telemetry (`calllog.Read()`, the same `calls.jsonl` behind
`sf history`), filtered to `source=agent` (real tool traffic; manual/test
runs are excluded) within the last `--days` days, and asks how many output
tokens sf actually handed back vs. what the same calls would have cost raw.

A per-entry savings baseline exists only when the summary carries `tok_raw`
(a full `sf code` call) or `tok_rep` (a dedup stub — the original call's
cost); `saved = max(baseline − out_tokens, 0)`, clamped so a compact
summary that happened to lose to raw never reports a negative saving.
Entries without either field — `grep`/`changed` (no single raw baseline) or
log lines written before this field existed — still count toward the call
and emitted-token totals, but contribute zero to the savings math: never
guessed. The report also names the single busiest 5-hour window by saved
tokens (a sliding-window scan, not a fixed clock grid) — the same span a
subscription's quota resets on, so it's the most concrete answer to "when
did `sf` actually stretch my usage."

`--project` doesn't apply to `--quota`: `calls.jsonl` isn't grouped by
project the way transcripts are, so combining the two flags is an error
rather than a silently ignored one.

## Reproducing

```
sf cc value                       last 7 days vs the previous 7
sf cc value --days 21             three weeks vs three weeks
sf cc value --project myapp       a single project
sf cc value --format json
sf history --tool cc.value --stats

sf cc value --quota               subscription angle: sf's own token savings, last 7d
sf cc value --quota --days 14     two weeks instead
```
