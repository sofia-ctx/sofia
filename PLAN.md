# sofia architecture

This document describes the current state. For short how-tos, read
[README.md](./README.md); for planned work, see [ROADMAP.md](./ROADMAP.md).

## Purpose

`sf` gives an AI coding agent compact, structured context about a codebase — a
file's structure, a symbol's uses, a classified git diff — instead of raw file
dumps, so the agent gets what it needs for fewer tokens. It's a single Go
toolkit that scans an application's layers locally and hands the model a
narrow, token-cheap slice. Two roles:

- **Agentic Tools** — the model calls a binary to take an action.
- **Context Providers** — a binary reads a project's code/git history and
  returns a narrow slice of it, by default as **TOON** (Token-Oriented Object
  Notation, a compact JSON-like format that's far cheaper in tokens than raw
  JSON or a full file).

The core principle: everything a tool hands to an LLM must be cheap in tokens
and unambiguous in structure.

## Directory layout

```
sofia/
├── cmd/                          Go binary entry points (thin wrappers)
│   ├── sf/                       master CLI (var RootCmd)
│   └── common/<tool>/            standalone binaries: grep, cc, code, changed,
│                                    doctor, composer, packagist, github, vue, worktrees
├── internal/                     reusable packages
│   ├── calllog/                  JSONL log, Counter, Fingerprint, Tracker
│   ├── cc/                       `sf cc` — Claude Code session digests
│   ├── cli/                      assembles the Cobra tree (RootCmd + init)
│   ├── cliflags/                 shared flag helpers (--md/--json, dir completion, arg hints)
│   ├── codectx/                  enclosing-context lookup for PHP/TS/Twig/INI
│   ├── common/changed/           `sf changed` — classified git diff
│   ├── common/code/              `sf code` — router: dispatch by extension + multi-file
│   │   ├── gocode/               Go backend (go/parser): summary + slice
│   │   ├── phpcode/               PHP backend (wraps common/php): summary + slice
│   │   ├── pycode/                Python backend (indentation heuristics): summary + slice
│   │   └── tscode/               TS/Vue backend (regex): summary + type members + SFC
│   ├── common/composer/          `sf composer` — PHP package tree overview (ls/show/check)
│   ├── common/doctor/            `sf doctor` — installation health (staleness of bin/sf vs git)
│   ├── common/github/            `sf github` — CI runs (`ci`), PR digest (`pr`), branch cleanup (`branches`), via `gh`
│   ├── common/grep/              `sf grep` — cross-project search
│   ├── common/gripe/             `sf gripe` — feedback on silent misses
│   ├── common/hook/              `sf hook pre` — PreToolUse guard for the Read channel
│   ├── common/packagist/         `sf packagist` — release status (status) + publishing (release)
│   ├── common/php/               PhpSymbolReader (VKCOM/php-parser AST)
│   ├── common/vue/               `sf vue routes` — vue-router route map
│   ├── common/worktrees/         `sf worktrees` — cross-project overview of worktree forks
│   ├── emit/                     output budget: compact-or-raw (SmallerOf)
│   ├── envfile/                  .env load/save/prompt
│   ├── history/                  the `sf history` command
│   ├── matcher/                  line-based search (literal + regex, UTF-8)
│   ├── strdist/                  Levenshtein for typo hints (did you mean)
│   ├── tokens/                   heuristic LLM token estimate
│   ├── toon/                     TOON primitives: Scalar, NeedsQuote, JoinList
│   └── walker/                   parallel tree walker with filters
└── bin/                          gitignored — build artifacts
```

Every Go package lives under `internal/`, exporting the minimum needed: the
module isn't meant to be imported from outside this repository.

> **A note on `internal/`.** In Go, `internal/` is an import-visibility
> marker (Go spec / Go 1.4 internal packages), not "a folder for shared
> code." A package under `internal/common/X/` can only be imported by code
> rooted at the parent of that `internal/` directory. Project-specific
> packages (when a project's own tools exist) follow the same rule under
> `internal/projects/<name>/`; cross-project helpers live under
> `internal/common/...`.

## Components

### Master CLI (`internal/cli`)

`var RootCmd = &cobra.Command{Use: "sf", ...}` plus an `init()` that wires up
subcommands. The `cli` package imports **only** concrete tool packages, never
the other way around, so adding a new tool touches one line:
`RootCmd.AddCommand(...)`.

### Tool: `sf grep` (cross-project search)

`internal/common/grep` — a cross-project TOON-grep. The same
walker + matcher + `codectx.Enclosing` infrastructure used elsewhere in the
repo. Two modes: literal (word-boundary aware, default) and `--regex` (Go
regexp, optionally case-insensitive via an `(?i:...)` wrapper). Filtered by
`--ext`; the default ignore list includes `vendor`, `node_modules`, `var`,
`.git`, `dist`, `build`, `target`, `__pycache__`, IDE directories. Registered
on the master CLI via `RootCmd.AddCommand(grep.NewCommand())`; standalone
binary — `bin/common/grep`.

### Tool: `sf history`

`internal/history` reads the shared call log (the XDG-resolved path, see
below) and returns either a recent-calls list or per-tool aggregates: count,
errors, total/mean/p50/p95/max duration, total output bytes, top-N popular
inputs. Filters: `--tool`, `--since 30m|24h|7d`, `--limit`.

### Tool: `sf code` (multi-language structural summary)

`internal/common/code` — a thin **router**: dispatch by extension to
per-language backends `{gocode, phpcode, tscode}`, plus a parallel run across
multiple files. A file's structure **without bodies**: Go via `go/parser`
(package, imports, types with fields/tags, signatures); PHP via a
`common/php` wrapper (namespace, attributes, constructor deps, methods);
Python via indentation heuristics (classes+methods, module functions,
assignments); TS/Vue via a regex extractor (type members, SFC
`defineProps`/stores/API calls). The second positional argument is a
**single-symbol slice** (Go/PHP/Python); `--exported` narrows to the public
API; `--api` (PHP) computes the
effective public surface across traits/inheritance. Invariant:
**compact-or-raw** — unparseable, or a summary that isn't shorter, returns
the full file rather than erroring.

### Tool: `sf cc` (Claude Code session digests)

`internal/cc` — reads transcripts under `~/.claude/projects/**.jsonl` and
compresses them into TOON: `ls` (an index), `show` (a digest with **real**
token counts from the transcript's own `usage` records), `prompts`, `bash`,
`candidates` (ranks by measured tokens to find where the budget goes → tool
candidates). A sibling to `sf history`, but about the agent's own sessions
rather than sofia's own log.

### Tools: other cross-project (`internal/common/*`)

Cross-project tools built on the same calllog/cliflags/toon infrastructure:

- **`sf changed`** — a classified git diff (status, churn, category, touched
  functions from git's own funcname context) instead of the full
  `git diff`.
- **`sf doctor`** — installation health; the core check is staleness
  (`bin/sf` older than git HEAD: "fixed it, but forgot to rebuild"), plus
  PATH/completions/claude/hook/skill/calllog.
- **`sf composer {ls,show,check}`** / **`sf packagist {status,release}`** /
  **`sf github {ci,pr,branches}`** — an overview of a PHP package tree,
  release status vs Packagist (plus publishing), GitHub Actions runs, an
  open-PR digest, and branch cleanup.
- **`sf vue routes`** — a vue-router route map.
- **`sf worktrees`** — a read-only, cross-project view of git-worktree dev
  forks; enriched with stack/health state for repos that ship their own
  `dev/worktree.sh`.
- **`sf hook pre`** — the PreToolUse guard behind the `sf-context` skill
  (see `docs/measurements/tools/hook.md`): redirects large full-file reads toward
  `sf code`.

### Call log (`internal/calllog`)

JSON-lines, one entry per call. Fields:

| Field | Purpose |
|---|---|
| `ts` | When (RFC3339Nano UTC) |
| `tool` | Tool name, e.g. `grep` |
| `source` | Who called it: `agent` (Claude, via `CLAUDECODE=1`) / `manual` / `test` |
| `sid` | Claude session id (`CLAUDE_CODE_SESSION_ID` = transcript filename → joins with `sf cc`); empty for manual calls |
| `tag` | Project (`SOFIA_TAG`, or basename of the git root) |
| `args` | The exact positional and flag args |
| `fp` | SHA-256 hex prefix of the sorted args (16 hex chars) |
| `dur_ms` | Call duration |
| `exit` / `err` | Exit code and error text |
| `out_bytes` | Size of the stdout payload (via `calllog.Counter`) |
| `out_tokens` | Approximate LLM token estimate (`internal/tokens`) |
| `summary` | A free-form `map[string]any` of tool-specific aggregates |

**Log path** resolution order:

1. `$SOFIA_LOG_DIR/calls.jsonl`
2. `$XDG_STATE_HOME/sofia/calls.jsonl`
3. `~/.local/state/sofia/calls.jsonl` (XDG default)
4. `./calls.jsonl` (last-resort fallback)

`bin/` is reserved for executables; operational data doesn't go there. One
log shared by the master binary and every standalone binary.

## Internal packages — what and why

- **`matcher`** — a single-pass scanner with UTF-8-aware word boundaries.
  Multiple patterns are checked per line in one pass — searching N names
  costs close to searching one.
- **`walker`** — a worker pool with `IgnoreDirs`/`IgnoreRels` filters and an
  extension whitelist. A producer→worker pipeline over channels.
- **`toon`** — three low-level functions (`Scalar`, `NeedsQuote`,
  `JoinList`). Individual tool renderers compose their own schema on top:
  `name[N]{f1,f2}: …`.
- **`envfile`** — `Load/Save/Resolve`. Save quotes values containing
  whitespace/special characters; Load reverses the escaping for double
  quotes.
- **`cliflags`** — `AttachFormatFlags(cmd, &format)` wires up
  `--format toon|md|json` plus the `--md`/`--json` aliases, with
  mutual-exclusion checks and completion. A separate package so it doesn't
  create an import cycle between `internal/cli` and the tool packages.
- **`tokens`** — a sub-microsecond heuristic LLM token estimate (ASCII
  bytes + non-ASCII runes). Feeds `out_tokens` in the log and the
  measurements in `sf cc`.
- **`common/php`** — reads a PHP class via the VKCOM/php-parser AST:
  structure, properties, attributes with their arguments. The foundation for
  `sf code`'s PHP backend.
- **`codectx`** — the enclosing function/class for PHP/TS/Twig/INI (context
  for a hit in `grep`).

## Tests

`go test ./...` is green. Coverage is focused on regression traps:

- `matcher`: UTF-8 word boundaries, multi-pattern matching, repeated hits on
  one line.
- `history`: `parseSince` supports `30m/1h/24h/7d`; aggregation computes
  p50/p95/total correctly; `extractInputs` tolerates varying JSON shapes.
- `calllog`: path-resolution priority (SOFIA_LOG_DIR > XDG > home), a stable
  fingerprint regardless of argument order.

## Shell completion

`spf13/cobra`'s `ValidArgsFunction` + `RegisterFlagCompletionFunc`:

- `--format` — a static `toon|md|json` list with descriptions.
- `--root` / `--www` — directory completion.

Fish/Bash scripts are generated with the standard
`sf completion fish|bash`.

## Conventions for future tools

When adding a new tool:

1. Put the package under `internal/common/<tool>/` (cross-project) or, for a
   project-specific tool, `internal/projects/<project>/<tool>/`.
2. Export `NewCommand() *cobra.Command`.
3. Log the call via `calllog.Start(...)`/`Finish(err)` and `calllog.Counter`
   to count output bytes.
4. Where it applies, use `cliflags.AttachFormatFlags` and render through
   `internal/toon` for a consistent format.
5. If it needs environment variables, describe them as `envfile.Field`
   values in a project-specific wrapper.
6. Add a table-driven `*_test.go` alongside it, especially for parsers and
   resolvers.
7. Write `docs/measurements/tools/<tool>.md` with a real measurement, and add an entry
   to `README.md`.
