package github

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/sofia-ctx/sofia/internal/calllog"
	"github.com/sofia-ctx/sofia/internal/toon"
)

// BranchOptions controls a `github branches` run.
type BranchOptions struct {
	Format string
	Delete string // "" (report only) | "merged" | "closed" (closed implies merged)
	Refs   int    // max branches fetched per repo
}

// Branch is one non-default branch on an owned repo, reduced to what you need to
// decide whether it can go: where it lives, how stale it is, and the state of
// the pull request it belongs to (the source of truth for "already merged").
type Branch struct {
	Repo    string `json:"repo"`   // nameWithOwner
	Branch  string `json:"branch"` // head ref name (may contain slashes)
	AgeDays int    `json:"age_days"`
	PR      string `json:"pr"`     // "MERGED#11" | "OPEN#15" | "CLOSED#4" | "none"
	Status  string `json:"status"` // merged | closed | open | no-pr
}

// branchQuery walks the viewer's own non-fork repos and each repo's head refs
// with their newest associated PR, in a single round-trip. A merged/closed PR
// is a more reliable "this branch is done" signal than `git branch --merged`,
// which misses squash-merges.
const branchQuery = `
query($login: String!, $repoLimit: Int!, $refLimit: Int!) {
  repositoryOwner(login: $login) {
    repositories(first: $repoLimit, ownerAffiliations: OWNER, isFork: false, orderBy: {field: NAME, direction: ASC}) {
      nodes {
        nameWithOwner
        isArchived
        defaultBranchRef { name }
        refs(refPrefix: "refs/heads/", first: $refLimit) {
          nodes {
            name
            target { ... on Commit { committedDate } }
            associatedPullRequests(first: 1, orderBy: {field: UPDATED_AT, direction: DESC}) {
              nodes { number state }
            }
          }
        }
      }
    }
  }
}`

// ghBranchResp mirrors the `gh api graphql` envelope for branchQuery.
type ghBranchResp struct {
	Data struct {
		RepositoryOwner struct {
			Repositories struct {
				Nodes []ghRepoNode `json:"nodes"`
			} `json:"repositories"`
		} `json:"repositoryOwner"`
	} `json:"data"`
}

type ghRepoNode struct {
	NameWithOwner    string `json:"nameWithOwner"`
	IsArchived       bool   `json:"isArchived"`
	DefaultBranchRef *struct {
		Name string `json:"name"`
	} `json:"defaultBranchRef"`
	Refs struct {
		Nodes []ghRefNode `json:"nodes"`
	} `json:"refs"`
}

type ghRefNode struct {
	Name   string `json:"name"`
	Target struct {
		CommittedDate string `json:"committedDate"`
	} `json:"target"`
	AssociatedPullRequests struct {
		Nodes []struct {
			Number int    `json:"number"`
			State  string `json:"state"`
		} `json:"nodes"`
	} `json:"associatedPullRequests"`
}

// RunBranches reports non-default branches across the viewer's own repos and,
// with --delete, removes the ones whose PR is already merged (or closed).
func RunBranches(opts BranchOptions, w io.Writer) error {
	if opts.Refs <= 0 {
		opts.Refs = 50
	}
	tracker := calllog.Start("github branches", []string{"--format=" + opts.Format, "--delete=" + opts.Delete})

	login, err := viewerLogin()
	if err != nil {
		tracker.Finish(err)
		return err
	}
	out, err := gh(".", 60*time.Second, "api", "graphql",
		"-f", "login="+login,
		"-F", "repoLimit=100",
		"-F", fmt.Sprintf("refLimit=%d", opts.Refs),
		"-f", "query="+branchQuery)
	if err != nil {
		tracker.Finish(err)
		return err
	}
	branches, err := parseBranches([]byte(out), time.Now())
	if err != nil {
		tracker.Finish(err)
		return err
	}
	tracker.SetSummary(map[string]any{"branches": len(branches)})

	cw := &calllog.Counter{W: w}
	if renderErr := renderBranches(cw, opts.Format, branches); renderErr != nil {
		tracker.RecordOutput(cw)
		tracker.Finish(renderErr)
		return renderErr
	}
	if opts.Delete != "" {
		if delErr := deleteBranches(cw, branches, opts.Delete); delErr != nil {
			tracker.RecordOutput(cw)
			tracker.Finish(delErr)
			return delErr
		}
	}
	tracker.RecordOutput(cw)
	tracker.Finish(nil)
	return nil
}

// parseBranches flattens the repo→refs tree into one ranked list: it skips
// archived repos and each repo's default branch, classifies every other branch
// by its newest PR, and floats the safe-to-delete ones (merged, then closed) up.
func parseBranches(data []byte, now time.Time) ([]Branch, error) {
	var resp ghBranchResp
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	var branches []Branch
	for _, repo := range resp.Data.RepositoryOwner.Repositories.Nodes {
		if repo.IsArchived || repo.DefaultBranchRef == nil {
			continue
		}
		def := repo.DefaultBranchRef.Name
		for _, ref := range repo.Refs.Nodes {
			if ref.Name == def {
				continue
			}
			branches = append(branches, Branch{
				Repo:    repo.NameWithOwner,
				Branch:  ref.Name,
				AgeDays: ageDays(ref.Target.CommittedDate, now),
				PR:      prLabel(ref),
				Status:  branchStatus(ref),
			})
		}
	}
	sort.Slice(branches, func(i, j int) bool {
		ri, rj := statusRank(branches[i].Status), statusRank(branches[j].Status)
		if ri != rj {
			return ri < rj
		}
		if branches[i].Repo != branches[j].Repo {
			return branches[i].Repo < branches[j].Repo
		}
		return branches[i].Branch < branches[j].Branch
	})
	return branches, nil
}

// branchStatus maps the newest associated PR's state onto a branch verdict,
// defaulting to no-pr when nothing is attached.
func branchStatus(ref ghRefNode) string {
	if len(ref.AssociatedPullRequests.Nodes) == 0 {
		return "no-pr"
	}
	switch ref.AssociatedPullRequests.Nodes[0].State {
	case "MERGED":
		return "merged"
	case "CLOSED":
		return "closed"
	case "OPEN":
		return "open"
	default:
		return "no-pr"
	}
}

func prLabel(ref ghRefNode) string {
	if len(ref.AssociatedPullRequests.Nodes) == 0 {
		return "none"
	}
	pr := ref.AssociatedPullRequests.Nodes[0]
	return fmt.Sprintf("%s#%d", pr.State, pr.Number)
}

// statusRank floats safe-to-delete branches (merged, then closed) to the top,
// then no-pr leftovers, with active open-PR branches last.
func statusRank(status string) int {
	switch status {
	case "merged":
		return 0
	case "closed":
		return 1
	case "no-pr":
		return 2
	default: // open
		return 3
	}
}

func ageDays(committedDate string, now time.Time) int {
	t, err := time.Parse(time.RFC3339, committedDate)
	if err != nil {
		return -1
	}
	return int(now.Sub(t).Hours() / 24)
}

// isWorktreeBranch flags the `wt/*` branches sf's own worktree forks live on, so
// --delete never yanks one out from under an active checkout.
func isWorktreeBranch(name string) bool {
	return strings.HasPrefix(name, "wt/")
}

// deletable reports whether a branch may be auto-removed under the given mode.
// merged-PR branches always qualify; closed-PR branches only under "closed".
// Worktree branches are never auto-removed.
func deletable(b Branch, mode string) bool {
	if isWorktreeBranch(b.Branch) {
		return false
	}
	switch mode {
	case "merged":
		return b.Status == "merged"
	case "closed":
		return b.Status == "merged" || b.Status == "closed"
	default:
		return false
	}
}

// deleteBranches removes the qualifying branches' refs and prints a summary,
// including the worktree branches it deliberately left alone.
func deleteBranches(w io.Writer, branches []Branch, mode string) error {
	var deleted, protected []string
	var failures []string
	for _, b := range branches {
		if isWorktreeBranch(b.Branch) && (b.Status == "merged" || b.Status == "closed") {
			protected = append(protected, b.Repo+"/"+b.Branch)
			continue
		}
		if !deletable(b, mode) {
			continue
		}
		if _, err := gh(".", 15*time.Second, "api", "-X", "DELETE", "repos/"+b.Repo+"/git/refs/heads/"+b.Branch); err != nil {
			failures = append(failures, fmt.Sprintf("%s/%s (%v)", b.Repo, b.Branch, err))
			continue
		}
		deleted = append(deleted, b.Repo+"/"+b.Branch)
	}

	fmt.Fprintf(w, "\ndeleted %d branch(es) [mode=%s]\n", len(deleted), mode)
	for _, d := range deleted {
		fmt.Fprintf(w, "  - %s\n", d)
	}
	if len(protected) > 0 {
		fmt.Fprintf(w, "skipped %d worktree branch(es) (delete by hand if done):\n", len(protected))
		for _, p := range protected {
			fmt.Fprintf(w, "  - %s\n", p)
		}
	}
	if len(failures) > 0 {
		fmt.Fprintf(w, "failed %d:\n", len(failures))
		for _, f := range failures {
			fmt.Fprintf(w, "  - %s\n", f)
		}
		return fmt.Errorf("failed to delete %d branch(es)", len(failures))
	}
	return nil
}

var branchFields = []string{"repo", "branch", "age_d", "pr", "status"}

func renderBranches(w io.Writer, format string, branches []Branch) error {
	switch format {
	case "", "toon":
		fmt.Fprintf(w, "branches[%d]{%s}:\n", len(branches), strings.Join(branchFields, ","))
		for _, b := range branches {
			fmt.Fprintf(w, "%s%s,%s,%d,%s,%s\n",
				toon.Indent, toon.Scalar(b.Repo), toon.Scalar(b.Branch), b.AgeDays, b.PR, b.Status)
		}
		return nil
	case "md":
		fmt.Fprintf(w, "# github branches (%d non-default)\n\n", len(branches))
		fmt.Fprintln(w, "| Repo | Branch | Age (d) | PR | Status |")
		fmt.Fprintln(w, "| --- | --- | --- | --- | --- |")
		for _, b := range branches {
			fmt.Fprintf(w, "| %s | %s | %d | %s | %s |\n", b.Repo, b.Branch, b.AgeDays, b.PR, b.Status)
		}
		return nil
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		return enc.Encode(branches)
	default:
		return fmt.Errorf("unknown format %q (use toon|md|json)", format)
	}
}
