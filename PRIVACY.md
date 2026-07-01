# Privacy

`sf` keeps a local call log (`internal/calllog`) so its own token-economy
claims can be measured rather than guessed at. This document describes
exactly what that log does, based on the current source
(`internal/calllog/calllog.go`) — not aspirational behaviour. If the code
changes, this file should change with it.

## Nothing leaves your machine

The call log is a local JSON-lines file. Writing an entry is a plain
filesystem append (`os.OpenFile` + `json.Encoder`); the `calllog` package
imports no networking of any kind. No entry, aggregate, or summary is ever
sent anywhere — not to the maintainers, not to any third party, not as part
of update checks (there are none). Some tools that need the network for
their own job do make outbound calls on your behalf — `sf github`/
`sf packagist` talk to GitHub/Packagist because that's literally what you
asked them to do — but the call log itself is a write-only local artifact
that nothing reads over the network.

## What gets written

One JSON line per tool invocation:

- `ts`, `tool`, `dur_ms`, `exit`, `err` — when, what, how long, and how it
  finished.
- `source` — `agent` (detected via `CLAUDECODE=1` in the environment),
  `manual` (typed at a terminal), or `test`. `go test` runs are excluded
  from the shared log entirely unless a test explicitly points
  `SOFIA_LOG_DIR` at a scratch directory.
- `sid` — Claude Code's own session id, when running under Claude Code
  (`CLAUDE_CODE_SESSION_ID`), so a call can be correlated with the session
  that made it.
- `tag` — a project label (`SOFIA_TAG`, or the basename of the nearest git
  root), so calls can be grouped by project.
- `args` — the exact arguments the tool was called with. Be aware this
  means whatever you pass on the command line — a search pattern, a file
  path, a package name — is recorded verbatim in this local file, since
  argument grouping (`fp`, a hash of the sorted args) and `sf history
  --top-inputs` depend on it.
- `out_bytes` / `out_tokens` — the size of the tool's output, in bytes and
  as an approximate token estimate (`internal/tokens`), not the output
  content itself.
- `summary` — a small, tool-specific map of aggregates (e.g. how many hits
  a search returned), not raw file contents.

The log never stores file contents, source code, or the full text of a
tool's output — only what's listed above.

## Where it lives

Resolved in this order, so it's easy to point somewhere else on purpose:

1. `$SOFIA_LOG_DIR/calls.jsonl` — explicit override.
2. `$XDG_STATE_HOME/sofia/calls.jsonl` — XDG default.
3. `~/.local/state/sofia/calls.jsonl` — fallback matching the XDG spec's own
   default.
4. `./calls.jsonl` — last resort, only if the user's home directory can't be
   determined at all.

Every `sf` binary (the master CLI and every standalone tool) writes to the
same file, which is exactly what lets `sf history` and `sf cc` join data
across tools and sessions.

## Inspecting and clearing it

The log is a plain, human-readable JSONL file — read it directly, or use
the tools built on top of it:

```bash
sf history                 # recent calls
sf history --stats         # per-tool aggregates
sf doctor                  # shows the resolved log path, among other checks
sf history --clear         # truncate the log
```

## Turning it off

There's no dedicated on/off flag today — logging is on by default for every
`sf` invocation outside of `go test`. What you can actually do, based on the
current code:

- **Point it somewhere you control**, via `$SOFIA_LOG_DIR` — e.g. a
  scratch directory you wipe periodically.
- **Clear it whenever you like** with `sf history --clear`, which truncates
  the file in place.
- Log-write errors are swallowed rather than failing the calling tool
  (`appendEntry`'s error is discarded), which means pointing
  `$SOFIA_LOG_DIR` at a location `sf` can't write to has the practical
  effect of disabling logging. This isn't an officially supported kill
  switch, just a consequence of how the writer is implemented — don't rely
  on it as a guarantee across future versions.

If you need a real opt-out flag, that's a reasonable thing to open an issue
or a small PR for.
