package cc

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sofia-ctx/sofia/internal/calllog"
	"github.com/sofia-ctx/sofia/pkg/cliflags"
	"github.com/sofia-ctx/sofia/pkg/toon"
)

func newLsCommand() *cobra.Command {
	var (
		projectsDir string
		project     string
		since       string
		limit       int
		format      string
	)
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "Index Claude Code sessions across all projects",
		Long: `ls lists sessions newest-first with the metrics that matter for triage:
project, title, last activity, message count, human-prompt count, real
output/cache tokens (from the transcript's usage records), branch, and PR
count.

Examples:
  sf cc ls                       all sessions, newest first
  sf cc ls --project myapp       only the myapp project
  sf cc ls --since 24h --limit 5 5 most recent in the past day`,
		SilenceUsage: true,
	}
	cmd.Flags().StringVar(&projectsDir, "projects-dir", "", "Claude Code projects root (overrides $CC_PROJECTS_DIR)")
	cmd.Flags().StringVar(&project, "project", "", "filter by project (substring of dir name or cwd)")
	cmd.Flags().StringVar(&since, "since", "", "only sessions active since duration (e.g. 30m, 24h, 7d)")
	cmd.Flags().IntVar(&limit, "limit", 30, "max sessions to list (0 = unlimited)")
	cliflags.AttachFormatFlags(cmd, &format)
	_ = cmd.RegisterFlagCompletionFunc("projects-dir", cliflags.DirOnly)

	cmd.RunE = func(_ *cobra.Command, _ []string) error {
		dir, err := ProjectsDir(projectsDir)
		if err != nil {
			return err
		}
		var sinceT time.Time
		if since != "" {
			d, err := parseSince(since)
			if err != nil {
				return err
			}
			sinceT = time.Now().Add(-d)
		}
		return runLs(dir, lsOptions{Project: project, Since: sinceT, Limit: limit, Format: format}, os.Stdout)
	}
	return cmd
}

type lsOptions struct {
	Project string
	Since   time.Time
	Limit   int
	Format  string
}

// LsRow is one line of the session index.
type LsRow struct {
	ID        string    `json:"id"`
	Project   string    `json:"project"`
	Title     string    `json:"title"`
	Updated   time.Time `json:"updated"`
	Messages  int       `json:"messages"`
	Prompts   int       `json:"prompts"`
	OutTokens int64     `json:"out_tokens"`
	CacheRead int64     `json:"cache_read"`
	Branch    string    `json:"branch"`
	PRs       int       `json:"prs"`
	SizeBytes int64     `json:"size_bytes"`
}

func runLs(projectsDir string, opts lsOptions, w io.Writer) error {
	tracker := calllog.Start("cc.ls", []string{"--format=" + opts.Format, "--project=" + opts.Project})

	rows, err := buildIndex(projectsDir, opts)
	if err != nil {
		tracker.Finish(err)
		return err
	}
	tracker.SetSummary(map[string]any{"sessions": len(rows)})

	cw := &calllog.Counter{W: w}
	var renderErr error
	switch opts.Format {
	case "", "toon":
		renderLsTOON(cw, rows)
	case "md":
		renderLsMarkdown(cw, rows)
	case "json":
		enc := json.NewEncoder(cw)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		renderErr = enc.Encode(rows)
	default:
		renderErr = fmt.Errorf("unknown format %q (use toon|md|json)", opts.Format)
	}
	tracker.RecordOutput(cw)
	tracker.Finish(renderErr)
	return renderErr
}

func buildIndex(projectsDir string, opts lsOptions) ([]LsRow, error) {
	files, err := listSessions(projectsDir)
	if err != nil {
		return nil, err
	}
	proj := strings.ToLower(opts.Project)
	var rows []LsRow
	for _, f := range files {
		if !opts.Since.IsZero() && f.ModTime.Before(opts.Since) {
			continue // cheap recency pre-filter before the parse
		}
		s, err := Parse(f.Path, false)
		if err != nil {
			continue
		}
		if proj != "" &&
			!strings.Contains(strings.ToLower(f.DirName), proj) &&
			!strings.Contains(strings.ToLower(s.Project), proj) &&
			!strings.Contains(strings.ToLower(s.Cwd), proj) {
			continue
		}
		updated := s.End
		if updated.IsZero() {
			updated = f.ModTime
		}
		rows = append(rows, LsRow{
			ID:        s.ID,
			Project:   s.Project,
			Title:     s.Title,
			Updated:   updated,
			Messages:  s.Messages,
			Prompts:   len(s.UserPrompts),
			OutTokens: s.OutputTokens,
			CacheRead: s.CacheReadTokens,
			Branch:    s.Branch,
			PRs:       len(s.PRs),
			SizeBytes: s.SizeBytes,
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Updated.After(rows[j].Updated) })
	if opts.Limit > 0 && len(rows) > opts.Limit {
		rows = rows[:opts.Limit]
	}
	return rows, nil
}

var lsFields = []string{"id", "project", "title", "updated", "msgs", "prompts", "out_tok", "cache_read", "branch", "prs"}

func renderLsTOON(w io.Writer, rows []LsRow) {
	fmt.Fprintf(w, "sessions[%d]{%s}:\n", len(rows), strings.Join(lsFields, ","))
	for _, r := range rows {
		fmt.Fprintf(w, "%s%s,%s,%s,%s,%d,%d,%d,%d,%s,%d\n",
			toon.Indent,
			toon.Scalar(r.ID),
			toon.Scalar(r.Project),
			toon.Scalar(truncate(r.Title, 40)),
			toon.Scalar(r.Updated.Format("2006-01-02T15:04")),
			r.Messages, r.Prompts, r.OutTokens, r.CacheRead,
			toon.Scalar(r.Branch), r.PRs,
		)
	}
}

func renderLsMarkdown(w io.Writer, rows []LsRow) {
	fmt.Fprintf(w, "# Sessions (%d)\n\n", len(rows))
	fmt.Fprintln(w, "| ID | Project | Title | Updated | Msgs | Prompts | Out tok | Cache read | Branch | PRs |")
	fmt.Fprintln(w, "| --- | --- | --- | --- | ---: | ---: | ---: | ---: | --- | ---: |")
	for _, r := range rows {
		fmt.Fprintf(w, "| %s | %s | %s | %s | %d | %d | %d | %d | %s | %d |\n",
			r.ID, r.Project, truncate(r.Title, 40), r.Updated.Format("2006-01-02 15:04"),
			r.Messages, r.Prompts, r.OutTokens, r.CacheRead, r.Branch, r.PRs)
	}
}

// parseSince mirrors history's duration grammar (adds a `d` = days suffix
// on top of Go's time.ParseDuration).
func parseSince(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		n, err := strconv.Atoi(s[:len(s)-1])
		if err != nil {
			return 0, fmt.Errorf("invalid --since %q", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid --since %q (use 30m, 1h, 24h, 7d)", s)
	}
	return d, nil
}
