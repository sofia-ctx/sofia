package composer

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/sofia-ctx/sofia/internal/cliflags"
)

// NewCommand returns the `composer` group (`sf composer …`).
func NewCommand() *cobra.Command {
	g := &cobra.Command{
		Use:   "composer",
		Short: "Compact views over a tree of PHP packages (composer.json)",
		Long: `composer reads the composer.json files under a directory and reports them
compactly, instead of cat-ing each one (plus git tag and phpstan.neon greps).

  sf composer ls [root]      one digest row per package across a tree
  sf composer show <pkg>     full metadata for a single package
  sf composer check [pkg]    run each package's own quality gate, summarised`,
	}
	g.AddCommand(newLsCommand())
	g.AddCommand(newShowCommand())
	g.AddCommand(newCheckCommand())
	return g
}

func newLsCommand() *cobra.Command {
	var (
		root   string
		format string
	)
	cmd := &cobra.Command{
		Use:   "ls [root]",
		Short: "One digest row per package across a tree (name, version, type, php, phpstan, scripts, deps)",
		Long: `ls walks a tree for composer.json files and emits one compact TOON row per
package: name, latest git tag (the version of record — composer.json carries
no version field), composer type, php constraint, PHPStan level, declared
script keys, and real (non-platform) dependencies.

  sf composer ls                          scan the current dir
  sf composer ls /path/to/your/packages
  sf composer ls --md`,
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
	}
	cmd.Flags().StringVar(&root, "root", "", "tree to scan (default: current dir or positional arg)")
	cliflags.AttachFormatFlags(cmd, &format)
	_ = cmd.RegisterFlagCompletionFunc("root", cliflags.DirOnly)

	cmd.RunE = func(_ *cobra.Command, args []string) error {
		r := root
		if r == "" && len(args) == 1 {
			r = args[0]
		}
		return Run(Options{Root: r, Format: format}, os.Stdout)
	}
	return cmd
}

func newShowCommand() *cobra.Command {
	var (
		root   string
		format string
	)
	cmd := &cobra.Command{
		Use:   "show <pkg|path>",
		Short: "Full metadata for a single package (scripts with commands, all deps)",
		Long: `show prints the full metadata for one package: version, type, php, phpstan,
namespace, every script with its command, and require / require-dev with
constraints. The target is a package name (or its short suffix), a directory
basename, or a path to the package dir / composer.json.

  sf composer show array-reader
  sf composer show acme/array-reader
  sf composer show ./array-reader`,
		Args:         cliflags.ExactArgsHint(1, "composer show needs a package; try: sf composer ls"),
		SilenceUsage: true,
	}
	cmd.Flags().StringVar(&root, "root", "", "tree to search (default: current dir)")
	cliflags.AttachFormatFlags(cmd, &format)
	_ = cmd.RegisterFlagCompletionFunc("root", cliflags.DirOnly)

	cmd.RunE = func(_ *cobra.Command, args []string) error {
		return RunShow(ShowOptions{Root: root, Target: args[0], Format: format}, os.Stdout)
	}
	return cmd
}

func newCheckCommand() *cobra.Command {
	var (
		root   string
		format string
	)
	cmd := &cobra.Command{
		Use:   "check [pkg]",
		Short: "Run each package's own `composer check` gate, summarised pass/fail",
		Long: `check runs the per-package "check" composer script (test + phpstan + cs) and
collapses its verbose output into a compact pass/fail row per package with the
first failure line. With a package argument, only that package runs.

  sf composer check                             all packages under the scan root
  sf composer check enum                        only the enum package
  sf composer check --root /path/to/your/packages`,
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
	}
	cmd.Flags().StringVar(&root, "root", "", "tree to scan (default: current dir)")
	cliflags.AttachFormatFlags(cmd, &format)
	_ = cmd.RegisterFlagCompletionFunc("root", cliflags.DirOnly)

	cmd.RunE = func(_ *cobra.Command, args []string) error {
		target := ""
		if len(args) == 1 {
			target = args[0]
		}
		return RunCheck(CheckOptions{Root: root, Target: target, Format: format}, os.Stdout)
	}
	return cmd
}
