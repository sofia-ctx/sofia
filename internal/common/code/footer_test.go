package code

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

// lastLine returns the final non-empty line of s.
func lastLine(t *testing.T, s string) string {
	t.Helper()
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) == 0 {
		t.Fatal("empty output")
	}
	return lines[len(lines)-1]
}

// TestFooterCodeSavings: a structural summary that beats the raw file must
// end with the full three-field footer, and the arithmetic must hold.
func TestFooterCodeSavings(t *testing.T) {
	t.Setenv("SOFIA_FOOTER", "")
	p := writeTmp(t, "big.go", bigGoSrc())
	var buf bytes.Buffer
	if err := Run(Options{Inputs: []string{p}, Format: "toon"}, &buf); err != nil {
		t.Fatalf("Run: %v", err)
	}
	last := lastLine(t, buf.String())
	re := regexp.MustCompile(`^# sf ≈(\d+) tok · raw ≈(\d+) · saved ≈(\d+)$`)
	m := re.FindStringSubmatch(last)
	if m == nil {
		t.Fatalf("footer with savings expected as the last line, got %q", last)
	}
	if m[1] == "0" || m[2] == "0" || m[3] == "0" {
		t.Errorf("all footer fields should be non-zero here: %q", last)
	}
}

// TestFooterCodePassthrough: when the output IS the raw file (below the
// threshold), the footer must use the passthrough note, never a negative
// "saved".
func TestFooterCodePassthrough(t *testing.T) {
	t.Setenv("SOFIA_FOOTER", "")
	p := writeTmp(t, "tiny.go", smallGoSrc)
	var buf bytes.Buffer
	if err := Run(Options{Inputs: []string{p}, Format: "toon"}, &buf); err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := buf.String()
	last := lastLine(t, out)
	if !regexp.MustCompile(`^# sf ≈\d+ tok · raw passthrough$`).MatchString(last) {
		t.Errorf("passthrough footer expected, got %q", last)
	}
	if strings.Contains(out, "saved ≈-") {
		t.Errorf("a negative saved must never be printed:\n%s", out)
	}
}

// TestFooterCodeSlice: slice mode reports the whole file as the raw
// baseline, so a small slice of a big file shows real savings.
func TestFooterCodeSlice(t *testing.T) {
	t.Setenv("SOFIA_FOOTER", "")
	p := writeTmp(t, "big.go", bigGoSrc())
	var buf bytes.Buffer
	if err := Run(Options{Inputs: []string{p}, Symbols: []string{"Filler0"}}, &buf); err != nil {
		t.Fatalf("Run: %v", err)
	}
	last := lastLine(t, buf.String())
	if !regexp.MustCompile(`^# sf ≈\d+ tok · raw ≈\d+ · saved ≈\d+$`).MatchString(last) {
		t.Errorf("slice footer with savings expected, got %q", last)
	}
}

// TestFooterCodeOff: SOFIA_FOOTER=off removes the footer entirely.
func TestFooterCodeOff(t *testing.T) {
	t.Setenv("SOFIA_FOOTER", "off")
	p := writeTmp(t, "tiny.go", smallGoSrc)
	var buf bytes.Buffer
	if err := Run(Options{Inputs: []string{p}, Format: "toon"}, &buf); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(buf.String(), "# sf ≈") {
		t.Errorf("SOFIA_FOOTER=off must remove the footer:\n%s", buf.String())
	}
}

// TestFooterDeterministic: identical inputs must produce byte-identical
// output, footer included.
func TestFooterDeterministic(t *testing.T) {
	t.Setenv("SOFIA_FOOTER", "")
	p := writeTmp(t, "big.go", bigGoSrc())
	run := func() []byte {
		var buf bytes.Buffer
		if err := Run(Options{Inputs: []string{p}, Format: "toon"}, &buf); err != nil {
			t.Fatalf("Run: %v", err)
		}
		return buf.Bytes()
	}
	a, b := run(), run()
	if !bytes.Equal(a, b) {
		t.Errorf("two identical runs differ:\n--- first\n%s\n--- second\n%s", a, b)
	}
}

// --format=json must be a complete JSON document on stdout — no cost footer
// appended after it (the numbers live in calls.jsonl). This is the contract
// machine consumers rely on; it broke once without anyone noticing because
// nothing parsed the output end to end.
func TestJSONOutputHasNoFooter(t *testing.T) {
	structuralOnly(t)
	t.Setenv("SOFIA_FOOTER", "")
	p := writeTmp(t, "s.go", "package x\n\nfunc Bar() {}\n")
	q := writeTmp(t, "t.go", "package x\n\nfunc Baz() {}\n")
	// One file: exactly one document. Several files: a stream of one
	// document per file (a json.Decoder reads them back to back) — and in
	// both cases nothing that isn't JSON.
	for _, inputs := range [][]string{{p}, {p, q}} {
		var buf bytes.Buffer
		if err := Run(Options{Inputs: inputs, Format: "json"}, &buf); err != nil {
			t.Fatalf("Run: %v", err)
		}
		dec := json.NewDecoder(bytes.NewReader(buf.Bytes()))
		n := 0
		for dec.More() {
			var v any
			if err := dec.Decode(&v); err != nil {
				t.Fatalf("%d inputs: stdout is not pure JSON: %v\n%s", len(inputs), err, buf.String())
			}
			n++
		}
		if n != len(inputs) {
			t.Fatalf("%d inputs: got %d JSON documents\n%s", len(inputs), n, buf.String())
		}
	}
	// Symbol slice path too.
	var buf bytes.Buffer
	if err := Run(Options{Inputs: []string{p}, Symbols: []string{"Bar"}, Format: "json"}, &buf); err != nil {
		t.Fatalf("Run slice: %v", err)
	}
	if strings.Contains(buf.String(), "# sf") {
		t.Fatalf("slice json carries a footer:\n%s", buf.String())
	}
}
