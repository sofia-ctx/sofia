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
| A file's shape: types, signatures, API | Read/cat the whole thing | `sf code <file>` (Go/PHP/TS/Vue; ~6–23×; small files auto-return raw) |
| Only the public surface | — | `sf code <file> --exported`; PHP with traits/parents — `--api` |
| One function/method/type's body | re-reading the whole file | `sf code <file> <Sym1> [Sym2 …]` (one call, several bodies) |
| Search across a tree | `rg -C` / `grep -rn` | `sf grep --ext=go,php '<pattern>'` (capped at 30 hits; `--regex`) |
| What changed | full `git diff` | `sf changed [range]` (~48×) |
| Resume work with a small context | re-reading everything from scratch | `sf cc resume [proj]` |

`sf <cmd> --help` for flags; `--md`/`--json` for output format; `sf --help` for the full catalog.

## Batching — one call, not one per file

Several relevant files? **One call**: `sf code file1.go file2.go file3.go` —
never one call per file. Every extra tool call costs a full round-trip over
your whole context; batching is where the real savings are. This applies to
`sf grep` too (multiple patterns in one call).

The same goes for symbol bodies: `sf code file.go Run Finish Track` pulls
several bodies in one call. And never re-request a structure or body you
already fetched — earlier tool results are still in your context; look back
instead of calling again.

## When `sf` is NOT needed
- Non-code files (md/json/yaml/config) — plain Read.
- **The file you are about to Edit — plain Read, not `sf`**: the harness
  requires a native Read before Edit anyway, so an `sf code` call on the
  edit target is pure overhead (measured: it re-created the exact loss it
  was meant to fix). Use `sf code` for files you only need to *understand* —
  there small files come back raw automatically, never worse than a full Read.
- The bodies you need cover most of the file — one full Read beats slicing it
  piece by piece (batched or not).
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
