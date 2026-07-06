// Package mcpserver exposes sofia's public-safe Context Providers to LLM
// coding agents over the Model Context Protocol (MCP), on the stdio
// transport. It is a thin adapter: every MCP tool wraps the very same Go
// function the corresponding `sf` Cobra command calls (code.Run, grep.Run,
// cc.RunShow, …) — the server never shells out to the `sf` binary.
//
// # Transport
//
// stdio only. `sf mcp` speaks newline-delimited JSON-RPC over stdin/stdout,
// which is the transport MCP clients (Claude Desktop/Code, mcp-inspector)
// launch a local server with. Because the JSON-RPC framing owns os.Stdout,
// every wrapped tool renders into a private bytes.Buffer (an io.Writer we
// pass in), never os.Stdout — see internal/cc/mcp.go for why cc needed thin
// exported wrappers to make that possible.
//
// # Exposed tools
//
// Only the public-safe Context Providers ship here (the private project /
// launch / lab tools are deliberately absent):
//
//	code            grep            changed         vue_routes
//	cc_ls  cc_show  cc_resume  cc_prompts  cc_bash  cc_candidates
//	composer_ls  composer_show  composer_check
//	packagist_status
//	github_ci  github_pr  github_branches
//
// MCP tool names mirror the CLI command path with '_' for spaces, so
// `sf cc show` ⇒ tool `cc_show`. A client already namespaces these under the
// server (e.g. `mcp__sofia__cc_show`), so no extra `sf_` prefix is added.
//
// # Deliberately excluded
//
//   - packagist release — mutating & agentic: it tags, pushes, fires the
//     Packagist webhook and publishes a package. That has no place in a
//     read-context tool set; it stays CLI-only where a human runs it (with
//     --dry-run first). Excluded entirely.
//   - github branches --delete — the reporting half is exposed (github_branches),
//     but the destructive --delete flag is not mapped, so the MCP tool is
//     report-only. Deleting branches is a human/CLI action.
//   - github ci --watch / --timeout — omitted: --watch blocks for up to 15
//     minutes waiting on CI, which is wrong for a request/response tool call.
//     github_ci reports the latest runs and returns immediately.
//
// Everything mapped is a read-only observer of the workspace (or, for the
// github/packagist/composer tools, of external services via `gh`/network),
// reflected in each tool's ToolAnnotations (ReadOnlyHint / OpenWorldHint).
//
// # Flag/arg → schema mapping
//
// Each tool has one Go input struct; github.com/google/jsonschema-go infers
// its JSON Schema. The conventions:
//
//   - Positional CLI args become named parameters with an intent-revealing
//     name: `sf code <file...>` ⇒ "files", `sf grep PATTERN...` ⇒ "patterns",
//     `sf cc show [session]` ⇒ "session", `sf composer show <pkg>` ⇒ "package".
//     This is clearer over MCP than a positional "args" array and lets the
//     schema mark exactly the genuinely-required ones.
//   - Flags become same-named snake_case parameters (--no-symbols ⇒
//     "no_symbols", --min-count ⇒ "min_count").
//   - Required vs optional follows the json tag: a field WITHOUT `omitempty`
//     is required in the generated schema; WITH `omitempty` it is optional.
//     Only genuinely-required inputs (code.files, grep.patterns,
//     composer_show.package) omit it.
//   - CLI flag defaults that are non-zero (grep --case=true, --max-per-pattern=30,
//     cc/github --limit, …) can't be expressed as a Go zero value, so those
//     fields are pointers (*int/*bool): nil ⇒ "apply the CLI default", via the
//     orInt/orBool helpers below. This keeps the MCP defaults identical to the
//     CLI's without inventing new numbers.
//   - Field descriptions come from the `jsonschema:"…"` struct tag.
//
// # Output payload
//
// Every tool renders with Format "json" — the existing `--format json` path
// each command already supports (verified per tool) — and returns it as a
// single MCP text-content block whose text is that JSON document. Rationale:
// an MCP client consumes tool output programmatically, so structured JSON is
// the right contract; TOON is a token optimization for the human/agent CLI
// read-through path and stays available there (`sf … --format toon`). We reuse
// the CLI serializer verbatim rather than re-encoding, so the MCP payload can
// never drift from the CLI's. (One exception: `code` in symbol-slice mode
// returns raw source text, not JSON — that command has no JSON form for a
// slice; it is surfaced as text as-is.)
package mcpserver

import (
	"bytes"
	"fmt"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sofia-ctx/sofia/internal/calllog"
)

// serverName / serverVersion identify this server to MCP clients. Version is
// intentionally standalone (not tied to the CLI's own --version wiring, which
// a sibling track owns) so the two can evolve without a merge coupling.
const (
	serverName    = "sofia"
	serverTitle   = "SF (Sophia Foundation) — code context tools"
	serverVersion = "0.1.0"
)

// jsonFormat is the render format used for every MCP payload. See the package
// doc for why JSON (not TOON) is the wire format here.
const jsonFormat = "json"

// NewServer builds the MCP server with every public-safe Context Provider
// registered as a tool. It is exported so tests can drive it in-memory.
func NewServer() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{
		Name:    serverName,
		Title:   serverTitle,
		Version: serverVersion,
	}, nil)
	registerTools(s)
	return s
}

// ensureSessionEnv synthesizes a session id for internal/dedup when none is
// already set. A long-lived `sf mcp` stdio process is effectively one
// continuous session — it never gets CLAUDE_CODE_SESSION_ID injected the way
// a Bash-tool call does — so without this, every `code` call would look
// session-less and dedup would never engage. Unique per process (pid + start
// time), so concurrent `sf mcp` invocations don't collide.
//
// Accepted wrinkle: a server that outlives a client's /clear can go on
// stubbing calls from the new conversation for up to the dedup window — it
// self-heals (the stub itself teaches force:true) and tearing down session
// state on some /clear heuristic would be worse.
func ensureSessionEnv() {
	if calllog.SessionID() != "" {
		return
	}
	_ = os.Setenv("SOFIA_SESSION_ID", fmt.Sprintf("mcp-%d-%d", os.Getpid(), time.Now().Unix()))
}

// textResult wraps rendered output as a single MCP text-content result.
func textResult(buf *bytes.Buffer) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: buf.String()}},
	}
}

// annotations returns the standard read-only tool annotations. openWorld is
// true for tools that reach external services (gh, network) and false for
// tools that only observe the local workspace.
func annotations(openWorld bool) *mcp.ToolAnnotations {
	ow := openWorld
	return &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &ow}
}

// orInt / orBool resolve a nilable input to the CLI's default when the caller
// omitted it (see the package doc, "Flag/arg → schema mapping").
func orInt(p *int, def int) int {
	if p == nil {
		return def
	}
	return *p
}

func orBool(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}
