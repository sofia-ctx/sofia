package pack

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/sofia-ctx/sofia/internal/toon"
)

// listRow is the flat per-pack summary `sf pack list` renders.
type listRow struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Plugins     int    `json:"plugins"`
	Projects    int    `json:"projects"`
}

func toRow(info Info) listRow {
	return listRow{
		Name:        info.Receipt.Name,
		Description: info.Description,
		Plugins:     len(info.Receipt.Plugins),
		Projects:    len(info.Receipt.Projects),
	}
}

// RenderList writes the installed-pack list in the requested format
// (toon|md|json), following the same --format convention as every other sf
// tool.
func RenderList(w io.Writer, format string, infos []Info) error {
	rows := make([]listRow, 0, len(infos))
	for _, info := range infos {
		rows = append(rows, toRow(info))
	}
	switch format {
	case "json":
		return writeJSON(w, map[string]any{"packs": rows})
	case "md":
		fmt.Fprintln(w, "| Name | Description | Plugins | Projects |")
		fmt.Fprintln(w, "| --- | --- | --- | --- |")
		for _, r := range rows {
			fmt.Fprintf(w, "| %s | %s | %d | %d |\n", r.Name, r.Description, r.Plugins, r.Projects)
		}
		return nil
	default:
		fmt.Fprintf(w, "packs[%d]{name,description,plugins,projects}:\n", len(rows))
		for _, r := range rows {
			fmt.Fprintf(w, "%s%s,%s,%d,%d\n", toon.Indent, toon.Scalar(r.Name), toon.Scalar(r.Description), r.Plugins, r.Projects)
		}
		return nil
	}
}

// RenderInfo writes one pack's full receipt: source, shelves, and every
// project it's installed in.
func RenderInfo(w io.Writer, format string, info Info) error {
	switch format {
	case "json":
		return writeJSON(w, info)
	case "md":
		return renderInfoMarkdown(w, info)
	default:
		return renderInfoTOON(w, info)
	}
}

// sourceString formats a Receipt's Source the way `sf plugin install` already
// reports a git landing: "<url> @ <ref> (<short-commit>)", or the bare path
// for a local-directory source.
func sourceString(s Source) string {
	if s.URL != "" {
		ref := s.Ref
		if ref == "" {
			ref = "HEAD"
		}
		commit := s.Commit
		if len(commit) > 7 {
			commit = commit[:7]
		}
		return fmt.Sprintf("%s @ %s (%s)", s.URL, ref, commit)
	}
	return s.Path
}

func sortedProjects(r Receipt) []string {
	projects := make([]string, 0, len(r.Projects))
	for p := range r.Projects {
		projects = append(projects, p)
	}
	sort.Strings(projects)
	return projects
}

func renderInfoTOON(w io.Writer, info Info) error {
	r := info.Receipt
	fmt.Fprintln(w, "pack:")
	kv := func(k, v string) {
		if v != "" {
			fmt.Fprintf(w, "%s%s: %s\n", toon.Indent, k, toon.Scalar(v))
		}
	}
	kv("name", r.Name)
	kv("description", info.Description)
	kv("source", sourceString(r.Source))
	if !r.InstalledAt.IsZero() {
		kv("installed_at", r.InstalledAt.Format(time.RFC3339))
	}

	if len(r.Plugins) > 0 {
		fmt.Fprintf(w, "plugins: %s\n", toon.JoinList(r.Plugins))
	}
	if len(r.Claude) > 0 {
		fmt.Fprintf(w, "claude[%d]{dest}:\n", len(r.Claude))
		for _, c := range r.Claude {
			fmt.Fprintf(w, "%s%s\n", toon.Indent, toon.Scalar(c.Dest))
		}
	}
	projects := sortedProjects(r)
	if len(projects) > 0 {
		fmt.Fprintf(w, "projects[%d]{root,files,installed_at}:\n", len(projects))
		for _, p := range projects {
			pi := r.Projects[p]
			fmt.Fprintf(w, "%s%s,%d,%s\n", toon.Indent, toon.Scalar(p), len(pi.Files), pi.InstalledAt.Format(time.RFC3339))
		}
	}
	return nil
}

func renderInfoMarkdown(w io.Writer, info Info) error {
	r := info.Receipt
	fmt.Fprintf(w, "# %s\n\n", r.Name)
	if info.Description != "" {
		fmt.Fprintf(w, "- description: %s\n", info.Description)
	}
	fmt.Fprintf(w, "- source: %s\n", sourceString(r.Source))
	if !r.InstalledAt.IsZero() {
		fmt.Fprintf(w, "- installed_at: %s\n", r.InstalledAt.Format(time.RFC3339))
	}
	if len(r.Plugins) > 0 {
		fmt.Fprintf(w, "- plugins: %s\n", strings.Join(r.Plugins, ", "))
	}
	if len(r.Claude) > 0 {
		fmt.Fprint(w, "\n## Claude files\n\n")
		for _, c := range r.Claude {
			fmt.Fprintf(w, "- %s\n", c.Dest)
		}
	}
	projects := sortedProjects(r)
	if len(projects) > 0 {
		fmt.Fprint(w, "\n## Projects\n\n")
		fmt.Fprintln(w, "| Root | Files | Installed |")
		fmt.Fprintln(w, "| --- | --- | --- |")
		for _, p := range projects {
			pi := r.Projects[p]
			fmt.Fprintf(w, "| %s | %d | %s |\n", p, len(pi.Files), pi.InstalledAt.Format(time.RFC3339))
		}
	}
	return nil
}

// statusLine is the bare drift summary shown by `sf pack status`: "ok (N
// file(s))" when nothing changed, else the non-zero modified/missing counts.
func statusLine(st PackStatus) string {
	if st.Modified == 0 && st.Missing == 0 {
		return fmt.Sprintf("ok (%d file%s)", st.Ok, plural(st.Ok))
	}
	var parts []string
	if st.Modified > 0 {
		parts = append(parts, fmt.Sprintf("%d modified", st.Modified))
	}
	if st.Missing > 0 {
		parts = append(parts, fmt.Sprintf("%d missing", st.Missing))
	}
	return strings.Join(parts, ", ")
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// RenderStatus writes one pack's drift status: a bare summary line for toon
// ("ok (14 files)" or "2 modified, 1 missing"), the full counts for md/json.
func RenderStatus(w io.Writer, format string, st PackStatus) error {
	switch format {
	case "json":
		return writeJSON(w, st)
	case "md":
		fmt.Fprintf(w, "**%s**: %s\n", st.Name, statusLine(st))
		return nil
	default:
		fmt.Fprintln(w, statusLine(st))
		return nil
	}
}

// RenderStatusAll writes drift status for every installed pack.
func RenderStatusAll(w io.Writer, format string, sts []PackStatus) error {
	switch format {
	case "json":
		return writeJSON(w, map[string]any{"packs": sts})
	case "md":
		fmt.Fprintln(w, "| Name | Status |")
		fmt.Fprintln(w, "| --- | --- |")
		for _, st := range sts {
			fmt.Fprintf(w, "| %s | %s |\n", st.Name, statusLine(st))
		}
		return nil
	default:
		fmt.Fprintf(w, "packs[%d]{name,status}:\n", len(sts))
		for _, st := range sts {
			fmt.Fprintf(w, "%s%s,%s\n", toon.Indent, toon.Scalar(st.Name), toon.Scalar(statusLine(st)))
		}
		return nil
	}
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}
