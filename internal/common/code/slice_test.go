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

// structuralOnly pins the raw passthrough off (SOFIA_CODE_RAW_BELOW=0) for
// tests that exercise the summariser on deliberately tiny fixtures —
// with the default threshold those files would come back raw before the
// machinery under test ever ran. The passthrough itself is covered in
// passthrough_test.go.
func structuralOnly(t *testing.T) {
	t.Helper()
	t.Setenv("SOFIA_CODE_RAW_BELOW", "0")
}

// --- single-symbol regression: Symbols with exactly one element must behave
// exactly as the old singular Symbol field did (no header comment, same
// not-found error) --------------------------------------------------------

func TestSliceGoMethodWithDoc(t *testing.T) {
	structuralOnly(t)
	p := writeTmp(t, "s.go", "package x\n\n// Doc line.\nfunc (s *S) Foo(a int) bool { return a > 0 }\n\nfunc Bar() {}\n")
	var buf bytes.Buffer
	if err := Run(Options{Inputs: []string{p}, Symbols: []string{"S.Foo"}}, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "func (s *S) Foo(a int) bool") || !strings.Contains(out, "// Doc line.") {
		t.Errorf("slice missing func body/doc:\n%s", out)
	}
	if strings.Contains(out, "func Bar") {
		t.Errorf("slice leaked another symbol:\n%s", out)
	}
	if strings.Contains(out, "---") {
		t.Errorf("single-symbol slice must not grow a multi-symbol header:\n%s", out)
	}
}

func TestSlicePHPMethod(t *testing.T) {
	structuralOnly(t)
	src := "<?php\nclass C {\n  public function a(): void {}\n  public function b(int $n): bool { return $n > 0; }\n}\n"
	p := writeTmp(t, "c.php", src)
	var buf bytes.Buffer
	if err := Run(Options{Inputs: []string{p}, Symbols: []string{"b"}}, &buf); err != nil {
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
	structuralOnly(t)
	src := "<?php\nclass C {\n  public function a(): void {}\n  public function b(int $n): bool { return $n > 0; }\n}\n"
	p := writeTmp(t, "c.php", src)
	var buf bytes.Buffer
	if err := Run(Options{Inputs: []string{p}, Symbols: []string{"C"}}, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "class C") || !strings.Contains(out, "function a()") || !strings.Contains(out, "function b(int $n): bool") {
		t.Errorf("whole-class slice missing class or members:\n%s", out)
	}
}

func TestSlicePHPWholeEnum(t *testing.T) {
	structuralOnly(t)
	src := "<?php\nenum Status: string {\n  case Open = 'open';\n  case Closed = 'closed';\n}\n"
	p := writeTmp(t, "s.php", src)
	var buf bytes.Buffer
	if err := Run(Options{Inputs: []string{p}, Symbols: []string{"Status"}}, &buf); err != nil {
		t.Fatal(err)
	}
	if out := buf.String(); !strings.Contains(out, "enum Status") || !strings.Contains(out, "case Open") {
		t.Errorf("whole-enum slice missing enum/cases:\n%s", out)
	}
}

func TestSliceNotFoundListsAvailable(t *testing.T) {
	structuralOnly(t)
	p := writeTmp(t, "s.go", "package x\nfunc Bar() {}\n")
	var buf bytes.Buffer
	err := Run(Options{Inputs: []string{p}, Symbols: []string{"Nope"}}, &buf)
	if err == nil || !strings.Contains(err.Error(), "available: Bar") {
		t.Errorf("want not-found listing available symbols, got %v", err)
	}
}

func TestSliceVueWithoutScript(t *testing.T) {
	p := writeTmp(t, "x.vue", "<template><div/></template>\n")
	for _, tt := range []struct {
		name    string
		symbols []string
	}{
		{"single symbol", []string{"Foo"}},
		{"multiple symbols", []string{"Foo", "Bar"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := Run(Options{Inputs: []string{p}, Symbols: tt.symbols}, &buf)
			if err == nil {
				t.Fatal("slice on .vue should error")
			}
			if !strings.Contains(err.Error(), "not found") && !strings.Contains(err.Error(), "none of the requested") {
				t.Errorf("want a missing-symbol error, got %v", err)
			}
		})
	}
}

func TestSmallScriptSymbolsUseSlices(t *testing.T) {
	t.Setenv("SOFIA_CODE_RAW_BELOW", "")
	t.Setenv("SOFIA_FOOTER", "off")
	for _, ext := range []string{"ts", "tsx", "mts", "cts", "js", "jsx", "mjs", "cjs", "vue"} {
		t.Run(ext, func(t *testing.T) {
			src := "function first() { return 1; }\nconst second = () => 2;\nfunction unrelated() { return 'Do not include this unrequested body in the result'; }\n"
			if ext == "vue" {
				src = "<script setup lang=\"ts\">\n" + src + "</script><template><div/></template>"
			}
			p := writeTmp(t, "small."+ext, src)
			var output bytes.Buffer
			if err := Run(Options{Inputs: []string{p}, Symbols: []string{"second", "first"}}, &output); err != nil {
				t.Fatal(err)
			}
			got := output.String()
			if strings.Contains(got, "unrelated") || !strings.Contains(got, "return 1;") || strings.Index(got, "const second") > strings.Index(got, "function first") {
				t.Fatalf("bad slice: %s", got)
			}
		})
	}
}

func TestSmallPHPMultipleMethods(t *testing.T) {
	t.Setenv("SOFIA_CODE_RAW_BELOW", "")
	src := `<?php
class Prices {
    public function get(int $id): int { /* Discounted total. */ return $id * $this->getDiscount(); }
    public function getDiscount(): int { return 2; }
    public function unrelated(): string { return 'unrelated data and logic that must not appear in a method slice'; }
    public function another(): string { return 'another unrelated method with enough body to keep the requested slice cheaper'; }
}
`
	p := writeTmp(t, "prices.php", src)
	var buf bytes.Buffer
	if err := Run(Options{Inputs: []string{p}, Symbols: []string{"get", "getDiscount"}}, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"Discounted total.", "function get(int $id): int", "return $id * $this->getDiscount();", "function getDiscount(): int", "return 2;"} {
		if !strings.Contains(out, want) {
			t.Errorf("requested PHP source missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "function unrelated") || strings.Contains(out, "function another") || strings.Contains(out, "# raw:") {
		t.Errorf("small-file slice returned unrelated source:\n%s", out)
	}
}

// --- multi-symbol slicing ---------------------------------------------

// multiGoSrc has real (multi-line) bodies, not one-liners: a slice of one or
// two of its funcs is meaningfully smaller than the raw file even after a
// not-found comment's overhead, so the compact-or-raw invariant doesn't
// interfere with asserting on partial-match content (that invariant gets its
// own dedicated, deliberately-tiny fixture in TestSliceMultiSymbolCompactOrRaw).
const multiGoSrc = `package x

// Foo sums the squares from 0 to n.
func Foo(n int) int {
	total := 0
	for i := 0; i < n; i++ {
		total += i * i
	}
	return total
}

// Bar joins the runes of s with a dash.
func Bar(s string) string {
	out := ""
	for _, r := range s {
		out += string(r) + "-"
	}
	return out
}

// Baz is unrelated padding so the raw file stays meaningfully bigger than
// any one or two of its slices.
func Baz() string {
	return "baz"
}
`

const multiPHPSrc = `<?php
class C {
  public function a(): void {}
  public function b(int $n): bool { return $n > 0; }
  public function c(): string { return 'z'; }
}
`

func TestSliceMultiSymbolAllFound(t *testing.T) {
	structuralOnly(t)
	for _, tt := range []struct {
		name    string
		file    string
		src     string
		symbols []string
		want    []string // must all appear, in this order
		exclude string   // must not appear
	}{
		{
			name:    "go",
			file:    "s.go",
			src:     multiGoSrc,
			symbols: []string{"Foo", "Bar"},
			want:    []string{"--- Foo ---", "func Foo(n int) int {", "--- Bar ---", "func Bar(s string) string {"},
			exclude: "Baz",
		},
		{
			name:    "php",
			file:    "c.php",
			src:     multiPHPSrc,
			symbols: []string{"a", "b"},
			want:    []string{"--- a ---", "function a(): void {}", "--- b ---", "function b(int $n): bool"},
			exclude: "function c()",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := writeTmp(t, tt.file, tt.src)
			var buf bytes.Buffer
			if err := Run(Options{Inputs: []string{p}, Symbols: tt.symbols}, &buf); err != nil {
				t.Fatalf("Run: %v", err)
			}
			out := buf.String()
			last := -1
			for _, w := range tt.want {
				idx := strings.Index(out, w)
				if idx < 0 {
					t.Fatalf("missing %q in:\n%s", w, out)
				}
				if idx < last {
					t.Errorf("%q out of input order in:\n%s", w, out)
				}
				last = idx
			}
			if strings.Contains(out, tt.exclude) {
				t.Errorf("leaked unrequested symbol %q in:\n%s", tt.exclude, out)
			}
			if strings.Contains(out, "not found") {
				t.Errorf("unexpected not-found marker when every symbol matched:\n%s", out)
			}
		})
	}
}

func TestSliceMultiSymbolPartialFound(t *testing.T) {
	structuralOnly(t)
	p := writeTmp(t, "s.go", multiGoSrc)
	var buf bytes.Buffer
	err := Run(Options{Inputs: []string{p}, Symbols: []string{"Foo", "NoSuchSymbol"}}, &buf)
	if err != nil {
		t.Fatalf("partial success must exit 0, got err: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "func Foo(n int) int {") {
		t.Errorf("found symbol missing from output:\n%s", out)
	}
	if !strings.Contains(out, "NoSuchSymbol: not found") {
		t.Errorf("missing symbol should get a marked comment:\n%s", out)
	}
	if !strings.Contains(out, "available:") || !strings.Contains(out, "Bar") {
		t.Errorf("missing-symbol comment should suggest available names:\n%s", out)
	}
}

func TestSliceMultiSymbolNoneFound(t *testing.T) {
	structuralOnly(t)
	p := writeTmp(t, "s.go", multiGoSrc)
	var buf bytes.Buffer
	err := Run(Options{Inputs: []string{p}, Symbols: []string{"Nope1", "Nope2"}}, &buf)
	if err == nil {
		t.Fatal("expected an error when none of the requested symbols exist")
	}
	if !strings.Contains(err.Error(), "Nope1") || !strings.Contains(err.Error(), "Nope2") {
		t.Errorf("error should name the requested symbols, got %v", err)
	}
	if !strings.Contains(err.Error(), "available:") || !strings.Contains(err.Error(), "Foo") {
		t.Errorf("error should suggest available names, got %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("a hard failure must not write partial output, got %q", buf.String())
	}
}

// TestSliceMultiSymbolCompactOrRaw exercises the compact-or-raw invariant on
// the COMBINED multi-symbol output: on a small enough file, the per-symbol
// header comments push the sliced text past the size of the raw file, so the
// invariant must fall back to the raw file rather than "save" negative tokens.
func TestSliceMultiSymbolCompactOrRaw(t *testing.T) {
	structuralOnly(t)
	t.Setenv("SOFIA_FOOTER", "off") // byte-exact comparison below
	src := "package x\n\nfunc A() {}\nfunc B() {}\n"
	p := writeTmp(t, "tiny.go", src)
	var buf bytes.Buffer
	if err := Run(Options{Inputs: []string{p}, Symbols: []string{"A", "B"}}, &buf); err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := buf.String()
	if out != src {
		t.Errorf("compact-or-raw should have preferred the smaller raw file:\ngot:\n%s\nwant (raw):\n%s", out, src)
	}
	if strings.Contains(out, "---") {
		t.Errorf("raw fallback should not carry multi-symbol header markers:\n%s", out)
	}
}
