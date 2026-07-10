// Package github implements `sf github ci` — a compact view of a repo's
// GitHub Actions runs via the `gh` CLI, with an optional `--watch` that blocks
// until the latest run finishes and prints only its final status (instead of
// gh's verbose live stream). Targets a single package dir (by name) or the
// current repo.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sofia-ctx/sofia/internal/calllog"
	"github.com/sofia-ctx/sofia/internal/common/composer"
	"github.com/sofia-ctx/sofia/pkg/toon"
)

// Options controls a `github ci` run.
type Options struct {
	Root    string
	Target  string // package name / dir basename / path; empty = current repo
	Format  string
	Limit   int
	Watch   bool
	Timeout time.Duration
	Poll    time.Duration
}

// Run is one GitHub Actions workflow run.
type Run struct {
	ID         int64  `json:"id"`
	Workflow   string `json:"workflow"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	Branch     string `json:"branch"`
	Event      string `json:"event"`
	Title      string `json:"title"`
	CreatedAt  string `json:"created"`
}

// ghRun mirrors the `gh run list/view --json` shape.
type ghRun struct {
	DatabaseID   int64  `json:"databaseId"`
	WorkflowName string `json:"workflowName"`
	Status       string `json:"status"`
	Conclusion   string `json:"conclusion"`
	HeadBranch   string `json:"headBranch"`
	Event        string `json:"event"`
	DisplayTitle string `json:"displayTitle"`
	CreatedAt    string `json:"createdAt"`
}

const jsonFields = "databaseId,workflowName,status,conclusion,headBranch,event,displayTitle,createdAt"

// RunCI lists (or watches) a repo's CI runs, renders them, and logs the call.
// In --watch mode it returns a non-nil error when the watched run concludes in
// failure, so the process exit code mirrors `gh run watch --exit-status`.
func RunCI(opts Options, w io.Writer) error {
	if opts.Limit <= 0 {
		opts.Limit = 5
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 15 * time.Minute
	}
	if opts.Poll <= 0 {
		opts.Poll = 8 * time.Second
	}
	tracker := calllog.Start("github ci", []string{opts.Target, "--format=" + opts.Format})

	dir, err := resolveDir(opts.Root, opts.Target)
	if err != nil {
		tracker.Finish(err)
		return err
	}

	// When the resolved dir is not a git repo it is a tree of packages: report
	// each package's latest run as a compact per-package rollup.
	if !isGitRepo(dir) {
		if opts.Watch {
			err := fmt.Errorf("github ci: --watch needs a single repo, but %s is a tree of packages", dir)
			tracker.Finish(err)
			return err
		}
		return runCIAggregate(dir, opts, w, tracker)
	}

	if opts.Watch {
		runs, watchErr := watchLatest(dir, opts.Timeout, opts.Poll)
		tracker.SetSummary(map[string]any{"dir": dir, "watch": true})
		cw := &calllog.Counter{W: w}
		_ = render(cw, opts.Format, runs)
		tracker.RecordOutput(cw)
		tracker.Finish(watchErr)
		return watchErr
	}

	runs, err := listRuns(dir, opts.Limit)
	if err != nil {
		tracker.Finish(err)
		return err
	}
	tracker.SetSummary(map[string]any{"dir": dir, "runs": len(runs)})
	cw := &calllog.Counter{W: w}
	renderErr := render(cw, opts.Format, runs)
	tracker.RecordOutput(cw)
	tracker.Finish(renderErr)
	return renderErr
}

// resolveDir maps an optional package target to a repo dir: a matching package
// under root, an explicit path, or the current repo when target is empty.
func resolveDir(root, target string) (string, error) {
	base := root
	if base == "" {
		base = "."
	}
	if target == "" {
		return base, nil
	}
	if pkgs, err := composer.Collect(base); err == nil {
		for _, p := range pkgs {
			if p.Name == target || strings.HasSuffix(p.Name, "/"+target) || filepath.Base(p.Dir) == target {
				return filepath.Join(base, p.Dir), nil
			}
		}
	}
	if st, err := os.Stat(target); err == nil && st.IsDir() {
		return target, nil
	}
	return "", fmt.Errorf("github ci: no package or directory matching %q under %s", target, base)
}

// isGitRepo reports whether dir is itself a git repository (a .git dir or, for
// worktrees, a .git file).
func isGitRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// PkgCI is one package's latest CI run in the aggregate (tree) view. The
// embedded Run is zero when the package has no runs or could not be probed.
type PkgCI struct {
	Pkg string `json:"pkg"`
	Run
}

// latestFn fetches the latest CI run for a repo dir; swapped in tests.
type latestFn func(dir string) (Run, bool, error)

// listLatest returns the most recent CI run for dir (ok=false when none).
func listLatest(dir string) (Run, bool, error) {
	runs, err := listRuns(dir, 1)
	if err != nil {
		return Run{}, false, err
	}
	if len(runs) == 0 {
		return Run{}, false, nil
	}
	return runs[0], true, nil
}

// collectAggregate walks root for package git-repos and fetches each one's
// latest run concurrently. Rows follow composer.Collect order (sorted by name);
// a per-package probe failure leaves that row's Run zero rather than aborting.
func collectAggregate(root string, latest latestFn) ([]PkgCI, error) {
	pkgs, err := composer.Collect(root)
	if err != nil {
		return nil, err
	}
	type repo struct{ label, dir string }
	var repos []repo
	for _, p := range pkgs {
		dir := filepath.Join(root, p.Dir)
		if !isGitRepo(dir) {
			continue
		}
		label := p.Name
		if label == "" {
			label = filepath.Base(p.Dir)
		}
		repos = append(repos, repo{label, dir})
	}
	if len(repos) == 0 {
		return nil, fmt.Errorf("github ci: %s is not a git repo and has no package repos under it", root)
	}
	rows := make([]PkgCI, len(repos))
	sem := make(chan struct{}, 6) // bound concurrent gh calls
	var wg sync.WaitGroup
	for i, r := range repos {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, label, dir string) {
			defer wg.Done()
			defer func() { <-sem }()
			row := PkgCI{Pkg: label}
			if run, ok, err := latest(dir); err == nil && ok {
				row.Run = run
			}
			rows[i] = row
		}(i, r.label, r.dir)
	}
	wg.Wait()
	return rows, nil
}

// isFailing reports a completed run that did not conclude in success.
func isFailing(r Run) bool {
	return r.Status == "completed" && r.Conclusion != "" && r.Conclusion != "success"
}

func countFailing(rows []PkgCI) int {
	n := 0
	for _, row := range rows {
		if isFailing(row.Run) {
			n++
		}
	}
	return n
}

// runCIAggregate renders the per-package latest-run rollup for a tree.
func runCIAggregate(root string, opts Options, w io.Writer, tracker *calllog.Tracker) error {
	rows, err := collectAggregate(root, listLatest)
	if err != nil {
		tracker.Finish(err)
		return err
	}
	failing := countFailing(rows)
	tracker.SetSummary(map[string]any{"root": root, "packages": len(rows), "failing": failing})
	cw := &calllog.Counter{W: w}
	renderErr := renderAggregate(cw, opts.Format, rows, failing)
	tracker.RecordOutput(cw)
	tracker.Finish(renderErr)
	return renderErr
}

func listRuns(dir string, limit int) ([]Run, error) {
	out, err := gh(dir, 20*time.Second, "run", "list", "--limit", fmt.Sprint(limit), "--json", jsonFields)
	if err != nil {
		return nil, err
	}
	return parseRuns([]byte(out))
}

func parseRuns(data []byte) ([]Run, error) {
	var raw []ghRun
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	out := make([]Run, 0, len(raw))
	for _, r := range raw {
		out = append(out, fromGH(r))
	}
	return out, nil
}

func fromGH(r ghRun) Run {
	return Run{
		ID:         r.DatabaseID,
		Workflow:   r.WorkflowName,
		Status:     r.Status,
		Conclusion: r.Conclusion,
		Branch:     r.HeadBranch,
		Event:      r.Event,
		Title:      r.DisplayTitle,
		CreatedAt:  r.CreatedAt,
	}
}

// watchLatest polls the latest run until it completes (or the timeout fires),
// returning it and a non-nil error if it did not conclude in success.
func watchLatest(dir string, timeout, poll time.Duration) ([]Run, error) {
	runs, err := listRuns(dir, 1)
	if err != nil {
		return nil, err
	}
	if len(runs) == 0 {
		return nil, fmt.Errorf("github ci: no runs found in %s", dir)
	}
	r := runs[0]
	deadline := time.Now().Add(timeout)
	for r.Status != "completed" {
		if time.Now().After(deadline) {
			return []Run{r}, fmt.Errorf("github ci: timed out after %s, run %d still %s", timeout, r.ID, r.Status)
		}
		time.Sleep(poll)
		got, err := viewRun(dir, r.ID)
		if err != nil {
			return []Run{r}, err
		}
		r = got
	}
	if r.Conclusion != "success" {
		return []Run{r}, fmt.Errorf("github ci: run %d concluded %q", r.ID, r.Conclusion)
	}
	return []Run{r}, nil
}

func viewRun(dir string, id int64) (Run, error) {
	out, err := gh(dir, 20*time.Second, "run", "view", fmt.Sprint(id), "--json", jsonFields)
	if err != nil {
		return Run{}, err
	}
	var raw ghRun
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return Run{}, err
	}
	return fromGH(raw), nil
}

func gh(dir string, timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(), "GH_PROMPT_DISABLED=1")
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("gh %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

var fields = []string{"id", "workflow", "status", "conclusion", "branch", "event", "created", "title"}

func render(w io.Writer, format string, runs []Run) error {
	switch format {
	case "", "toon":
		fmt.Fprintf(w, "ci[%d]{%s}:\n", len(runs), strings.Join(fields, ","))
		for _, r := range runs {
			fmt.Fprintf(w, "%s%d,%s,%s,%s,%s,%s,%s,%s\n",
				toon.Indent, r.ID,
				toon.Scalar(r.Workflow), toon.Scalar(r.Status), toon.Scalar(orDash(r.Conclusion)),
				toon.Scalar(r.Branch), toon.Scalar(r.Event), toon.Scalar(r.CreatedAt), toon.Scalar(r.Title))
		}
		return nil
	case "md":
		fmt.Fprintf(w, "# github ci (%d runs)\n\n", len(runs))
		fmt.Fprintln(w, "| ID | Workflow | Status | Conclusion | Branch | Event | Created | Title |")
		fmt.Fprintln(w, "| --- | --- | --- | --- | --- | --- | --- | --- |")
		for _, r := range runs {
			fmt.Fprintf(w, "| %d | %s | %s | %s | %s | %s | %s | %s |\n",
				r.ID, r.Workflow, r.Status, orDash(r.Conclusion), r.Branch, r.Event, r.CreatedAt, r.Title)
		}
		return nil
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		return enc.Encode(runs)
	default:
		return fmt.Errorf("unknown format %q (use toon|md|json)", format)
	}
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func idOrDash(id int64) string {
	if id == 0 {
		return "—"
	}
	return fmt.Sprint(id)
}

// aggFields is the single-repo field list prefixed with the package column.
var aggFields = append([]string{"pkg"}, fields...)

// renderAggregate renders the per-package latest-run rollup. When failing > 0
// it is surfaced in the header (mirroring packagist's `# drift=K`).
func renderAggregate(w io.Writer, format string, rows []PkgCI, failing int) error {
	switch format {
	case "", "toon":
		fmt.Fprintf(w, "ci[%d]{%s}:", len(rows), strings.Join(aggFields, ","))
		if failing > 0 {
			fmt.Fprintf(w, " # failing=%d", failing)
		}
		fmt.Fprintln(w)
		for _, row := range rows {
			r := row.Run
			fmt.Fprintf(w, "%s%s,%s,%s,%s,%s,%s,%s,%s,%s\n",
				toon.Indent, toon.Scalar(row.Pkg), toon.Scalar(idOrDash(r.ID)),
				toon.Scalar(orDash(r.Workflow)), toon.Scalar(orDash(r.Status)), toon.Scalar(orDash(r.Conclusion)),
				toon.Scalar(orDash(r.Branch)), toon.Scalar(orDash(r.Event)), toon.Scalar(orDash(r.CreatedAt)), toon.Scalar(orDash(r.Title)))
		}
		return nil
	case "md":
		if failing > 0 {
			fmt.Fprintf(w, "# github ci (%d packages, %d failing)\n\n", len(rows), failing)
		} else {
			fmt.Fprintf(w, "# github ci (%d packages)\n\n", len(rows))
		}
		fmt.Fprintln(w, "| Package | ID | Workflow | Status | Conclusion | Branch | Event | Created | Title |")
		fmt.Fprintln(w, "| --- | --- | --- | --- | --- | --- | --- | --- | --- |")
		for _, row := range rows {
			r := row.Run
			fmt.Fprintf(w, "| %s | %s | %s | %s | %s | %s | %s | %s | %s |\n",
				row.Pkg, idOrDash(r.ID), orDash(r.Workflow), orDash(r.Status), orDash(r.Conclusion),
				orDash(r.Branch), orDash(r.Event), orDash(r.CreatedAt), orDash(r.Title))
		}
		return nil
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		return enc.Encode(rows)
	default:
		return fmt.Errorf("unknown format %q (use toon|md|json)", format)
	}
}
