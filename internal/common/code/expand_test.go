package code

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeIn writes content at root/rel, creating any intermediate directories,
// and returns the absolute path.
func writeIn(t *testing.T, root, rel, content string) string {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestDirInputExpands: a directory input expands recursively to every
// supported file under it, skipping vendor/ (the default ignore dirs `sf
// grep` uses) and unsupported extensions.
func TestDirInputExpands(t *testing.T) {
	structuralOnly(t)
	dir := t.TempDir()
	writeIn(t, dir, "a.go", "package a\n\nfunc Hello() string { return \"hi\" }\n")
	writeIn(t, dir, "sub/b.php", samplePHP)
	writeIn(t, dir, "notes.txt", "not code\n")
	writeIn(t, dir, "vendor/skip.go", "package skip\n\nfunc ShouldNotAppear() {}\n")

	var buf bytes.Buffer
	if err := Run(Options{Inputs: []string{dir}, Format: "toon"}, &buf); err != nil {
		t.Fatalf("Run dir: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Hello") {
		t.Errorf("missing go file's content:\n%s", out)
	}
	if !strings.Contains(out, "ApproveUserController") {
		t.Errorf("missing php file's content:\n%s", out)
	}
	if strings.Contains(out, "ShouldNotAppear") {
		t.Errorf("vendor/ must be skipped:\n%s", out)
	}
}

// TestGlobInput: a glob pattern expands via filepath.Glob; matched files are
// filtered to supported extensions.
func TestGlobInput(t *testing.T) {
	structuralOnly(t)
	dir := t.TempDir()
	writeIn(t, dir, "a.go", "package a\n\nfunc Alpha() {}\n")
	writeIn(t, dir, "b.go", "package a\n\nfunc Beta() {}\n")
	writeIn(t, dir, "c.txt", "not code\n")

	var buf bytes.Buffer
	pattern := filepath.Join(dir, "*.go")
	if err := Run(Options{Inputs: []string{pattern}, Format: "toon"}, &buf); err != nil {
		t.Fatalf("Run glob: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Alpha") || !strings.Contains(out, "Beta") {
		t.Errorf("glob should match both .go files:\n%s", out)
	}
}

// TestGlobInputExpandsMatchedDir: a glob that matches a directory recurses
// into it the same way a plain directory input would.
func TestGlobInputExpandsMatchedDir(t *testing.T) {
	structuralOnly(t)
	dir := t.TempDir()
	writeIn(t, dir, "pkgs/one/a.go", "package one\n\nfunc One() {}\n")
	writeIn(t, dir, "pkgs/two/b.go", "package two\n\nfunc Two() {}\n")

	var buf bytes.Buffer
	pattern := filepath.Join(dir, "pkgs", "*")
	if err := Run(Options{Inputs: []string{pattern}, Format: "toon"}, &buf); err != nil {
		t.Fatalf("Run glob-dir: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "One") || !strings.Contains(out, "Two") {
		t.Errorf("glob-matched dirs should recurse:\n%s", out)
	}
}

// TestMixedDirAndFile: a directory input and a literal file in the same
// call, in input order.
func TestMixedDirAndFile(t *testing.T) {
	structuralOnly(t)
	dir := t.TempDir()
	writeIn(t, dir, "pkg/a.go", "package pkg\n\nfunc InPkg() {}\n")
	other := writeTmp(t, "main.go", "package main\n\nfunc InMain() {}\n")

	var buf bytes.Buffer
	if err := Run(Options{Inputs: []string{filepath.Join(dir, "pkg"), other}, Format: "toon"}, &buf); err != nil {
		t.Fatalf("Run mixed: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "InPkg") || !strings.Contains(out, "InMain") {
		t.Errorf("missing one of the mixed inputs:\n%s", out)
	}
	if strings.Index(out, "InPkg") > strings.Index(out, "InMain") {
		t.Errorf("mixed inputs out of order:\n%s", out)
	}
}

// TestDirDeterministic: two identical directory calls produce byte-identical
// output — directory walk order isn't guaranteed stable, so expansion must
// sort.
func TestDirDeterministic(t *testing.T) {
	t.Setenv("SOFIA_FOOTER", "")
	dir := t.TempDir()
	for i := 0; i < 12; i++ {
		writeIn(t, dir, fmt.Sprintf("f%02d.go", i), fmt.Sprintf("package p\n\nfunc F%02d() {}\n", i))
	}
	run := func() []byte {
		var buf bytes.Buffer
		if err := Run(Options{Inputs: []string{dir}, Format: "toon"}, &buf); err != nil {
			t.Fatalf("Run: %v", err)
		}
		return buf.Bytes()
	}
	a, b := run(), run()
	if !bytes.Equal(a, b) {
		t.Errorf("two identical directory runs differ:\n--- first\n%s\n--- second\n%s", a, b)
	}
}

// TestTooManyFiles: expansion past the 250-file cap is a named, deterministic
// refusal, not a silent truncation.
func TestTooManyFiles(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 251; i++ {
		writeIn(t, dir, fmt.Sprintf("f%03d.go", i), "package p\n")
	}
	var buf bytes.Buffer
	err := Run(Options{Inputs: []string{dir}, Format: "toon"}, &buf)
	if err == nil {
		t.Fatal("expected an error past the 250-file cap")
	}
	if !strings.Contains(err.Error(), "251") || !strings.Contains(err.Error(), "250") {
		t.Errorf("error should name both the count and the limit, got: %v", err)
	}
}

// TestEmptyDirErrors: a directory with no supported files is a named error,
// not a silent no-op.
func TestEmptyDirErrors(t *testing.T) {
	dir := t.TempDir()
	writeIn(t, dir, "notes.txt", "not code\n")

	var buf bytes.Buffer
	err := Run(Options{Inputs: []string{dir}, Format: "toon"}, &buf)
	if err == nil {
		t.Fatal("expected an error for a directory with no supported files")
	}
	if !strings.Contains(err.Error(), dir) {
		t.Errorf("error should name the input directory, got: %v", err)
	}
}

// TestGlobNoMatchErrors: a glob pattern matching nothing is a named error.
func TestGlobNoMatchErrors(t *testing.T) {
	dir := t.TempDir()
	pattern := filepath.Join(dir, "*.go")

	var buf bytes.Buffer
	err := Run(Options{Inputs: []string{pattern}, Format: "toon"}, &buf)
	if err == nil {
		t.Fatal("expected an error for a glob matching nothing")
	}
	if !strings.Contains(err.Error(), pattern) {
		t.Errorf("error should name the glob pattern, got: %v", err)
	}
}

// TestDirWithSymbolsErrors: slice mode needs a single real file — a
// directory or glob input is a clear error, not a silent first-file pick.
func TestDirWithSymbolsErrors(t *testing.T) {
	dir := t.TempDir()
	writeIn(t, dir, "a.go", "package a\n\nfunc A() {}\n")

	var buf bytes.Buffer
	err := Run(Options{Inputs: []string{dir}, Symbols: []string{"A"}}, &buf)
	if err == nil {
		t.Fatal("expected an error slicing symbols from a directory")
	}
	if !strings.Contains(err.Error(), "directory") {
		t.Errorf("error should call out the directory, got: %v", err)
	}

	pattern := filepath.Join(dir, "*.go")
	var buf2 bytes.Buffer
	err = Run(Options{Inputs: []string{pattern}, Symbols: []string{"A"}}, &buf2)
	if err == nil {
		t.Fatal("expected an error slicing symbols from a glob pattern")
	}
	if !strings.Contains(err.Error(), "glob") {
		t.Errorf("error should call out the glob pattern, got: %v", err)
	}
}

// TestDedupKeyCoversExpandedFiles: the dedup key must be built from the
// EXPANDED file list, not the directory arg itself — editing one file inside
// the directory between two otherwise-identical calls must bust the dedup.
func TestDedupKeyCoversExpandedFiles(t *testing.T) {
	dedupEnv(t, "sid-dir-expand")
	dir := t.TempDir()
	writeIn(t, dir, "a.go", "package p\n\nfunc A() {}\n")
	writeIn(t, dir, "b.go", "package p\n\nfunc B() {}\n")

	if err := Run(Options{Inputs: []string{dir}, Format: "toon"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	// Rewrite one file inside the directory: size changes, so the expanded
	// file's own in=path@size:mtime key part changes too.
	writeIn(t, dir, "a.go", "package p\n\nfunc A() { /* edited */ }\n")

	var buf bytes.Buffer
	if err := Run(Options{Inputs: []string{dir}, Format: "toon"}, &buf); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if strings.Contains(buf.String(), "already returned") {
		t.Fatalf("a directory call with one edited file inside must not be stubbed:\n%s", buf.String())
	}
}
