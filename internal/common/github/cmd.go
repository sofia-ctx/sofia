package github

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/sofia-ctx/sofia/internal/cliflags"
)

// NewCommand returns the `github` group (`sf github …`).
func NewCommand() *cobra.Command {
	g := &cobra.Command{
		Use:   "github",
		Short: "GitHub helpers for PHP package repos (via the gh CLI)",
		Long: `github wraps the gh CLI for the recurring repo chores, returning compact
output instead of gh's verbose stream.

  sf github ci [pkg]            latest CI runs for a repo
  sf github ci array-reader     a package by name (resolved under --root)
  sf github ci --watch          block until the latest run finishes
  sf github pr                  your open PRs across all repos + CI rollup
  sf github branches            non-default branches across your repos; --delete tidies merged ones`,
	}
	g.AddCommand(newCICommand())
	g.AddCommand(newPRCommand())
	g.AddCommand(newBranchesCommand())
	return g
}

func newPRCommand() *cobra.Command {
	var (
		format string
		limit  int
	)
	cmd := &cobra.Command{
		Use:   "pr",
		Short: "Your open PRs across all repos (authored, review-requested, on your public repos) with CI rollup",
		Long: `pr digests every open pull request you authored, were asked to review, or that
sits on one of your own public repos — the last dimension surfaces bot and
third-party PRs you'd otherwise only spot by opening each repo — into one
compact table: repo, number, CI rollup (✓/✗/⏳/–), review verdict, your role,
draft, title. Action-needed PRs (broken CI or requested changes) sort first.

It is a single gh api graphql call — collapsing the per-PR ` + "`gh pr checks`" + ` fan-out
into one head-commit rollup, so it stays cheap on tokens regardless of how many
PRs or CI jobs you have.

  sf github pr               all your open PRs with CI status
  sf github pr --md          markdown table (human view)
  sf github pr --limit 50`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
	}
	cmd.Flags().IntVar(&limit, "limit", 30, "max PRs to fetch per search dimension (authored / review-requested / owned)")
	cliflags.AttachFormatFlags(cmd, &format)

	cmd.RunE = func(_ *cobra.Command, _ []string) error {
		return RunPR(PROptions{Format: format, Limit: limit}, os.Stdout)
	}
	return cmd
}

func newBranchesCommand() *cobra.Command {
	var (
		format string
		del    string
		refs   int
	)
	cmd := &cobra.Command{
		Use:   "branches",
		Short: "Non-default branches across your own repos, with merged/closed/stale verdicts",
		Long: `branches lists every non-default branch on your own (non-fork, non-archived)
repos in one compact table: repo, branch, age in days, the newest associated PR,
and a status (merged | closed | open | no-pr). Safe-to-delete branches — those
whose PR is already merged, then closed — sort first.

A merged/closed PR is the source of truth for "this branch is done"; it catches
squash-merges that ` + "`git branch --merged`" + ` misses. It is a single gh api graphql call.

By default it only reports. --delete removes the branches whose PR is merged
(or, with --delete=closed, merged and closed too); worktree branches (wt/*) are
always left for you to remove by hand.

  sf github branches              report only
  sf github branches --md         markdown table (human view)
  sf github branches --delete     delete branches whose PR is merged
  sf github branches --delete=closed   also delete closed-PR branches`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
	}
	cmd.Flags().StringVar(&del, "delete", "", "delete done branches: merged | closed (default: report only)")
	cmd.Flags().Lookup("delete").NoOptDefVal = "merged"
	cmd.Flags().IntVar(&refs, "refs", 50, "max branches to inspect per repo")
	cliflags.AttachFormatFlags(cmd, &format)

	cmd.RunE = func(_ *cobra.Command, _ []string) error {
		if del != "" && del != "merged" && del != "closed" {
			return fmt.Errorf("invalid --delete %q (use merged|closed)", del)
		}

		return RunBranches(BranchOptions{Format: format, Delete: del, Refs: refs}, os.Stdout)
	}
	return cmd
}

func newCICommand() *cobra.Command {
	var (
		root    string
		format  string
		limit   int
		watch   bool
		timeout time.Duration
	)
	cmd := &cobra.Command{
		Use:   "ci [pkg]",
		Short: "Latest GitHub Actions runs for a repo; --watch blocks until the latest finishes",
		Long: `ci summarises a repo's recent GitHub Actions runs (id, workflow, status,
conclusion, branch, event, time). The target is a package name / dir basename
resolved under --root, or the current repo when omitted. When the resolved dir
is a tree of packages (not a git repo itself), it reports each package's latest
run as a compact per-package rollup.

  sf github ci                                     current repo, last 5 runs
  sf github ci array-reader                        a package, resolved under --root
  sf github ci enum --watch                        wait for the latest run, print final status
  sf github ci --limit 10
  sf github ci --root /path/to/your/packages       each package under a tree, latest run
  sf github ci /path/to/your/packages              (same, tree as positional)`,
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
	}
	cmd.Flags().StringVar(&root, "root", "", "tree to resolve a package under (default: current dir)")
	cmd.Flags().IntVar(&limit, "limit", 5, "max runs to list")
	cmd.Flags().BoolVar(&watch, "watch", false, "block until the latest run completes, then print its status")
	cmd.Flags().DurationVar(&timeout, "timeout", 15*time.Minute, "max time to wait in --watch mode")
	cliflags.AttachFormatFlags(cmd, &format)
	_ = cmd.RegisterFlagCompletionFunc("root", cliflags.DirOnly)

	cmd.RunE = func(_ *cobra.Command, args []string) error {
		target := ""
		if len(args) == 1 {
			target = args[0]
		}
		return RunCI(Options{Root: root, Target: target, Format: format, Limit: limit, Watch: watch, Timeout: timeout}, os.Stdout)
	}
	return cmd
}
