package cc

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sofia-ctx/sofia/internal/calllog"
	"github.com/sofia-ctx/sofia/internal/cliflags"
	"github.com/sofia-ctx/sofia/internal/toon"
)

func newShowCommand() *cobra.Command {
	var (
		projectsDir string
		format      string
	)
	cmd := &cobra.Command{
		Use:   "show [session]",
		Short: "Compact digest of a single Claude Code session",
		Long: `show turns one transcript (~2-4 MB of JSONL) into a one-screen digest:
meta, real token usage (from the transcript's own usage records), the
human prompts that drove the session, a tool histogram, a bash-category
breakdown, the files touched, and the token-heaviest tool results
(compaction candidates).

The [session] selector resolves like a name in xref:
  (omitted) / last   most recently modified session anywhere
  6bd96fc7           by session-id prefix
  myapp              latest session whose project dir matches
  /path/to/x.jsonl   an explicit transcript path

Replaces reading the raw transcript. Economy: docs/measurements/tools/cc-show.md.`,
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
		return runShow(path, format, os.Stdout)
	}
	return cmd
}

func runShow(path, format string, w io.Writer) error {
	tracker := calllog.Start("cc.show", []string{"--format=" + format, path})
	s, err := Parse(path, true)
	if err != nil {
		tracker.Finish(err)
		return err
	}
	tracker.SetSummary(map[string]any{
		"inputs":     []string{s.ID, s.Project},
		"out_tokens": s.OutputTokens,
		"tool_calls": total(s.ToolCalls),
		"raw_bytes":  s.SizeBytes,
	})

	cw := &calllog.Counter{W: w}
	var renderErr error
	switch format {
	case "", "toon":
		renderShowTOON(cw, s)
	case "md":
		renderShowMarkdown(cw, s)
	case "json":
		renderErr = renderShowJSON(cw, s)
	default:
		renderErr = fmt.Errorf("unknown format %q (use toon|md|json)", format)
	}
	tracker.RecordOutput(cw)
	tracker.Finish(renderErr)
	return renderErr
}

func renderShowTOON(w io.Writer, s *Session) {
	fmt.Fprintln(w, "session:")
	kv(w, "id", s.ID)
	kv(w, "project", s.Project)
	kv(w, "cwd", s.Cwd)
	kv(w, "branch", s.Branch)
	kv(w, "model", s.Model)
	kv(w, "version", s.Version)
	if s.Title != "" {
		kv(w, "title", s.Title)
	}
	if !s.Start.IsZero() {
		kv(w, "span", fmt.Sprintf("%s → %s (%s)",
			s.Start.Format("2006-01-02T15:04"), s.End.Format("15:04"), fmtDur(s.Span())))
	}
	fmt.Fprintf(w, "%smessages: %d\n", toon.Indent, s.Messages)
	fmt.Fprintf(w, "%sraw_bytes: %d\n", toon.Indent, s.SizeBytes)

	fmt.Fprintln(w, "tokens:")
	fmt.Fprintf(w, "%soutput: %d\n", toon.Indent, s.OutputTokens)
	fmt.Fprintf(w, "%sinput: %d\n", toon.Indent, s.InputTokens)
	fmt.Fprintf(w, "%scache_read: %d\n", toon.Indent, s.CacheReadTokens)
	fmt.Fprintf(w, "%scache_create: %d\n", toon.Indent, s.CacheCreateTokens)

	if len(s.UserPrompts) > 0 {
		shown, extra := capPrompts(s.UserPrompts)
		fmt.Fprintf(w, "prompts[%d]{i,text}:\n", len(shown))
		for i, p := range shown {
			fmt.Fprintf(w, "%s%d,%s\n", toon.Indent, i+1, toon.Scalar(truncate(p, promptMaxLen)))
		}
		if extra > 0 {
			fmt.Fprintf(w, "# +%d more prompts (sf cc prompts %s)\n", extra, s.ID)
		}
	}

	tools := s.SortedTools()
	fmt.Fprintf(w, "tools[%d]{tool,calls,result_tokens}:\n", len(tools))
	for _, t := range tools {
		fmt.Fprintf(w, "%s%s,%d,%d\n", toon.Indent, toon.Scalar(t.Tool), t.Calls, t.ResultTokens)
	}

	if cats := s.BashCategories(); len(cats) > 0 {
		fmt.Fprintf(w, "bash[%d]{category,calls}:\n", len(cats))
		for _, c := range cats {
			fmt.Fprintf(w, "%s%s,%d\n", toon.Indent, c.Category, c.Calls)
		}
	}

	if len(s.Files) > 0 {
		files := s.Files
		const fileLimit = 20
		extra := 0
		if len(files) > fileLimit {
			extra = len(files) - fileLimit
			files = files[:fileLimit]
		}
		fmt.Fprintf(w, "files[%d]{path,r,e,w}:\n", len(files))
		for _, f := range files {
			fmt.Fprintf(w, "%s%s,%d,%d,%d\n", toon.Indent, toon.Scalar(relPath(s.Cwd, f.Path)), f.Reads, f.Edits, f.Writes)
		}
		if extra > 0 {
			fmt.Fprintf(w, "# +%d more files not shown\n", extra)
		}
	}

	if len(s.FatResults) > 0 {
		fmt.Fprintf(w, "fat_results[%d]{tokens,tool,brief}:\n", len(s.FatResults))
		for _, fr := range s.FatResults {
			fmt.Fprintf(w, "%s%d,%s,%s\n", toon.Indent, fr.Tokens, toon.Scalar(fr.Tool), toon.Scalar(fr.Brief))
		}
	}

	if len(s.PRs) > 0 {
		fmt.Fprintf(w, "prs[%d]{number,url}:\n", len(s.PRs))
		for _, pr := range s.PRs {
			fmt.Fprintf(w, "%s%d,%s\n", toon.Indent, pr.Number, toon.Scalar(pr.URL))
		}
	}
}

func renderShowMarkdown(w io.Writer, s *Session) {
	fmt.Fprintf(w, "# Session %s — %s\n\n", s.ID, s.Project)
	if s.Title != "" {
		fmt.Fprintf(w, "_%s_\n\n", s.Title)
	}
	fmt.Fprintf(w, "- **cwd**: `%s` (branch `%s`)\n", s.Cwd, s.Branch)
	fmt.Fprintf(w, "- **model**: %s · cli %s\n", s.Model, s.Version)
	if !s.Start.IsZero() {
		fmt.Fprintf(w, "- **span**: %s → %s (%s)\n",
			s.Start.Format("2006-01-02 15:04"), s.End.Format("15:04"), fmtDur(s.Span()))
	}
	fmt.Fprintf(w, "- **messages**: %d · raw %s\n", s.Messages, fmtBytes(s.SizeBytes))
	fmt.Fprintf(w, "- **tokens**: out %d · cache read %d · cache create %d · in %d\n\n",
		s.OutputTokens, s.CacheReadTokens, s.CacheCreateTokens, s.InputTokens)

	if len(s.UserPrompts) > 0 {
		shown, extra := capPrompts(s.UserPrompts)
		fmt.Fprintln(w, "## Prompts")
		for _, p := range shown {
			fmt.Fprintf(w, "- %s\n", truncate(p, promptMaxLen))
		}
		if extra > 0 {
			fmt.Fprintf(w, "- _+%d more (`sf cc prompts %s`)_\n", extra, s.ID)
		}
		fmt.Fprintln(w)
	}

	fmt.Fprintln(w, "## Tools")
	fmt.Fprintln(w, "| Tool | Calls | Result tokens |")
	fmt.Fprintln(w, "| --- | ---: | ---: |")
	for _, t := range s.SortedTools() {
		fmt.Fprintf(w, "| %s | %d | %d |\n", t.Tool, t.Calls, t.ResultTokens)
	}
	if cats := s.BashCategories(); len(cats) > 0 {
		fmt.Fprintln(w, "\n## Bash by category")
		for _, c := range cats {
			fmt.Fprintf(w, "- %s × %d\n", c.Category, c.Calls)
		}
	}
	if len(s.FatResults) > 0 {
		fmt.Fprintln(w, "\n## Heaviest tool results")
		for _, fr := range s.FatResults {
			fmt.Fprintf(w, "- %d tok `%s` — %s\n", fr.Tokens, fr.Tool, fr.Brief)
		}
	}
	if len(s.PRs) > 0 {
		fmt.Fprintln(w, "\n## PRs")
		for _, pr := range s.PRs {
			fmt.Fprintf(w, "- #%d %s\n", pr.Number, pr.URL)
		}
	}
}

// showJSON adds string timestamps the Session struct keeps as time.Time.
type showJSON struct {
	*Session
	Start string          `json:"start"`
	End   string          `json:"end"`
	Span  string          `json:"span"`
	Tools []ToolCount     `json:"tools"`
	Bash  []CategoryCount `json:"bash"`
}

func renderShowJSON(w io.Writer, s *Session) error {
	v := showJSON{
		Session: s,
		Tools:   s.SortedTools(),
		Bash:    s.BashCategories(),
		Span:    fmtDur(s.Span()),
	}
	if !s.Start.IsZero() {
		v.Start = s.Start.Format(time.RFC3339)
		v.End = s.End.Format(time.RFC3339)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

func kv(w io.Writer, k, v string) {
	if v == "" {
		return
	}
	fmt.Fprintf(w, "%s%s: %s\n", toon.Indent, k, toon.Scalar(v))
}

// capPrompts limits how many prompts `show` displays, returning the shown
// slice and the count omitted.
func capPrompts(prompts []string) (shown []string, extra int) {
	if len(prompts) > maxPrompts {
		return prompts[:maxPrompts], len(prompts) - maxPrompts
	}
	return prompts, 0
}

func total(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

func fmtDur(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		h := int(d.Hours())
		m := int(d.Minutes()) - h*60
		return fmt.Sprintf("%dh%dm", h, m)
	}
}

func fmtBytes(b int64) string {
	switch {
	case b < 1024:
		return fmt.Sprintf("%dB", b)
	case b < 1024*1024:
		return fmt.Sprintf("%.1fKB", float64(b)/1024)
	default:
		return fmt.Sprintf("%.1fMB", float64(b)/(1024*1024))
	}
}

// sessionCompletion suggests selectors for `sf cc show <Tab>`: "last",
// project names, and recent session-id prefixes. Never prompts; best
// effort, capped.
func sessionCompletion(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	dir, err := ProjectsDir("")
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	files, err := listSessions(dir)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	out := []string{"last\tmost recent session"}
	seenProj := map[string]bool{}
	for i, f := range files {
		proj := projectFromDir(f.DirName)
		if proj != "" && !seenProj[proj] {
			seenProj[proj] = true
			out = append(out, proj+"\tlatest session of "+proj)
		}
		if i < 15 {
			out = append(out, f.Stem[:8]+"\t"+proj+" "+f.ModTime.Format("01-02 15:04"))
		}
	}
	if toComplete != "" {
		filtered := out[:0:0]
		for _, o := range out {
			if strings.HasPrefix(o, toComplete) {
				filtered = append(filtered, o)
			}
		}
		out = filtered
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}
