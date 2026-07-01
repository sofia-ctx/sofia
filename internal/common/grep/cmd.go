package grep

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sofia-ctx/sofia/internal/cliflags"
)

// NewCommand returns the `sf grep` Cobra command.
func NewCommand() *cobra.Command {
	var (
		opts       Options
		extFlag    string
		ignoreFlag string
		rootFlag   string
	)

	cmd := &cobra.Command{
		Use:   "grep PATTERN [PATTERN...]",
		Short: "Layer-agnostic TOON-emitting search across a project tree",
		Long: `grep is the generic cross-project search tool: scan a directory tree
in parallel and emit one TOON section per pattern, with enclosing
function/class/block for every hit (xref's superpower, minus the
constant resolution).

By default patterns are literal substrings (grep-style), so a stem like
"технолог" hits inside "технологии". Pass --word for whole-word matching,
or --regex for Go regular expressions. Default ignores cover vendor/,
node_modules/, var/, .git/ and friends.

Output is capped at 30 hits per pattern (a "# +N more truncated" line marks
the rest); pass --max-per-pattern 0 for every hit, or a higher number.

A pattern that starts with "-" (e.g. "->method") looks like a flag; put "--"
first so it's read as a pattern: sf grep -- "->method".`,
		Args:         cliflags.MinArgs(1, `grep needs a pattern; try: sf grep "<regex>" [--root DIR]`),
		SilenceUsage: true,
	}

	cmd.Flags().StringVar(&rootFlag, "root", ".", "directory to search (default: cwd)")
	cmd.Flags().BoolVar(&opts.CaseSensitive, "case", true, "case-sensitive search")
	cmd.Flags().BoolVar(&opts.WordBound, "word", false, "literal mode only: require whole-word match (word-char boundaries around the hit)")
	cmd.Flags().BoolVar(&opts.Regex, "regex", false, "treat patterns as Go regular expressions; --word is ignored")
	cmd.Flags().StringVar(&extFlag, "ext", "", "comma-separated extensions to include (e.g. \"php,ts,vue\"); empty = all")
	cmd.Flags().StringVar(&ignoreFlag, "ignore-dir", "", "comma-separated extra directory names to skip")
	cmd.Flags().IntVar(&opts.MaxPerPattern, "max-per-pattern", 30, "limit hits per pattern (0 = unlimited)")
	cliflags.AttachFormatFlags(cmd, &opts.Format)

	_ = cmd.RegisterFlagCompletionFunc("root", cliflags.DirOnly)

	cmd.RunE = func(_ *cobra.Command, args []string) error {
		opts.Root = rootFlag
		opts.Patterns = args
		opts.Exts = splitCSV(extFlag)
		opts.ExtraIgnore = splitCSV(ignoreFlag)
		if opts.Regex && opts.WordBound {
			// --regex implicitly disables --word, but only emit a hint when
			// the user explicitly asked for both (avoid silent surprise).
			if cmd.Flags().Changed("word") {
				fmt.Fprintln(os.Stderr, "note: --regex overrides --word; use \\b in your pattern instead")
			}
			opts.WordBound = false
		}
		return Run(opts, os.Stdout)
	}

	return cmd
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
