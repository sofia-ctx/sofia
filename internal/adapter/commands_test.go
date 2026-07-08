package adapter

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// dddCfg is the reference PHP-DDD adapter config the command tests drive.
func dddCfg() Config {
	return Config{
		Kind:        "php-ddd",
		RootMarkers: []string{"composer.json"},
		Ext:         []string{".php"},
		Layers: []Layer{
			{Name: "Domain", Match: []string{"src/Domain/**"}},
			{Name: "Application", Match: []string{"src/Application/**"}},
			{Name: "Infrastructure", Match: []string{"src/Infrastructure/**"}},
		},
	}
}

// dddProject writes a tiny PHP-DDD tree with a composer.json root marker and one
// file per layer, each referencing the User symbol.
func dddProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("composer.json", "{}\n")
	write("src/Domain/User.php", "<?php\nnamespace Domain;\nclass User {}\n")
	write("src/Application/RegisterUser.php", "<?php\nnamespace Application;\nclass RegisterUser {\n  public function run(User $u) {}\n}\n")
	write("src/Infrastructure/UserRepository.php", "<?php\nnamespace Infrastructure;\nclass UserRepository {\n  public function save(User $u) {}\n}\n")
	return root
}

// run executes one synthesized command by name against cfg, capturing stdout.
func run(t *testing.T, cfg Config, args ...string) string {
	t.Helper()
	t.Setenv("SOFIA_LOG_DIR", t.TempDir())
	root := &cobra.Command{Use: "ddd", SilenceUsage: true, SilenceErrors: true}
	for _, c := range Commands("ddd", cfg) {
		root.AddCommand(c)
	}
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		t.Fatalf("execute %v: %v\n%s", args, err, buf.String())
	}
	return buf.String()
}

func TestCommands_LayersList(t *testing.T) {
	out := run(t, dddCfg(), "layers")
	if !strings.Contains(out, "layers[3]{name,match}:") {
		t.Errorf("layers header missing:\n%s", out)
	}
	for _, name := range []string{"Domain", "Application", "Infrastructure"} {
		if !strings.Contains(out, name) {
			t.Errorf("layer %q missing from list:\n%s", name, out)
		}
	}
}

func TestCommands_LayersClassifyPath(t *testing.T) {
	out := run(t, dddCfg(), "layers", "src/Domain/User.php")
	if !strings.Contains(out, "layer: Domain") {
		t.Errorf("path should classify to Domain:\n%s", out)
	}
	un := run(t, dddCfg(), "layers", "tests/UserTest.php")
	if !strings.Contains(un, "layer: (unclassified)") {
		t.Errorf("unmatched path should be (unclassified):\n%s", un)
	}
}

func TestCommands_GrepGroupedByLayer(t *testing.T) {
	root := dddProject(t)
	out := run(t, dddCfg(), "grep", "--root", root, "User")

	// Every layer that references User must head its own TOON group, in
	// declared order.
	dom := strings.Index(out, "Domain{hits=")
	app := strings.Index(out, "Application{hits=")
	infra := strings.Index(out, "Infrastructure{hits=")
	if dom < 0 || app < 0 || infra < 0 {
		t.Fatalf("missing a layer group:\n%s", out)
	}
	if dom >= app || app >= infra {
		t.Errorf("groups not in declared order (Domain<Application<Infrastructure):\n%s", out)
	}
	// The Domain group owns User.php; Infrastructure owns UserRepository.php.
	if !strings.Contains(out, "src/Domain/User.php") {
		t.Errorf("Domain hit missing:\n%s", out)
	}
	if !strings.Contains(out, "src/Infrastructure/UserRepository.php") {
		t.Errorf("Infrastructure hit missing:\n%s", out)
	}
}

func TestCommands_GrepEmptyTreeNoMatches(t *testing.T) {
	// With an explicit --root at an empty/absent tree, grep finds nothing and
	// says so cleanly rather than crashing.
	empty := t.TempDir()
	t.Setenv("SOFIA_LOG_DIR", t.TempDir())
	root := &cobra.Command{Use: "ddd", SilenceUsage: true, SilenceErrors: true}
	for _, c := range Commands("ddd", dddCfg()) {
		root.AddCommand(c)
	}
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"grep", "--root", filepath.Join(empty, "nope"), "User"})
	// --root is made absolute and used as-is; a missing tree just finds nothing.
	// The real not-found path is exercised by resolve_test; here we assert the
	// command runs without crashing on an empty tree.
	if err := root.Execute(); err != nil {
		t.Fatalf("grep on an empty tree should not error: %v", err)
	}
	if !strings.Contains(buf.String(), "no matches") {
		t.Errorf("empty tree should report no matches:\n%s", buf.String())
	}
}
