package adapter

import (
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/sofia-ctx/sofia/internal/calllog"
	"github.com/sofia-ctx/sofia/internal/cliflags"
	"github.com/sofia-ctx/sofia/internal/common/grep"
	"github.com/sofia-ctx/sofia/internal/common/refs"
)

// unclassifiedLayer is the bucket a path lands in when no declared layer claims
// it. It always renders last, after every declared layer, so the output order
// is stable and the declared layers read as the "known" surface.
const unclassifiedLayer = "(unclassified)"

// grepMaxPerPattern caps grep hits per pattern, matching `sf grep`'s own default
// so an adapter's grep doesn't dump a broader result than the tool it wraps.
const grepMaxPerPattern = 30

// Commands synthesizes the project-aware subcommands the host attaches under
// `sf <pluginName>` for an adapter plugin: `layers`, `grep`, and `refs`. They
// are real, in-process cobra commands — they resolve a project root, run the
// generic grep/refs scanners scoped to the adapter's extensions, and group the
// results by layer. Nothing here forks a subprocess; a pure-adapter plugin has
// no executable to fork.
func Commands(pluginName string, cfg Config) []*cobra.Command {
	return []*cobra.Command{
		layersCmd(pluginName, cfg),
		grepCmd(pluginName, cfg),
		refsCmd(pluginName, cfg),
	}
}

func layersCmd(pluginName string, cfg Config) *cobra.Command {
	var format, rootFlag string
	c := &cobra.Command{
		Use:          "layers [<path>]",
		Short:        "List the project's layers, or classify a path into one",
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
	}
	attachRoot(c, &rootFlag)
	cliflags.AttachFormatFlags(c, &format)
	c.RunE = func(cmd *cobra.Command, args []string) error {
		tracker := calllog.Start(pluginName+".layers", append([]string{"--format=" + format}, args...))
		cw := &calllog.Counter{W: cmd.OutOrStdout()}
		var renderErr error
		if len(args) == 0 {
			renderErr = renderLayerList(cw, format, cfg)
		} else {
			rel, err := relForClassify(cfg, rootFlag, args[0])
			if err != nil {
				tracker.Finish(err)
				return err
			}
			layer := Classify(cfg, rel)
			if layer == "" {
				layer = unclassifiedLayer
			}
			renderErr = renderClassify(cw, format, rel, layer)
		}
		tracker.RecordOutput(cw)
		tracker.Finish(renderErr)
		return renderErr
	}
	return c
}

func grepCmd(pluginName string, cfg Config) *cobra.Command {
	var format, rootFlag string
	c := &cobra.Command{
		Use:          "grep <pattern> [pattern...]",
		Short:        "Search the project, grouped by layer",
		Args:         cliflags.MinArgs(1, "grep needs a pattern; try: sf "+pluginName+" grep <pattern>"),
		SilenceUsage: true,
	}
	attachRoot(c, &rootFlag)
	cliflags.AttachFormatFlags(c, &format)
	c.RunE = func(cmd *cobra.Command, args []string) error {
		tracker := calllog.Start(pluginName+".grep", append([]string{"--format=" + format}, args...))
		root, err := resolveCmdRoot(cfg, rootFlag)
		if err != nil {
			tracker.Finish(err)
			return err
		}
		res, err := grep.Scan(grep.Options{
			Root:          root,
			Patterns:      args,
			CaseSensitive: true,
			Exts:          cfg.Ext,
			MaxPerPattern: grepMaxPerPattern,
		})
		if err != nil {
			tracker.Finish(err)
			return err
		}
		cw := &calllog.Counter{W: cmd.OutOrStdout()}
		renderErr := renderGrepGroups(cw, format, groupHits(cfg, res))
		tracker.RecordOutput(cw)
		tracker.Finish(renderErr)
		return renderErr
	}
	return c
}

func refsCmd(pluginName string, cfg Config) *cobra.Command {
	var format, rootFlag string
	c := &cobra.Command{
		Use:          "refs <symbol>",
		Short:        "Where a symbol is defined and used, grouped by layer",
		Args:         cliflags.ExactArgsHint(1, "refs needs a symbol; try: sf "+pluginName+" refs <name>"),
		SilenceUsage: true,
	}
	attachRoot(c, &rootFlag)
	cliflags.AttachFormatFlags(c, &format)
	c.RunE = func(cmd *cobra.Command, args []string) error {
		tracker := calllog.Start(pluginName+".refs", []string{"--format=" + format, args[0]})
		root, err := resolveCmdRoot(cfg, rootFlag)
		if err != nil {
			tracker.Finish(err)
			return err
		}
		res, err := refs.Scan(refs.Options{
			Root:   root,
			Symbol: args[0],
			Exts:   cfg.Ext,
		})
		if err != nil {
			tracker.Finish(err)
			return err
		}
		cw := &calllog.Counter{W: cmd.OutOrStdout()}
		renderErr := renderRefGroups(cw, format, res.Symbol, groupRefs(cfg, res))
		tracker.RecordOutput(cw)
		tracker.Finish(renderErr)
		return renderErr
	}
	return c
}

// attachRoot adds the shared --root flag, whose empty default means "resolve the
// root from the cwd" (see resolveCmdRoot).
func attachRoot(c *cobra.Command, rootFlag *string) {
	c.Flags().StringVar(rootFlag, "root", "", "project root (default: resolve upward from the cwd)")
	_ = c.RegisterFlagCompletionFunc("root", cliflags.DirOnly)
}

// resolveCmdRoot implements the full root precedence a command uses: an explicit
// --root (made absolute) wins, else the adapter's own resolution ($root_key,
// then a walk-up for a root marker) runs from the cwd.
func resolveCmdRoot(cfg Config, rootFlag string) (string, error) {
	if rootFlag != "" {
		return filepath.Abs(rootFlag)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return ResolveRoot(cfg, cwd)
}

// relForClassify turns the path argument to `layers <path>` into a root-relative
// path Classify can match. A relative path is used as given (the common case:
// the user pastes a project-relative path); an absolute one is made relative to
// the resolved root, so a classification is possible regardless of the cwd.
func relForClassify(cfg Config, rootFlag, path string) (string, error) {
	if !filepath.IsAbs(path) {
		return path, nil
	}
	root, err := resolveCmdRoot(cfg, rootFlag)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", err
	}
	return rel, nil
}
