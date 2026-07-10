package cliflags

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestMinArgs(t *testing.T) {
	const hint = `grep needs a pattern; try: sf grep "<regex>"`
	validate := MinArgs(1, hint)
	cmd := &cobra.Command{Use: "x"}
	for _, tt := range []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{"none", nil, true},
		{"one", []string{"a"}, false},
		{"many", []string{"a", "b"}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := validate(cmd, tt.args)
			if (err != nil) != tt.wantErr {
				t.Fatalf("MinArgs(%v) err=%v, wantErr=%v", tt.args, err, tt.wantErr)
			}
			if err != nil && err.Error() != hint {
				t.Errorf("message = %q, want verbatim hint %q", err.Error(), hint)
			}
		})
	}
}

func TestExactArgsHint(t *testing.T) {
	const hint = "composer show needs a package; try: sf composer ls"
	validate := ExactArgsHint(1, hint)
	cmd := &cobra.Command{Use: "x"}
	for _, tt := range []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{"none", nil, true},
		{"one", []string{"pkg"}, false},
		{"two", []string{"a", "b"}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := validate(cmd, tt.args)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ExactArgsHint(%v) err=%v, wantErr=%v", tt.args, err, tt.wantErr)
			}
			if err != nil && err.Error() != hint {
				t.Errorf("message = %q, want verbatim hint %q", err.Error(), hint)
			}
		})
	}
}
