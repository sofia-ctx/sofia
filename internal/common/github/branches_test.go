package github

import (
	"strings"
	"testing"
	"time"
)

// sampleBranchResp mirrors a `gh api graphql` envelope for branchQuery: one repo
// with a default branch (skipped), a merged-PR branch, a worktree branch, and a
// no-PR branch; an archived repo (skipped wholesale); and a repo with an open-PR
// branch.
const sampleBranchResp = `{
  "data": {
    "repositoryOwner": {
      "repositories": {
        "nodes": [
          {
            "nameWithOwner": "acme/a",
            "isArchived": false,
            "defaultBranchRef": {"name": "main"},
            "refs": {"nodes": [
              {"name": "main", "target": {"committedDate": "2026-06-10T00:00:00Z"}, "associatedPullRequests": {"nodes": []}},
              {"name": "feat/done", "target": {"committedDate": "2026-06-01T00:00:00Z"}, "associatedPullRequests": {"nodes": [{"number": 11, "state": "MERGED"}]}},
              {"name": "wt/s2", "target": {"committedDate": "2026-06-05T00:00:00Z"}, "associatedPullRequests": {"nodes": [{"number": 10, "state": "MERGED"}]}},
              {"name": "spike", "target": {"committedDate": "2026-01-01T00:00:00Z"}, "associatedPullRequests": {"nodes": []}}
            ]}
          },
          {
            "nameWithOwner": "acme/archived",
            "isArchived": true,
            "defaultBranchRef": {"name": "main"},
            "refs": {"nodes": [
              {"name": "leftover", "target": {"committedDate": "2025-01-01T00:00:00Z"}, "associatedPullRequests": {"nodes": [{"number": 1, "state": "MERGED"}]}}
            ]}
          },
          {
            "nameWithOwner": "acme/b",
            "isArchived": false,
            "defaultBranchRef": {"name": "main"},
            "refs": {"nodes": [
              {"name": "feat/live", "target": {"committedDate": "2026-06-12T00:00:00Z"}, "associatedPullRequests": {"nodes": [{"number": 7, "state": "OPEN"}]}}
            ]}
          }
        ]
      }
    }
  }
}`

func TestParseBranches(t *testing.T) {
	now := time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC)
	branches, err := parseBranches([]byte(sampleBranchResp), now)
	if err != nil {
		t.Fatal(err)
	}
	// 4 branches: a/feat/done, a/wt/s2, a/spike, b/feat/live. Default branches and
	// the archived repo are dropped.
	if len(branches) != 4 {
		t.Fatalf("parseBranches = %d, want 4", len(branches))
	}
	for _, b := range branches {
		if b.Branch == "main" {
			t.Errorf("default branch %s/main must be dropped", b.Repo)
		}
		if strings.Contains(b.Repo, "archived") {
			t.Errorf("archived repo branch must be dropped: %s/%s", b.Repo, b.Branch)
		}
	}

	// Merged sorts first.
	if branches[0].Status != "merged" {
		t.Errorf("want a merged branch first, got %s (%s/%s)", branches[0].Status, branches[0].Repo, branches[0].Branch)
	}
	// Open sorts last.
	last := branches[len(branches)-1]
	if last.Status != "open" || last.Branch != "feat/live" {
		t.Errorf("want open feat/live last, got %s %s", last.Status, last.Branch)
	}

	byBranch := map[string]Branch{}
	for _, b := range branches {
		byBranch[b.Branch] = b
	}
	if got := byBranch["feat/done"]; got.Status != "merged" || got.PR != "MERGED#11" || got.AgeDays != 13 {
		t.Errorf("feat/done: want merged MERGED#11 age=13, got %s %s age=%d", got.Status, got.PR, got.AgeDays)
	}
	if got := byBranch["spike"]; got.Status != "no-pr" || got.PR != "none" {
		t.Errorf("spike: want no-pr none, got %s %s", got.Status, got.PR)
	}
}

func TestDeletable(t *testing.T) {
	merged := Branch{Branch: "feat/done", Status: "merged"}
	closed := Branch{Branch: "feat/dead", Status: "closed"}
	open := Branch{Branch: "feat/live", Status: "open"}
	wt := Branch{Branch: "wt/s2", Status: "merged"}

	cases := []struct {
		b    Branch
		mode string
		want bool
	}{
		{merged, "merged", true},
		{merged, "closed", true},
		{closed, "merged", false},
		{closed, "closed", true},
		{open, "closed", false},
		{wt, "closed", false}, // worktree branches are never auto-removed
		{merged, "", false},   // report-only
	}
	for _, c := range cases {
		if got := deletable(c.b, c.mode); got != c.want {
			t.Errorf("deletable(%s status=%s, mode=%q) = %t, want %t", c.b.Branch, c.b.Status, c.mode, got, c.want)
		}
	}
}
