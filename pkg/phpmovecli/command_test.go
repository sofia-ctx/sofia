package phpmovecli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func cliFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for name, data := range map[string]string{
		"composer.json":  `{"autoload":{"psr-4":{"App\\":"src/"}}}`,
		"src/Foo.php":    `<?php namespace App; class Foo {}`,
		"src/Client.php": `<?php namespace App; class Client { public Foo $value; }`,
	} {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
func execute(root string, args ...string) (string, error) {
	cmd := NewCommand(func(string) (string, error) { return root, nil })
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}
func TestPlanReviewApplyCLI(t *testing.T) {
	root := cliFixture(t)
	plan := filepath.Join(t.TempDir(), "plan.json")
	out, err := execute(root, "plan", "src/Foo.php", "src/New/Foo.php", "--out", plan, "--diff")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Files: 2", `class App\Foo -> App\New\Foo`, "--- src/Client.php", "+++ src/New/Foo.php", "@@", "Scan excludes"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %s: %s", want, out)
		}
	}
	info, err := os.Stat(plan)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("private plan: %v %v", info, err)
	}
	out, err = execute(root, "apply", plan)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"status":"applied"`) {
		t.Fatal(out)
	}
	if _, err := os.Stat(filepath.Join(root, "src/New/Foo.php")); err != nil {
		t.Fatal(err)
	}
}
func TestCLIRefusesUnsafePlanLocationsAndOtherProject(t *testing.T) {
	root := cliFixture(t)
	plan := filepath.Join(t.TempDir(), "plan.json")
	if _, err := execute(root, "plan", "src/Foo.php", "src/New/Foo.php", "--out", filepath.Join(root, "plan.json")); err == nil {
		t.Fatal("accepted self-invalidating plan path")
	}
	if _, err := execute(root, "plan", "src/Foo.php", "src/New/Foo.php", "--out", plan); err != nil {
		t.Fatal(err)
	}
	if _, err := execute(root, "plan", "src/Foo.php", "src/New/Foo.php", "--out", plan); err == nil {
		t.Fatal("overwrote existing plan")
	}
	if _, err := execute(cliFixture(t), "apply", plan); err == nil || !strings.Contains(err.Error(), "active project") {
		t.Fatal(err)
	}
}
func TestHelpDoesNotResolveRoot(t *testing.T) {
	cmd := NewCommand(func(string) (string, error) { t.Fatal("help resolved root"); return "", nil })
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"plan", "--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "--exclude") {
		t.Fatal(out.String())
	}
}
func TestExplicitExclusionRemainsVisible(t *testing.T) {
	root := cliFixture(t)
	if err := os.WriteFile(filepath.Join(root, "src/Broken.php"), []byte(`<?php class Foo BROKEN`), 0644); err != nil {
		t.Fatal(err)
	}
	plan := filepath.Join(t.TempDir(), "plan.json")
	out, err := execute(root, "plan", "src/Foo.php", "src/New/Foo.php", "--out", plan, "--exclude", "src/Broken.php")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "explicit exclusion src/Broken.php") {
		t.Fatal(out)
	}
	if _, err := execute(root, "apply", plan); err != nil {
		t.Fatal(err)
	}
}

func TestReviewOverflowDoesNotSaveOrChangeSources(t *testing.T) {
	root := cliFixture(t)
	source := `<?php namespace App; class Foo { public const LARGE = '` + strings.Repeat("x", 3<<20) + `'; }`
	if err := os.WriteFile(filepath.Join(root, "src/Foo.php"), []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	plan := filepath.Join(t.TempDir(), "plan.json")
	_, err := execute(root, "plan", "src/Foo.php", "src/New/Foo.php", "--out", plan, "--diff")
	if err == nil || !strings.Contains(err.Error(), "plan was not saved") {
		t.Fatal(err)
	}
	if _, err := os.Stat(plan); !os.IsNotExist(err) {
		t.Fatalf("plan exists: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "src/Foo.php"))
	if err != nil || string(got) != source {
		t.Fatalf("source changed: %v", err)
	}
}

func TestPlanCannotOccupyTransactionPaths(t *testing.T) {
	root := cliFixture(t)
	if err := os.Mkdir(filepath.Join(root, ".sf-move"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"lock", "txn-reserved"} {
		out := filepath.Join(root, ".sf-move", name)
		_, err := execute(root, "plan", "src/Foo.php", "src/New/Foo.php", "--out", out)
		if err == nil || !strings.Contains(err.Error(), "reserved") {
			t.Fatal(err)
		}
		if _, err := os.Stat(out); !os.IsNotExist(err) {
			t.Fatalf("reserved path was created: %v", err)
		}
	}
}
