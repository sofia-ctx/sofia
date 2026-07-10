package worktrees

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/sofia-ctx/sofia/pkg/cliflags"
)

// NewCommand returns the `worktrees` Cobra command (`sf worktrees`).
func NewCommand() *cobra.Command {
	var (
		www    string
		format string
	)
	cmd := &cobra.Command{
		Use:     "worktrees",
		Aliases: []string{"wt"},
		Short:   "Cross-project view of git-worktree dev forks under a parent dir",
		Long: `worktrees lists git-worktree dev forks across all repos under a parent dir,
so you can see every parallel session at a glance. For repos that ship
dev/worktree.sh it enriches each fork with that script's stack state, health,
ports and dirty/ahead flags; other repos show their plain linked worktrees.

The parent dir defaults to $SOFIA_WWW, else /www when that exists on this
machine, else the current directory — override per-call with --www.

Read-only: create/remove forks with the project's own dev/worktree.sh.

  sf worktrees            all forks (TOON)
  sf worktrees --json     machine-readable
  sf worktrees --www /srv scan a different parent dir`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
	}
	cmd.Flags().StringVar(&www, "www", DefaultWww(), "parent dir to scan for repos (env SOFIA_WWW)")
	cliflags.AttachFormatFlags(cmd, &format)
	_ = cmd.RegisterFlagCompletionFunc("www", cliflags.DirOnly)

	cmd.RunE = func(_ *cobra.Command, _ []string) error {
		return Run(Options{Www: www, Format: format}, os.Stdout)
	}
	return cmd
}
