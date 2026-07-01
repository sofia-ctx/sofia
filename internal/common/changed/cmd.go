package changed

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/sofia-ctx/sofia/internal/cliflags"
)

// NewCommand returns the `changed` Cobra command (`sf changed`).
func NewCommand() *cobra.Command {
	var (
		root      string
		format    string
		staged    bool
		noSymbols bool
	)
	cmd := &cobra.Command{
		Use:   "changed [revision|range]",
		Short: "Classified summary of a git diff (files, churn, category, touched symbols)",
		Long: `changed summarises a git diff compactly instead of dumping it: per file
its status, churn (+/-), category (source/test/config/docs/build/migration),
language, and the enclosing functions/classes touched (from git's own
hunk-header function context — no file parsing).

  sf changed                 working tree vs HEAD (incl. untracked)
  sf changed --staged        staged changes only
  sf changed HEAD~3          since 3 commits ago
  sf changed main..HEAD      a range
  sf changed --no-symbols    files + churn only`,
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
	}
	cmd.Flags().StringVar(&root, "root", "", "git repo dir (default: current dir)")
	cmd.Flags().BoolVar(&staged, "staged", false, "only staged changes (vs HEAD)")
	cmd.Flags().BoolVar(&noSymbols, "no-symbols", false, "skip touched-symbol extraction")
	cliflags.AttachFormatFlags(cmd, &format)
	_ = cmd.RegisterFlagCompletionFunc("root", cliflags.DirOnly)

	cmd.RunE = func(_ *cobra.Command, args []string) error {
		rng := ""
		if len(args) == 1 {
			rng = args[0]
		}
		return Run(Options{Root: root, Range: rng, Staged: staged, Symbols: !noSymbols, Format: format}, os.Stdout)
	}
	return cmd
}
