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
