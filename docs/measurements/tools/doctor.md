# Token economy: `sf doctor`

**Status: not a token-ratio tool — turn/regression avoidance.** To be
honest: `doctor` doesn't "compress" anything against `cat`/`rg`, and
measuring it in token multiples wouldn't mean anything. Its value is
catching a failure class that quietly burns tokens and turns while staying
invisible in normal output.

## Target: the deploy gap (a real incident)

A fix to one of the project-specific tools (schema discovery, plus tighter
token caps) was committed, but `~/.local/bin/sf` is a symlink to `bin/sf`,
which only gets rebuilt by `make install`/`make build` — **not** by
`git commit`. `bin/sf` stayed pinned to the previous day's build, and the
agent kept running the stale binary.

The visible cost in the log: **29 "fresh" failures** of that tool (including
a burst of 12 calls in a row, guessing at entity names) — exactly the
scenario the already-committed fix was supposed to solve. The savings from
that whole day of work were **zero** until the binary got rebuilt. And an
audit based on that data would have mis-prioritised the roadmap (see
"measure first" in `CONTRIBUTING.md`).

A single `sf doctor` catches this immediately: `staleness=fail` plus a
precise hint, `make install`.

## What it checks

A TOON checklist, `checks[N]{check,status,detail}` (status
`ok|warn|fail`):

| check | what | signal |
|---|---|---|
| **staleness** | `bin/sf`'s mtime vs the git HEAD commit time | `fail` if HEAD is newer → `make install`; `warn` if there are unbuilt `*.go` changes |
| **path** | whether `sf` on `$PATH` == the binary actually running (symlinks resolved) | `warn` if shadowed by another copy |
| **completions** | fish/bash scripts in their standard locations | `warn` if missing |
| **claude** | whether `claude` is on `$PATH` (needed to launch Claude Code sessions) | `warn` if missing |
| **calllog** | the resolved path of the shared log | informational |

Not a dev/bin install (`go install`, no repo root found) → staleness
gracefully reports `ok`/n/a.

## Exit code

Non-zero on any `fail` (the core one being staleness). *A deliberate
exception to the "non-zero only when there's no useful output" rule:
`doctor` is a health gate — the exit code is meant for scripts/make targets,
and there's always output.* `warn`/`ok` → exit 0.

## Reproduce

```
make install && sf doctor          # everything ok, exit 0
# negative case: roll the binary's mtime back before HEAD
touch -d "@$(($(git log -1 --format=%ct) - 3600))" bin/sf
sf doctor                          # staleness=fail + hint, exit 1
go build -o bin/sf ./cmd/sf        # restore
```
