// Package worktrees implements `sf worktrees` — a read-only, cross-project view
// of git-worktree dev forks under a configured parent directory (see
// DefaultWww). For each repo it lists linked worktrees; where a repo ships
// dev/worktree.sh it enriches them with that script's `ls --json` (stack
// state, health, ports — the single source of truth for the per-project
// scheme). This tool never mutates: create/remove forks via the project's own
// dev/worktree.sh.
package worktrees

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sofia-ctx/sofia/internal/calllog"
	"github.com/sofia-ctx/sofia/internal/gitexec"
)

type Options struct {
	Www    string // parent dir to scan for repos; empty means DefaultWww()
	Format string
}

// DefaultWww is the parent dir sf worktrees scans when neither --www nor
// Options.Www is set: the SOFIA_WWW env var when present, else the
// conventional /www directory when it actually exists on this machine (many
// dev setups symlink it to a shared checkout root — but that's a convention,
// not something every install has), else the current working directory. No
// single absolute path is universal, so unlike a hardcoded "/www" this never
// points somewhere that doesn't exist without the caller opting in.
func DefaultWww() string {
	if v := os.Getenv("SOFIA_WWW"); v != "" {
		return v
	}
	if fi, err := os.Stat("/www"); err == nil && fi.IsDir() {
		return "/www"
	}
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return "."
}

// Fork is one linked worktree, optionally enriched with dev-stack info when the
// owning repo ships dev/worktree.sh (otherwise stack fields stay empty/nil).
type Fork struct {
	Project  string `json:"project"`        // repo name (basename of /www/<project>)
	Slug     string `json:"slug,omitempty"` // fork id from dev/worktree.sh; empty for plain worktrees
	Branch   string `json:"branch"`
	Dir      string `json:"dir"`
	Running  *bool  `json:"running,omitempty"`
	Health   string `json:"health,omitempty"`
	Dirty    bool   `json:"dirty"`
	Ahead    *int   `json:"ahead,omitempty"`
	URL      string `json:"url,omitempty"`
	FrontURL string `json:"front_url,omitempty"` // front-end (vite) URL, where the scheme provides one
	Age      string `json:"age,omitempty"`
}

type Result struct {
	Www   string `json:"www"`
	Forks []Fork `json:"forks"`
}

// Run collects the cross-project fork view, renders it, and logs the call.
func Run(opts Options, w io.Writer) error {
	tracker := calllog.Start("worktrees", []string{"--format=" + opts.Format})
	res, err := Collect(opts)
	if err != nil {
		tracker.Finish(err)
		return err
	}
	tracker.SetSummary(map[string]any{"forks": len(res.Forks), "www": res.Www})

	cw := &calllog.Counter{W: w}
	var renderErr error
	switch opts.Format {
	case "", "toon":
		renderTOON(cw, res)
	case "md":
		renderMarkdown(cw, res)
	case "json":
		renderErr = renderJSON(cw, res)
	default:
		renderErr = fmt.Errorf("unknown format %q (use toon|md|json)", opts.Format)
	}
	tracker.RecordOutput(cw)
	tracker.Finish(renderErr)
	return renderErr
}

// Collect scans <www>/* for git repos and gathers their linked worktrees.
func Collect(opts Options) (*Result, error) {
	www := opts.Www
	if www == "" {
		www = DefaultWww()
	}
	res := &Result{Www: www}

	entries, err := os.ReadDir(www)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", www, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)

	for _, name := range names {
		repo := filepath.Join(www, name)
		if !isGitRepo(repo) {
			continue
		}
		// Repos that own the scheme report through their script (single source
		// of truth); everything else gets a plain git-worktree listing.
		if script := filepath.Join(repo, "dev", "worktree.sh"); isFile(script) {
			if forks, err := scriptForks(repo, name, script); err == nil {
				res.Forks = append(res.Forks, forks...)
				continue
			}
		}
		res.Forks = append(res.Forks, plainForks(repo, name)...)
	}
	return res, nil
}

// scriptForks runs <repo>/dev/worktree.sh ls --json and maps it to Forks.
func scriptForks(repo, proj, script string) ([]Fork, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, script, "ls", "--json")
	cmd.Dir = repo
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Slug     string  `json:"slug"`
		Branch   string  `json:"branch"`
		Dir      string  `json:"dir"`
		URL      string  `json:"url"`
		FrontURL string  `json:"front_url"`
		Running  bool    `json:"running"`
		Health   *string `json:"health"`
		Dirty    bool    `json:"dirty"`
		Ahead    *int    `json:"ahead"`
		Age      string  `json:"age"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, err
	}
	forks := make([]Fork, 0, len(raw))
	for _, r := range raw {
		running := r.Running
		f := Fork{
			Project: proj, Slug: r.Slug, Branch: r.Branch, Dir: r.Dir,
			URL: r.URL, FrontURL: r.FrontURL, Running: &running, Dirty: r.Dirty, Ahead: r.Ahead, Age: r.Age,
		}
		if r.Health != nil {
			f.Health = *r.Health
		}
		forks = append(forks, f)
	}
	return forks, nil
}

// plainForks lists a repo's linked worktrees (excluding its primary checkout)
// for repos without dev/worktree.sh — branch + dir + dirty, no stack columns.
func plainForks(repo, proj string) []Fork {
	out, err := gitexec.Run(repo, "worktree", "list", "--porcelain")
	if err != nil {
		return nil
	}
	mainPath := resolve(repo)
	var forks []Fork
	var cur Fork
	flush := func() {
		if cur.Dir != "" && resolve(cur.Dir) != mainPath {
			cur.Project = proj
			cur.Dirty = worktreeDirty(cur.Dir)
			forks = append(forks, cur)
		}
		cur = Fork{}
	}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			cur.Dir = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "branch "):
			cur.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		case line == "detached":
			cur.Branch = "(detached)"
		}
	}
	flush()
	return forks
}

func isGitRepo(repo string) bool {
	fi, err := os.Stat(repo)
	if err != nil || !fi.IsDir() {
		return false
	}
	_, err = os.Stat(filepath.Join(repo, ".git"))
	return err == nil
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

func worktreeDirty(dir string) bool {
	out, err := gitexec.Run(dir, "status", "--porcelain")
	return err == nil && strings.TrimSpace(out) != ""
}

func resolve(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}
