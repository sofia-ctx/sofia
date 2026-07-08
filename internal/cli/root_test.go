package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func cmdWithFlags() *cobra.Command {
	cmd := &cobra.Command{Use: "x"}
	cmd.Flags().Bool("exported", false, "")
	cmd.Flags().Bool("api", false, "")
	cmd.Flags().String("format", "toon", "")
	return cmd
}

func TestFlagErrorHint_SuggestsNearest(t *testing.T) {
	err := flagErrorHint(cmdWithFlags(), errors.New("unknown flag: --exportd"))
	if err == nil || !strings.Contains(err.Error(), "did you mean --exported?") {
		t.Fatalf("got %v, want a suggestion of --exported", err)
	}
}

func TestFlagErrorHint_NoSuggestionWhenFar(t *testing.T) {
	in := errors.New("unknown flag: --zzzzzzzz")
	if got := flagErrorHint(cmdWithFlags(), in); got != in {
		t.Fatalf("got %q, want the original error unchanged", got)
	}
}

func TestFlagErrorHint_PassesThroughNonFlagErrors(t *testing.T) {
	in := errors.New("some other error")
	if got := flagErrorHint(cmdWithFlags(), in); got != in {
		t.Fatalf("got %q, want the original error unchanged", got)
	}
}

// Setting the func on RootCmd must reach subcommands: cobra resolves
// FlagErrorFunc by walking up to the parent.
func TestRootCmd_FlagErrorFuncInherited(t *testing.T) {
	for _, c := range RootCmd.Commands() {
		if got := c.FlagErrorFunc(); got == nil {
			t.Errorf("subcommand %q has no inherited FlagErrorFunc", c.Name())
		}
	}
}

// Bare `sf` (no args) should lead with a version line before falling
// through to the usual help/command list.
func TestRootCmd_BareRunPrintsVersion(t *testing.T) {
	buf := &bytes.Buffer{}
	RootCmd.SetOut(buf)
	RootCmd.SetArgs([]string{})
	defer func() {
		RootCmd.SetOut(nil)
		RootCmd.SetArgs(nil)
	}()

	if err := RootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "sf version") {
		t.Fatalf("got %q, want output to contain \"sf version\"", out)
	}
	if !strings.Contains(out, "Usage:") {
		t.Fatalf("got %q, want the usual help to still follow", out)
	}
}
