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

// PROptions controls a `github pr` run.
type PROptions struct {
	Format string
	Limit  int
}

// PR is one open pull request across the user's repos, collapsed to the few
// fields an agent actually needs to triage it: where it lives, its CI rollup,
// its review verdict, and the user's role on it.
type PR struct {
	Repo   string `json:"repo"`
	Number int    `json:"number"`
	CI     string `json:"ci"`     // statusCheckRollup.state, normalised (SUCCESS/FAILURE/PENDING/NONE)
	Review string `json:"review"` // reviewDecision (APPROVED/CHANGES_REQUESTED/REVIEW_REQUIRED/NONE)
	Role   string `json:"role"`   // author | reviewer | "–" (open PR on your repo, e.g. a bot's)
	Draft  bool   `json:"draft"`
	Title  string `json:"title"`
	URL    string `json:"url"`
}

// prQuery fetches the viewer's open PRs across three dimensions — authored,
// review-requested, and every open PR on the viewer's own public repos — plus
// each one's head-commit CI rollup, in a single round-trip. The third dimension
// is the one that surfaces bot/third-party PRs you'd otherwise only spot by
// opening each repo (authored/review-requested both miss them). Collapsing the
// per-PR `gh pr checks` fan-out into one rollup state is where the token win
// comes from (see docs/measurements/tools/github-pr.md), mirroring `github ci --watch`.
// The three search strings are variables so the owner can be injected at
// runtime (`user:@me` isn't a valid search qualifier).
const prQuery = `
query($limit: Int!, $authored: String!, $review: String!, $owned: String!) {
  authored: search(query: $authored, type: ISSUE, first: $limit) {
    nodes { ...prFields }
  }
  review: search(query: $review, type: ISSUE, first: $limit) {
    nodes { ...prFields }
  }
  owned: search(query: $owned, type: ISSUE, first: $limit) {
    nodes { ...prFields }
  }
}
fragment prFields on PullRequest {
  number
  title
  isDraft
  reviewDecision
  url
  repository { nameWithOwner isFork }
  commits(last: 1) { nodes { commit { statusCheckRollup { state } } } }
}`

// ghPRResp mirrors the `gh api graphql` envelope for prQuery.
type ghPRResp struct {
	Data struct {
		Authored ghPRSearch `json:"authored"`
		Review   ghPRSearch `json:"review"`
		Owned    ghPRSearch `json:"owned"`
	} `json:"data"`
}

type ghPRSearch struct {
	Nodes []ghPRNode `json:"nodes"`
}

type ghPRNode struct {
	Number         int    `json:"number"`
	Title          string `json:"title"`
	IsDraft        bool   `json:"isDraft"`
	ReviewDecision string `json:"reviewDecision"`
	URL            string `json:"url"`
	Repository     struct {
		NameWithOwner string `json:"nameWithOwner"`
		IsFork        bool   `json:"isFork"`
	} `json:"repository"`
	Commits struct {
		Nodes []struct {
			Commit struct {
				StatusCheckRollup *struct {
					State string `json:"state"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
}

// RunPR digests the viewer's open PRs across all repos into a compact table,
// then logs the call.
func RunPR(opts PROptions, w io.Writer) error {
	if opts.Limit <= 0 {
		opts.Limit = 30
	}
	tracker := calllog.Start("github pr", []string{"--format=" + opts.Format})

	login, err := viewerLogin()
	if err != nil {
		tracker.Finish(err)
		return err
	}
	out, err := gh(".", 30*time.Second, "api", "graphql",
		"-F", fmt.Sprintf("limit=%d", opts.Limit),
		"-f", "authored=is:open is:pr author:@me archived:false",
		"-f", "review=is:open is:pr review-requested:@me archived:false",
		"-f", "owned=is:open is:pr is:public archived:false user:"+login,
		"-f", "query="+prQuery)
	if err != nil {
		tracker.Finish(err)
		return err
	}
	prs, err := parsePRs([]byte(out))
	if err != nil {
		tracker.Finish(err)
		return err
	}
	tracker.SetSummary(map[string]any{"prs": len(prs)})
	cw := &calllog.Counter{W: w}
	renderErr := renderPRs(cw, opts.Format, prs)
	tracker.RecordOutput(cw)
	tracker.Finish(renderErr)
	return renderErr
}

// viewerLogin resolves the authenticated user's login so the owned-repo search
// can be scoped to `user:<login>` — `@me` isn't valid for the `user:` qualifier.
func viewerLogin() (string, error) {
	out, err := gh(".", 10*time.Second, "api", "user", "--jq", ".login")
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(out), nil
}

// parsePRs merges the authored, review-requested, and owned-repo result sets,
// de-duplicates by repo+number (precedence authored > reviewer > owned), drops
// forks from the owned set, normalises the CI rollup, and sorts action-needed
// PRs first.
func parsePRs(data []byte) ([]PR, error) {
	var resp ghPRResp
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	byKey := make(map[string]PR)
	add := func(nodes []ghPRNode, role string, skipForks bool) {
		for _, n := range nodes {
			if n.Repository.NameWithOwner == "" {
				continue // non-PR / empty union node
			}
			if skipForks && n.Repository.IsFork {
				continue // a fork's PRs aren't ours to maintain
			}
			key := fmt.Sprintf("%s#%d", n.Repository.NameWithOwner, n.Number)
			if _, ok := byKey[key]; ok {
				continue // a higher-precedence source already recorded it
			}
			byKey[key] = PR{
				Repo:   n.Repository.NameWithOwner,
				Number: n.Number,
				CI:     ciState(n),
				Review: orNone(n.ReviewDecision),
				Role:   role,
				Draft:  n.IsDraft,
				Title:  n.Title,
				URL:    n.URL,
			}
		}
	}
	add(resp.Data.Authored.Nodes, "author", false)
	add(resp.Data.Review.Nodes, "reviewer", false)
	add(resp.Data.Owned.Nodes, "–", true)

	prs := make([]PR, 0, len(byKey))
	for _, p := range byKey {
		prs = append(prs, p)
	}
	sort.Slice(prs, func(i, j int) bool {
		ri, rj := actionRank(prs[i]), actionRank(prs[j])
		if ri != rj {
			return ri < rj
		}
		if prs[i].Repo != prs[j].Repo {
			return prs[i].Repo < prs[j].Repo
		}
		return prs[i].Number < prs[j].Number
	})
	return prs, nil
}

// ciState pulls the head commit's status-check rollup, defaulting to NONE when
// the PR has no checks configured.
func ciState(n ghPRNode) string {
	if len(n.Commits.Nodes) == 0 {
		return "NONE"
	}
	roll := n.Commits.Nodes[0].Commit.StatusCheckRollup
	if roll == nil || roll.State == "" {
		return "NONE"
	}
	return roll.State
}

// actionRank orders PRs so the ones needing the user's attention float up:
// broken CI or requested changes first, then anything still pending, then the
// green/approved ones.
func actionRank(p PR) int {
	switch {
	case p.CI == "FAILURE" || p.CI == "ERROR" || p.Review == "CHANGES_REQUESTED":
		return 0
	case p.CI == "PENDING" || p.CI == "EXPECTED" || p.Review == "REVIEW_REQUIRED":
		return 1
	default:
		return 2
	}
}

// ciSymbol renders a rollup state as a single glyph for the compact view.
func ciSymbol(state string) string {
	switch state {
	case "SUCCESS":
		return "✓"
	case "FAILURE", "ERROR":
		return "✗"
	case "PENDING", "EXPECTED":
		return "⏳"
	default:
		return "–"
	}
}

// reviewShort abbreviates a reviewDecision for the compact view.
func reviewShort(decision string) string {
	switch decision {
	case "APPROVED":
		return "approved"
	case "CHANGES_REQUESTED":
		return "changes"
	case "REVIEW_REQUIRED":
		return "review"
	default:
		return "–"
	}
}

func orNone(s string) string {
	if s == "" {
		return "NONE"
	}
	return s
}

var prFields = []string{"repo", "num", "ci", "review", "role", "draft", "title"}

func renderPRs(w io.Writer, format string, prs []PR) error {
	switch format {
	case "", "toon":
		fmt.Fprintf(w, "pr[%d]{%s}:\n", len(prs), strings.Join(prFields, ","))
		for _, p := range prs {
			fmt.Fprintf(w, "%s%s,%d,%s,%s,%s,%t,%s\n",
				toon.Indent, toon.Scalar(p.Repo), p.Number,
				ciSymbol(p.CI), reviewShort(p.Review), p.Role, p.Draft, toon.Scalar(p.Title))
		}
		return nil
	case "md":
		fmt.Fprintf(w, "# github pr (%d open)\n\n", len(prs))
		fmt.Fprintln(w, "| Repo | # | CI | Review | Role | Draft | Title |")
		fmt.Fprintln(w, "| --- | --- | --- | --- | --- | --- | --- |")
		for _, p := range prs {
			fmt.Fprintf(w, "| %s | %d | %s | %s | %s | %t | %s |\n",
				p.Repo, p.Number, ciSymbol(p.CI), reviewShort(p.Review), p.Role, p.Draft, p.Title)
		}
		return nil
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		return enc.Encode(prs)
	default:
		return fmt.Errorf("unknown format %q (use toon|md|json)", format)
	}
}
