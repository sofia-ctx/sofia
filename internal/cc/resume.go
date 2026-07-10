package cc

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sofia-ctx/sofia/internal/calllog"
	"github.com/sofia-ctx/sofia/pkg/cliflags"
	"github.com/sofia-ctx/sofia/pkg/toon"
)

const (
	resumeGoalMax  = 240
	resumeNowMax   = 240
	resumeNextMax  = 600
	resumeMaxFiles = 12
)

func newResumeCommand() *cobra.Command {
	var (
		projectsDir string
		format      string
	)
	cmd := &cobra.Command{
		Use:   "resume [session]",
		Short: "Tiny resume brief to restart a session with a small context",
		Long: `resume distills a transcript into a "where we are" brief — the original
goal, the latest ask, the model's last narrative (the next step), and the
working-set files — so a fresh session starts from a few hundred tokens
instead of carrying or re-reading the whole prior context. It attacks the
biggest token sink: cache_read of large long-running sessions.

The [session] selector resolves like 'sf cc show':
  (omitted) / last   most recently modified session anywhere
  6bd96fc7           by session-id prefix
  myapp              latest session whose project dir matches
  /path/to/x.jsonl   an explicit transcript path`,
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
		return runResume(path, format, os.Stdout)
	}
	return cmd
}

func runResume(path, format string, w io.Writer) error {
	tracker := calllog.Start("cc.resume", []string{"--format=" + format, path})
	s, err := Parse(path, true)
	if err != nil {
		tracker.Finish(err)
		return err
	}
	b := buildBrief(s)
	tracker.SetSummary(map[string]any{"inputs": []string{s.ID, s.Project}, "files": len(b.Files)})

	cw := &calllog.Counter{W: w}
	var renderErr error
	switch format {
	case "", "toon":
		renderResumeTOON(cw, b)
	case "md":
		renderResumeMarkdown(cw, b)
	case "json":
		enc := json.NewEncoder(cw)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		renderErr = enc.Encode(b)
	default:
		renderErr = fmt.Errorf("unknown format %q (use toon|md|json)", format)
	}
	tracker.RecordOutput(cw)
	tracker.Finish(renderErr)
	return renderErr
}

// Brief is the compact resume payload — a handful of fields, each capped, so
// the whole thing is a few hundred tokens regardless of session size.
type Brief struct {
	Project  string      `json:"project"`
	Branch   string      `json:"branch"`
	Session  string      `json:"session"`
	Age      string      `json:"age,omitempty"`
	Messages int         `json:"messages"`
	Goal     string      `json:"goal,omitempty"`
	Now      string      `json:"now,omitempty"`
	Next     string      `json:"next,omitempty"`
	Files    []FileTouch `json:"files,omitempty"`
}

func buildBrief(s *Session) Brief {
	b := Brief{
		Project:  s.Project,
		Branch:   s.Branch,
		Session:  s.ID,
		Messages: s.Messages,
	}
	if !s.End.IsZero() {
		b.Age = fmtDur(time.Since(s.End)) + " ago"
	}
	if len(s.UserPrompts) > 0 {
		b.Goal = oneLine(s.UserPrompts[0], resumeGoalMax)
		if last := oneLine(s.UserPrompts[len(s.UserPrompts)-1], resumeNowMax); last != b.Goal {
			b.Now = last
		}
	}
	if s.LastText != "" {
		b.Next = oneLine(s.LastText, resumeNextMax)
	}

	// Working set: files we changed first (edits+writes), then most-read.
	files := append([]FileTouch(nil), s.Files...)
	sort.SliceStable(files, func(i, j int) bool {
		ci, cj := files[i].Edits+files[i].Writes, files[j].Edits+files[j].Writes
		if ci != cj {
			return ci > cj
		}
		return files[i].Reads > files[j].Reads
	})
	if len(files) > resumeMaxFiles {
		files = files[:resumeMaxFiles]
	}
	for i := range files {
		files[i].Path = relPath(s.Cwd, files[i].Path)
	}
	b.Files = files
	return b
}

func renderResumeTOON(w io.Writer, b Brief) {
	fmt.Fprintln(w, "resume:")
	kv(w, "project", b.Project)
	kv(w, "branch", b.Branch)
	kv(w, "session", b.Session)
	kv(w, "age", b.Age)
	fmt.Fprintf(w, "%smessages: %d\n", toon.Indent, b.Messages)
	if b.Goal != "" {
		fmt.Fprintf(w, "goal: %s\n", toon.Scalar(b.Goal))
	}
	if b.Now != "" {
		fmt.Fprintf(w, "now: %s\n", toon.Scalar(b.Now))
	}
	if b.Next != "" {
		fmt.Fprintf(w, "next: %s\n", toon.Scalar(b.Next))
	}
	if len(b.Files) > 0 {
		fmt.Fprintf(w, "files[%d]{path,r,e,w}:\n", len(b.Files))
		for _, f := range b.Files {
			fmt.Fprintf(w, "%s%s,%d,%d,%d\n", toon.Indent, toon.Scalar(f.Path), f.Reads, f.Edits, f.Writes)
		}
	}
}

func renderResumeMarkdown(w io.Writer, b Brief) {
	fmt.Fprintf(w, "# Resume — %s (%s)\n\n", b.Project, b.Session)
	fmt.Fprintf(w, "- branch `%s` · %d messages · %s\n\n", b.Branch, b.Messages, b.Age)
	if b.Goal != "" {
		fmt.Fprintf(w, "**Goal:** %s\n\n", b.Goal)
	}
	if b.Now != "" {
		fmt.Fprintf(w, "**Now:** %s\n\n", b.Now)
	}
	if b.Next != "" {
		fmt.Fprintf(w, "**Next:** %s\n\n", b.Next)
	}
	if len(b.Files) > 0 {
		fmt.Fprintln(w, "**Working set:**")
		for _, f := range b.Files {
			fmt.Fprintf(w, "- `%s` (r%d e%d w%d)\n", f.Path, f.Reads, f.Edits, f.Writes)
		}
	}
}

// oneLine collapses whitespace/newlines and truncates to keep a field compact.
func oneLine(s string, max int) string {
	return truncate(strings.Join(strings.Fields(s), " "), max)
}
