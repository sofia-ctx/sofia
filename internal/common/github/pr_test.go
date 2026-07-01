package github

import (
	"bytes"
	"strings"
	"testing"
)

// sampleResp mirrors a real `gh api graphql` envelope for prQuery: an authored
// green PR, an authored failing PR, a checks-less PR, a review-requested PR
// (also present in authored to exercise de-dup), and an owned-repo set with a
// bot PR (role "–"), a fork PR (dropped), and a dup of the authored green PR
// (authored wins).
const sampleResp = `{
  "data": {
    "authored": {
      "nodes": [
        {"number":5,"title":"green one","isDraft":false,"reviewDecision":null,"url":"https://github.com/acme/a/pull/5",
         "repository":{"nameWithOwner":"acme/a","isFork":false},
         "commits":{"nodes":[{"commit":{"statusCheckRollup":{"state":"SUCCESS"}}}]}},
        {"number":11,"title":"broken","isDraft":false,"reviewDecision":"CHANGES_REQUESTED","url":"https://github.com/acme/b/pull/11",
         "repository":{"nameWithOwner":"acme/b","isFork":false},
         "commits":{"nodes":[{"commit":{"statusCheckRollup":{"state":"FAILURE"}}}]}},
        {"number":3,"title":"no checks","isDraft":true,"reviewDecision":null,"url":"https://github.com/acme/c/pull/3",
         "repository":{"nameWithOwner":"acme/c","isFork":false},
         "commits":{"nodes":[{"commit":{"statusCheckRollup":null}}]}}
      ]
    },
    "review": {
      "nodes": [
        {"number":42,"title":"please review","isDraft":false,"reviewDecision":"REVIEW_REQUIRED","url":"https://github.com/other/d/pull/42",
         "repository":{"nameWithOwner":"other/d","isFork":false},
         "commits":{"nodes":[{"commit":{"statusCheckRollup":{"state":"PENDING"}}}]}},
        {"number":5,"title":"green one","isDraft":false,"reviewDecision":null,"url":"https://github.com/acme/a/pull/5",
         "repository":{"nameWithOwner":"acme/a","isFork":false},
         "commits":{"nodes":[{"commit":{"statusCheckRollup":{"state":"SUCCESS"}}}]}}
      ]
    },
    "owned": {
      "nodes": [
        {"number":7,"title":"bump deps","isDraft":false,"reviewDecision":null,"url":"https://github.com/acme/e/pull/7",
         "repository":{"nameWithOwner":"acme/e","isFork":false},
         "commits":{"nodes":[{"commit":{"statusCheckRollup":{"state":"SUCCESS"}}}]}},
        {"number":9,"title":"fork noise","isDraft":false,"reviewDecision":null,"url":"https://github.com/acme/f/pull/9",
         "repository":{"nameWithOwner":"acme/f","isFork":true},
         "commits":{"nodes":[{"commit":{"statusCheckRollup":{"state":"SUCCESS"}}}]}},
        {"number":5,"title":"green one","isDraft":false,"reviewDecision":null,"url":"https://github.com/acme/a/pull/5",
         "repository":{"nameWithOwner":"acme/a","isFork":false},
         "commits":{"nodes":[{"commit":{"statusCheckRollup":{"state":"SUCCESS"}}}]}}
      ]
    }
  }
}`

func TestParsePRs(t *testing.T) {
	prs, err := parsePRs([]byte(sampleResp))
	if err != nil {
		t.Fatal(err)
	}
	// 5 distinct PRs: a#5 (deduped across review+owned, author wins), b#11, c#3,
	// other/d#42, e#7 (owned). The owned fork f#9 is dropped.
	if len(prs) != 5 {
		t.Fatalf("parsePRs = %d PRs, want 5", len(prs))
	}

	// Action-needed first: b#11 (FAILURE/changes) ranks ahead of green a#5.
	if prs[0].Repo != "acme/b" || prs[0].Number != 11 {
		t.Errorf("expected acme/b#11 first (failing CI), got %s#%d", prs[0].Repo, prs[0].Number)
	}

	byKey := map[string]PR{}
	for _, p := range prs {
		byKey[p.Repo] = p
	}

	if got := byKey["acme/a"]; got.CI != "SUCCESS" || got.Role != "author" {
		t.Errorf("a#5: want CI=SUCCESS role=author, got CI=%s role=%s", got.CI, got.Role)
	}
	if got := byKey["acme/b"]; got.CI != "FAILURE" || got.Review != "CHANGES_REQUESTED" {
		t.Errorf("b#11: want CI=FAILURE review=CHANGES_REQUESTED, got CI=%s review=%s", got.CI, got.Review)
	}
	if got := byKey["acme/c"]; got.CI != "NONE" || !got.Draft {
		t.Errorf("c#3: want CI=NONE draft=true, got CI=%s draft=%t", got.CI, got.Draft)
	}
	if got := byKey["other/d"]; got.Role != "reviewer" || got.CI != "PENDING" {
		t.Errorf("other/d#42: want role=reviewer CI=PENDING, got role=%s CI=%s", got.Role, got.CI)
	}
	if got := byKey["acme/e"]; got.Number != 7 || got.Role != "–" || got.CI != "SUCCESS" {
		t.Errorf("e#7: want num=7 role=– CI=SUCCESS, got num=%d role=%s CI=%s", got.Number, got.Role, got.CI)
	}
	if _, ok := byKey["acme/f"]; ok {
		t.Error("acme/f#9 is a fork and must be dropped from the owned set")
	}
}

func TestParsePRs_Empty(t *testing.T) {
	prs, err := parsePRs([]byte(`{"data":{"authored":{"nodes":[]},"review":{"nodes":[]},"owned":{"nodes":[]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 0 {
		t.Errorf("expected 0 PRs, got %d", len(prs))
	}
}

func TestRenderPRs_TOON(t *testing.T) {
	prs, err := parsePRs([]byte(sampleResp))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := renderPRs(&buf, "toon", prs); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.HasPrefix(out, "pr[5]{repo,num,ci,review,role,draft,title}:") {
		t.Errorf("unexpected TOON header:\n%s", out)
	}
	if !strings.Contains(out, "acme/b,11,✗,changes,author,false,broken") {
		t.Errorf("missing failing-PR row:\n%s", out)
	}
	if !strings.Contains(out, "acme/a,5,✓,–,author,false,green one") {
		t.Errorf("missing green-PR row:\n%s", out)
	}
	if !strings.Contains(out, "acme/e,7,✓,–,–,false,bump deps") {
		t.Errorf("missing owned-repo PR row:\n%s", out)
	}
}

func TestRenderPRs_BadFormat(t *testing.T) {
	if err := renderPRs(&bytes.Buffer{}, "xml", nil); err == nil {
		t.Error("expected error for unknown format")
	}
}

func TestCISymbol(t *testing.T) {
	cases := map[string]string{
		"SUCCESS": "✓", "FAILURE": "✗", "ERROR": "✗",
		"PENDING": "⏳", "EXPECTED": "⏳", "NONE": "–", "": "–",
	}
	for state, want := range cases {
		if got := ciSymbol(state); got != want {
			t.Errorf("ciSymbol(%q) = %q, want %q", state, got, want)
		}
	}
}
