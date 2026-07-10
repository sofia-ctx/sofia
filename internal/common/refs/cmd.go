package refs

import (
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sofia-ctx/sofia/pkg/cliflags"
)

// NewCommand returns the `sf refs` Cobra command.
func NewCommand() *cobra.Command {
	var (
		opts       Options
		extFlag    string
		ignoreFlag string
		rootFlag   string
	)

	cmd := &cobra.Command{
		Use:   "refs <symbol>",
		Short: "Where a symbol is defined and used, each hit labeled with its enclosing function/type",
		Long: `refs answers "who defines/uses this symbol across the tree, and from
where" in one call — a deterministic, no-toolchain alternative to
grep -rn <symbol> followed by opening every caller to see what function
it's in.

Matching is a literal, word-boundary search (not regex — SYMBOL must be a
bare identifier), across Go/PHP/TS/TSX/Vue by default. Every hit is labeled
with its enclosing function/type: AST-derived for Go, the same regex
heuristics sf grep uses for PHP/TS/Vue. A hit is classified "def" or "use"
textually (a language-appropriate declaration pattern on that line), not by
type-checking — a symbol declared in several places shows several defs.

Output is capped at 30 hits by default, defs first, then file, then line;
pass --max 0 to widen (a negative --max removes the cap entirely).`,
		Args:         cliflags.MinArgs(1, `refs needs a symbol; try: sf refs <name> [--root DIR]`),
		SilenceUsage: true,
	}

	cmd.Flags().StringVar(&rootFlag, "root", ".", "directory to search (default: cwd)")
	cmd.Flags().StringVar(&extFlag, "ext", "", "comma-separated extensions to include (default: go,php,ts,tsx,vue)")
	cmd.Flags().StringVar(&ignoreFlag, "ignore-dir", "", "comma-separated extra directory names to skip")
	cmd.Flags().IntVar(&opts.Max, "max", 0, "cap on refs shown, defs first (0 = default 30; negative = unlimited)")
	cliflags.AttachFormatFlags(cmd, &opts.Format)

	_ = cmd.RegisterFlagCompletionFunc("root", cliflags.DirOnly)

	cmd.RunE = func(_ *cobra.Command, args []string) error {
		opts.Root = rootFlag
		opts.Symbol = args[0]
		opts.Exts = splitCSV(extFlag)
		opts.IgnoreDirs = splitCSV(ignoreFlag)
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
