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

  sf plugin list                 # what's installed and whether it's enabled
  sf plugin info <name>          # a plugin's manifest and, if disabled, why
  sf plugin disable <name>       # stop dispatching to a plugin
  sf plugin enable <name>        # undo a disable
  sf plugin update               # rescan $PATH + the managed dir, refresh cache
  sf plugin install <dir|url>    # install a local plugin dir, or clone one from git
  sf plugin uninstall <name>     # remove a managed plugin`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
	}
	cmd.AddCommand(listCmd(), infoCmd(), enableCmd(), disableCmd(), updateCmd(), installCmd(), uninstallCmd())
	return cmd
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
sofia never sees a token.`,
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
			if o, err := readOrigin(name); err == nil {
				commit = o.Commit
			}
			fmt.Fprintf(os.Stdout, "installed %s (from %s @ %.7s)\n", name, src, commit)
			return nil
		},
	}
	c.Flags().StringVar(&ref, "ref", "", "branch or tag to clone (git URLs only; commit shas not supported)")
	return c
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
