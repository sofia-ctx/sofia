package cliflags

import (
	"testing"

	"github.com/spf13/cobra"
)

func newCmdWithFormat() (*cobra.Command, *string) {
	cmd := &cobra.Command{
		Use:  "test",
		RunE: func(*cobra.Command, []string) error { return nil },
	}
	format := ""
	AttachFormatFlags(cmd, &format)
	return cmd, &format
}

func TestAttachFormatFlags_RegistersAllFlags(t *testing.T) {
	cmd, _ := newCmdWithFormat()
	for _, name := range []string{"format", "md", "json"} {
		if cmd.Flag(name) == nil {
			t.Errorf("flag %q not registered", name)
		}
	}
}

func TestAttachFormatFlags_DefaultIsToon(t *testing.T) {
	cmd, format := newCmdWithFormat()
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if *format != "toon" {
		t.Errorf("default format = %q, want toon", *format)
	}
}

func TestAttachFormatFlags_MdAlias(t *testing.T) {
	cmd, format := newCmdWithFormat()
	cmd.SetArgs([]string{"--md"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if *format != "md" {
		t.Errorf("got %q, want md", *format)
	}
}

func TestAttachFormatFlags_JsonAlias(t *testing.T) {
	cmd, format := newCmdWithFormat()
	cmd.SetArgs([]string{"--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if *format != "json" {
		t.Errorf("got %q, want json", *format)
	}
}

func TestAttachFormatFlags_MdJsonConflict(t *testing.T) {
	cmd, _ := newCmdWithFormat()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"--md", "--json"})
	if err := cmd.Execute(); err == nil {
		t.Error("expected --md/--json conflict to fail")
	}
}

func TestAttachFormatFlags_MdConflictsWithExplicitFormat(t *testing.T) {
	cmd, _ := newCmdWithFormat()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"--md", "--format=json"})
	if err := cmd.Execute(); err == nil {
		t.Error("expected --md vs --format=json to fail")
	}
}

func TestFormatCompletion(t *testing.T) {
	results, _ := FormatCompletion(nil, nil, "")
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
}
