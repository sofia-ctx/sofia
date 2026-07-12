# Personal project overlays

A project's shared `AGENTS.md` is for everyone on it. An **overlay** is the part
that's just yours — how *your* agent should work this project, plus your own
slash-commands for it — kept in a private repo instead of the shared one.
`sf claude <tag>` loads it on top of the project, with precedence over the repo's
`AGENTS.md`, and opens its dir for editing so you can refine it in-session and
push it back.

Different people drive the same project with different agents and harnesses;
their personal instructions don't belong in the shared repo. An overlay keeps
them out of it while staying available on every machine you clone the overlay
repo to.

## Layout

Overlays live under an overlays root — `$SF_CLAUDE_OVERLAY_DIR`, else
`$XDG_DATA_HOME/sofia/overlays` — as one or more cloned repos, each holding a dir
per project tag:

```
<overlays root>/
└── <repo>/                 # a clone of your private overlay repo
    ├── projectA/
    │   ├── AGENTS.md            # instructions — injected as a system prompt
    │   ├── .claude-plugin/
    │   │   └── plugin.json      # makes the tag dir a Claude Code plugin
    │   └── commands/
    │       └── release.md       # a slash-command: /<plugin-name>:release
    └── projectB/
        └── AGENTS.md            # a tag can carry instructions, commands, or both
```

The tag is the project name you launch — `sf claude projectA` reads the
`projectA/` dir. A tag dir counts as an overlay if it has an `AGENTS.md`, a
plugin manifest, or both. If two cloned repos define the same tag, the
alphabetically-first repo wins (and sf says so).

## Use it

```bash
sf claude overlay add git@github.com:you/overlays.git   # clone your private repo
sf claude overlay list                                  # repos and the tags they provide
sf claude projectA                                      # launches with the projectA overlay applied
```

At launch sf adds up to three things to claude's invocation:

- `--add-dir <overlay dir>` — so the session can read and edit the overlay files.
- `--append-system-prompt` — the `AGENTS.md` text (if any), prefixed with a note
  that it takes precedence over the repo's `AGENTS.md` on conflict. System-prompt
  text outranks the project file claude reads from cwd, so the overlay wins.
- `--plugin-dir <overlay dir>` — when the dir is a Claude Code plugin, so its
  commands load for the session only.

`--no-overlay` skips all of it for one launch. `sf claude <tag> --dry-run` prints
exactly what would be passed, including the overlay dir, the resolved prompt, and
the plugin dir.

## Commands

To ship personal slash-commands with an overlay, make the tag dir a Claude Code
plugin: add a `.claude-plugin/plugin.json` (at minimum `{"name": "...",
"version": "..."}`) and a `commands/<name>.md` per command (markdown, optional
`description` frontmatter). sf loads it with `--plugin-dir`, so the command is
available as `/<plugin-name>:<name>` for that session — never installed globally,
never in the shared repo. The command file's body is the prompt; use `!` inline
or a fenced `!` block to splice in shell output (e.g. `git status`) before the
model sees it.

## Editing and syncing

The overlay dir is a normal git checkout. Edit `AGENTS.md` (in-session or not),
commit, and:

```bash
sf claude overlay sync <repo>     # git pull --ff-only, then push
sf claude overlay sync --all      # every clone
```

On another machine, `sf claude overlay add` the same repo and the overlay travels
with you.

The private repo is cloned over whatever URL you give `add` — SSH
(`git@github.com:you/overlays.git`) authenticates through your ssh-agent, no
tokens needed.
