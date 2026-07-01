package vue

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/sofia-ctx/sofia/internal/cliflags"
)

// NewCommand returns the `vue` group (`sf vue …`).
func NewCommand() *cobra.Command {
	g := &cobra.Command{
		Use:   "vue",
		Short: "Helpers over a Vue frontend",
		Long: `vue summarises a Vue frontend compactly.

  sf vue routes                          find router/index.ts under the tree
  sf vue routes frontend/src/router/index.ts
  sf vue routes --root /path/to/your/app`,
	}
	g.AddCommand(newRoutesCommand())
	return g
}

func newRoutesCommand() *cobra.Command {
	var (
		root   string
		format string
	)
	cmd := &cobra.Command{
		Use:   "routes [file]",
		Short: "Flat route map (path/name/component/meta) from a vue-router config",
		Long: `routes parses createRouter({ routes: [...] }) into a flat, depth-resolved
table — full path (children joined to parents), name, component, and meta —
so you read one compact map instead of the whole router file.

  sf vue routes                          # search for router/index.ts under cwd
  sf vue routes --root /path/to/your/app
  sf vue routes frontend/src/router/index.ts --md`,
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
	}
	cmd.Flags().StringVar(&root, "root", "", "tree to search for router/index.ts (default: current dir)")
	cliflags.AttachFormatFlags(cmd, &format)
	_ = cmd.RegisterFlagCompletionFunc("root", cliflags.DirOnly)

	cmd.RunE = func(_ *cobra.Command, args []string) error {
		file := ""
		if len(args) == 1 {
			file = args[0]
		}
		return Run(Options{Root: root, File: file, Format: format}, os.Stdout)
	}
	return cmd
}
