# Token economy: `sf gripe`

**Status: not a token-ratio tool — a feedback loop / regression-avoidance
tool.** To be honest: `gripe` doesn't "compress" anything against `cat`/`rg`,
and measuring it in token multiples wouldn't mean anything. Its value is
closing a blind spot in the log: a failure class that quietly costs tokens
and turns while staying invisible in the metrics.

## Target: sf's silent failures

`calls.jsonl` already catches calls and their exit codes, so **hard**
failures are visible: `sf history --failed --source agent`. What stays
invisible is something else:

- sf exited 0 but produced the wrong thing (wrong structure / failed to
  parse / empty);
- the agent fell back to `cat`/`rg`/`grep` because sf didn't cover the case
  it needed.

In both cases the log looks **clean**: no errors, a good error rate. The
agent silently routes around the weak spot, the signal is lost, the
missing feature never gets built — the tool stays stuck exactly where it
falls short. This is the worst kind of loss: expensive (the agent still
read the whole file) and invisible (a log audit won't show it — see the
"agent-only numbers" discipline in `CONTRIBUTING.md`).

## What it does

- `sf gripe '<one line>'` — record a complaint. Logged like any other call
  (`tool=gripe`, `args=[msg]`, `summary.note=msg`), auto-tagged with
  `source`, `sid`, `tag`, `ts` — the message itself can stay short, since
  the call log captures the context. One cheap call, only on a real miss;
  there is no "end-of-session report."
- `sf gripe` (no arguments) — lists recent complaints (newest first:
  `when · project · source · note`). A reader for the maintainer; like
  `history`, it doesn't write to the log itself.

## The loop

1. An agent on any project hits a gap in what sf covers →
   `sf gripe '<command + what went wrong>'`.
2. The entry lands in the shared `calls.jsonl`.
3. In a development session, `sf doctor` (after `make install`) surfaces
   "N gripes since the last build" (a warning) → `sf gripe` shows them →
   fix or dismiss.

`doctor` as the notifier is the key part: the maintainer already runs it
after every install, so accumulated feedback surfaces on its own, with no
habit of digging through the log and no manual copy-pasting from users.

## Cost asymmetry

A miss (the agent forgot to gripe) costs nothing — that's the status quo. A
hit (it did) is one short line that captures a real coverage gap from
actual usage. It can't make things worse; the cost of the signal is close
to zero.

## Boundary of applicability

- **Not** for hard errors (non-zero exit) — those are already in the log
  (`history --failed`).
- **Not** a general "friction journal" for environment quirks (`gh`
  oddities, "needs flag X here") — those belong in a project's own agent
  instructions; otherwise the list turns into a dumping ground.
- Only fires if the agent remembers the rule — coverage is intentionally
  less than 100% by design.

## Reproduce

```
export SOFIA_LOG_DIR=$(mktemp -d) SOFIA_SOURCE=agent
sf gripe 'sf code .kt does not structure it — read raw'
sf gripe                                   # the entry is visible, newest first
wc -l "$SOFIA_LOG_DIR/calls.jsonl"         # 1 — the list view itself wasn't logged
sf doctor | grep gripes                    # warn: 1 gripe
```
