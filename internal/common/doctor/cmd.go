package doctor

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/sofia-ctx/sofia/pkg/cliflags"
)

// NewCommand returns the `doctor` Cobra command (`sf doctor`).
func NewCommand() *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check sf install health (stale binary, PATH, completions, claude)",
		Long: `doctor verifies the local sf install in one call:

  - staleness   bin/sf older than git HEAD? (the "fixed in git, not rebuilt"
                trap that silently makes the agent run outdated tools)
  - path        does ` + "`sf`" + ` on $PATH resolve to the running binary?
  - completions are the shell-completion scripts installed?
  - claude      is the claude CLI (needed by ` + "`sf claude`" + `) present?

Exit code is non-zero when a check FAILs (e.g. a stale binary) so it can gate
scripts or a make target.`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
	}
	cliflags.AttachFormatFlags(cmd, &format)
	cmd.RunE = func(_ *cobra.Command, _ []string) error {
		return Run(Options{Format: format}, os.Stdout)
	}
	return cmd
}
