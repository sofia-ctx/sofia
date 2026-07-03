package code

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// smallGoSrc is comfortably below any realistic passthrough threshold.
const smallGoSrc = `package tiny

// Hello greets.
func Hello() string { return "hi" }

func Goodbye() string { return "bye" }
`

// bigGoSrc builds a syntactically valid Go file just past the default
// passthrough threshold, whose structural summary is still far smaller than
// the raw text (real bodies), so compact-or-raw keeps the summary.
func bigGoSrc() string {
	var b strings.Builder
	b.WriteString("package big\n\n")
	for i := 0; b.Len() < defaultRawBelow+512; i++ {
		fmt.Fprintf(&b, "// Filler%d pads the file with a real body.\nfunc Filler%d() int {\n\tx := %d\n\ty := x * 3\n\tz := y - x\n\treturn x + y + z\n}\n\n", i, i, i)
	}
	return b.String()
}

// TestPassthroughSummary pins the H-A invariant in single-file summary mode:
// below the threshold `sf code` returns the raw file behind a one-line
// marker header instead of a structure — never worse than cat.
func TestPassthroughSummary(t *testing.T) {
	p := writeTmp(t, "tiny.go", smallGoSrc)
	var buf bytes.Buffer
	if err := Run(Options{Inputs: []string{p}, Format: "toon"}, &buf); err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "# raw: "+p+" (") {
		t.Errorf("missing passthrough header for %s in:\n%s", p, out)
	}
	if !strings.Contains(out, "full file is cheaper than structure") {
		t.Errorf("header should say why the file is raw:\n%s", out)
	}
	if !strings.Contains(out, smallGoSrc) {
		t.Errorf("passthrough must carry the complete raw file:\n%s", out)
	}
	if strings.Contains(out, "funcs[") {
		t.Errorf("a below-threshold file must not be summarised:\n%s", out)
	}
}

// TestPassthroughLargeFileStructural: above the threshold nothing changes —
// the structural summary (and the compact-or-raw guard) still apply.
func TestPassthroughLargeFileStructural(t *testing.T) {
	p := writeTmp(t, "big.go", bigGoSrc())
	var buf bytes.Buffer
	if err := Run(Options{Inputs: []string{p}, Format: "toon"}, &buf); err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "# raw:") {
		t.Errorf("an above-threshold file must not be passed through raw:\n%s", out[:200])
	}
	if !strings.Contains(out, "funcs[") {
		t.Errorf("expected a structural summary:\n%s", out[:200])
	}
}

// TestPassthroughMixedBatch: in a multi-file call each file decides for
// itself — small ones come back raw inline, large ones structural, in input
// order.
func TestPassthroughMixedBatch(t *testing.T) {
	small := writeTmp(t, "tiny.go", smallGoSrc)
	big := writeTmp(t, "big.go", bigGoSrc())
	var buf bytes.Buffer
	if err := Run(Options{Inputs: []string{small, big}, Format: "toon"}, &buf); err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := buf.String()
	rawIdx := strings.Index(out, "# raw: "+small)
	bigIdx := strings.Index(out, "file: big.go")
	if rawIdx < 0 {
		t.Fatalf("small file should be raw inline:\n%s", out[:300])
	}
	if bigIdx < 0 {
		t.Fatalf("large file should stay structural:\n%s", out)
	}
	if rawIdx > bigIdx {
		t.Errorf("blocks out of input order (raw at %d, structural at %d)", rawIdx, bigIdx)
	}
	if strings.Count(out, "# raw:") != 1 {
		t.Errorf("exactly one passthrough block expected:\n%s", out)
	}
}

// TestPassthroughSlice: slicing symbols out of a below-threshold file is
// pure ceremony — the whole raw file comes back, the header naming the
// requested symbols as included in full.
func TestPassthroughSlice(t *testing.T) {
	p := writeTmp(t, "tiny.go", smallGoSrc)
	var buf bytes.Buffer
	if err := Run(Options{Inputs: []string{p}, Symbols: []string{"Hello", "Goodbye"}}, &buf); err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "# raw: "+p+" (") {
		t.Errorf("missing passthrough header:\n%s", out)
	}
	if !strings.Contains(out, "full file (includes Hello, Goodbye)") {
		t.Errorf("header should name the requested symbols:\n%s", out)
	}
	if !strings.Contains(out, smallGoSrc) {
		t.Errorf("passthrough must carry the complete raw file:\n%s", out)
	}
}

// TestPassthroughEnvOverride covers SOFIA_CODE_RAW_BELOW: 0 disables the
// passthrough entirely, any other value moves the threshold.
func TestPassthroughEnvOverride(t *testing.T) {
	for _, tt := range []struct {
		name    string
		env     string
		src     string
		wantRaw bool
	}{
		{"0 disables passthrough", "0", multiGoSrc, false},
		{"threshold above the file size passes through", "4096", multiGoSrc, true},
		{"threshold below the file size stays structural", "64", multiGoSrc, false},
		{"invalid value falls back to the default (small file → raw)", "many", smallGoSrc, true},
		{"negative value falls back to the default (small file → raw)", "-1", smallGoSrc, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SOFIA_CODE_RAW_BELOW", tt.env)
			p := writeTmp(t, "f.go", tt.src)
			var buf bytes.Buffer
			if err := Run(Options{Inputs: []string{p}, Format: "toon"}, &buf); err != nil {
				t.Fatalf("Run: %v", err)
			}
			out := buf.String()
			if got := strings.Contains(out, "# raw:"); got != tt.wantRaw {
				t.Errorf("passthrough = %v, want %v; output:\n%s", got, tt.wantRaw, out)
			}
			if !tt.wantRaw && !strings.Contains(out, "funcs[") {
				t.Errorf("expected a structural summary:\n%s", out)
			}
		})
	}
}

// TestPassthroughJSON: in --format json the passthrough is a real JSON
// object with an explicit raw marker, not a bare content dump.
func TestPassthroughJSON(t *testing.T) {
	p := writeTmp(t, "tiny.go", smallGoSrc)
	var buf bytes.Buffer
	if err := Run(Options{Inputs: []string{p}, Format: "json"}, &buf); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var v struct {
		File    string `json:"file"`
		Raw     bool   `json:"raw"`
		Note    string `json:"note"`
		Content string `json:"content"`
	}
	// Decode the first JSON value only; trailing non-JSON lines (e.g. a cost
	// footer) are not this test's concern.
	if err := json.NewDecoder(&buf).Decode(&v); err != nil {
		t.Fatalf("passthrough JSON does not decode: %v", err)
	}
	if !v.Raw {
		t.Error("raw marker missing from JSON passthrough")
	}
	if v.File != p {
		t.Errorf("file = %q, want %q", v.File, p)
	}
	if v.Content != smallGoSrc {
		t.Errorf("content must be the exact raw file, got:\n%s", v.Content)
	}
	if !strings.Contains(v.Note, "full file is cheaper than structure") {
		t.Errorf("note should carry the header text, got %q", v.Note)
	}
}
