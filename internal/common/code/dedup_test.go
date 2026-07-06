package code

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/sofia-ctx/sofia/internal/calllog"
)

// dedupEnv turns dedup on deterministically for a test: an explicit session
// id — overriding any CLAUDE_CODE_SESSION_ID inherited from the outer Claude
// Code session running `go test` — a private call-log dir, and an explicit
// window (SOFIA_DEDUP_WINDOW left unset would otherwise disable dedup
// entirely under test; see internal/dedup.beginDisabled).
func dedupEnv(t *testing.T, sid string) {
	t.Helper()
	t.Setenv("SOFIA_LOG_DIR", t.TempDir())
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("SOFIA_SESSION_ID", sid)
	t.Setenv("SOFIA_DEDUP_WINDOW", "180")
}

func TestCodeStubOnRepeat(t *testing.T) {
	dedupEnv(t, "sid-stub")
	p := writeTmp(t, "big.go", bigGoSrc())

	var first bytes.Buffer
	if err := Run(Options{Inputs: []string{p}, Format: "toon"}, &first); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if strings.Contains(first.String(), "already returned") {
		t.Fatalf("first call must not be stubbed:\n%s", first.String())
	}

	var second bytes.Buffer
	if err := Run(Options{Inputs: []string{p}, Format: "toon"}, &second); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	out := second.String()
	if !strings.Contains(out, "# sf: already returned in call #1") {
		t.Fatalf("second identical call must be stubbed, got:\n%s", out)
	}
	if strings.Contains(out, "package big") {
		t.Errorf("stub must not carry the original content:\n%s", out)
	}
	if !strings.Contains(out, "saved") {
		t.Errorf("footer after a stub should still advertise the savings:\n%s", out)
	}
}

func TestCodeForceBypassesStub(t *testing.T) {
	dedupEnv(t, "sid-force")
	p := writeTmp(t, "big.go", bigGoSrc())

	if err := Run(Options{Inputs: []string{p}, Format: "toon"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	var buf bytes.Buffer
	if err := Run(Options{Inputs: []string{p}, Format: "toon", Force: true}, &buf); err != nil {
		t.Fatalf("forced Run: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "already returned") {
		t.Fatalf("--force must bypass the stub:\n%s", out)
	}
	if !strings.Contains(out, "Filler0") {
		t.Errorf("forced call must return full content:\n%s", out)
	}
}

func TestCodeEditedFileNotStubbed(t *testing.T) {
	dedupEnv(t, "sid-edit")
	p := writeTmp(t, "big.go", bigGoSrc())

	if err := Run(Options{Inputs: []string{p}, Format: "toon"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	// Rewrite the file: size changes, so the dedup key changes too — a
	// re-read after an edit is a legitimately new call and must go through
	// in full, not get stubbed.
	if err := os.WriteFile(p, []byte(bigGoSrc()+"\n// trailing edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := Run(Options{Inputs: []string{p}, Format: "toon"}, &buf); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if strings.Contains(buf.String(), "already returned") {
		t.Fatalf("an edited file must not be stubbed:\n%s", buf.String())
	}
}

func TestCodeSupersetArgsNotStubbed(t *testing.T) {
	dedupEnv(t, "sid-superset")
	a := writeTmp(t, "a.go", "package a\n\nfunc A() {}\n")
	b := writeTmp(t, "b.go", "package b\n\nfunc B() {}\n")

	if err := Run(Options{Inputs: []string{a}, Format: "toon"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	var buf bytes.Buffer
	if err := Run(Options{Inputs: []string{a, b}, Format: "toon"}, &buf); err != nil {
		t.Fatalf("superset Run: %v", err)
	}
	if strings.Contains(buf.String(), "already returned") {
		t.Fatalf("a superset of files is a different call and must not be stubbed:\n%s", buf.String())
	}
}

func TestCodeJSONStubShape(t *testing.T) {
	dedupEnv(t, "sid-json")
	p := writeTmp(t, "big.go", bigGoSrc())

	if err := Run(Options{Inputs: []string{p}, Format: "json"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	var buf bytes.Buffer
	if err := Run(Options{Inputs: []string{p}, Format: "json"}, &buf); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) == 0 {
		t.Fatal("empty stub output")
	}
	var v struct {
		Dedup     bool   `json:"dedup"`
		DupOfCall int    `json:"dup_of_call"`
		Age       string `json:"age"`
		Hint      string `json:"hint"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &v); err != nil {
		t.Fatalf("stub json line does not decode: %v\nline: %s", err, lines[0])
	}
	if !v.Dedup || v.DupOfCall != 1 {
		t.Errorf("decoded = %+v, want dedup=true dup_of_call=1", v)
	}
	if !strings.Contains(v.Hint, "--force") || !strings.Contains(v.Hint, "force:true") {
		t.Errorf("hint must mention both --force and force:true, got %q", v.Hint)
	}
}

func TestCodeNoSessionNoStub(t *testing.T) {
	t.Setenv("SOFIA_LOG_DIR", t.TempDir())
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("SOFIA_SESSION_ID", "")
	t.Setenv("SOFIA_DEDUP_WINDOW", "180")

	p := writeTmp(t, "big.go", bigGoSrc())
	if err := Run(Options{Inputs: []string{p}, Format: "toon"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	var buf bytes.Buffer
	if err := Run(Options{Inputs: []string{p}, Format: "toon"}, &buf); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if strings.Contains(buf.String(), "already returned") {
		t.Fatalf("without a session, dedup must never stub:\n%s", buf.String())
	}
}

func TestCodeSliceErrorNotRecorded(t *testing.T) {
	dedupEnv(t, "sid-sliceerr")
	structuralOnly(t)
	p := writeTmp(t, "a.go", "package a\n\nfunc A() {}\n")

	if err := Run(Options{Inputs: []string{p}, Symbols: []string{"NoSuchSymbol"}}, &bytes.Buffer{}); err == nil {
		t.Fatal("expected an error for a missing symbol")
	}
	var buf bytes.Buffer
	if err := Run(Options{Inputs: []string{p}, Symbols: []string{"NoSuchSymbol"}}, &buf); err == nil {
		t.Fatal("expected an error again")
	}
	if strings.Contains(buf.String(), "already returned") {
		t.Fatalf("a failed call must never be dedup-stubbed on retry:\n%s", buf.String())
	}
}

func TestCodeStubCalllogSummary(t *testing.T) {
	dedupEnv(t, "sid-summary")
	p := writeTmp(t, "big.go", bigGoSrc())

	if err := Run(Options{Inputs: []string{p}, Format: "toon"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if err := Run(Options{Inputs: []string{p}, Format: "toon"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("second Run: %v", err)
	}

	entries, err := calllog.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("expected call log entries")
	}
	last := entries[len(entries)-1]
	if last.Summary["dedup"] != true {
		t.Errorf("last entry summary = %+v, want dedup=true", last.Summary)
	}
	// JSON round-trips numbers as float64.
	dupOf, ok := last.Summary["dup_of"].(float64)
	if !ok || dupOf != 1 {
		t.Errorf("dup_of = %v, want 1", last.Summary["dup_of"])
	}
}

func TestForceFlagRegistered(t *testing.T) {
	if NewCommand().Flags().Lookup("force") == nil {
		t.Fatal("expected --force flag to be registered")
	}
}
