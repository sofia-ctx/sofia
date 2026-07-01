# measurements

Durable measurement docs that don't fit in the binary's `--help`. Two kinds,
kept in separate directories so a single per-command number and a full A/B
evaluation aren't filed as the same kind of evidence:

## `tools/`

Token-economy writeups, one per command — the mandatory artifact behind the
"measure first" discipline ([CONTRIBUTING.md](../../CONTRIBUTING.md)). One
file per tool: `tools/<tool>.md`.

Format of each file:
- **Methodology** — what was measured (the `internal/tokens` heuristic), how
  the baseline was built (a real LLM session / a scripted equivalent /
  structural comparison), against what target.
- **Scenario** — a table of "without the tool" (the model's steps) vs "with
  the tool" (one command), with token counts and the ratio.
- **Scaling** — how the win changes with input size.
- **Boundary of applicability** — where the tool does *not* help.

Rule: numbers must be real measurements. If a tool isn't implemented yet, the
file is marked `Status: projection` and the tool-side number gets replaced
with a real `sf history --tool <name>` figure once it exists. Numbers are
never invented.

## `evaluation/`

End-to-end A/B evaluations of `sf` itself — an `sf`-assisted agent session
against a plain baseline on the same real task, judged and measured, not
projected. [`micro.md`](evaluation/micro.md) is per-task; [`macro.md`](evaluation/macro.md)
is a full plan-then-implement pass on a real feature. These are a different
kind of evidence than `tools/`: a per-tool doc says "this one command saves
N tokens on this one operation," an evaluation run says "here's what actually
happened to a whole session, including the parts that didn't help."
