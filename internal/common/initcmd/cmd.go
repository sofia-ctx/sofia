package initcmd

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/sofia-ctx/sofia/internal/cliflags"
)

// NewCommand returns the `init` Cobra command (`sf init`).
func NewCommand() *cobra.Command {
	var project string
	var corporate, force bool
	var format string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "One-shot per-project onboarding: wire sf into AGENTS.md, Claude Code, and MCP",
		Long: `init wires a project up for its coding agents in one pass:

  1. a managed sf block in the project's AGENTS.md (always)
  2. the sf-context skill installed into $CLAUDE_DIR/skills
  3. the PreToolUse hook wired into $CLAUDE_DIR/settings.json
  4. the sofia MCP server registered in the project's .mcp.json

Steps 2-3 require Claude Code on the machine ($CLAUDE_DIR exists); step 4
also fires when Claude Code is only detected in the project (.claude or
CLAUDE.md). A gate that doesn't hold reports the step as skipped rather than
failing the call.

--corporate does only step 1 — no global writes, no .claude, no .mcp.json —
for locked-down environments where only instruction files are writable.`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
	}
	cmd.Flags().StringVar(&project, "project", "", "target project root (default: current directory)")
	cmd.Flags().BoolVar(&corporate, "corporate", false, "only write the AGENTS.md block — no .claude/.mcp.json writes")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite a hand-edited/stale installed skill")
	cliflags.AttachFormatFlags(cmd, &format)

	cmd.RunE = func(_ *cobra.Command, _ []string) error {
		return Run(Options{Project: project, Corporate: corporate, Force: force, Format: format}, os.Stdout)
	}
	return cmd
}
