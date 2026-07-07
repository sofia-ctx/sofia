package code

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// goStructSrc builds a syntactically valid Go file big enough that its
// structural summary stays smaller than the raw text (compact-or-raw keeps
// the summary), with a tagged-field struct and an interface to exercise
// --brief's cut.
func goStructSrc() string {
	var b strings.Builder
	b.WriteString("package big\n\n")
	b.WriteString("type Widget struct {\n\tName string `json:\"name\"`\n\tCount int\n}\n\n")
	b.WriteString("type Fetcher interface {\n\tFetch(ctx context.Context) (string, error)\n}\n\n")
	for i := 0; b.Len() < defaultRawBelow+512; i++ {
		fmt.Fprintf(&b, "// Filler%d pads the file with a real body.\nfunc Filler%d() int {\n\tx := %d\n\ty := x * 3\n\tz := y - x\n\treturn x + y + z\n}\n\n", i, i, i)
	}
	return b.String()
}

// TestCodeBriefGo: --brief drops the struct's field list (and tag) but keeps
// the type inventory, the interface's method signature, and every func sig.
func TestCodeBriefGo(t *testing.T) {
	structuralOnly(t)
	p := writeTmp(t, "big.go", goStructSrc())
	var buf bytes.Buffer
	if err := Run(Options{Inputs: []string{p}, Format: "toon", Brief: true}, &buf); err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "json:\"name\"") || strings.Contains(out, "Name string") {
		t.Errorf("brief must drop struct field/tag detail:\n%s", out[:min(400, len(out))])
	}
	if !strings.Contains(out, "struct,Widget") {
		t.Errorf("brief must keep the struct's inventory row:\n%s", out[:min(400, len(out))])
	}
	if !strings.Contains(out, "Fetch(ctx context.Context) (string, error)") {
		t.Errorf("brief must keep interface method signatures:\n%s", out[:min(400, len(out))])
	}
	if !strings.Contains(out, "Filler0") {
		t.Errorf("brief must keep func signatures:\n%s", out[:min(400, len(out))])
	}
	if !strings.Contains(out, "# sf ≈") {
		t.Errorf("brief output should still end with the cost footer:\n%s", lastLine(t, out))
	}
}

// TestCodeBriefPHP: --brief drops properties but keeps the class name, ctor
// deps, and method signatures.
func TestCodeBriefPHP(t *testing.T) {
	structuralOnly(t)
	p := writeTmp(t, "Widget.php", briefPHPSrc())
	var buf bytes.Buffer
	if err := Run(Options{Inputs: []string{p}, Format: "toon", Brief: true}, &buf); err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "properties[") {
		t.Errorf("brief must drop the properties block:\n%s", out)
	}
	if !strings.Contains(out, "name: WidgetController") {
		t.Errorf("brief must keep the class name:\n%s", out)
	}
	if !strings.Contains(out, `rename,"(string $name): void"`) {
		t.Errorf("brief must keep method signatures:\n%s", out)
	}
}

func briefPHPSrc() string {
	var b strings.Builder
	b.WriteString("<?php\nclass WidgetController\n{\n")
	b.WriteString("    public string $label = 'x';\n")
	b.WriteString("    public function __construct(private LoggerInterface $log) {}\n\n")
	b.WriteString("    public function rename(string $name): void\n    {\n        $this->label = $name;\n    }\n\n")
	for i := 0; b.Len() < defaultRawBelow+512; i++ {
		fmt.Fprintf(&b, "    public function filler%d(): int\n    {\n        $x = %d;\n        $y = $x * 3;\n        return $x + $y;\n    }\n\n", i, i)
	}
	b.WriteString("}\n")
	return b.String()
}

// TestCodeBriefTS: --brief drops the interface's member list but keeps its
// name and the enum's (already bare) member list.
func TestCodeBriefTS(t *testing.T) {
	structuralOnly(t)
	p := writeTmp(t, "auth.ts", tsBriefSrc())
	var buf bytes.Buffer
	if err := Run(Options{Inputs: []string{p}, Format: "toon", Brief: true}, &buf); err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "id: string") || strings.Contains(out, "name: string") {
		t.Errorf("brief must drop interface member list:\n%s", out)
	}
	if !strings.Contains(out, "interface,CurrentUser") {
		t.Errorf("brief must keep the interface's inventory row:\n%s", out)
	}
}

func tsBriefSrc() string {
	var b strings.Builder
	b.WriteString("export interface CurrentUser {\n  id: string\n  name: string\n  roles: string[]\n}\n\n")
	for i := 0; b.Len() < defaultRawBelow+512; i++ {
		fmt.Fprintf(&b, "export function filler%d(): number {\n  const x = %d\n  const y = x * 3\n  return x + y\n}\n\n", i, i)
	}
	return b.String()
}

// TestBriefComposesExported: --brief and --exported apply independently —
// an unexported func disappears (exported) and the struct's field detail is
// gone (brief), in the same call.
func TestBriefComposesExported(t *testing.T) {
	structuralOnly(t)
	src := goStructSrc() + "\nfunc unexportedHelper() {}\n"
	p := writeTmp(t, "big.go", src)
	var buf bytes.Buffer
	if err := Run(Options{Inputs: []string{p}, Format: "toon", Brief: true, ExportedOnly: true}, &buf); err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "unexportedHelper") {
		t.Errorf("--exported should drop the unexported func:\n%s", out[:min(400, len(out))])
	}
	if strings.Contains(out, "Name string") {
		t.Errorf("--brief should still drop struct field detail:\n%s", out[:min(400, len(out))])
	}
	if !strings.Contains(out, "struct,Widget") {
		t.Errorf("brief+exported should keep the struct's inventory row:\n%s", out[:min(400, len(out))])
	}
}

// TestBriefJSON: in --format json, brief simply omits the detail fields —
// the struct's tagged field text is absent from the payload, the type/func
// inventory is still there, and the footer still prints.
func TestBriefJSON(t *testing.T) {
	structuralOnly(t)
	p := writeTmp(t, "big.go", goStructSrc())
	var buf bytes.Buffer
	if err := Run(Options{Inputs: []string{p}, Format: "json", Brief: true}, &buf); err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, `json:\"name\"`) || strings.Contains(out, "Name string") {
		t.Errorf("brief JSON must omit struct field/tag detail:\n%s", out[:min(400, len(out))])
	}
	var v struct {
		Package string `json:"package"`
		Types   []struct {
			Kind string `json:"kind"`
			Name string `json:"name"`
		} `json:"types"`
	}
	dec := json.NewDecoder(strings.NewReader(out))
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("payload does not decode as JSON: %v", err)
	}
	found := false
	for _, ty := range v.Types {
		if ty.Name == "Widget" && ty.Kind == "struct" {
			found = true
		}
	}
	if !found {
		t.Errorf("Widget struct's inventory entry should survive brief JSON: %+v", v.Types)
	}
	if !strings.Contains(out, "# sf ≈") {
		t.Errorf("brief JSON output should still end with the cost footer:\n%s", lastLine(t, out))
	}
}
