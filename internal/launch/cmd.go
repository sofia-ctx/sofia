package launch

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// validEfforts / validPermissionModes are the values claude accepts; used to
// validate flags up front with a friendly message instead of leaving it to
// claude's own (less local) error. Models aren't validated the same way —
// claude resolves any alias or full name itself.
var (
	validEfforts         = []string{"low", "medium", "high", "xhigh", "max"}
	validPermissionModes = []string{"acceptEdits", "auto", "bypassPermissions", "default", "dontAsk", "plan"}
)

func validate(flag, v string, allowed []string) error {
	if v == "" {
		return nil
	}
	for _, a := range allowed {
		if v == a {
			return nil
		}
	}
	return fmt.Errorf("invalid --%s %q; valid: %s", flag, v, strings.Join(allowed, ", "))
}

// NewCommand returns the `claude` Cobra command (`sf claude`).
func NewCommand() *cobra.Command {
	var (
		model      string
		effort     string
		permission string
		task       string
		out        string
		jsonOut    bool
		quiet      bool
		dir        string
		dryRun     bool
		noOverlay  bool
	)

	cmd := &cobra.Command{
		Use:   "claude [project] [fork] [-- claude args...]",
		Short: "Launch Claude Code for a project",
		Long: `claude starts the claude CLI with its cwd set to a project's dir, so it
loads that project's own root AGENTS.md/CLAUDE.md the normal way — no
separate instruction tree, no injected prompt beyond what you opt into.

Project dir resolution for a bare name, in order: --dir wins outright; else a
saved alias; else an existing $SF_CLAUDE_DIR/<project>; else a dir next to you
(./<project> or ../<project> — the latter also picks the current dir when it's
named <project>). If none match, sf searches a couple of levels under your
projects root ($SF_CLAUDE_DIR) and cwd for a dir of that name: a single hit
launches, several list for you to pick, and the choice is remembered as an
alias (~/.config/sofia/projects.yaml) so next time is instant. With no name at
all, it's the current directory. Set $SF_CLAUDE_PROMPT_FILE to a file of extra
system-prompt text
if you want claude to see something beyond AGENTS.md — unset, none is added.

If you keep a personal overlay for the project (see "sf claude overlay"), its
instructions are injected on top with precedence over the repo's AGENTS.md, and
its dir is opened for editing; --no-overlay skips that.

A second positional is a fork selector: it redirects the session into an
isolated git-worktree copy of the project, created on first use via that
project's own dev/worktree.sh (own branch, own stack — see that script for
what "up"/"new" actually do). A bare number N maps to fork "sN". Needs the
project to ship dev/worktree.sh; --dir fixes the project, so with --dir a
single positional is the fork selector instead.

  sf claude                       launch claude in the current directory
  sf claude myproj                launch $SF_CLAUDE_DIR/myproj
  sf claude myproj 2              fork s2 of myproj: create-if-absent, then launch
  sf claude --dir ~/code/myproj
  sf claude myproj --model opus --permission-mode plan
  sf claude myproj --task "add error handling to fetchUser in api.js"
  sf claude myproj --dry-run      print the resolved command, don't launch

Task mode (--task) runs non-interactively (claude -p), prints the result to
stdout (or --out file; add --quiet to write only the file and return just
the exit code), and exits with claude's status code. Args after -- are
passed through to claude verbatim.`,
		SilenceUsage: true,
	}

	cmd.Flags().StringVar(&model, "model", "", "model: opus | sonnet | haiku | full name")
	cmd.Flags().StringVar(&effort, "effort", "", "thinking effort: low | medium | high | xhigh | max")
	cmd.Flags().StringVar(&permission, "permission-mode", "", "permission mode: default | plan | acceptEdits | bypassPermissions | auto | dontAsk")
	cmd.Flags().StringVarP(&task, "task", "t", "", "run one task non-interactively (claude -p) and exit with its code")
	cmd.Flags().StringVar(&out, "out", "", "with --task: save the result to this file")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "with --task: structured JSON output (result, is_error, cost)")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "with --task --out: write only the file, print nothing (exit code only)")
	cmd.Flags().StringVar(&dir, "dir", "", "project working dir (overrides the positional project + $SF_CLAUDE_DIR)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the resolved claude command without launching")
	cmd.Flags().BoolVar(&noOverlay, "no-overlay", false, "don't inject the project's personal overlay")

	cmd.RunE = func(c *cobra.Command, args []string) error {
		// Split positional args at `--`: before = project/fork, after = passthrough.
		pre := args
		var extra []string
		if d := c.ArgsLenAtDash(); d >= 0 {
			pre, extra = args[:d], args[d:]
		}

		// Positional layout: [project] [fork]. --dir already fixes the
		// project dir, so with --dir a lone positional is the fork selector.
		var project, forkSel string
		if dir != "" {
			if len(pre) >= 1 {
				forkSel = pre[0]
			}
			if len(pre) >= 2 {
				return fmt.Errorf("too many arguments with --dir; only a fork selector is expected")
			}
		} else {
			if len(pre) >= 1 {
				project = pre[0]
			}
			if len(pre) >= 2 {
				forkSel = pre[1]
			}
			if len(pre) >= 3 {
				return fmt.Errorf("too many arguments; usage: sf claude [project] [fork]")
			}
		}

		if err := validate("effort", effort, validEfforts); err != nil {
			return err
		}
		if err := validate("permission-mode", permission, validPermissionModes); err != nil {
			return err
		}
		if quiet && task == "" {
			return fmt.Errorf("--quiet applies only to --task")
		}
		if quiet && out == "" {
			return fmt.Errorf("--quiet requires --out (otherwise the result is discarded)")
		}
		if task == "" && (out != "" || jsonOut) {
			return fmt.Errorf("--out/--json apply only to --task")
		}

		target, err := ResolveTarget(dir, project)
		if err != nil {
			var nf *NotFoundError
			if dir == "" && errors.As(err, &nf) {
				picked, found, perr := searchAndPick(nf.Name, os.Stdout)
				if perr != nil {
					return perr
				}
				if !found {
					return err // the actionable NotFoundError
				}
				target = Target{Name: filepath.Base(picked), Dir: picked}
			} else {
				return err
			}
		}

		if forkSel != "" {
			forkDir, err := ResolveFork(target.Dir, forkSel, dryRun)
			if err != nil {
				return err
			}
			target.Dir = forkDir
		}

		opts := Options{
			Model: model, Effort: effort, Permission: permission,
			Task: task, Out: out, JSON: jsonOut, Quiet: quiet,
			DryRun: dryRun, NoOverlay: noOverlay, Extra: extra,
		}

		code, err := Run(target, opts, os.Stdout)
		if err != nil {
			return err
		}
		// Task mode: exit with claude's status code.
		if task != "" && !dryRun {
			os.Exit(code)
		}
		return nil
	}

	cmd.AddCommand(newOverlayCommand())

	return cmd
}
