# Token economy: `sf hook pre` + skill `sf-context`

**Status: adoption infrastructure, not a token tool** (like doctor/gripe) —
it doesn't compress anything itself; its value is converting someone else's
traffic (full Read/cat calls) into the structural `sf code` path. A ratio
doesn't apply here by design; it's measured by channel conversion instead.

## Target (measured before building it)

`sf cc candidates --since 7d` on a busy week (40 sessions): full `Read`
calls accounted for **1,092,980** result-tokens over the week (766 calls) —
the single largest channel, and one `sf` wasn't intercepting at all; for
comparison, `sf code` had returned ≈270k tokens over its entire lifetime up
to that point. The effect is amplified further: anything that lands in the
context window gets re-read from cache on every subsequent turn (the
`cache_read` total for one 48-minute session on a large codebase was
≈20M tokens), so a prevented full Read is worth a multiple of its face
value.

## How it works

- **A PreToolUse hook** (`~/.claude/settings.json`, matcher `Read|Bash` →
  `sf hook pre`): a full read of a large source file
  (`.go/.php/.ts/.tsx/.vue`, ≥ `SOFIA_HOOK_MIN_BYTES`, default 8192) is
  caught before it runs. `nudge` mode (the default) denies the **first**
  such Read/cat per (session, file) with a hint toward `sf code <file>` /
  `sf code <file> <Symbol>`; an identical repeat call is allowed — so a
  Read-before-Edit flow doesn't break, and the agent self-corrects within
  the same turn. Other modes (`SOFIA_HOOK_MODE`): `suggest` — allow, with
  advice attached (`additionalContext`); `strict` — always deny; `off`.
- **The `sf-context` skill** (`skills/sf-context/SKILL.md` →
  `~/.claude/skills/`, installed by `make install`): its description is
  always in the agent's context, the body loads lazily; it documents the
  same decision tree — structural read → narrow search → point-read a
  single body.

## Measuring the effect (don't guess a ratio)

- `sf history --tool hook.nudge --stats` — how many nudges actually fired
  (summary: file/bytes/mode/tool); the pass-through path writes nothing to
  the log at all.
- `sf history --adoption --since 7d` — is the share of `sf` calls growing by
  project?
- `sf cc candidates --since 7d` — is the Read channel shrinking week over
  week?

Conversion numbers: **TBD** after a week of running it (fill in from the
commands above).

## Boundary of applicability

- Only nudges where `sf code` actually works (5 extensions) and the file is
  large. Passes silently: non-code files (md/json/yaml), small files, a
  `Read` with `offset`/`limit` (already targeted), `cat` with a pipe/
  redirect/flags, paths that don't exist. `rg` isn't intercepted: against a
  bare `grep -rn`, `sf grep` is already near parity (see `grep.md`) — nudging
  it wouldn't be honest.
- Fail-open: any internal hook error is a silent allow; with no `sf` on
  `$PATH`, sessions behave exactly as before.
- Hooks are snapshotted when a Claude Code session starts — settings
  changes take effect from the next session onward.

## Reproduce

```
echo '{"session_id":"t","tool_name":"Read","tool_input":{"file_path":"<big>.go"}}' | sf hook pre
sf history --tool hook.nudge --stats
sf doctor          # checks both the hook in settings.json and the installed skill's freshness
```
