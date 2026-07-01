---
name: sf-context
description: Token-cheap source context via the `sf` CLI — structural file summaries instead of full reads, capped tree search, single-symbol bodies, classified diffs, cheap session resume. Use BEFORE reading or searching source code (.go/.php/.ts/.tsx/.vue) — especially full Read/cat of a big file, repeated reads of the same file, or tree-wide grep.
---

# sf-context — cheap context instead of a full read

Rule: **structural read → narrow search → point body**. A full Read of a
large source file is the last step, not the first: everything that lands in
the context window gets re-read from cache on every following turn.

## Decision tree

| Need | Instead of | Call |
|---|---|---|
| A file's shape: types, signatures, API | Read/cat the whole thing | `sf code <file>` (Go/PHP/TS/Vue; ~6–23×) |
| Only the public surface | — | `sf code <file> --exported`; PHP with traits/parents — `--api` |
| One function/method/type's body | re-reading the whole file | `sf code <file> <Symbol>` (or `<Type.Method>`) |
| Search across a tree | `rg -C` / `grep -rn` | `sf grep --ext=go,php '<pattern>'` (capped at 30 hits; `--regex`) |
| What changed | full `git diff` | `sf changed [range]` (~48×) |
| Resume work with a small context | re-reading everything from scratch | `sf cc resume [proj]` |

`sf <cmd> --help` for flags; `--md`/`--json` for output format; `sf --help` for the full catalog.

## When `sf` is NOT needed
- Small files (<~100 lines) and non-code (md/json/yaml/config) — a plain Read is fine.
- You need many function bodies at once, or a line-by-line read — read the
  file (or pull several symbols one at a time via `sf code <file> <Symbol>`).
- `sf code` is safe on any supported file: the compact-or-raw invariant means
  a summary that isn't shorter, or a failed parse, returns the whole file —
  never an error.

## About the PreToolUse hook
The `sf hook pre` hook may deny the **first** full Read/cat of a large code
file, pointing back here. An identical repeat call goes through (e.g. when a
Read is required right before an Edit).

## If `sf` let you down
It exited 0 but gave you the wrong thing, or you had to fall back to cat/rg —
record it in one line: `sf gripe '<what went wrong>'` (context is attached
automatically).
