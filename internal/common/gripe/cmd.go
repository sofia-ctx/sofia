package gripe

import (
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sofia-ctx/sofia/pkg/cliflags"
)

// NewCommand returns the `gripe` Cobra command (`sf gripe`).
func NewCommand() *cobra.Command {
	var (
		format string
		limit  int
	)
	cmd := &cobra.Command{
		Use:   "gripe [message]",
		Short: "Record (or list) a complaint that sf fell short",
		Long: `gripe captures the one failure the call log can't see on its own: sf
exited 0 but gave the wrong thing, or you had to fall back to cat/rg/grep
because sf couldn't do what you needed. Hard errors (exit != 0) are already
logged — read those with ` + "`sf history --failed --source agent`" + `.

  sf gripe 'sf code .kt does not structure it — fell back to raw, read the whole file'  # record
  sf gripe                                                                              # list recent
  sf gripe --limit 50 --md

Each record is auto-tagged with project, session and time, so the message can be
terse. The bare list view is for the sf author: the coverage gaps worth fixing.`,
		Args:         cobra.ArbitraryArgs,
		SilenceUsage: true,
	}
	cmd.Flags().IntVar(&limit, "limit", 20, "list view: max gripes to show (0 = unlimited)")
	cliflags.AttachFormatFlags(cmd, &format)

	cmd.RunE = func(_ *cobra.Command, args []string) error {
		return Run(Options{
			Message: strings.TrimSpace(strings.Join(args, " ")),
			Format:  format,
			Limit:   limit,
		}, os.Stdout)
	}
	return cmd
}
