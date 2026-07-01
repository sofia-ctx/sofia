package cc

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sofia-ctx/sofia/internal/calllog"
	"github.com/sofia-ctx/sofia/internal/cliflags"
	"github.com/sofia-ctx/sofia/internal/toon"
)

func newBashCommand() *cobra.Command {
	var (
		projectsDir string
		category    string
		minCount    int
		limit       int
		full        bool
		format      string
	)
	cmd := &cobra.Command{
		Use:   "bash [session]",
		Short: "Shell commands a session ran, deduped & categorised",
		Long: `bash extracts the shell commands a session ran, deduplicated with a
frequency count and a category (search/read/git/test/build/db/fs/other).
Most-repeated commands surface first — those are the recurring, expensive
operations worth turning into a deterministic tool (see sf cc candidates).

Session selector resolves like in show: last / id-prefix / project / path.

Examples:
  sf cc bash last                  top commands by frequency (--limit 0 = all)
  sf cc bash myapp --category db   only database commands
  sf cc bash myapp --min-count 2   only commands run more than once`,
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
	}
	cmd.Flags().StringVar(&projectsDir, "projects-dir", "", "Claude Code projects root (overrides $CC_PROJECTS_DIR)")
	cmd.Flags().StringVar(&category, "category", "", "filter to one category (search|read|git|test|build|db|fs|other)")
	cmd.Flags().IntVar(&minCount, "min-count", 1, "only commands run at least this many times")
	cmd.Flags().IntVar(&limit, "limit", 30, "max commands to list, by frequency (0 = all)")
	cmd.Flags().BoolVar(&full, "full", false, "show full commands instead of truncating to one line")
	cliflags.AttachFormatFlags(cmd, &format)
	_ = cmd.RegisterFlagCompletionFunc("projects-dir", cliflags.DirOnly)
	_ = cmd.RegisterFlagCompletionFunc("category", categoryCompletion)
	cmd.ValidArgsFunction = sessionCompletion

	cmd.RunE = func(_ *cobra.Command, args []string) error {
		sel := ""
		if len(args) == 1 {
			sel = args[0]
		}
		dir, err := ProjectsDir(projectsDir)
		if err != nil {
			return err
		}
		path, err := ResolveSelector(dir, sel)
		if err != nil {
			return err
		}
		return runBash(path, bashOptions{Category: category, MinCount: minCount, Limit: limit, Full: full, Format: format}, os.Stdout)
	}
	return cmd
}

type bashOptions struct {
	Category string
	MinCount int
	Limit    int
	Full     bool
	Format   string
}

// cmdDisplay collapses a command to one line unless full is set. Keeps the
// bash listing compact — multi-line shell scripts otherwise blow the token
// budget the tool is meant to save.
func cmdDisplay(cmd string, full bool) string {
	if full {
		return cmd
	}
	return truncate(cmd, 120)
}

// BashCmd is one deduplicated command with its frequency and category.
type BashCmd struct {
	Count    int    `json:"count"`
	Category string `json:"category"`
	Command  string `json:"command"`
}

// dedupBash collapses the ordered command list into unique commands with
// counts and categories, plus a per-category summary.
func dedupBash(cmds []string) (commands []BashCmd, summary []CategoryCount) {
	byCmd := map[string]*BashCmd{}
	for _, c := range cmds {
		bc := byCmd[c]
		if bc == nil {
			bc = &BashCmd{Command: c, Category: Categorize(c)}
			byCmd[c] = bc
		}
		bc.Count++
	}
	cat := map[string]*CategoryCount{}
	for _, bc := range byCmd {
		commands = append(commands, *bc)
		cc := cat[bc.Category]
		if cc == nil {
			cc = &CategoryCount{Category: bc.Category}
			cat[bc.Category] = cc
		}
		cc.Calls += bc.Count // total invocations in this category
	}
	uniq := map[string]int{}
	for _, bc := range byCmd {
		uniq[bc.Category]++
	}
	sort.Slice(commands, func(i, j int) bool {
		if commands[i].Count != commands[j].Count {
			return commands[i].Count > commands[j].Count
		}
		return commands[i].Command < commands[j].Command
	})
	for _, cc := range cat {
		summary = append(summary, *cc)
	}
	sort.Slice(summary, func(i, j int) bool {
		if summary[i].Calls != summary[j].Calls {
			return summary[i].Calls > summary[j].Calls
		}
		return summary[i].Category < summary[j].Category
	})
	// stash unique-count alongside category via the map for rendering
	for i := range summary {
		summary[i].Unique = uniq[summary[i].Category]
	}
	return commands, summary
}

// capCommands truncates the command list to limit (0 = all), returning the
// kept slice and how many were dropped — `sf cc bash` lists most-frequent
// first, so the tail is the long-and-rare part that bloats the meta-tool's
// own output. The count drives the "+N more (--limit 0)" footer.
func capCommands(cmds []BashCmd, limit int) ([]BashCmd, int) {
	if limit > 0 && len(cmds) > limit {
		return cmds[:limit], len(cmds) - limit
	}
	return cmds, 0
}

func runBash(path string, opts bashOptions, w io.Writer) error {
	tracker := calllog.Start("cc.bash", []string{"--format=" + opts.Format, path})
	s, err := Parse(path, true)
	if err != nil {
		tracker.Finish(err)
		return err
	}
	commands, summary := dedupBash(s.Bash)

	// apply filters to the command list (summary stays whole-session)
	filtered := commands[:0:0]
	for _, c := range commands {
		if c.Count < opts.MinCount {
			continue
		}
		if opts.Category != "" && c.Category != opts.Category {
			continue
		}
		filtered = append(filtered, c)
	}
	filtered, omitted := capCommands(filtered, opts.Limit)
	tracker.SetSummary(map[string]any{"inputs": []string{s.ID, s.Project}, "unique_cmds": len(commands)})

	cw := &calllog.Counter{W: w}
	var renderErr error
	switch opts.Format {
	case "", "toon":
		fmt.Fprintf(cw, "bash_summary[%d]{category,calls,unique}: # %s %s\n", len(summary), s.ID, s.Project)
		for _, c := range summary {
			fmt.Fprintf(cw, "%s%s,%d,%d\n", toon.Indent, c.Category, c.Calls, c.Unique)
		}
		fmt.Fprintf(cw, "commands[%d]{count,category,command}:\n", len(filtered))
		for _, c := range filtered {
			fmt.Fprintf(cw, "%s%d,%s,%s\n", toon.Indent, c.Count, c.Category, toon.Scalar(cmdDisplay(c.Command, opts.Full)))
		}
		if omitted > 0 {
			fmt.Fprintf(cw, "# +%d more commands (--limit 0 for all)\n", omitted)
		}
	case "md":
		fmt.Fprintf(cw, "# Bash — %s (%s)\n\n", s.ID, s.Project)
		for _, c := range summary {
			fmt.Fprintf(cw, "- **%s**: %d calls, %d unique\n", c.Category, c.Calls, c.Unique)
		}
		fmt.Fprintln(cw, "\n| Count | Category | Command |")
		fmt.Fprintln(cw, "| ---: | --- | --- |")
		for _, c := range filtered {
			fmt.Fprintf(cw, "| %d | %s | `%s` |\n", c.Count, c.Category, strings.ReplaceAll(cmdDisplay(c.Command, opts.Full), "`", "'"))
		}
		if omitted > 0 {
			fmt.Fprintf(cw, "\n_+%d more (--limit 0 for all)_\n", omitted)
		}
	case "json":
		enc := json.NewEncoder(cw)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		renderErr = enc.Encode(map[string]any{
			"id": s.ID, "project": s.Project, "summary": summary, "commands": filtered,
		})
	default:
		renderErr = fmt.Errorf("unknown format %q (use toon|md|json)", opts.Format)
	}
	tracker.RecordOutput(cw)
	tracker.Finish(renderErr)
	return renderErr
}

func categoryCompletion(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	return []string{CatSearch, CatRead, CatGit, CatTest, CatBuild, CatDB, CatFS, CatOther}, cobra.ShellCompDirectiveNoFileComp
}
