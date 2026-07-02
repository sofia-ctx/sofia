// Package cli builds the master Cobra command tree for the `sf` binary.
// Every Agentic Tool / Context Provider in the repo plugs in here so that
// `sf <namespace> <tool>` is the single entry point for users and AI
// agents.
package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/sofia-ctx/sofia/internal/cc"
	"github.com/sofia-ctx/sofia/internal/common/changed"
	commoncode "github.com/sofia-ctx/sofia/internal/common/code"
	"github.com/sofia-ctx/sofia/internal/common/composer"
	"github.com/sofia-ctx/sofia/internal/common/doctor"
	"github.com/sofia-ctx/sofia/internal/common/github"
	"github.com/sofia-ctx/sofia/internal/common/grep"
	"github.com/sofia-ctx/sofia/internal/common/gripe"
	"github.com/sofia-ctx/sofia/internal/common/hook"
	"github.com/sofia-ctx/sofia/internal/common/packagist"
	"github.com/sofia-ctx/sofia/internal/common/vue"
	"github.com/sofia-ctx/sofia/internal/common/worktrees"
	"github.com/sofia-ctx/sofia/internal/history"
	"github.com/sofia-ctx/sofia/internal/mcpserver"
	"github.com/sofia-ctx/sofia/internal/plugin"
	"github.com/sofia-ctx/sofia/internal/strdist"
	"github.com/sofia-ctx/sofia/internal/version"
)

// RootCmd is the master `sf` Cobra command. Subcommands attach in init()
// so consumers (cmd/sf/main.go) just call RootCmd.Execute().
var RootCmd = &cobra.Command{
	Use:   "sf",
	Short: "SF (Sophia Foundation) — structural, auditable context for LLM coding agents",
	Long: `SF (Sophia Foundation / Source Fabric) — a single fast binary for scanning a
project's code and assembling compact, structural context for an LLM agent.`,
	Version:      version.Version,
	SilenceUsage: true,
	// main() prints the error once; let it own that so we don't get a
	// duplicate cobra "Error:" line above our "error:" line.
	SilenceErrors: true,
}

func init() {
	// Help sections: command paths stay flat (no rename — telemetry identity
	// and muscle memory survive), only `sf --help` gets organised.
	RootCmd.AddGroup(
		&cobra.Group{ID: "context", Title: "Context providers:"},
		&cobra.Group{ID: "php", Title: "PHP packages:"},
		&cobra.Group{ID: "projects", Title: "Project tools:"},
		&cobra.Group{ID: "infra", Title: "Infra:"},
	)
	add := func(groupID string, cmds ...*cobra.Command) {
		for _, c := range cmds {
			c.GroupID = groupID
			RootCmd.AddCommand(c)
		}
	}
	add("context", commoncode.NewCommand(), grep.NewCommand(), changed.NewCommand(), cc.NewCommand())
	add("php", composer.NewCommand(), packagist.NewCommand(), github.NewCommand())
	add("projects", vue.NewCommand())
	add("infra", doctor.NewCommand(), gripe.NewCommand(), history.NewCommand(), worktrees.NewCommand(), mcpserver.NewCommand(), plugin.NewCommand())
	RootCmd.AddCommand(hook.NewCommand()) // hidden plumbing — deliberately ungrouped
	RootCmd.SetHelpCommandGroupID("infra")
	RootCmd.SetCompletionCommandGroupID("infra")

	// Enrich cobra's flag-parse errors with a nearest-flag suggestion. Set
	// once on the root; cobra inherits FlagErrorFunc to every subcommand that
	// doesn't define its own (none do), so this covers all of `sf …`.
	RootCmd.SetFlagErrorFunc(flagErrorHint)
}

// flagErrorHint turns "unknown flag: --exportd" into
// "unknown flag: --exportd; did you mean --exported?" so a typo self-corrects
// in the same turn instead of being a dead-end. Shorthand (-x) typos are left
// as-is — one char is too little signal to guess from. When nothing is within
// strdist's typo tolerance the original error passes through unchanged.
func flagErrorHint(cmd *cobra.Command, err error) error {
	const prefix = "unknown flag: --"
	msg := err.Error()
	idx := strings.Index(msg, prefix)
	if idx < 0 {
		return err
	}
	typed := msg[idx+len(prefix):]
	if i := strings.IndexAny(typed, " \t="); i >= 0 {
		typed = typed[:i]
	}
	if typed == "" {
		return err
	}

	var names []string
	collect := func(f *pflag.Flag) { names = append(names, f.Name) }
	cmd.Flags().VisitAll(collect)
	cmd.InheritedFlags().VisitAll(collect)

	if best, ok := strdist.Nearest(typed, names); ok && best != typed {
		return fmt.Errorf("%w; did you mean --%s?", err, best)
	}
	return err
}
