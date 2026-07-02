package code

import (
	"reflect"
	"strings"
	"testing"
)

// TestClassifyArgs covers `sf code`'s dispatch: files vs. symbols. Cobra
// wiring itself is a thin pass-through (see NewCommand), so the interesting
// logic — and the seam tests hook into — lives in classifyArgs.
func TestClassifyArgs(t *testing.T) {
	fileA := writeTmp(t, "a.go", "package x\n")
	fileB := writeTmp(t, "b.go", "package x\n")

	for _, tt := range []struct {
		name        string
		args        []string
		wantFiles   []string
		wantSymbols []string
		wantErr     string // substring; empty means no error
	}{
		{
			name:      "single file is a summary",
			args:      []string{fileA},
			wantFiles: []string{fileA},
		},
		{
			name:      "single nonexistent arg falls back to a file (Run reports the read error)",
			args:      []string{"/no/such/file.go"},
			wantFiles: []string{"/no/such/file.go"},
		},
		{
			name:      "two existing files is a multi-file summary",
			args:      []string{fileA, fileB},
			wantFiles: []string{fileA, fileB},
		},
		{
			name:        "file plus one non-file is a single-symbol slice",
			args:        []string{fileA, "Foo"},
			wantFiles:   []string{fileA},
			wantSymbols: []string{"Foo"},
		},
		{
			name:        "file plus several non-files is a multi-symbol slice, in order",
			args:        []string{fileA, "Run", "Finalize", "Track", "Start", "Finish"},
			wantFiles:   []string{fileA},
			wantSymbols: []string{"Run", "Finalize", "Track", "Start", "Finish"},
		},
		{
			name:    "a real file among the requested symbols is a mixed-args error",
			args:    []string{fileA, "Foo", fileB},
			wantErr: "mixed files and symbols",
		},
		{
			name:      "args[0] not a file, even with a real file following, is not slice mode",
			args:      []string{"/no/such/file.go", fileB},
			wantFiles: []string{"/no/such/file.go", fileB},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			files, symbols, err := classifyArgs(tt.args)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(files, tt.wantFiles) {
				t.Errorf("files = %v, want %v", files, tt.wantFiles)
			}
			if !reflect.DeepEqual(symbols, tt.wantSymbols) {
				t.Errorf("symbols = %v, want %v", symbols, tt.wantSymbols)
			}
		})
	}
}

// TestClassifyArgsMixedErrorIsSelfCorrecting pins the exact error text: the
// house style ("<what>; try: <command>") lets the agent self-correct in the
// same turn instead of burning one on a bare usage error.
func TestClassifyArgsMixedErrorIsSelfCorrecting(t *testing.T) {
	fileA := writeTmp(t, "a.go", "package x\n")
	fileB := writeTmp(t, "b.go", "package x\n")
	_, _, err := classifyArgs([]string{fileA, "Sym", fileB})
	const want = "mixed files and symbols; try: sf code <file...> (summaries) or sf code <file> <symbol...> (bodies)"
	if err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
}
