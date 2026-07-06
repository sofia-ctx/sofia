# sofia

**SF (Sophia Foundation / Source Fabric)** — a Go toolkit of CLI tools and
AI-agent instructions for working on software projects.

- **Agentic Tools** — Go binaries the model calls to take actions.
- **Context Providers** — Go binaries that collect compact context from
  code/git and hand it to the model as TOON.

Architecture and design rationale — [PLAN.md](./PLAN.md).

## Requirements

- Go ≥ 1.24 (this repository is tested on 1.25)
- Linux/macOS, shell — Fish or Bash (for completion)

## Build

```bash
bash scripts/build.sh            # every binary into bin/** (one per tool)
# → bin/sf                              master CLI with all subcommands
# → bin/common/<tool>                   standalone binaries: grep, gripe, cc, code, changed,
#                                         doctor, composer, packagist, github, vue, worktrees
```

Install onto `$PATH` (symlink `~/.local/bin/sf`, no shell aliases needed):

```bash
make install     # build + ln -s bin/sf → $BINDIR (default ~/.local/bin)
                  # + regenerate bash/fish completion (kept in sync with the binary)
```

`make install` also refreshes shell completion (`~/.config/fish/completions/sf.fish`
and `~/.local/share/bash-completion/completions/sf`), so it always matches the
current command tree. Regenerate just the completions with `make completions`.

Update an already-installed `sf`:

```bash
make update      # git pull --ff-only + rebuild + regenerate completions
```

`make` with no target prints self-documented help for every target.

## Usage

A single entry point — `sf` (built on Cobra) — or a dedicated standalone
binary; they're equivalent:

```bash
# via the master CLI
./bin/sf grep --root=/path/to/project --ext=php "DeleteUser"

# directly (equivalent)
./bin/common/grep --root=/path/to/project --ext=php "DeleteUser"
```

### `sf grep` — cross-project search with enclosing context

Cross-project TOON-grep for arbitrary searches (a class name, a chunk of SQL,
a regex): layered output with the enclosing function/class attached to every
hit, so a bare match comes with the context needed to understand it.

```bash
sf grep --root=/path/to/project --ext=php "DeleteUser"
sf grep --root=/path/to/project --ext=php --regex '#\[Route\('
sf grep --case=false --ignore-dir=storage 'TODO'
```

By default the pattern is matched as a substring (like `grep`), so the stem
`techno` matches `technology`; `--word` restricts to whole-word matches.
Flags: `--regex` (Go regexp), `--ext php,ts,vue`, `--ignore-dir extra,paths`,
`--root`, `--case`, `--word` (literal mode only), `--max-per-pattern N`, the
common `--md`/`--json` aliases. Default ignores: `vendor`, `node_modules`,
`var`, `.git`, `dist`, `build`, `target`, `__pycache__`, IDE directories.

### `sf code` — structural summary of a source file (Go + PHP + TS/Vue)

A compact structural summary of a file **without function bodies**: for Go —
package, imports, types (struct fields + tags, interface methods), function
and method signatures, const/var; for PHP — namespace, class/interface/trait/
enum, extends/implements, attributes (`#[ORM\…]`/`#[Route]`/`#[IsGranted]`),
constructor dependencies, properties, method signatures; for TS/Vue —
imports, top-level declarations, **members** of `interface`/`type`/`enum`,
and for `.vue` — the component, `defineProps`/`defineEmits`/`defineModel`,
stores and API calls it uses, and the components referenced from
`<template>`. It replaces `cat`-ing a whole file when what's needed is shape
or API — which is exactly where read tokens go.

Go uses the stdlib `go/parser`. PHP uses a shared parser with 8.2–8.5 syntax
normalised down to the 8.1 grammar it understands (covers >99.5% of real
files). TS/Vue use a line-based extractor (approximate; there's no good
pure-Go TS parser).

`sf code` is a thin **router**: it dispatches by extension to per-language
libraries under `internal/common/code/{gocode,phpcode,tscode}` (each tested
in isolation), runs multiple files **in parallel**, and aggregates the
result. Pass a list of files: `sf code a.go b.php api/types.ts`. Or
`<file> <symbol...>` to **slice** one or more symbols (the source of each
function/method/type; Go and PHP) instead of the whole file — a symbol that
isn't found doesn't fail the others, it's just noted as missing. Invariant:
**compact-or-raw** — if a file can't be parsed, or the summary isn't
shorter, the full file is returned (== `cat`), never an error, so `sf code`
is safe to run on anything.

Below **8 KB** the tool skips structure entirely: the raw file comes back
behind a one-line `# raw: …` header (per file inside a batch; in slice mode
the whole file, with the requested symbols noted as included). The project's
own A/B measured structural round-trips losing to a plain read on small
files, so `sf code` is **never worse than `cat`** — "always use sf code" is
a safe rule. `SOFIA_CODE_RAW_BELOW=<bytes>` moves the threshold; `0`
disables the passthrough.

Every `code`/`grep`/`changed` call ends with a one-line cost footer — e.g.
`# sf ≈612 tok · raw ≈3120 · saved ≈2508` — so the per-call token economics
are visible to the agent itself; `SOFIA_FOOTER=off` hides it.

**`--api`** (PHP): the effective public surface of a class — its own methods
plus methods from `use`-d traits (recursively) plus inherited ones from
`extends`, each with a `via` column naming its source. One call instead of
`find vendor | grep`/`cat`-ing traits and parents by hand; resolved via PSR-4
(the class's own namespace plus `vendor/composer/autoload_psr4.php`),
gracefully falling back to `# unresolved` for anything it can't resolve.
`--exported` on a class with traits/a parent adds a one-line hint pointing at
`--api`.

```bash
sf code internal/server/server.go
sf code frontend/src/api/types.ts frontend/src/views/ProductsView.vue  # several at once
sf code src/User/Entity/User.php --exported    # public API only
sf code vendor/acme/lib/src/FluentThing.php --api  # the full surface: traits+inheritance
sf code internal/server/server.go Server.Routes  # slice of a single method
sf code internal/cc/cc.go Parse ingestEntry      # slice several symbols at once
```

Measured: Go **6–23×**, PHP **2–20×** (`--api` over traits/inheritance
**~10×**), TS/Vue **~6–14×** against `cat`
([docs/measurements/tools/code.md](docs/measurements/tools/code.md)).

### `sf vue routes` — vue-router route map

```bash
sf vue routes --root /path/to/frontend               # finds router/index.ts
sf vue routes frontend/src/router/index.ts --md
```

Parses `createRouter({ routes: [...] })` into a flat table `{path, name,
component, meta}` (nested `children` are flattened onto the parent). The
frontend counterpart of a backend routes command. Measured: **~6.3×** against
reading the router file
([docs/measurements/tools/vue-routes.md](docs/measurements/tools/vue-routes.md)).

### `sf changed` — classified git diff

A compact diff summary instead of reading the full `git diff`: per file —
status, churn (+/-), category (source/test/config/docs/build/migration),
language, and the **functions/classes touched** (from git's own funcname
context, no file parsing), plus per-category totals.

```bash
sf changed                 # working tree vs HEAD (+ untracked)
sf changed --staged        # staged only
sf changed HEAD~3          # last 3 commits
sf changed main..HEAD --md
```

Measured: **~48×** against the full `git diff` (22.7k→0.47k tokens), at
roughly the size of `--stat`, but with classification and touched symbols
included ([docs/measurements/tools/changed.md](docs/measurements/tools/changed.md)).

### `sf worktrees` — cross-project overview of worktree forks

Read-only, cross-project view of git-worktree dev forks under a parent
directory (`/www` by default, override with `--www`), so every parallel
session is visible at a glance. For repos that ship a `dev/worktree.sh`
script, each row is enriched with that script's stack state, health, ports
and dirty/ahead flags (via its own `ls --json`); other repos just show their
plain linked worktrees. The tool itself is read-only — create/remove forks
through the project's own `dev/worktree.sh`.

```bash
sf worktrees               # all forks (TOON)
sf worktrees --json        # machine-readable
sf wt --md                 # alias + markdown
sf worktrees --www ~/code  # scan a different parent directory
```

### `sf doctor` — installation health

Not a token tool but insurance against a silent deploy gap: one call checks
that the local `sf` install is current and working.

```bash
sf doctor          # TOON checklist; exit 1 if anything fails
sf doctor --json
```

Checks: **staleness** — `bin/sf` older than the git HEAD it's linked from
(the classic "fixed it in git but forgot to rebuild" trap, where the agent
keeps running the stale binary; core check, `fail`); **path** — whether `sf`
on `$PATH` resolves to the binary that's actually running; **completions** —
whether the fish/bash scripts are installed; **claude** — whether the
`claude` CLI is available; **hook** — whether the PreToolUse `sf hook pre`
hook is configured in `~/.claude/settings.json`; **skill** — whether the
`sf-context` skill is installed under `~/.claude/skills/` and not stale;
**calllog** — the resolved log path. A non-zero exit code on any `fail`
makes it a usable gate in a script or make target
([docs/measurements/tools/doctor.md](docs/measurements/tools/doctor.md)).

### `sf gripe` — feedback when sf didn't help

Not a token tool but a feedback channel. It catches the one failure class
invisible in `calls.jsonl`: sf exited 0 but produced the wrong thing, **or**
the agent had to fall back to `cat`/`rg`/`grep` because sf didn't cover the
case. Hard failures (non-zero exit) are already in the log and visible via
`sf history --failed --source agent`.

```bash
sf gripe 'sf code .kt does not structure it — dumped raw, had to read in full'  # record one
sf gripe                                                                        # list (newest first)
sf gripe --limit 50 --md
```

An entry is auto-tagged with project, session and time (so the text itself
can stay short) and logged like any other call (`tool=gripe`); a bare
`sf gripe` is a reader for the maintainer and doesn't itself write a log
entry. `sf doctor` surfaces the count of gripes accumulated since the last
build, so the loop closes without manual copy-pasting
([docs/measurements/tools/gripe.md](docs/measurements/tools/gripe.md)).

### `sf hook` + skill `sf-context` — guarding the Read channel

Intercepts the single biggest spend channel: full reads of large source
files. A Claude Code PreToolUse hook calls the hidden `sf hook pre` command:
a full `Read` or bare `cat` of a `.go/.php/.ts/.tsx/.vue` file ≥4K is denied
**once** with a hint toward `sf code <file>` / `sf code <file> <Symbol>`; an
identical repeat call is allowed (so a Read-before-Edit flow doesn't break).
Modes via `SOFIA_HOOK_MODE`: `off | suggest | nudge (default) | strict`;
threshold via `SOFIA_HOOK_MIN_BYTES`. The pass-through path writes nothing to
the log; fired nudges show up under `sf history --tool hook.nudge --stats`.
Fail-open: with no `sf` on `$PATH`, sessions behave exactly as before.
Configuration (global, `~/.claude/settings.json`):

```json
"hooks": {"PreToolUse": [{"matcher": "Read|Bash",
  "hooks": [{"type": "command", "command": "sf hook pre", "timeout": 10}]}]}
```

The `sf-context` skill (`skills/sf-context/SKILL.md` → `~/.claude/skills/`,
installed by `make install`) documents the same decision tree — structural
read → narrow search → point-read a single body — for any project;
`sf doctor` checks both the hook and the freshness of the installed skill
copy. Philosophy and metrics — [docs/measurements/tools/hook.md](docs/measurements/tools/hook.md).

### `sf composer` — PHP package tree overview

Compact views over the `composer.json` files in a tree instead of `cat`-ing
each one by hand (plus `git tag` and grepping `phpstan.neon`). Useful for a
monorepo or a set of sibling package repos.

```bash
sf composer ls /path/to/your/packages   # one row per package: version/type/php/phpstan/scripts/deps/dev
sf composer show array-reader           # one package in detail (scripts with their commands, all deps)
sf composer check enum                  # run the check gate, collapsed to pass/fail
sf composer check --root /path/to/your/packages   # every package
```

- **`ls`** — walks the tree (`internal/walker`) → TOON `{pkg, version
  (git tag), type, php, phpstan, scripts, deps, dev}` (`deps` = require,
  `dev` = require-dev, both without `php`/`ext-*`). Measured: **~7.3×**
  against reading 9 `composer.json` files
  ([docs/measurements/tools/composer-ls.md](docs/measurements/tools/composer-ls.md)).
- **`show`** — full metadata for one package (by name/dir/path): scripts
  with their commands, require/require-dev. Measured **~2.6×**.
- **`check`** — runs each package's `check` script (test+phpstan+cs) and
  collapses the verbose output into `{pkg, status, exit, dur_ms, fail}`.
  Measured: **~17×** per package, ~30× across a whole collection
  ([docs/measurements/tools/composer-check.md](docs/measurements/tools/composer-check.md)).

### `sf packagist` — release status vs Packagist

For every package in a tree: local tag vs whether it's pushed vs the latest
version on Packagist → shows what still needs tagging/updating (the webhook
doesn't fire for every package).

```bash
sf packagist status /path/to/your/packages
sf packagist status --offline /path/to/your/packages   # local tags only
sf packagist release array-reader 2.1.0 --dry-run       # preview a release
sf packagist release array-reader 2.1.0                 # tag+push+update+verify
```

- **`status`** — one row per package: `{pkg, local_tag, pushed, packagist,
  state}`; `state` = in-sync | needs-update | unpublished | no-tags |
  local-stale. Measured: **~23×** against a manual check (git plus parsing 9
  p2 documents)
  ([docs/measurements/tools/packagist-status.md](docs/measurements/tools/packagist-status.md)).
  Read-only (HTTP to p2 + `git ls-remote`).
- **`release <pkg> <version>`** — **mutating**: an annotated tag (reused if
  it already exists) → push to origin → Packagist's `update-package` →
  polling p2 until the version shows up. Token: `$PACKAGIST_API_TOKEN`, or a
  per-vendor dotfile under `~/.config/<vendor>/packagist.env`. `--dry-run`
  prints every step without touching anything; `--allow-dirty`,
  `--username`, `--timeout`
  ([docs/measurements/tools/packagist-release.md](docs/measurements/tools/packagist-release.md)).

### `sf github` — CI runs, PR digest, branch cleanup

```bash
sf github ci array-reader --root /path/to/your/packages   # latest runs
sf github ci enum --root /path/to/your/packages --watch    # wait for the final status, one row
sf github ci --root /path/to/your/packages                 # rollup across the whole tree

sf github pr            # your open PRs across all repos + CI status
sf github pr --md       # markdown table (for humans)

sf github branches              # non-default branches across your own repos
sf github branches --delete     # delete branches whose PR is already merged
```

- **`ci`** wraps `gh run list/view` into TOON `{id, workflow, status,
  conclusion, branch, event, created, title}`. The listing itself is
  roughly 1× on tokens (but scriptable, and resolves a package by name);
  the real win is `--watch`: silent polling until the run finishes plus one
  final row, instead of `gh run watch`'s verbose stream (and a non-zero exit
  code on failure). When the target is a tree of packages (the root isn't a
  git repo itself), it prints a per-package rollup — one row per package
  with its latest run (a `pkg` column, `# failing=K` in the header when
  something's red) — one call instead of looping over packages by hand
  ([docs/measurements/tools/github-ci.md](docs/measurements/tools/github-ci.md)).
- **`pr`** — a single `gh api graphql` call collects every open PR
  (`author:@me` + `review-requested:@me`) across all repos, plus a CI rollup
  of the head commit and the review decision. TOON
  `{repo, num, ci(✓/✗/⏳/–), review, role, draft, title}`; PRs needing
  action (broken CI / changes requested) sort first. The saving comes from
  **collapsing** per-PR `gh pr checks` into one rollup (like `--watch`
  above): **14.7×** on a real run, and the ratio grows with the number of
  PRs and matrix jobs
  ([docs/measurements/tools/github-pr.md](docs/measurements/tools/github-pr.md)).
- **`branches`** — lists every non-default branch across your own
  (non-fork, non-archived) repos with age, the newest associated PR, and a
  status (merged | closed | open | no-pr); branches safe to delete (PR
  already merged) sort first. A single `gh api graphql` call. `--delete`
  removes branches whose PR is merged (`--delete=closed` also removes
  closed-PR branches); worktree branches are always left for manual removal.

### `sf cc` — Claude Code session analysis

A context provider over the agent's own transcripts
(`~/.claude/projects/**.jsonl`). Unlike `sf history` (which is about
**sofia's own** `calls.jsonl`), `cc` reads Claude Code's own sessions: 2–4 MB
of raw JSON compressed into a TOON digest of a few hundred tokens. The
project root is resolved from `--projects-dir`, then `$CC_PROJECTS_DIR`,
then `~/.claude/projects`.

```bash
sf cc ls                          # session index across all projects, newest first
sf cc ls --project myproject --since 24h
sf cc show last                   # digest of the last session
sf cc show 6bd96fc7                # by session-id prefix
sf cc show myproject               # last session of a project, by name
sf cc resume myproject             # a tiny brief for a cheap session restart
sf cc prompts myproject            # human turns only, in full
sf cc bash myproject --category db # executed commands: deduped, categorised, counted
sf cc candidates --project myproject # tool candidates: what's repeated/expensive
```

A session selector resolves everywhere the same way as an xref lookup:
`last` → id prefix → project name → path.

- **`show`** — an on-screen digest: metadata, **real** token counts from the
  transcript's own `usage` records, human prompts (system-reminders,
  continuation summaries and tool_results filtered out), a tool-call
  histogram, a bash breakdown by category, files touched, token-heavy
  results, PRs. Measured: **~400–940×** against reading the raw transcript
  ([docs/measurements/tools/cc-show.md](docs/measurements/tools/cc-show.md)).
- **`ls`** — a session index with real out/cache tokens; ≈22 tokens/session.
- **`prompts`** — human turns verbatim, in order.
- **`bash`** — commands deduped + categorised (search/read/git/test/build/db/
  fs) + counted; `--full` for the full text, `--min-count`/`--category`.
- **`candidates`** — a meta-tool: scans one or many sessions and ranks where
  the token budget actually goes, by **measured** tokens: `heavy_tools`
  (tokens per tool), `repeated_commands`, `repeated_reads`. This is
  "measure the value before building" as a command — how the next tool
  candidates get found.

### Output format

Every tool defaults to **TOON** (Token-Oriented Object Notation) — a compact
format built for LLM tokens. The same aliases work across every tool:

```bash
sf grep --md   TODO   # equivalent to --format=md
sf grep --json TODO   # equivalent to --format=json
```

## Shell completion

Cobra generates dynamic suggestions: package names, `--format` values,
directories for `--root`. Both Fish and Bash.

**Fish:**

```fish
./bin/sf completion fish | source                                       # for this session
./bin/sf completion fish > ~/.config/fish/completions/sf.fish           # permanently
```

**Bash:**

```bash
source <(./bin/sf completion bash)                                                       # for this session
./bin/sf completion bash > ~/.local/share/bash-completion/completions/sf                 # permanently
```

Once installed, `<Tab>` works:

```
sf composer show <Tab>          → array-reader, enum, ...
sf grep --ignore-dir <Tab>      → vendor, node_modules, ...
sf grep --format <Tab>          → toon, md, json
```

## Call history

Every call is written to a JSONL log. The path is resolved by priority:

1. `$SOFIA_LOG_DIR/calls.jsonl` — explicit override (CI, dev mode).
2. `$XDG_STATE_HOME/sofia/calls.jsonl` — XDG default.
3. `~/.local/state/sofia/calls.jsonl` — fallback (matches the XDG spec's
   own default).

Every sofia binary (`sf` and the standalone binaries) writes to the same
file, so `sf history` aggregates the full picture. Every entry is tagged
with a `source` — `agent` (how Claude invokes it; detected via
`CLAUDECODE=1` in the Bash tool's environment), `manual` (typed by hand in a
terminal), or `test` (`go test` runs never write to the shared log at all).
Entries also carry `sid` — Claude's session id
(`CLAUDE_CODE_SESSION_ID`, matches the transcript filename → joins with
`sf cc`) — and `tag` — the project (`SOFIA_TAG`, or the basename of the git
root otherwise). Parallel sessions don't interfere with each other: the env
is inherited down the process tree, and log writes are atomic.

```bash
sf history                                  # last 20 calls
sf history --since 24h --limit 50           # last 24h
sf history --tool grep                      # a single tool
sf history --stats --source agent           # clean agent-only aggregates (no manual/test noise)
sf history --stats --top-inputs 10          # aggregates + top-10 input arguments
sf history --session 6bd96fc7               # one session (same id as `sf cc`)
sf history --tag myproject --source agent   # agent traffic for one project
sf history --adoption --since 7d            # adoption by project × source over a week
sf history --stats --format md              # markdown, for reports
sf history --histogram=hour                 # calls by hour of day
sf history --histogram=day --since 7d       # last week, by day
sf history --clear                          # truncate the log
```

`--adoption` aggregates by `(project, source)`: call count, distinct
sessions (by `sid`), errors, failed% and total tokens — which projects
actually adopt `sf` (agent) versus running it by hand.

`--stats` reports, per tool: call count, errors, total/mean/p50/p95/max
duration, total output size, and the top-N most frequent arguments (to spot
repeated queries — candidates for caching).

### Sample entry

```json
{"ts":"2026-05-21T01:36:00Z","tool":"grep","source":"agent",
 "sid":"fb92295c-9af0-4931-960c-8d0567ec309f","tag":"myproject",
 "args":["--format=toon","DeleteUser"],
 "fp":"0f290d253081ac56","dur_ms":477,"exit":0,"out_bytes":4533,"out_tokens":1180,
 "summary":{"inputs":["DeleteUser"],"queries":1,"total_hits":22}}
```

### Log fields (useful for analysis)

| Field | What it gives you |
|---|---|
| `ts` | When it was called — time-of-day patterns |
| `tool` | Which tool — per-tool breakdowns |
| `source` | `agent` (Claude, via `CLAUDECODE=1`) / `manual` / `test` — separates agent traffic from manual use |
| `sid` | Claude session id (`CLAUDE_CODE_SESSION_ID`, = transcript filename) — per-session slicing, joins with `sf cc` |
| `tag` | Project (`SOFIA_TAG`, or basename of the git root) — adoption by project |
| `args` | Exact arguments — for reproducing a call |
| `fp` | SHA-256 prefix of the sorted args — groups equivalent queries |
| `dur_ms` | Duration — total/mean/p50/p95 |
| `exit` | Exit code — error rate |
| `out_bytes` | Size of the stdout payload, in bytes |
| `out_tokens` | Approximate LLM token cost (see `internal/tokens`: ASCII bytes/4 + non-ASCII runes×1) |
| `summary.inputs` | Canonical names/values that top-inputs is built from |
| `summary.total_hits` | How many places in the code matched — spotting bloated responses |

### What might get added later

- **Cache hits** — once tools learn to cache, a `cache_hit` field in the log.
- **Delta between calls sharing an `fp`** — to detect speed regressions.
- **Anti-pareto: rarest but slowest `fp`** — optimisation candidates.
- **A real tokenizer (tiktoken-go)** — the ASCII heuristic currently
  diverges from actual billing by ±20–30%; fine for trends, not for literal
  cost attribution.

## Packs

`sf pack install <git-url|dir>` installs a "pack" — a git repo or local
directory holding a `pack.yaml` — laying out everything it declares: sf
plugins (`$XDG_DATA_HOME/sofia/plugins`), Claude skills/commands (`$CLAUDE_DIR`,
env override, default `~/.claude`), and project instructions/templates
(`--project`, default cwd). A destination the pack doesn't own, or one edited
by hand since install, blocks the install as a conflict; `--force` overwrites
it.

```yaml
schema: 1
name: xcraft
description: CRM agent pack
plugins:
  - path: plugins/crm
instructions:
  - src: instructions/AGENTS.md
claude:
  skills: [ { src: skills/my-skill } ]
```

`sf pack list` / `info <name>` / `status [<name>]` / `uninstall <name>` round
out the lifecycle.

## Layout

```
sofia/
├── cmd/                          # Go binary entry points (one per tool)
│   ├── sf/                       # master CLI with all subcommands
│   └── common/<tool>/            # standalone binaries: grep, cc, code, changed, doctor,
│                                  #   composer, packagist, github, vue, worktrees
├── internal/                     # reusable packages
│   ├── calllog/                  # JSONL call log (XDG path) + Counter + Fingerprint
│   ├── cc/                       # `sf cc` — Claude Code session digests
│   ├── cli/                      # Cobra command tree for the master binary (RootCmd)
│   ├── cliflags/                 # shared flag helpers (--md/--json, dir completion, arg hints)
│   ├── codectx/                  # enclosing-function lookup for PHP/TS/Twig/INI
│   ├── common/changed/           # `sf changed` — classified git diff
│   ├── common/code/              # `sf code` — structural file summary (Go/PHP/TS/Vue)
│   ├── common/composer/          # `sf composer` — PHP package tree overview
│   ├── common/doctor/            # `sf doctor` — installation health (staleness)
│   ├── common/github/            # `sf github` — CI runs, PR digest, branch cleanup
│   ├── common/grep/              # `sf grep` — cross-project search
│   ├── common/gripe/             # `sf gripe` — feedback on silent misses
│   ├── common/hook/              # `sf hook pre` — PreToolUse guard for the Read channel
│   ├── common/packagist/         # `sf packagist` — release status + publishing
│   ├── common/php/               # PhpSymbolReader (VKCOM/php-parser AST)
│   ├── common/vue/               # `sf vue routes` — vue-router route map
│   ├── common/worktrees/         # `sf worktrees` — cross-project worktree overview
│   ├── emit/                     # output budget: compact-or-raw (SmallerOf)
│   ├── envfile/                  # .env load/save/prompt
│   ├── tokens/                   # fast heuristic LLM token estimate
│   ├── history/                  # `sf history` — reads and aggregates the call log
│   ├── matcher/                  # line-based search (literal + regex)
│   ├── strdist/                  # Levenshtein for typo hints (did you mean)
│   ├── toon/                     # TOON primitives (Scalar, JoinList)
│   └── walker/                   # parallel tree walker
└── bin/                          # gitignored — build artifacts
```
