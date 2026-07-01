# Token economy: `sf cc show`

**Status: measured** (on real session transcripts from
`~/.claude/projects`).

## Methodology

- Heuristic — `internal/tokens.Estimate` (ASCII bytes/4 + non-ASCII runes).
- Baseline — the cost of "find out what happened in this session" without
  the tool. There's no direct equivalent: a model either reads the raw
  `*.jsonl` transcript in full (full fidelity, but impractical — megabytes
  of JSON), or writes an ad-hoc `jq`/`python` script, which itself costs
  tokens and only gives a partial slice. In the table, the raw-token count
  is an upper bound (the full-fidelity baseline).
- The session numbers come from **the transcript itself**: tokens are read
  from its own `usage` records (`output/input/cache_*`), not estimated.
  That makes the digest more accurate than any recomputation.

## Scenario: "what happened in this session"

| Target | raw `*.jsonl` | `sf cc show` (TOON) | ratio |
|---|---|---|---|
| Session A (Symfony/Doctrine project, 784 msgs, 2.5 MB) | 660,551 | **701** | **~942×** |
| Session B (Go-service project, 497 msgs, 2.4 MB) | 675,886 | **1,659** | **~407×** |

The digest barely depends on session size: what inflates it is the number of
human prompts (session B had 14, many long and in Cyrillic, hence the
larger output) and the list of "fat" results. The bulk of a transcript
(hundreds of tool_use/tool_result entries, megabytes of stdout) compresses
into a histogram and aggregates.

## What the digest returns

On one screen, instead of reading the raw file:

- **meta** — project, cwd, branch, model, CLI version, time span, size;
- **token usage** — real `output/input/cache_read/cache_create` from the
  session's own records (not an estimate);
- **prompts** — human turns only (injected system-reminders, continuation
  summaries and tool_results are filtered out);
- **tools** — a call histogram plus total result tokens per tool;
- **bash** — commands broken down by category (search/read/git/test/build/
  db/fs);
- **files** — the top touched files, flagged r/e/w;
- **fat_results** — token-heavy results (candidates for compression);
- **prs** — open pull requests.

## `sf cc ls`

An index across every project's sessions — for 20 sessions, the whole index
is ≈**445 tokens** (≈22 tokens/session): id, project, title, activity,
message count, prompts, real out/cache tokens, branch, PR. A replacement for
`ls -la` plus manual inspection, plus real token metrics that `ls` doesn't
have.

## Boundary of applicability

- The digest is **aggregates and samples**, not the conversation itself. To
  read a specific exchange you need the source transcript; for a targeted
  slice there's `sf cc prompts` (turns), `sf cc bash` (commands), and
  `sf cc candidates` (repeated, expensive operations).
- Attribution of a result to its tool is lost for `tool_result` entries from
  sidechains (subagents) — those land under a `?` tool.
- The `usage` tokens are whatever Claude Code itself recorded; if a
  transcript is truncated or corrupted, the aggregates are computed over
  whatever's available.

## Reproduce

```
sf cc show myproject                        # digest of the last session for a project
sf cc show 6bd96fc7                         # by session-id prefix
sf cc ls --since 24h                        # index over the last day
sf history --tool cc.show --stats           # accounting for its own calls
```
