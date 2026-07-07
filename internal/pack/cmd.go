package pack

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/sofia-ctx/sofia/internal/cliflags"
)

// NewCommand returns the `sf pack` command group: install/list/inspect/
// remove packs — git repos or local directories bundling sf plugins, claude
// skills/commands, and project instructions/templates. The heavy lifting
// lives in this package (see pack.go's package doc); the commands are thin
// wrappers, matching the repo's NewCommand()/RunE→package-function pattern
// plugin/cmd.go already uses.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pack",
		Short: "Install and manage packs: plugins + claude skills/commands + project files, bundled together",
		Long: `pack installs a "pack" — a git repository or local directory holding a
pack.yaml manifest — laying its artifacts onto the shelf each belongs to:
sf plugins, Claude skills/commands, and a target project's own instructions/
templates.

  sf pack new <name>              # scaffold a new pack, ready to install
  sf pack install <git-url|dir>   # fetch/read a pack.yaml and lay it out
  sf pack list                    # installed packs: plugins, projects
  sf pack info <name>             # a pack's source, shelves and projects
  sf pack status [<name>]         # drift: has anything been hand-edited?
  sf pack uninstall <name>        # remove a pack's footprint from a project`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
	}
	cmd.AddCommand(newCmd(), installCmd(), listCmd(), infoCmd(), statusCmd(), uninstallCmd())
	return cmd
}

func newCmd() *cobra.Command {
	var dir string
	c := &cobra.Command{
		Use:   "new <name>",
		Short: "Scaffold a new, installable pack",
		Long: `new scaffolds a pack skeleton at <dir>/<name> (--dir defaults to the
current directory): a pack.yaml with one active instructions entry plus a
commented example of every other section, a sample instructions/AGENTS.md,
and a README. The scaffold installs as-is —

  sf pack new my-pack
  sf pack install ./my-pack --project /some/project

— edit pack.yaml to add plugins, claude skills/commands, or templates.`,
		Args:         cliflags.ExactArgsHint(1, "new needs a pack name; try: sf pack new <name>"),
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, args []string) error {
			name := args[0]
			dst, err := Scaffold(name, dir)
			if err != nil {
				return err
			}
			// Show a "./name" style path for the common (--dir-less) case rather
			// than filepath.Join's cleaned-away "./" prefix.
			if dir == "" {
				dst = "./" + name
			}
			fmt.Fprintf(os.Stdout, "created %s — edit pack.yaml, then: sf pack install %s\n", dst, dst)
			return nil
		},
	}
	c.Flags().StringVar(&dir, "dir", "", "parent directory to scaffold into (default: current directory)")
	_ = c.RegisterFlagCompletionFunc("dir", cliflags.DirOnly)
	return c
}

func installCmd() *cobra.Command {
	var project, ref string
	var force bool
	c := &cobra.Command{
		Use:   "install <git-url|dir>",
		Short: "Install a pack's plugins, claude skills/commands, and project files",
		Long: `install fetches a pack (a git repository or local directory holding a
pack.yaml) and lays out everything it declares: sf plugins go to
$XDG_DATA_HOME/sofia/plugins, claude skills/commands go to $CLAUDE_DIR
(env override; default ~/.claude), and instructions/templates go to
--project (default: the current directory).

A destination that already holds content the pack doesn't own — or that was
hand-edited since a previous install — is left alone and reported as a
conflict; --force overwrites it anyway.`,
		Args:         cliflags.ExactArgsHint(1, "install needs a source directory or git URL; try: sf pack install ./my-pack"),
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, args []string) error {
			res, err := Install(InstallOptions{Src: args[0], Ref: ref, Project: project, Force: force})
			if err != nil {
				return err
			}
			fmt.Fprintf(os.Stdout, "installed %s (%d file(s), %d plugin(s))\n", res.Name, res.Files, len(res.Plugins))
			return nil
		},
	}
	c.Flags().StringVar(&project, "project", "", "target project root (default: current directory)")
	c.Flags().StringVar(&ref, "ref", "", "branch or tag to clone (git sources only; commit shas not supported)")
	c.Flags().BoolVar(&force, "force", false, "overwrite conflicting files")
	return c
}

func listCmd() *cobra.Command {
	var format string
	c := &cobra.Command{
		Use:          "list",
		Short:        "List installed packs",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
	}
	cliflags.AttachFormatFlags(c, &format)
	c.RunE = func(_ *cobra.Command, _ []string) error {
		names, err := ListInstalled()
		if err != nil {
			return err
		}
		infos := make([]Info, 0, len(names))
		for _, name := range names {
			info, err := LoadInfo(name)
			if err != nil {
				return err
			}
			infos = append(infos, info)
		}
		return RenderList(os.Stdout, format, infos)
	}
	return c
}

func infoCmd() *cobra.Command {
	var format string
	c := &cobra.Command{
		Use:          "info <name>",
		Short:        "Show a pack's source, shelves and projects",
		Args:         cliflags.ExactArgsHint(1, "info needs a pack name; try: sf pack info <name> (see sf pack list)"),
		SilenceUsage: true,
	}
	cliflags.AttachFormatFlags(c, &format)
	c.RunE = func(_ *cobra.Command, args []string) error {
		info, err := LoadInfo(args[0])
		if err != nil {
			return err
		}
		return RenderInfo(os.Stdout, format, info)
	}
	return c
}

func statusCmd() *cobra.Command {
	var format string
	c := &cobra.Command{
		Use:   "status [<name>]",
		Short: "Report drift between what's installed and what's on disk",
		Long: `status sha-compares every file a pack (or, with no argument, every
installed pack) recorded at install time against what's actually on disk now.
It always exits 0 — drift is reported, not treated as a failure; only a real
error (e.g. an unknown pack name) is.`,
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
	}
	cliflags.AttachFormatFlags(c, &format)
	c.RunE = func(_ *cobra.Command, args []string) error {
		if len(args) == 1 {
			st, err := Status(args[0])
			if err != nil {
				return err
			}
			return RenderStatus(os.Stdout, format, st)
		}
		sts, err := StatusAll()
		if err != nil {
			return err
		}
		return RenderStatusAll(os.Stdout, format, sts)
	}
	return c
}

func uninstallCmd() *cobra.Command {
	var project string
	c := &cobra.Command{
		Use:          "uninstall <name>",
		Short:        "Remove a pack's footprint from a project (and its globals once no project references it)",
		Args:         cliflags.ExactArgsHint(1, "uninstall needs a pack name; try: sf pack uninstall <name>"),
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, args []string) error {
			res, err := Uninstall(args[0], project)
			if err != nil {
				return err
			}
			for _, w := range res.Warnings {
				fmt.Fprintln(os.Stdout, w)
			}
			if res.Global {
				fmt.Fprintf(os.Stdout, "uninstalled %s\n", args[0])
			} else {
				fmt.Fprintf(os.Stdout, "uninstalled %s from this project (still installed elsewhere)\n", args[0])
			}
			return nil
		},
	}
	c.Flags().StringVar(&project, "project", "", "project root to uninstall from (default: current directory)")
	return c
}
