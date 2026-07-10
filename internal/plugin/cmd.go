package plugin

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/sofia-ctx/sofia/internal/cliflags"
	"github.com/sofia-ctx/sofia/internal/gitclone"
)

// NewCommand returns the `sf plugin` command group: discover, inspect and
// manage subprocess plugins. The heavy lifting (discovery, compatibility
// gating, invocation) lives in this package; the commands are thin wrappers, in
// keeping with the repo's NewCommand()/RunE→package-function pattern.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plugin",
		Short: "Discover, inspect and manage sf plugins",
		Long: `plugin manages sofia's subprocess plugins — third-party executables that
extend the sf command tree without being compiled in.

Plugins are found two ways: a bare ` + "`sf-<name>`" + ` executable on $PATH (the
git-subcommand convention), or a managed directory under
$XDG_DATA_HOME/sofia/plugins/<name>/ carrying a plugin.yaml manifest. Discovered
metadata is cached so the command tree is built without forking any plugin.

Installing from a URL whose clone ships no binary and whose manifest declares
a ` + "`release:`" + ` block fetches a prebuilt executable from the repo's GitHub
release instead of disabling the plugin — see docs/plugins.md.

  sf plugin new <name>           # scaffold a new plugin, ready to install
  sf plugin list                 # what's installed and whether it's enabled
  sf plugin info <name>          # a plugin's manifest and, if disabled, why
  sf plugin disable <name>       # stop dispatching to a plugin
  sf plugin enable <name>        # undo a disable
  sf plugin update               # rescan $PATH + the managed dir, refresh cache
  sf plugin install <dir|url>    # install a local plugin dir, or clone one from git
  sf plugin upgrade [<name>]     # re-install git-installed plugin(s) from their origin
  sf plugin uninstall <name>     # remove a managed plugin`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
	}
	cmd.AddCommand(newCmd(), listCmd(), infoCmd(), enableCmd(), disableCmd(), updateCmd(),
		installCmd(), upgradeCmd(), uninstallCmd())
	return cmd
}

func newCmd() *cobra.Command {
	var (
		dir     string
		adapter bool
	)
	c := &cobra.Command{
		Use:   "new <name>",
		Short: "Scaffold a working plugin in <dir>/<name>",
		Long: `new scaffolds a working plugin at <dir>/<name> (--dir defaults to the
current directory): a plugin.yaml manifest, an executable stub with one
example command, and a README. The scaffold installs as-is —

  sf plugin new hello
  sf plugin install ./hello
  sf hello greet

With --adapter it scaffolds a Tier-1 adapter instead: a plugin.yaml with an
adapter block and no executable, whose layers/grep/refs commands the host
synthesizes —

  sf plugin new php-ddd --adapter
  sf plugin install ./php-ddd
  sf php-ddd layers

— see docs/plugins.md and docs/adapters.md for the full walkthrough.`,
		Args:         cliflags.ExactArgsHint(1, "new needs a plugin name; try: sf plugin new <name>"),
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, args []string) error {
			name := args[0]
			dst, err := Scaffold(name, dir, adapter)
			if err != nil {
				return err
			}
			// Show a "./name" style path for the common (--dir-less) case rather
			// than filepath.Join's cleaned-away "./" prefix.
			if dir == "" {
				dst = "./" + name
			}
			fmt.Fprintf(os.Stdout, "created %s — try: sf plugin install %s\n", dst, dst)
			return nil
		},
	}
	c.Flags().StringVar(&dir, "dir", "", "parent directory to scaffold into (default: current directory)")
	c.Flags().BoolVar(&adapter, "adapter", false, "scaffold a Tier-1 adapter (adapter block, no executable)")
	_ = c.RegisterFlagCompletionFunc("dir", cliflags.DirOnly)
	return c
}

func listCmd() *cobra.Command {
	var format string
	c := &cobra.Command{
		Use:          "list",
		Short:        "List discovered plugins and their status (enabled/disabled + reason)",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
	}
	cliflags.AttachFormatFlags(c, &format)
	c.RunE = func(_ *cobra.Command, _ []string) error {
		return RenderList(os.Stdout, format, Load())
	}
	return c
}

func infoCmd() *cobra.Command {
	var format string
	c := &cobra.Command{
		Use:          "info <name>",
		Short:        "Show a plugin's manifest and status",
		Args:         cliflags.ExactArgsHint(1, "info needs a plugin name; try: sf plugin info <name> (see sf plugin list)"),
		SilenceUsage: true,
	}
	cliflags.AttachFormatFlags(c, &format)
	c.RunE = func(_ *cobra.Command, args []string) error {
		d, ok := Find(Load(), args[0])
		if !ok {
			return fmt.Errorf("no plugin named %q (see `sf plugin list`)", args[0])
		}
		return RenderInfo(os.Stdout, format, d)
	}
	return c
}

func enableCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "enable <name>",
		Short:        "Re-enable a plugin previously turned off with `sf plugin disable`",
		Args:         cliflags.ExactArgsHint(1, "enable needs a plugin name; try: sf plugin enable <name>"),
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, args []string) error {
			name := args[0]
			if _, ok := Find(Load(), name); !ok {
				return fmt.Errorf("no plugin named %q (see `sf plugin list`)", name)
			}
			if err := Enable(name); err != nil {
				return err
			}
			fmt.Fprintf(os.Stdout, "enabled %s\n", name)
			// A compatibility failure isn't something enable can fix — say so.
			if d, _ := Find(Load(), name); !d.Enabled {
				fmt.Fprintf(os.Stdout, "note: still disabled — %s\n", d.Reason)
			}
			return nil
		},
	}
}

func disableCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "disable <name>",
		Short:        "Stop dispatching to a plugin (reversible with `sf plugin enable`)",
		Args:         cliflags.ExactArgsHint(1, "disable needs a plugin name; try: sf plugin disable <name>"),
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, args []string) error {
			name := args[0]
			if _, ok := Find(Load(), name); !ok {
				return fmt.Errorf("no plugin named %q (see `sf plugin list`)", name)
			}
			if err := Disable(name); err != nil {
				return err
			}
			fmt.Fprintf(os.Stdout, "disabled %s\n", name)
			return nil
		},
	}
}

func updateCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "update",
		Short:        "Rescan $PATH and the managed plugins dir, refreshing the cache",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			ds, err := Update()
			if err != nil {
				return err
			}
			fmt.Fprintf(os.Stdout, "discovered %d plugin(s)\n", len(ds))
			return nil
		},
	}
}

func installCmd() *cobra.Command {
	var ref string
	c := &cobra.Command{
		Use:   "install <dir|git-url>",
		Short: "Install a plugin from a local directory or a git repository",
		Long: `install copies a local plugin directory (which must contain a plugin.yaml)
into $XDG_DATA_HOME/sofia/plugins/<name>, where <name> is the directory's base
name, then refreshes the cache. Installing over an existing name reinstalls it.

Given a git URL instead of a directory, install shallow-clones it to a temp
dir first — the repo's name becomes the plugin name, same as a local install.
Auth is whatever your git already trusts (ssh-agent, credential helper, …);
sofia never sees a token.

If the clone has no runnable executable and its manifest declares a
` + "`release:`" + ` block, install fetches the matching asset from the repo's
latest GitHub release (or the release tagged --ref) over https and verifies
it against the release's checksums.txt before installing it.`,
		Args:         cliflags.ExactArgsHint(1, "install needs a source directory or git URL; try: sf plugin install ./my-plugin"),
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, args []string) error {
			src := args[0]
			if !gitclone.IsURL(src) {
				if ref != "" {
					return fmt.Errorf("--ref only applies to a git URL, not a local directory")
				}
				name, err := Install(src)
				if err != nil {
					return err
				}
				if _, err := Update(); err != nil {
					return err
				}
				fmt.Fprintf(os.Stdout, "installed %s\n", name)
				return nil
			}

			name, err := InstallFromGit(src, ref)
			if err != nil {
				return err
			}
			if _, err := Update(); err != nil {
				return err
			}
			commit := "unknown"
			var asset string
			if o, err := readOrigin(name); err == nil {
				commit, asset = o.Commit, o.Asset
			}
			fmt.Fprintf(os.Stdout, "installed %s (from %s @ %.7s)\n", name, src, commit)
			if asset != "" {
				fmt.Fprintf(os.Stdout, "  fetched release binary %s\n", asset)
			}
			return nil
		},
	}
	c.Flags().StringVar(&ref, "ref", "", "branch or tag to clone (git URLs only; commit shas not supported)")
	return c
}

func upgradeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "upgrade [<name>]",
		Short: "Re-install git-installed plugins from their recorded origin",
		Long: `upgrade re-clones a plugin from the git URL and ref it was installed with
(recorded in .sf-origin.json by ` + "`sf plugin install <git-url>`" + `) and reports
the commit it moved from/to. It re-clones the ref's current tip — a floating
branch moves, a pinned tag stays put.

Given no name, upgrade re-clones every managed plugin that was installed from
git, skipping (and saying so) any that were installed from a local directory.`,
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) == 1 {
				res, err := reinstallFromOrigin(args[0])
				if err != nil {
					return err
				}
				if _, err := Update(); err != nil {
					return err
				}
				fmt.Fprint(os.Stdout, formatUpgrade(res))
				return nil
			}

			git, local := gitInstalledPlugins(Load())
			for _, name := range local {
				fmt.Fprintf(os.Stdout, "skipping %s (not a git install)\n", name)
			}
			if len(git) == 0 {
				fmt.Fprintln(os.Stdout, "nothing git-installed to upgrade")
				return nil
			}
			results := make([]upgradeResult, 0, len(git))
			for _, name := range git {
				res, err := reinstallFromOrigin(name)
				if err != nil {
					return err
				}
				results = append(results, res)
			}
			// One rescan for the whole batch, not one per plugin.
			if _, err := Update(); err != nil {
				return err
			}
			for _, res := range results {
				fmt.Fprint(os.Stdout, formatUpgrade(res))
			}
			return nil
		},
	}
}

func uninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "uninstall <name>",
		Short:        "Remove a managed plugin",
		Args:         cliflags.ExactArgsHint(1, "uninstall needs a plugin name; try: sf plugin uninstall <name>"),
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, args []string) error {
			name := args[0]
			if err := Uninstall(name); err != nil {
				return err
			}
			if _, err := Update(); err != nil {
				return err
			}
			fmt.Fprintf(os.Stdout, "uninstalled %s\n", name)
			return nil
		},
	}
}
