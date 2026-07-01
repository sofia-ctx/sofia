package mcpserver

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// NewCommand returns the `sf mcp` command: an MCP server over stdio that
// exposes sofia's public-safe Context Providers as MCP tools. A client
// (Claude Desktop/Code, mcp-inspector, …) launches `sf mcp` and speaks
// JSON-RPC over its stdin/stdout.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Serve sofia's context tools to LLM agents over MCP (stdio)",
		Long: `mcp runs a Model Context Protocol server on stdio, exposing the public-safe
Context Providers (code, grep, changed, cc, composer, packagist, github, vue)
as MCP tools. It wraps the same Go functions the CLI commands call — no
subprocess — and returns each tool's --format json output as the payload.

Point an MCP client at this command over stdio, e.g.:

  npx @modelcontextprotocol/inspector sf mcp

The server runs until the client disconnects or the process is signalled.`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
	}
	cmd.RunE = func(c *cobra.Command, _ []string) error {
		// Shut the server down cleanly on Ctrl-C / SIGTERM; it also returns on
		// its own when the client closes stdin (EOF on the stdio transport).
		parent := c.Context()
		if parent == nil {
			parent = context.Background()
		}
		ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
		defer stop()
		return NewServer().Run(ctx, &mcp.StdioTransport{})
	}
	return cmd
}
