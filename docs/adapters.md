# Tier-1 adapters

An **adapter** is a plugin that ships no code. It declares a project's
conventions — where the root is, which files count, what the layers are — and
the host interprets that declaration directly, synthesizing project-aware
commands over its own generic readers. It is the most declarative of sofia's
three plugin tiers:

- **Tier 1 — adapter** (this page): a `plugin.yaml` `adapter:` block, no
  executable. The host resolves the project root, classifies files into layers,
  and builds `layers`/`grep`/`refs` from the block.
- **Tier 2 — subprocess plugin** ([plugins.md](plugins.md)): an executable plus a
  manifest. The host dispatches to it; the plugin does whatever it likes.
- **Tier 3 — Go SDK**: compiled in. Not yet shipped.

Reach for an adapter when everything you want is "run the generic tools, but
scoped to my project and grouped by my layers". Reach for a subprocess plugin
the moment you need real parsing or logic the host can't express declaratively.
A plugin can be both: an `adapter:` block *and* an `exec` — it gets the
synthesized commands and its own.

## The block

```yaml
schema: 1
protocol: "1.0.0"
description: "PHP DDD layer adapter"
adapter:
  kind: php-ddd
  root_key: PROJECT_ROOT        # optional
  root_markers: [composer.json] # required
  ext: [php]                    # optional
  layers:                       # optional
    - name: Domain
      match: ["src/Domain/**"]
    - name: Application
      match: ["src/Application/**"]
    - name: Infrastructure
      match: ["src/Infrastructure/**"]
```

- **`kind`** — a free-form name for the adapter family. Informational.
- **`root_markers`** *(required)* — files that mark a project root. The host
  walks up from the cwd to the nearest ancestor containing any of them. Each must
  be a safe relative path (no absolute paths, no climbing above the root).
- **`root_key`** *(optional)* — an environment variable that, when set to an
  existing directory, pins the root outright, ahead of the walk-up. The escape
  hatch for working outside the tree.
- **`ext`** *(optional)* — extensions `grep`/`refs` scope to (`php` and `.php`
  are equivalent; the host normalizes to a leading-dot, lower-case form). Omit to
  search every file.
- **`layers`** *(optional)* — named globs. A file is classified into the **first**
  layer (in declared order) whose any `match` glob hits its root-relative path; a
  file no layer claims is `(unclassified)`. Each layer needs a unique, non-empty
  name and at least one glob, and every glob must be a safe relative path.

A pure adapter declares no `exec` and no `commands`, so it has no binary on disk.
It is enabled on the strength of its adapter block alone; `sf plugin list` shows
it enabled, and `sf --help` lists it, without anything ever being forked.

## Globs

Matching is segmented over `/`:

- a single `*` (and `?`, `[…]`) matches within one path segment and never crosses
  a `/` — so `src/*` matches `src/User.php` but not `src/Domain/User.php`;
- `**` matches zero or more whole segments — so `src/Domain/**` matches
  `src/Domain` itself and everything beneath it, and `**/*.php` matches a `.php`
  file at any depth.

## Root resolution

Every synthesized command resolves the project root the same way, highest
precedence first:

1. an explicit `--root DIR` on the command;
2. `$root_key`, when the block names one and it points at an existing directory;
3. a walk up from the cwd for any `root_markers` file.

If none of these finds a root, the command fails with a message naming the
markers it looked for — it never silently searches the wrong tree.

## The synthesized commands

- **`sf <name> layers [<path>]`** — list the declared layers, or classify one
  path into its layer.
- **`sf <name> grep <pattern>…`** — the generic `sf grep`, scoped to the
  adapter's `ext` and rooted at the resolved project root, with the hits grouped
  by layer.
- **`sf <name> refs <symbol>`** — the generic `sf refs`, same scoping, defs and
  uses grouped by layer.

All three take `--format` (`toon`/`md`/`json`) and `--root`. Layer groups render
in declared order with `(unclassified)` last, so the output is byte-stable across
runs. Each invocation writes one `calls.jsonl` line (`<name>.layers`,
`<name>.grep`, `<name>.refs`) — the same telemetry every other `sf` command gets.

## Scaffold one

```bash
sf plugin new php-ddd --adapter
```

writes a `plugin.yaml` with an adapter block (one active example layer, the rest
commented) and a README, and nothing else — no executable. It installs and
enables as-is:

```bash
sf plugin install ./php-ddd
```

A canonical, installable example lives at
[`adapters/example/`](../adapters/example/plugin.yaml) in this repository.
