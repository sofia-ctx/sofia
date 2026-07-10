package php

import "testing"

// TestNormalizeModern checks that PHP 8.2–8.5 syntax which the VKCOM 8.1
// grammar rejects is recovered (via normalize + tolerant extraction) so the
// class structure is still produced.
func TestNormalizeModern(t *testing.T) {
	cases := map[string]string{
		"typed const (8.3)":        `<?php class E { const string FOO = "x"; public function f(): void {} }`,
		"asymmetric vis (8.4)":     `<?php class H { public private(set) int $x = 0; public function f(): void {} }`,
		"new without parens (8.4)": `<?php class I { public function f(): mixed { return new J()->g(); } }`,
		"property hook get (8.4)":  `<?php class P { public bool $v { get => true; } public function f(): void {} }`,
		"property hook get+set":    `<?php class Q { public string $s { get => $this->s; set => $this->s = $value; } public function f(): void {} }`,
		"hook after asym vis":      `<?php class R { public private(set) string $s { get => $this->s; } public function f(): void {} }`,
		"DNF type (8.2)":           `<?php class S { public function f((A&B)|C $x): void {} }`,
		"first-class callable":     `<?php class T { public function f(): void { $g = strlen(...); } }`,
		"pipe operator (8.5)":      `<?php class U { public function run(string $s): string { return $s |> trim(...) |> strtoupper(...); } public function f(): void {} }`,
	}
	for name, src := range cases {
		sym, err := ReadString(src, name+".php")
		if err != nil {
			t.Errorf("%s: ReadString failed: %v", name, err)
			continue
		}
		hasF := false
		for _, m := range sym.Methods {
			if m.Name == "f" {
				hasF = true
			}
		}
		if !hasF {
			t.Errorf("%s: method f() not recovered (methods=%v)", name, sym.Methods)
		}
	}
}

// TestValidPHPUntouched confirms normalization is a no-op on valid ≤8.1
// code: a normal class still parses with all its members.
func TestValidPHPUntouched(t *testing.T) {
	src := `<?php
namespace App;
class Plain {
    public function __construct(private int $id) {}
    public function getId(): int { return $this->id; }
    public function name(): string { return "x"; }
}`
	sym, err := ReadString(src, "Plain.php")
	if err != nil {
		t.Fatalf("ReadString: %v", err)
	}
	if len(sym.Methods) != 2 { // getId, name (ctor is a CtorDep)
		t.Errorf("methods = %d, want 2", len(sym.Methods))
	}
	if len(sym.CtorDeps) != 1 || sym.CtorDeps[0].Name != "id" {
		t.Errorf("ctor deps = %+v, want [id]", sym.CtorDeps)
	}
}

// TestPropertyHookRecoversMembers confirms a hooked property keeps its
// name/type/visibility and, crucially, that members declared *after* the hook
// (ctor, methods) are still recovered — the whole point of the better
// retry-acceptance rule.
func TestPropertyHookRecoversMembers(t *testing.T) {
	src := `<?php
namespace App;
class Account {
    #[ORM\Column]
    public string $password {
        get => $this->password;
        set => $this->password = $value;
    }
    public bool $active { get => $this->state === 1; }
    public function __construct(private int $id) {}
    public function rename(string $n): void {}
}`
	sym, err := ReadString(src, "Account.php")
	if err != nil {
		t.Fatalf("ReadString: %v", err)
	}
	if sym.Partial {
		t.Error("expected AST recovery, got partial")
	}
	prop := propByName(sym, "password")
	if prop == nil || prop.Type != "string" || prop.Visibility != "public" {
		t.Errorf("password property = %+v, want public string", prop)
	}
	if propByName(sym, "active") == nil {
		t.Error("hooked property 'active' not recovered")
	}
	if !hasMethod(sym, "rename") {
		t.Error("method after hooks not recovered")
	}
	if len(sym.CtorDeps) != 1 || sym.CtorDeps[0].Name != "id" {
		t.Errorf("ctor deps = %+v, want [id]", sym.CtorDeps)
	}
}

// TestHookBodyBraceInString checks the brace matcher is string-aware: a `}`
// inside a string literal in the hook body must not end the block early.
func TestHookBodyBraceInString(t *testing.T) {
	src := `<?php class B { public string $x { get => '}'; } public function f(): void {} }`
	sym, err := ReadString(src, "B.php")
	if err != nil {
		t.Fatalf("ReadString: %v", err)
	}
	if !hasMethod(sym, "f") {
		t.Errorf("method f() not recovered — brace match ended early (methods=%v)", sym.Methods)
	}
}

// TestMethodBodyNotMangled is the negative case: stripPropertyHooks must not
// touch a normal method whose body is a `{…}` block.
func TestMethodBodyNotMangled(t *testing.T) {
	got := string(downgradePropertyHooks([]byte(`public function f($x) { return $x; }`)))
	if want := `public function f($x) { return $x; }`; got != want {
		t.Errorf("method mangled:\n got  %q\n want %q", got, want)
	}
}

// TestExtractPartial confirms degrade-to-partial yields a skeleton (kind,
// FQCN, extends) for a file too broken for the AST, instead of a hard error.
func TestExtractPartial(t *testing.T) {
	// `;;;` after the class head is a hard syntax error VKCOM can't recover
	// into a declaration, forcing the regex fallback.
	src := []byte(`<?php
namespace App\Bad;
class Foo extends Bar implements Baz {
    ;;; @@@ totally broken @@@ ;;;
`)
	sym := extractPartial(src, "Foo.php")
	if sym == nil {
		t.Fatal("extractPartial returned nil")
	}
	if !sym.Partial {
		t.Error("Partial flag not set")
	}
	if sym.FQCN != `App\Bad\Foo` {
		t.Errorf("FQCN = %q, want App\\Bad\\Foo", sym.FQCN)
	}
	if sym.Kind != KindClass || sym.Extends != "Bar" {
		t.Errorf("kind/extends = %q/%q, want class/Bar", sym.Kind, sym.Extends)
	}
	if len(sym.Implements) != 1 || sym.Implements[0] != "Baz" {
		t.Errorf("implements = %v, want [Baz]", sym.Implements)
	}
}

func propByName(sym *Symbol, name string) *Property {
	for i := range sym.Properties {
		if sym.Properties[i].Name == name {
			return &sym.Properties[i]
		}
	}
	return nil
}

func hasMethod(sym *Symbol, name string) bool {
	for _, m := range sym.Methods {
		if m.Name == name {
			return true
		}
	}
	return false
}
