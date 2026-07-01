package github

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseRuns(t *testing.T) {
	data := []byte(`[
		{"databaseId":101,"workflowName":"CI","status":"completed","conclusion":"success","headBranch":"main","event":"push","displayTitle":"Release 2.0.0","createdAt":"2026-05-31T06:00:00Z"},
		{"databaseId":102,"workflowName":"CI","status":"in_progress","conclusion":"","headBranch":"main","event":"push","displayTitle":"WIP","createdAt":"2026-05-31T07:00:00Z"}
	]`)
	runs, err := parseRuns(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 {
		t.Fatalf("parseRuns = %d runs, want 2", len(runs))
	}
	if runs[0].ID != 101 || runs[0].Workflow != "CI" || runs[0].Conclusion != "success" || runs[0].Branch != "main" {
		t.Errorf("run[0] mapped wrong: %+v", runs[0])
	}
	if runs[1].Status != "in_progress" || runs[1].Conclusion != "" {
		t.Errorf("run[1] mapped wrong: %+v", runs[1])
	}
}

func TestParseRuns_Empty(t *testing.T) {
	runs, err := parseRuns([]byte(`[]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 0 {
		t.Errorf("expected 0 runs, got %d", len(runs))
	}
}

func TestCollectAggregate(t *testing.T) {
	root := t.TempDir()
	// Two package git-repos and one package dir without .git (must be skipped).
	mkPkg(t, root, "alpha", "acme/alpha", true)
	mkPkg(t, root, "beta", "acme/beta", true)
	mkPkg(t, root, "gamma", "acme/gamma", false) // no .git → skipped

	latest := func(dir string) (Run, bool, error) {
		switch filepath.Base(dir) {
		case "alpha":
			return Run{ID: 1, Workflow: "CI", Status: "completed", Conclusion: "success"}, true, nil
		case "beta":
			return Run{}, false, errors.New("gh boom") // probe error → zero Run
		default:
			return Run{}, false, nil
		}
	}
	rows, err := collectAggregate(root, latest)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (gamma has no .git → skipped)", len(rows))
	}
	if rows[0].Pkg != "acme/alpha" || rows[1].Pkg != "acme/beta" {
		t.Errorf("rows not sorted by name: %q, %q", rows[0].Pkg, rows[1].Pkg)
	}
	if rows[0].ID != 1 || rows[0].Conclusion != "success" {
		t.Errorf("alpha run mapped wrong: %+v", rows[0])
	}
	if rows[1].ID != 0 || rows[1].Status != "" {
		t.Errorf("beta probe error should leave zero Run: %+v", rows[1])
	}

	// A tree with no package git-repos → error.
	if _, err := collectAggregate(filepath.Join(root, "gamma"), latest); err == nil {
		t.Error("want error for a root with no package repos")
	}
}

func TestCountFailing(t *testing.T) {
	rows := []PkgCI{
		{Run: Run{Status: "completed", Conclusion: "success"}},   // ok
		{Run: Run{Status: "completed", Conclusion: "failure"}},   // failing
		{Run: Run{Status: "completed", Conclusion: "cancelled"}}, // failing
		{Run: Run{Status: "in_progress", Conclusion: ""}},        // not completed
		{Run: Run{}}, // no run
	}
	if got := countFailing(rows); got != 2 {
		t.Errorf("countFailing = %d, want 2", got)
	}
}

func TestRenderAggregate(t *testing.T) {
	rows := []PkgCI{
		{Pkg: "acme/alpha", Run: Run{ID: 1, Workflow: "CI", Status: "completed", Conclusion: "success", Branch: "main"}},
		{Pkg: "acme/beta"}, // no run → dashes
	}

	// failing > 0 surfaced in header; pkg column present; missing run dashed.
	var b strings.Builder
	if err := renderAggregate(&b, "toon", rows, 1); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{"ci[2]{pkg,id,", "# failing=1", "acme/alpha", "acme/beta,—,—"} {
		if !strings.Contains(out, want) {
			t.Errorf("toon output missing %q:\n%s", want, out)
		}
	}

	// failing == 0 → no failing token (header stays bare).
	var b2 strings.Builder
	if err := renderAggregate(&b2, "toon", rows, 0); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b2.String(), "# failing") {
		t.Errorf("toon header should omit failing when 0:\n%s", b2.String())
	}

	// md carries the failing count and the Package column.
	var bmd strings.Builder
	if err := renderAggregate(&bmd, "md", rows, 1); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"1 failing", "| Package |"} {
		if !strings.Contains(bmd.String(), want) {
			t.Errorf("md output missing %q:\n%s", want, bmd.String())
		}
	}
}

// mkPkg creates root/<dir>/composer.json (named name); when repo is true it also
// creates a .git dir so isGitRepo treats it as its own repository.
func mkPkg(t *testing.T, root, dir, name string, repo bool) {
	t.Helper()
	d := filepath.Join(root, dir)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "composer.json"), []byte(`{"name":"`+name+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if repo {
		if err := os.MkdirAll(filepath.Join(d, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}
