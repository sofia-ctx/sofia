package code

import (
	"errors"
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
		force    bool
	)
	cmd := &cobra.Command{
		Use:   "code <file|dir|glob...> | <file> <symbol...>",
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

Pass MULTIPLE files to summarise them together, or a directory to summarise
every supported file under it (recursively, skipping vendor/node_modules/.git
and friends — same defaults as sf grep), or a glob pattern (*, ?, [) — a
whole-package map in one call. Expansion caps at 250 files — narrow the path
if you hit that.

Or pass one file and one or more symbol names to slice their full source
(signature + body) instead of the whole file — Go and PHP only; match a
func/type/const/var by name, or a method by name or Recv.Method /
Class::method. A requested symbol that isn't found doesn't fail the whole
call: whatever's found still comes back, with a comment marking what's
missing (and the available names) — unless NONE of the requested symbols
exist, which errors. Symbol slicing needs a single real file, not a
directory or glob.

  sf code internal/server/server.go
  sf code frontend/src/api/types.ts frontend/src/router/index.ts   # several at once
  sf code internal/plugin/                          # whole-package map (recursive)
  sf code src/User/Entity/User.php --exported       # public API only
  sf code vendor/acme/lib/src/FluentThing.php --api  # full surface across traits/parents
  sf code internal/server/server.go Server.Routes   # slice one method
  sf code internal/cc/cc.go Parse ingestEntry        # slice several symbols at once
  sf code src/Sales/Entity/Task.php complete         # slice one PHP method
  sf code internal/server/server.go --json`,
		Args:         cliflags.MinArgs(1, "code needs a file; try: sf code <file> [symbol...]"),
		SilenceUsage: true,
	}
	cmd.Flags().BoolVar(&exported, "exported", false, "show only exported/public symbols")
	cmd.Flags().BoolVar(&api, "api", false, "PHP: effective public API — own + trait + inherited methods (implies --exported)")
	cmd.Flags().BoolVar(&force, "force", false, "re-emit the full output even if this exact call was answered moments ago (skip the dedup stub)")
	cliflags.AttachFormatFlags(cmd, &format)

	cmd.RunE = func(_ *cobra.Command, args []string) error {
		opts := Options{Format: format, ExportedOnly: exported, API: api, Force: force}
		files, symbols, err := classifyArgs(args)
		if err != nil {
			return err
		}
		opts.Inputs = files
		opts.Symbols = symbols
		return Run(opts, os.Stdout)
	}
	return cmd
}

// classifyArgs splits `sf code` positional args into either every-arg-a-file
// (multi-file summary) or one file plus one-or-more symbols (slice mode):
//
//   - a single arg is always a file (summary of that one file).
//   - args[0] is not an existing file → every arg is treated as a file, same
//     as the "otherwise all args are files" fallback this replaces; Run's own
//     validation reports a precise per-file error.
//   - args[0] is an existing file and none of args[1:] are → slice mode,
//     symbols = args[1:] in input order.
//   - every arg is an existing file → multi-file summary.
//   - args[1:] mixes files and non-files → ambiguous whether the caller wants
//     a summary or a slice, so it's a self-correcting usage error rather than
//     a silent guess.
func classifyArgs(args []string) (files, symbols []string, err error) {
	if len(args) == 1 || !fileExists(args[0]) {
		return args, nil, nil
	}
	var nonFiles []string
	for _, a := range args[1:] {
		if !fileExists(a) {
			nonFiles = append(nonFiles, a)
		}
	}
	switch len(nonFiles) {
	case 0:
		return args, nil, nil // every arg is a file
	case len(args) - 1:
		return args[:1], nonFiles, nil // args[0] is a file, nothing else is
	default:
		return nil, nil, errors.New("mixed files and symbols; try: sf code <file...> (summaries) or sf code <file> <symbol...> (bodies)")
	}
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
