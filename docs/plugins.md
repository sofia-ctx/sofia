# Your first sf plugin, in 5 minutes

A plugin is just an executable plus a manifest. This walks through scaffolding
one, running it, and distributing it — the full authoring contract (the
invocation env, protocol versioning, capabilities, settings) lives in the
`internal/plugin` package doc (`go doc ./internal/plugin` from a checkout, or
read [plugin.go](../internal/plugin/plugin.go) directly); this page stays a
tutorial and links there instead of repeating it.

## Scaffold it

```bash
sf plugin new hello
```

This creates `./hello/`:

```
hello/
├── plugin.yaml   # manifest: protocol, one example command, a commented settings block
├── hello         # the executable — a POSIX-sh stub, chmod 0755
└── README.md     # how to test and distribute this plugin
```

It's installable as-is — the manifest already declares a real protocol
version and a runnable executable — so you can try it immediately, before
changing a line:

```bash
sf plugin install ./hello
sf hello greet world
```

```
Hello, world!
format=toon root=/path/to/your/project
```

Those two env vars (`SOFIA_FORMAT`, `SOFIA_PROJECT_ROOT`) are the first two
of five the host always sets; see the package doc's "Invocation contract" for
the rest.

## Edit the stub

Open `./hello/hello`. It's a `case` statement keyed on the first argument
(the resolved command path); add a branch for your own logic, or extend
`greet`. Add a new subcommand by giving it an entry under `commands:` in
`plugin.yaml` and a matching case in the script.

## Iterate

- Edited the **executable**? Nothing to refresh — sofia execs it fresh on
  every invocation, so `sf hello greet` picks up the change immediately.
- Edited **`plugin.yaml`** (a new command, a new setting, the description)?
  That gets cached, so run `sf plugin update` (or reinstall with
  `sf plugin install ./hello`) to make `sf --help` and dispatch see it.

## Distribute it

Push the plugin directory to a git repo. Anyone installs it straight from
the URL:

```bash
sf plugin install https://github.com/you/hello
```

— no source directory required; sofia clones it, strips `.git`, and installs
exactly as if it were local. When you push a new commit, users pick it up
with:

```bash
sf plugin upgrade hello
```

`upgrade` re-clones the URL and ref recorded at install time and reports the
commit it moved from/to (`upgraded hello: 1a2b3c4 → 9f8e7d6`, or `hello is up
to date (1a2b3c4)` if the ref's tip hasn't moved). Run it with no name to
upgrade every git-installed plugin at once; anything installed from a local
directory is reported and left alone.

### Distributing a binary plugin

The `hello` fixture above ships its executable straight in the repo, which is
fine for a shell-script plugin but doesn't scale to a compiled one — you don't
want to commit a binary per platform. Instead, publish it as a **GitHub
release** and let `sf plugin install` fetch the right one:

1. Build with [goreleaser](https://goreleaser.com), `formats: [binary]` (bare
   executables, no tar/zip):

   ```yaml
   # .goreleaser.yml
   builds:
     - id: myplugin
       binary: myplugin
   archives:
     - formats: [binary]
       name_template: "myplugin_{{ .Os }}_{{ .Arch }}"
   checksum:
     name_template: checksums.txt
   ```

   `goreleaser release` then uploads `myplugin_linux_amd64`,
   `myplugin_darwin_arm64`, … and a `checksums.txt` to the GitHub release.

2. Declare it in `plugin.yaml` — `exec:` names the file the download lands as
   (not committed to the repo), and `release.asset` is the same name template,
   with `{os}`/`{arch}` standing in for `runtime.GOOS`/`runtime.GOARCH`:

   ```yaml
   schema: 1
   protocol: "1.1.0"
   min_sf: "1.1.0"
   exec: myplugin
   release:
     asset: "myplugin_{os}_{arch}"
   ```

3. Consumers just `sf plugin install https://github.com/you/myplugin`. If the
   clone carries no `myplugin` executable, install fetches the matching asset
   from the repo's latest release (or the release tagged `--ref`) over https
   and verifies it against `checksums.txt` before installing — no `gh` CLI, no
   archive extraction, no signing. `min_sf: "1.1.0"` makes an sf older than
   this feature report a clean "requires host protocol >= 1.1" instead of
   silently disabling the plugin for having no binary.

   A repo that already ships its binary in-tree, or a local-directory
   install (`sf plugin install ./myplugin`), never consults `release:` — it's
   purely a fallback for the "clone has no exec" case.

## Adapter (Tier-1): a plugin with no code

Some plugins don't need an executable at all — they just declare a project's
conventions and let the host do the work. A **Tier-1 adapter** is a `plugin.yaml`
with an `adapter:` block and no `exec`. From that block the host synthesizes
three project-aware commands, grouped by the layers you name:

```bash
sf plugin new php-ddd --adapter
sf plugin install ./php-ddd
cd your-php-project      # a dir with the adapter's root marker somewhere above
sf php-ddd layers                    # the declared layers
sf php-ddd layers src/Domain/User.php  # → Domain
sf php-ddd grep User                 # matches, grouped by layer
sf php-ddd refs User                 # defs/uses, grouped by layer
```

The block:

```yaml
schema: 1
protocol: "1.0.0"
description: "PHP DDD layer adapter"
adapter:
  kind: php-ddd
  # root_key: PROJECT_ROOT      # optional: an env var that pins the root
  root_markers: [composer.json] # required: how the host finds the project root
  ext: [php]                    # optional: scope grep/refs to these extensions
  layers:                       # optional: named globs, matched in this order
    - name: Domain
      match: ["src/Domain/**"]
    - name: Application
      match: ["src/Application/**"]
    - name: Infrastructure
      match: ["src/Infrastructure/**"]
```

The three synthesized commands:

- **`layers [<path>]`** — with no argument, lists the declared layers; with a
  path, classifies it into one (or `(unclassified)`).
- **`grep <pattern>…`** — searches the project (scoped to `ext`) and groups the
  hits by layer.
- **`refs <symbol>`** — finds a symbol's definitions and uses and groups them by
  layer.

Each takes `--format` (`toon`/`md`/`json`), `--root` (skip resolution and point
at a directory), and resolves the project root the same way otherwise: `$root_key`
if set, else a walk up from the cwd for a `root_markers` file. Globs understand
`**` (zero or more path segments); a single `*` never crosses a `/`. Layers are
matched top to bottom, so the first that claims a path wins and the output order
is stable. See [docs/adapters.md](adapters.md) for the full concept.
