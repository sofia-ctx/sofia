# Token economy: `sf github pr`

**Status: measured** (on a real personal GitHub account, 2 open PRs).

## Methodology

- Heuristic — `internal/tokens.Estimate` (ASCII/4 + non-ASCII runes×1).
- **Baseline** — what an agent does by hand to answer "what's the state of
  my PRs and their CI, across every repo": `gh search prs
  --author=@me --state=open` plus `gh search prs --review-requested=@me
  --state=open` (find the PRs), then, for **each** PR, `gh pr checks <url>`
  (CI status).
- **`sf github pr`** — a single `gh api graphql` call: both `search` queries
  (author + review) in one document, plus the `statusCheckRollup` of each
  PR's head commit → a TOON summary, one row per PR.

## Where the ROI is

`gh pr checks` prints **a line per job × matrix × attempt**, with URLs —
dozens of lines per PR. `sf github pr` collapses this into **one rollup
symbol** (✓/✗/⏳/–) per PR, the same way `github ci --watch` collapses a live
stream into one row. So the gain isn't from "compress the listing" (that's
≈1×, see `github-ci.md`) — it's from eliminating the per-PR fan-out.

## Measurement

| | baseline (search×2 + `gh pr checks`×N) | `sf github pr` |
|---|---|---|
| tokens | 692 | 47 |

**ratio ≈ 14.7×** on 2 PRs. Baseline breakdown: the two searches cost ~92
tokens combined; `gh pr checks` on the two PRs cost 254 + 346 tokens (the
dominant cost). The digest is 47 tokens.

**Scaling:** the baseline grows both with the number of PRs and the number
of jobs per PR (a version × workflow matrix); the digest stays one row per
PR (≈15–25 tokens/row). More PRs or a bigger matrix means a higher ratio.
One HTTP round-trip versus `2 + N` calls.

## What it returns

Per line: `{repo, num, ci, review, role, draft, title}`. CI is
`statusCheckRollup.state`, shown as ✓/✗/⏳/– in TOON, with the full string
available in `--json`. `role` = author or reviewer. PRs needing action (CI
`FAILURE`/`ERROR` or review `CHANGES_REQUESTED`) sort first, then pending,
then green.

## Boundary of applicability

- Requires `gh` (authenticated) and network access. An error/timeout (30s)
  → a command error.
- Coverage is "everything of mine on GitHub," via GraphQL `search`
  (`author:@me` + `review-requested:@me`), with no repo configuration
  needed. `--limit` (default 30) caps each of the two searches; a PR
  appearing in both is deduplicated (author role wins).
- Notifications (`inbox`) and a merged `status` field are deliberately
  deferred (YAGNI — measure before building).

## Reproduce

```
sf github pr
sf github pr --md
sf history --tool "github pr" --stats --source agent
```
