package code

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/sofia-ctx/sofia/internal/cliflags"
)

// NewCommand returns the `code` Cobra command (`sf code`).
func NewCommand() *cobra.Command {
	var (
		format   string
		exported bool
		api      bool
	)
	cmd := &cobra.Command{
		Use:   "code <file...> | <file> <symbol>",
		Short: "Structural summary of source files, without bodies",
		Long: `code prints a compact structure of a source file — without function
bodies — so you needn't cat the whole file just to see its shape/API
(the same files get re-read many times per session, and that's where read
tokens go). It dispatches by extension and summarises several files in
parallel, aggregating the output.

  Go   (.go):  package, imports, types (struct fields+tags, interface
               methods), function/method signatures, consts, vars.
  PHP  (.php): namespace, class/interface/trait/enum, extends/implements,
               attributes, constructor deps, properties, method signatures.
               Parses PHP 8.2–8.5 syntax (normalized to the 8.1 grammar).
               --api flattens the effective public surface (own + trait +
               inherited methods, each tagged with its source) so you needn't
               chase a class across its traits and parents to learn its API.
  TS/Vue (.ts/.tsx/.vue): imports, top-level declarations, interface/type/enum
               members, and for Vue SFCs props/emits/models, the stores and
               API calls used, and components rendered (line-based, approximate).

Pass MULTIPLE files to summarise them together. Or pass exactly one file and a
symbol name to slice ONE symbol's full source (signature + body) instead of the
whole file — Go and PHP only; match a func/type/const/var by name, or a method
by name or Recv.Method / Class::method.

  sf code internal/server/server.go
  sf code frontend/src/api/types.ts frontend/src/router/index.ts   # several at once
  sf code src/User/Entity/User.php --exported       # public API only
  sf code vendor/acme/lib/src/FluentThing.php --api  # full surface across traits/parents
  sf code internal/server/server.go Server.Routes   # slice one method
  sf code src/Sales/Entity/Task.php complete         # slice one PHP method
  sf code internal/server/server.go --json`,
		Args:         cliflags.MinArgs(1, "code needs a file; try: sf code <file> [symbol]"),
		SilenceUsage: true,
	}
	cmd.Flags().BoolVar(&exported, "exported", false, "show only exported/public symbols")
	cmd.Flags().BoolVar(&api, "api", false, "PHP: effective public API — own + trait + inherited methods (implies --exported)")
	cliflags.AttachFormatFlags(cmd, &format)

	cmd.RunE = func(_ *cobra.Command, args []string) error {
		opts := Options{Format: format, ExportedOnly: exported, API: api}
		// `<file> <symbol>`: exactly two args where the second is not a file →
		// slice mode. Otherwise every arg is a file (multi-file summary).
		if len(args) == 2 && !fileExists(args[1]) {
			opts.Inputs = []string{args[0]}
			opts.Symbol = args[1]
		} else {
			opts.Inputs = args
		}
		return Run(opts, os.Stdout)
	}
	return cmd
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
