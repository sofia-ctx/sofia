package code

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTmp(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSliceGoMethodWithDoc(t *testing.T) {
	p := writeTmp(t, "s.go", "package x\n\n// Doc line.\nfunc (s *S) Foo(a int) bool { return a > 0 }\n\nfunc Bar() {}\n")
	var buf bytes.Buffer
	if err := Run(Options{Inputs: []string{p}, Symbol: "S.Foo"}, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "func (s *S) Foo(a int) bool") || !strings.Contains(out, "// Doc line.") {
		t.Errorf("slice missing func body/doc:\n%s", out)
	}
	if strings.Contains(out, "func Bar") {
		t.Errorf("slice leaked another symbol:\n%s", out)
	}
}

func TestSlicePHPMethod(t *testing.T) {
	src := "<?php\nclass C {\n  public function a(): void {}\n  public function b(int $n): bool { return $n > 0; }\n}\n"
	p := writeTmp(t, "c.php", src)
	var buf bytes.Buffer
	if err := Run(Options{Inputs: []string{p}, Symbol: "b"}, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "function b(int $n): bool") {
		t.Errorf("got %q", out)
	}
	if strings.Contains(out, "function a()") {
		t.Errorf("leaked another method:\n%s", out)
	}
}

func TestSlicePHPWholeClass(t *testing.T) {
	src := "<?php\nclass C {\n  public function a(): void {}\n  public function b(int $n): bool { return $n > 0; }\n}\n"
	p := writeTmp(t, "c.php", src)
	var buf bytes.Buffer
	if err := Run(Options{Inputs: []string{p}, Symbol: "C"}, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "class C") || !strings.Contains(out, "function a()") || !strings.Contains(out, "function b(int $n): bool") {
		t.Errorf("whole-class slice missing class or members:\n%s", out)
	}
}

func TestSlicePHPWholeEnum(t *testing.T) {
	src := "<?php\nenum Status: string {\n  case Open = 'open';\n  case Closed = 'closed';\n}\n"
	p := writeTmp(t, "s.php", src)
	var buf bytes.Buffer
	if err := Run(Options{Inputs: []string{p}, Symbol: "Status"}, &buf); err != nil {
		t.Fatal(err)
	}
	if out := buf.String(); !strings.Contains(out, "enum Status") || !strings.Contains(out, "case Open") {
		t.Errorf("whole-enum slice missing enum/cases:\n%s", out)
	}
}

func TestSliceNotFoundListsAvailable(t *testing.T) {
	p := writeTmp(t, "s.go", "package x\nfunc Bar() {}\n")
	var buf bytes.Buffer
	err := Run(Options{Inputs: []string{p}, Symbol: "Nope"}, &buf)
	if err == nil || !strings.Contains(err.Error(), "available: Bar") {
		t.Errorf("want not-found listing available symbols, got %v", err)
	}
}

func TestSliceUnsupportedLang(t *testing.T) {
	p := writeTmp(t, "x.vue", "<template><div/></template>\n")
	var buf bytes.Buffer
	if err := Run(Options{Inputs: []string{p}, Symbol: "Foo"}, &buf); err == nil {
		t.Errorf("slice on .vue should error (Go/PHP only)")
	}
}
