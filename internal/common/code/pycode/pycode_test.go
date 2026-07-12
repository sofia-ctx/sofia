package pycode

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `"""Module docstring.

def not_a_real_function():  # inside a docstring — must be ignored
"""
import os
import sys, json
from typing import List, Dict
from . import helpers as h

MAX_RETRIES = 3
_private_flag: bool = False

def top_level(a, b: int = 2) -> bool:
    def nested():  # a closure — must NOT be captured
        return 1
    return nested()

@decorator
async def fetch(
    url: str,
    timeout: float = 1.0,
) -> Dict[str, int]:
    """multi-line signature + docstring"""
    return {}

class Widget(Base, Mixin):
    """A widget."""
    count = 0

    def __init__(self, name):
        self.name = name

    @property
    def label(self) -> str:
        return self.name

    def _helper(self):
        pass
`

func parse(t *testing.T) *PyFile {
	t.Helper()
	return parsePy([]byte(sample))
}

func funcByFull(p *PyFile, full string) *PyFunc {
	for i := range p.Funcs {
		f := &p.Funcs[i]
		name := f.Name
		if f.Class != "" {
			name = f.Class + "." + f.Name
		}
		if name == full {
			return f
		}
	}
	return nil
}

func TestParseStructure(t *testing.T) {
	p := parse(t)

	// Imports (comma-free, module-qualified for from-imports).
	for _, want := range []string{"os", "sys", "json", "typing:List", "typing:Dict"} {
		if !contains(p.Imports, want) {
			t.Errorf("imports missing %q; got %v", want, p.Imports)
		}
	}

	// One top-level class with its bases.
	if len(p.Classes) != 1 || p.Classes[0].Name != "Widget" || p.Classes[0].Bases != "Base, Mixin" {
		t.Fatalf("classes = %+v, want one Widget(Base, Mixin)", p.Classes)
	}

	// A closure and a def buried in the module docstring must not appear.
	if funcByFull(p, "top_level.nested") != nil || funcByFull(p, "nested") != nil {
		t.Error("nested closure was captured as a function")
	}
	for _, f := range p.Funcs {
		if f.Name == "not_a_real_function" {
			t.Error("captured a def from inside the module docstring")
		}
	}

	// Signatures: multi-line collapsed, async marked, return annotations kept.
	if f := funcByFull(p, "top_level"); f == nil || f.Sig != "(a, b: int = 2) -> bool" {
		t.Errorf("top_level sig = %q", sigOf(f))
	}
	if f := funcByFull(p, "fetch"); f == nil || f.Sig != "async (url: str, timeout: float = 1.0) -> Dict[str, int]" {
		t.Errorf("fetch sig = %q", sigOf(f))
	}
	// Methods attribute to their class.
	if f := funcByFull(p, "Widget.label"); f == nil || f.Class != "Widget" || f.Sig != "(self) -> str" {
		t.Errorf("Widget.label = %+v", f)
	}
	if funcByFull(p, "Widget.__init__") == nil {
		t.Error("Widget.__init__ not captured")
	}

	// Module-level assignments; a class attribute (count) is not module-level.
	if !hasVar(p, "MAX_RETRIES") || !hasVar(p, "_private_flag") {
		t.Errorf("module vars = %+v", p.Vars)
	}
	if hasVar(p, "count") {
		t.Error("class attribute 'count' leaked into module vars")
	}
}

func TestFilterExported(t *testing.T) {
	p := parse(t)
	p.FilterExported()
	if funcByFull(p, "Widget._helper") != nil {
		t.Error("_helper should be filtered out")
	}
	if funcByFull(p, "Widget.__init__") == nil {
		t.Error("dunder __init__ should survive the exported filter (it's API)")
	}
	if hasVar(p, "_private_flag") {
		t.Error("_private_flag should be filtered out")
	}
	if !hasVar(p, "MAX_RETRIES") {
		t.Error("MAX_RETRIES should survive")
	}
}

func TestBriefDropsVarTypes(t *testing.T) {
	p := parse(t)
	p.Brief()
	for _, v := range p.Vars {
		if v.Type != "" {
			t.Errorf("Brief should clear var types; %s kept %q", v.Name, v.Type)
		}
	}
}

func TestSliceMethod(t *testing.T) {
	got, _, err := Slice([]byte(sample), "Widget.label")
	if err != nil {
		t.Fatalf("Slice: %v", err)
	}
	if !strings.Contains(got, "def label(self) -> str:") ||
		!strings.Contains(got, "@property") ||
		!strings.Contains(got, "return self.name") {
		t.Errorf("slice missing expected lines:\n%s", got)
	}
	if strings.Contains(got, "_helper") || strings.Contains(got, "__init__") {
		t.Errorf("slice bled into neighbouring methods:\n%s", got)
	}
}

func TestSliceBareAndNotFound(t *testing.T) {
	if got, _, err := Slice([]byte(sample), "top_level"); err != nil || !strings.HasPrefix(got, "def top_level") {
		t.Errorf("bare slice top_level: %q err %v", got, err)
	}
	_, names, err := Slice([]byte(sample), "nope")
	if err == nil {
		t.Fatal("expected not-found error")
	}
	if !contains(names, "Widget.label") || !contains(names, "top_level") {
		t.Errorf("available names should list symbols; got %v", names)
	}
}

func TestSummarizeFormats(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.py")
	if err := os.WriteFile(path, []byte(sample), 0o644); err != nil {
		t.Fatal(err)
	}

	var toon bytes.Buffer
	m, err := Summarize(&toon, path, "toon", false, false, false)
	if err != nil {
		t.Fatalf("Summarize toon: %v", err)
	}
	if m["lang"] != "python" || m["classes"].(int) != 1 {
		t.Errorf("summary map = %+v", m)
	}
	for _, want := range []string{"module: sample", "classes[1]{name,bases}:", "funcs[", "Widget"} {
		if !strings.Contains(toon.String(), want) {
			t.Errorf("toon output missing %q:\n%s", want, toon.String())
		}
	}

	var js bytes.Buffer
	if _, err := Summarize(&js, path, "json", false, false, false); err != nil {
		t.Fatalf("Summarize json: %v", err)
	}
	var pf PyFile
	if err := json.Unmarshal(js.Bytes(), &pf); err != nil {
		t.Fatalf("json not valid: %v", err)
	}
	if pf.Module != "sample" || len(pf.Classes) != 1 {
		t.Errorf("decoded json = %+v", pf)
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

func hasVar(p *PyFile, name string) bool {
	for _, v := range p.Vars {
		if v.Name == name {
			return true
		}
	}
	return false
}

func sigOf(f *PyFunc) string {
	if f == nil {
		return "<nil>"
	}
	return f.Sig
}
