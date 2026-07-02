package plugin

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// newRoot builds a bare `sf`-like root and attaches the plugin shims, mirroring
// how internal/cli wires them.
func newRoot(t *testing.T) (*cobra.Command, []Descriptor) {
	t.Helper()
	ds := Load()
	root := &cobra.Command{Use: "sf", SilenceUsage: true, SilenceErrors: true}
	for _, c := range BuildCommands(ds) {
		root.AddCommand(c)
	}
	return root, ds
}

// The help-tree fork-bomb guard: rendering `sf --help` and `sf <plugin> --help`
// with N managed plugins present must list them from their manifests without
// executing a single one. Each fixture writes a sentinel if run.
func TestHelpTreeNeverExecutesPlugins(t *testing.T) {
	data := isolate(t)
	sentinel := filepath.Join(data, "ran")
	for _, name := range []string{"one", "two", "three"} {
		writeManaged(t, name,
			"schema: 1\nprotocol: \"1.0.0\"\ndescription: plugin "+name+"\ncommands:\n  - path: go\n    short: run "+name+"\n",
			"echo ran >> "+sentinel+"\n")
	}

	root, ds := newRoot(t)
	if len(BuildCommands(ds)) != 3 {
		t.Fatalf("want 3 shims, got %d", len(BuildCommands(ds)))
	}

	render := func(args ...string) string {
		var buf bytes.Buffer
		root.SetOut(&buf)
		root.SetErr(&buf)
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatalf("execute %v: %v", args, err)
		}
		return buf.String()
	}

	rootHelp := render("--help")
	if !strings.Contains(rootHelp, "one") || !strings.Contains(rootHelp, "plugin two") {
		t.Errorf("root --help missing plugin listing:\n%s", rootHelp)
	}
	subHelp := render("one", "--help")
	if !strings.Contains(subHelp, "go") || !strings.Contains(subHelp, "run one") {
		t.Errorf("`sf one --help` missing subcommand from manifest:\n%s", subHelp)
	}

	if _, err := os.Stat(sentinel); err == nil {
		body, _ := os.ReadFile(sentinel)
		t.Fatalf("building/rendering help executed a plugin (fork bomb):\n%s", body)
	}
}

// A disabled plugin is absent from the command tree (visible only via
// `sf plugin list`), so `sf <disabled>` is a plain unknown command.
func TestBuildCommands_SkipsDisabled(t *testing.T) {
	isolate(t)
	writeManaged(t, "old", "schema: 1\nprotocol: \"99.0.0\"\n", "echo hi\n") // too new → disabled
	writeManaged(t, "ok", "schema: 1\nprotocol: \"1.0.0\"\ncommands:\n  - path: run\n", "echo hi\n")

	cmds := BuildCommands(Load())
	var names []string
	for _, c := range cmds {
		names = append(names, c.Name())
	}
	if strings.Contains(strings.Join(names, ","), "old") {
		t.Errorf("disabled plugin must not get a shim, got %v", names)
	}
	if len(names) != 1 || names[0] != "ok" {
		t.Errorf("want only the enabled plugin, got %v", names)
	}
}

// A managed plugin with no declared commands is a single passthrough command,
// not a help-only group.
func TestBuildCommands_Passthrough(t *testing.T) {
	isolate(t)
	writeManaged(t, "solo", "schema: 1\nprotocol: \"1.0.0\"\ndescription: solo tool\n", "echo hi\n")

	cmds := BuildCommands(Load())
	if len(cmds) != 1 {
		t.Fatalf("want 1 command, got %d", len(cmds))
	}
	c := cmds[0]
	if !c.DisableFlagParsing {
		t.Error("passthrough command should disable flag parsing so flags reach the plugin")
	}
	if c.RunE == nil {
		t.Error("passthrough command must be runnable")
	}
	if len(c.Commands()) != 0 {
		t.Error("passthrough command must not be a group")
	}
}
