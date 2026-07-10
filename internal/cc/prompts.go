package cc

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/sofia-ctx/sofia/internal/calllog"
	"github.com/sofia-ctx/sofia/pkg/cliflags"
	"github.com/sofia-ctx/sofia/pkg/toon"
)

func newPromptsCommand() *cobra.Command {
	var (
		projectsDir string
		format      string
	)
	cmd := &cobra.Command{
		Use:   "prompts [session]",
		Short: "Full human turns of a session, in order",
		Long: `prompts prints the genuine human messages of a session verbatim and in
order — the injected system-reminders, continuation summaries, and
tool_result batches are filtered out. It reconstructs intent ("what was
asked") without reading the transcript or the model's replies.

Session selector resolves like in show: last / id-prefix / project / path.`,
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
	}
	cmd.Flags().StringVar(&projectsDir, "projects-dir", "", "Claude Code projects root (overrides $CC_PROJECTS_DIR)")
	cliflags.AttachFormatFlags(cmd, &format)
	_ = cmd.RegisterFlagCompletionFunc("projects-dir", cliflags.DirOnly)
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
		return runPrompts(path, format, os.Stdout)
	}
	return cmd
}

func runPrompts(path, format string, w io.Writer) error {
	tracker := calllog.Start("cc.prompts", []string{"--format=" + format, path})
	s, err := Parse(path, false)
	if err != nil {
		tracker.Finish(err)
		return err
	}
	tracker.SetSummary(map[string]any{"inputs": []string{s.ID, s.Project}, "prompts": len(s.UserPrompts)})

	cw := &calllog.Counter{W: w}
	var renderErr error
	switch format {
	case "", "toon":
		fmt.Fprintf(cw, "prompts[%d]{i,text}: # %s %s\n", len(s.UserPrompts), s.ID, s.Project)
		for i, p := range s.UserPrompts {
			fmt.Fprintf(cw, "%s%d,%s\n", toon.Indent, i+1, toon.Scalar(p))
		}
	case "md":
		fmt.Fprintf(cw, "# Prompts — %s (%s)\n\n", s.ID, s.Project)
		for i, p := range s.UserPrompts {
			fmt.Fprintf(cw, "%d. %s\n", i+1, p)
		}
	case "json":
		enc := json.NewEncoder(cw)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		renderErr = enc.Encode(map[string]any{"id": s.ID, "project": s.Project, "prompts": s.UserPrompts})
	default:
		renderErr = fmt.Errorf("unknown format %q (use toon|md|json)", format)
	}
	tracker.RecordOutput(cw)
	tracker.Finish(renderErr)
	return renderErr
}
