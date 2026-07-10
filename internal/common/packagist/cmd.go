package packagist

import (
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/sofia-ctx/sofia/pkg/cliflags"
)

// NewCommand returns the `packagist` group (`sf packagist …`).
func NewCommand() *cobra.Command {
	g := &cobra.Command{
		Use:   "packagist",
		Short: "Release health for PHP packages vs Packagist",
		Long: `packagist reports, per package under a tree, the latest local git tag, whether
that tag is pushed to origin, and the latest version published on Packagist —
so you can see at a glance which packages still need a tag or a Packagist
update (the webhook does not auto-fire for every package).

  sf packagist status                        scan the current dir
  sf packagist status /path/to/your/packages
  sf packagist status --offline              tags only, no network
  sf packagist release <pkg> <version>   tag + push + Packagist update + verify`,
	}
	g.AddCommand(newStatusCommand())
	g.AddCommand(newReleaseCommand())
	return g
}

func newStatusCommand() *cobra.Command {
	var (
		root    string
		format  string
		offline bool
	)
	cmd := &cobra.Command{
		Use:          "status [root]",
		Short:        "Per-package local tag vs pushed vs Packagist version",
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
	}
	cmd.Flags().StringVar(&root, "root", "", "tree to scan (default: current dir or positional arg)")
	cmd.Flags().BoolVar(&offline, "offline", false, "skip Packagist/remote probes (local tags only)")
	cliflags.AttachFormatFlags(cmd, &format)
	_ = cmd.RegisterFlagCompletionFunc("root", cliflags.DirOnly)

	cmd.RunE = func(_ *cobra.Command, args []string) error {
		r := root
		if r == "" && len(args) == 1 {
			r = args[0]
		}
		return Run(Options{Root: r, Format: format, Offline: offline}, os.Stdout)
	}
	return cmd
}

func newReleaseCommand() *cobra.Command {
	var (
		root       string
		message    string
		username   string
		allowDirty bool
		dryRun     bool
		timeout    time.Duration
	)
	cmd := &cobra.Command{
		Use:   "release <pkg> <version>",
		Short: "Tag + push + Packagist update + verify for one package (mutating)",
		Long: `release publishes one package: it creates an annotated semver tag (reusing an
existing one), pushes it to origin, triggers the Packagist update-package
webhook (which does not auto-fire for every package), then polls Packagist
until the new version appears.

Mutating + network. Token: $PACKAGIST_API_TOKEN, else ~/.config/sofia/packagist.env.
Run with --dry-run first to preview every step without touching anything.

  sf packagist release array-reader 2.1.0 --dry-run
  sf packagist release array-reader 2.1.0
  sf packagist release enum 2.0.3 --root /path/to/your/packages`,
		Args:         cliflags.ExactArgsHint(2, "release needs <pkg> <version>; try: sf packagist status"),
		SilenceUsage: true,
	}
	cmd.Flags().StringVar(&root, "root", "", "tree to resolve the package under (default: current dir)")
	cmd.Flags().StringVar(&message, "message", "", "annotated-tag message (default: \"Release <version>\")")
	cmd.Flags().StringVar(&username, "username", "", "Packagist API username (default: the vendor prefix of the package's composer.json name)")
	cmd.Flags().BoolVar(&allowDirty, "allow-dirty", false, "tag even if the working tree is not clean")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print every step without tagging/pushing/publishing")
	cmd.Flags().DurationVar(&timeout, "timeout", 90*time.Second, "max time to poll Packagist for the new version")
	_ = cmd.RegisterFlagCompletionFunc("root", cliflags.DirOnly)

	cmd.RunE = func(_ *cobra.Command, args []string) error {
		return RunRelease(ReleaseOptions{
			Root:       root,
			Target:     args[0],
			Version:    args[1],
			Message:    message,
			Username:   username,
			AllowDirty: allowDirty,
			DryRun:     dryRun,
			Timeout:    timeout,
		}, os.Stdout)
	}
	return cmd
}
