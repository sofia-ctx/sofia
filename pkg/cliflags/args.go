package cliflags

import (
	"errors"

	"github.com/spf13/cobra"
)

// MinArgs is like cobra.MinimumNArgs(n) but, on too few args, returns hint
// verbatim instead of cobra's generic "requires at least N arg(s)". hint should
// be a parsable "<what>; try: <command>" string (no "error:" prefix — main()
// adds it) so the agent self-corrects in the same turn instead of burning one
// on a bare usage error.
func MinArgs(n int, hint string) cobra.PositionalArgs {
	return func(_ *cobra.Command, args []string) error {
		if len(args) < n {
			return errors.New(hint)
		}
		return nil
	}
}

// ExactArgsHint is like cobra.ExactArgs(n) with the same hint contract as
// MinArgs: a wrong arg count returns hint verbatim.
func ExactArgsHint(n int, hint string) cobra.PositionalArgs {
	return func(_ *cobra.Command, args []string) error {
		if len(args) != n {
			return errors.New(hint)
		}
		return nil
	}
}
