# sf for Codex CLI

`sf` isn't Claude-Code-specific: the same binary works as a Codex hook and
MCP server, and `sf init` wires both up automatically once it detects Codex
on the machine.

## Quickstart

```bash
sf init
```

`sf init` always writes the vendor-neutral `AGENTS.md` block, regardless of
which agents are installed — Codex, Claude Code, and anything else that
reads `AGENTS.md` share it. If `~/.codex` exists (or `$CODEX_HOME`), it also:

1. wires the PreToolUse hook into `$CODEX_HOME/config.toml`
2. registers the `sofia` MCP server in the same file
3. installs the `sf-context` skill to `$HOME/.agents/skills/sf-context/SKILL.md`

Each of the three is reported `skipped` (not failed) when Codex isn't
detected, exactly like the existing Claude Code steps. `sf init --corporate`
skips all of it — Codex included — down to just the `AGENTS.md` block; see
[Enterprise lockdown](#enterprise-lockdown) below.

## What the hook does on Codex

Codex has no `Read` tool — a model reads a file by running `cat` through
`Bash`. `sf hook pre` already parses that shape (a bare `cat <file>` with no
pipe/redirect/flags is a full-file dump, same as a Claude Code `Read`), so
the identical binary works unmodified: same stdin fields
(`session_id`/`cwd`/`tool_name`/`tool_input`), same
`hookSpecificOutput.permissionDecision` response. The only difference is the
matcher — `^Bash$` instead of `Read|Bash`, since there's no `Read` call to
match on Codex. See the payload/response contract in OpenAI's own docs:
<https://developers.openai.com/codex/hooks>.

Per that same page, Codex's `PreToolUse` intercepts `Bash`, `apply_patch`,
and MCP tool calls — not `WebSearch` or other built-ins — which is a
non-issue here since the hook only ever matches `Bash`.

## Manual TOML snippets

`sf init` appends rather than parses `config.toml` — a new top-level table
at EOF is always valid TOML and leaves your existing tables/comments
untouched by construction, which is also why it's safe to do by hand. If you
don't want `sf init` touching `$CODEX_HOME/config.toml`, paste these in
yourself:

```toml
# sf:hook:begin — managed by `sf init`
[[hooks.PreToolUse]]
matcher = "^Bash$"

[[hooks.PreToolUse.hooks]]
type = "command"
command = "sf hook pre"
timeout = 10
# sf:hook:end
```

```toml
# sf:mcp:begin — managed by `sf init`
[mcp_servers.sofia]
command = "sf"
args = ["mcp"]
# sf:mcp:end
```

MCP config reference: <https://developers.openai.com/codex/mcp>.

## Skills path

Codex reads `SKILL.md` folders following the agentskills.io standard from
`$HOME/.agents/skills` (user-level) and `<project>/.agents/skills`
(project-level) — the same frontmatter `sf-context` already uses for Claude
Code, so nothing project-specific needs to change:
<https://developers.openai.com/codex/skills/>.

## Enterprise lockdown

A managed deployment can set `allow_managed_hooks_only`, which kills
user-level hooks (and can gate MCP servers behind an allowlist) — see
<https://developers.openai.com/codex/enterprise/managed-configuration>. Both
`config.toml` steps go dark under that policy. `AGENTS.md` instruction files
aren't config, so they survive lockdown untouched — which is exactly what
`sf init --corporate` targets: it writes only the `AGENTS.md` block, so a
locked-down seat still gets sf's guidance even with hooks and MCP off the
table.

## Quota pitch

Codex has metered tokens against 5-hour and weekly quotas since 2026-04-02,
the same unit Claude Code subscriptions reset on. `sf cc value --quota`
reads sf's own call log, not agent transcripts, so its savings numbers
already include any `sf code`/`sf grep`/`sf hook pre` calls a Codex session
made — cutting raw tokens out of a session stretches the same quota either
agent runs against.
