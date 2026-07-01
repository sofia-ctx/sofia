# Token economy: `sf history`

**Status: not applicable in the per-call sense.** `history` isn't a context
provider, it's analytics over sofia's own call log
(`$XDG_STATE_HOME/sofia/...`). It doesn't pull project context into the
model, so a "without the tool / with the tool" token comparison doesn't mean
anything here.

## Why it's in this directory anyway

`history` is the **instrument every other tool is measured with**. The
savings claimed in every other file under `docs/measurements/tools/*` and `docs/measurements/evaluation/*` come from here:

- `out_tokens` / `out_bytes` are written to the call log on every call;
- `sf history --tool <name> --stats` aggregates a tool's real cost from
  actual calls (not from synthetic benchmarks);
- `--histogram` and a planned `--regressions` show trends and regressions
  over time.

In other words, `history` doesn't save tokens itself — it closes the loop
that makes the other tools' savings **measurable and observable**; without
it, the rest of this directory would be guesswork.

## Attribution: session id + project tag

The loop closes down to the level of **session and project**: every entry
carries `sid` and `tag`, so savings and adoption can be sliced honestly.

- `sid` = `CLAUDE_CODE_SESSION_ID` (Claude injects it into every Bash tool's
  environment; equal to the transcript's filename) → `sf history --session
  <id>` matches `sf cc bash/show <id>`. Read on every call, so it follows a
  live session across `/clear`/`--resume`.
- `tag` = `SOFIA_TAG` (stamped by the `sf claude`-style launcher pattern
  with the authoritative project name, where a fork provides one), else the
  basename of the git root from the working directory — so manual calls and
  sessions started without a launcher still attribute correctly.
- `source=agent` is detected via `CLAUDECODE=1` (more reliable than
  `term.IsTerminal`) — fixes an "untagged" tail in the log and a fragile
  detection method.
- Concurrency: the environment is inherited down the process tree (parent
  → shell → sf), so every parallel session gets its own `sid`; the log is
  append-only, and each line is under `PIPE_BUF`, so writes are atomic.
  Sessions and projects don't get mixed up.
- A manual call outside Claude: `source=manual`, `tag` from cwd, `sid` empty
  (there's no Claude session) — this isn't agent friction, it's expected.

`sf history --adoption` aggregates by `(tag, source)`: calls, distinct
sessions, errors, failed%, tokens — a direct answer to "which projects
actually use `sf` instead of `cat`/`rg`."

## Indirect savings

The one meaningful savings channel is **spotting heavy calls**:
`history --stats` highlights tools/`fp` values with bloated `out_tokens`
that are worth optimising or caching. This saves tokens not on the
`history` call itself, but on the calls it helps trim.

## Reproduce

```
sf history --stats                      # overall picture across every tool
sf history --tool code --stats          # cost of one specific tool
sf history --histogram=day              # distribution over time
```
