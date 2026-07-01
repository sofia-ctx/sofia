# Contributing

`sf` exists for one reason: **token economy**. Everything a tool hands back
to an LLM should be cheap in tokens and unambiguous in structure. Every
decision below — what to build, where it lives, how it's tested — is judged
against that.

## Working discipline

1. **Measure before you build.** Before writing a tool, estimate its value
   against a real target: a scripted-equivalent baseline (`cat` plus a
   chain of `rg`) against a projection of the tool's output. Count tokens
   with the `internal/tokens` heuristic (ASCII bytes/4 + non-ASCII runes).
   Numbers must be real — no measurement means the doc says `TBD`, not "N×
   savings." A ~1× result means the tool probably isn't worth building.
2. **An economy doc is mandatory.** Every tool needs a
   `docs/measurements/tools/<tool>.md`: "without the tool" vs "with the tool," the
   ratio, and where it stops helping (format — see `docs/README.md`).
   Numbers come from a real run (`sf history --tool <name> --stats`), never
   from a guess.
3. **YAGNI, no redundancy.** Don't build a foundation for a consumer that
   doesn't exist yet; don't reimplement something already covered.
   Correctness beats premature minimalism — for example, PHP is parsed via
   an AST (`internal/common/php`, on VKCOM/php-parser), not regexes.
4. **Ownership: common vs. project-specific.** A two-look test: if a tool
   hardcodes a project's names/paths/schema, it belongs under
   `internal/projects/<project>/`; if stripping the project's name from the
   API still makes sense and another project would plausibly want the same
   tool, it belongs under `internal/common/`.

## Adding a tool

A short checklist; see `PLAN.md`'s "Conventions for future tools" for more
detail.

1. Package under `internal/common/<tool>/` (cross-project) or
   `internal/projects/<project>/<tool>/` (project-specific), exporting
   `NewCommand() *cobra.Command`.
2. A standalone entry point at `cmd/common/<tool>/main.go` (a thin wrapper),
   plus a one-line registration in `internal/cli/root.go`.
3. Logging: `calllog.Start(...)` → `tracker.RecordOutput(cw)` →
   `Finish(err)`; write output through `calllog.Counter`; fill in a
   structured `summary`.
4. Formats: `cliflags.AttachFormatFlags(cmd, &format)`, rendering through
   `internal/toon` for a consistent shape.
5. Reuse existing building blocks rather than duplicating them: PHP parsing
   via `internal/common/php`; search via `walker`+`matcher` (or
   `internal/common/grep`).
6. A table-driven `*_test.go` alongside the package, especially for parsers
   and resolvers.
7. `docs/measurements/tools/<tool>.md` with a real measurement, plus an entry in
   `README.md`.

If your fork carries project-specific agent instructions (an
`instructions/<project>/instruction.md`), keep them in sync when a tool's
behaviour changes — an updated tool without an updated instruction is
unfinished work.

## Build, test, verify

```bash
make                # no target: self-documented help
make build           # every binary into bin/**
make check           # go vet + go test (the pre-commit gate)
```

Before committing:

- `go test ./...` is green.
- `gofmt -l` on the packages you touched prints nothing.
- `sf doctor` after `make install` if you're testing changes to the CLI
  itself — it catches the classic "fixed it in git, forgot to rebuild"
  trap (a stale `bin/sf` still on `$PATH`).

CI runs the same gate (`go vet`, `go test ./...`, `gofmt -l`) on every pull
request; a build that fails locally will fail there too.

Smoke-test a new tool against a real project, not just its unit tests —
that's where the economy-doc numbers come from.

## Git workflow

Commit messages: short, in the imperative, about *why* rather than *what*,
matching the style already in `git log`. Keep commits scoped to one logical
change.
