package cc

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/sofia-ctx/sofia/internal/calllog"
	"github.com/sofia-ctx/sofia/internal/cliflags"
	"github.com/sofia-ctx/sofia/internal/toon"
)

func newCandidatesCommand() *cobra.Command {
	var (
		projectsDir string
		project     string
		since       string
		minCount    int
		limit       int
		format      string
	)
	cmd := &cobra.Command{
		Use:   "candidates [session]",
		Short: "Find tool candidates: repeated/expensive operations across sessions",
		Long: `candidates scans one or many sessions for the recurring, token-expensive
operations that a new sf tool would pay off on — sofia's "measure value
before building" turned into a command. Three measured signals:

  heavy_tools        where the tokens actually go (per-tool result tokens)
  repeated_commands  identical shell commands run again and again
  repeated_reads     the same file read more than once

All numbers come from the transcripts (usage records, tool results), so
the ranking is ROI, not a guess.

  sf cc candidates                 across every session
  sf cc candidates --project myapp one project
  sf cc candidates --since 7d      recent sessions
  sf cc candidates 6bd96fc7        a single session`,
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
	}
	cmd.Flags().StringVar(&projectsDir, "projects-dir", "", "Claude Code projects root (overrides $CC_PROJECTS_DIR)")
	cmd.Flags().StringVar(&project, "project", "", "filter by project (substring of dir name or cwd)")
	cmd.Flags().StringVar(&since, "since", "", "only sessions active since duration (e.g. 24h, 7d)")
	cmd.Flags().IntVar(&minCount, "min-count", 2, "minimum repeats for repeated_commands / repeated_reads")
	cmd.Flags().IntVar(&limit, "limit", 20, "max rows per repeated_* section (0 = unlimited)")
	cliflags.AttachFormatFlags(cmd, &format)
	_ = cmd.RegisterFlagCompletionFunc("projects-dir", cliflags.DirOnly)
	cmd.ValidArgsFunction = sessionCompletion

	cmd.RunE = func(_ *cobra.Command, args []string) error {
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

		var sessions []*Session
		if len(args) == 1 {
			path, err := ResolveSelector(dir, args[0])
			if err != nil {
				return err
			}
			s, err := Parse(path, true)
			if err != nil {
				return err
			}
			sessions = []*Session{s}
		} else {
			sessions, err = collectSessions(dir, project, sinceT, true)
			if err != nil {
				return err
			}
		}
		return runCandidates(sessions, minCount, limit, format, os.Stdout)
	}
	return cmd
}

// HeavyTool aggregates one tool's footprint across the scanned sessions.
type HeavyTool struct {
	Tool         string `json:"tool"`
	Calls        int    `json:"calls"`
	ResultTokens int64  `json:"result_tokens"`
	Suggestion   string `json:"suggestion,omitempty"`
}

// RepeatedCmd is an identical shell command seen more than once.
type RepeatedCmd struct {
	Count    int    `json:"count"`
	Sessions int    `json:"sessions"`
	Category string `json:"category"`
	Command  string `json:"command"`
}

// RepeatedRead is a file read more than once.
type RepeatedRead struct {
	Reads    int    `json:"reads"`
	Sessions int    `json:"sessions"`
	Path     string `json:"path"`
}

// Candidates is the full result of a scan.
type Candidates struct {
	Scanned          int            `json:"scanned_sessions"`
	HeavyTools       []HeavyTool    `json:"heavy_tools"`
	RepeatedCommands []RepeatedCmd  `json:"repeated_commands"`
	RepeatedReads    []RepeatedRead `json:"repeated_reads"`
}

// buildCandidates aggregates signals across sessions. Pure (no I/O) so it
// is unit-testable.
func buildCandidates(sessions []*Session, minCount, limit int) Candidates {
	type agg struct {
		count int
		sess  map[string]bool
		cat   string
	}
	heavy := map[string]*HeavyTool{}
	cmds := map[string]*agg{}
	reads := map[string]*agg{}

	for _, s := range sessions {
		for tool, n := range s.ToolCalls {
			h := heavy[tool]
			if h == nil {
				h = &HeavyTool{Tool: tool, Suggestion: toolSuggestion(tool)}
				heavy[tool] = h
			}
			h.Calls += n
			h.ResultTokens += s.ToolResultTokens[tool]
		}
		for _, c := range s.Bash {
			a := cmds[c]
			if a == nil {
				a = &agg{sess: map[string]bool{}, cat: Categorize(c)}
				cmds[c] = a
			}
			a.count++
			a.sess[s.ID] = true
		}
		for _, f := range s.Files {
			if f.Reads <= 0 {
				continue
			}
			a := reads[f.Path]
			if a == nil {
				a = &agg{sess: map[string]bool{}}
				reads[f.Path] = a
			}
			a.count += f.Reads
			a.sess[s.ID] = true
		}
	}

	out := Candidates{Scanned: len(sessions)}
	for _, h := range heavy {
		out.HeavyTools = append(out.HeavyTools, *h)
	}
	sort.Slice(out.HeavyTools, func(i, j int) bool {
		if out.HeavyTools[i].ResultTokens != out.HeavyTools[j].ResultTokens {
			return out.HeavyTools[i].ResultTokens > out.HeavyTools[j].ResultTokens
		}
		return out.HeavyTools[i].Calls > out.HeavyTools[j].Calls
	})

	for cmd, a := range cmds {
		if a.count < minCount {
			continue
		}
		out.RepeatedCommands = append(out.RepeatedCommands, RepeatedCmd{
			Count: a.count, Sessions: len(a.sess), Category: a.cat, Command: cmd,
		})
	}
	sort.Slice(out.RepeatedCommands, func(i, j int) bool {
		if out.RepeatedCommands[i].Count != out.RepeatedCommands[j].Count {
			return out.RepeatedCommands[i].Count > out.RepeatedCommands[j].Count
		}
		return out.RepeatedCommands[i].Command < out.RepeatedCommands[j].Command
	})

	for path, a := range reads {
		if a.count < minCount {
			continue
		}
		out.RepeatedReads = append(out.RepeatedReads, RepeatedRead{
			Reads: a.count, Sessions: len(a.sess), Path: path,
		})
	}
	sort.Slice(out.RepeatedReads, func(i, j int) bool {
		if out.RepeatedReads[i].Reads != out.RepeatedReads[j].Reads {
			return out.RepeatedReads[i].Reads > out.RepeatedReads[j].Reads
		}
		return out.RepeatedReads[i].Path < out.RepeatedReads[j].Path
	})

	if limit > 0 {
		if len(out.RepeatedCommands) > limit {
			out.RepeatedCommands = out.RepeatedCommands[:limit]
		}
		if len(out.RepeatedReads) > limit {
			out.RepeatedReads = out.RepeatedReads[:limit]
		}
	}
	return out
}

// toolSuggestion maps a heavy tool to the kind of sf tool that would cut
// its token cost. Empty when there's no obvious lever.
func toolSuggestion(tool string) string {
	switch tool {
	case "Read":
		return "compact/structural provider over full reads (cf. sf code, sf cc show)"
	case "Bash":
		return "deterministic tool for hot shell ops (see repeated_commands)"
	case "Grep":
		return "layered grep with enclosing (sf grep) instead of raw match dumps"
	default:
		return ""
	}
}

func runCandidates(sessions []*Session, minCount, limit int, format string, w io.Writer) error {
	tracker := calllog.Start("cc.candidates", []string{"--format=" + format})
	c := buildCandidates(sessions, minCount, limit)
	tracker.SetSummary(map[string]any{"scanned": c.Scanned, "repeated_cmds": len(c.RepeatedCommands)})

	cw := &calllog.Counter{W: w}
	var renderErr error
	switch format {
	case "", "toon":
		renderCandidatesTOON(cw, c)
	case "md":
		renderCandidatesMarkdown(cw, c)
	case "json":
		enc := json.NewEncoder(cw)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		renderErr = enc.Encode(c)
	default:
		renderErr = fmt.Errorf("unknown format %q (use toon|md|json)", format)
	}
	tracker.RecordOutput(cw)
	tracker.Finish(renderErr)
	return renderErr
}

func renderCandidatesTOON(w io.Writer, c Candidates) {
	fmt.Fprintf(w, "# scanned %d session(s)\n", c.Scanned)
	fmt.Fprintf(w, "heavy_tools[%d]{tool,calls,result_tokens,suggestion}:\n", len(c.HeavyTools))
	for _, h := range c.HeavyTools {
		fmt.Fprintf(w, "%s%s,%d,%d,%s\n", toon.Indent, toon.Scalar(h.Tool), h.Calls, h.ResultTokens, toon.Scalar(h.Suggestion))
	}
	fmt.Fprintf(w, "repeated_commands[%d]{count,sessions,category,command}:\n", len(c.RepeatedCommands))
	for _, r := range c.RepeatedCommands {
		fmt.Fprintf(w, "%s%d,%d,%s,%s\n", toon.Indent, r.Count, r.Sessions, r.Category, toon.Scalar(truncate(r.Command, 140)))
	}
	fmt.Fprintf(w, "repeated_reads[%d]{reads,sessions,path}:\n", len(c.RepeatedReads))
	for _, r := range c.RepeatedReads {
		fmt.Fprintf(w, "%s%d,%d,%s\n", toon.Indent, r.Reads, r.Sessions, toon.Scalar(r.Path))
	}
}

func renderCandidatesMarkdown(w io.Writer, c Candidates) {
	fmt.Fprintf(w, "# Tool candidates (scanned %d session(s))\n\n", c.Scanned)

	fmt.Fprintln(w, "## Heavy tools — where the tokens go")
	fmt.Fprintln(w, "| Tool | Calls | Result tokens | Suggestion |")
	fmt.Fprintln(w, "| --- | ---: | ---: | --- |")
	for _, h := range c.HeavyTools {
		fmt.Fprintf(w, "| %s | %d | %d | %s |\n", h.Tool, h.Calls, h.ResultTokens, h.Suggestion)
	}

	fmt.Fprintln(w, "\n## Repeated commands")
	fmt.Fprintln(w, "| Count | Sessions | Category | Command |")
	fmt.Fprintln(w, "| ---: | ---: | --- | --- |")
	for _, r := range c.RepeatedCommands {
		fmt.Fprintf(w, "| %d | %d | %s | `%s` |\n", r.Count, r.Sessions, r.Category, r.Command)
	}

	fmt.Fprintln(w, "\n## Repeated reads")
	fmt.Fprintln(w, "| Reads | Sessions | Path |")
	fmt.Fprintln(w, "| ---: | ---: | --- |")
	for _, r := range c.RepeatedReads {
		fmt.Fprintf(w, "| %d | %d | `%s` |\n", r.Reads, r.Sessions, r.Path)
	}
}
