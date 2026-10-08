package php

import (
	"reflect"
	"strings"
	"testing"

	"github.com/dimasma0305/php-parser-go/ast"
)

func TestModernPropertyDetails(t *testing.T) {
	src := `<?php
namespace App;
use Vendor\{Left as L, Right, Value};
use function Other\Value;
use const Other\L;
final class Example {
 public private(set) (L&Right)|Value $item {
  #[Audit] final &get { return $this->item; }
  set(Value $value) => $value;
 }
 public function __construct(final public string $name { set => trim($value); }) {}
 public function accept((l&Right)|namespace\Value $x): (L&Right)|\Fallback { return $x; }
}`
	s, err := ReadStringStrict(src, "Example.php")
	if err != nil {
		t.Fatal(err)
	}
	if s.Partial || s.Recovery != "" || len(s.Diagnostics) != 0 {
		t.Fatalf("unexpected recovery: %+v", s)
	}
	p := propByName(s, "item")
	if p == nil {
		t.Fatal("missing hooked property")
	}
	if p.Type != `(Vendor\Left&Vendor\Right)|Vendor\Value` || p.Visibility != "public" || p.WriteVisibility != "private" {
		t.Fatalf("property: %+v", p)
	}
	if len(p.Hooks) != 2 {
		t.Fatalf("hooks: %+v", p.Hooks)
	}
	get, set := p.Hooks[0], p.Hooks[1]
	if get.Name != "get" || !get.ByRef || get.BodyKind != "block" || !reflect.DeepEqual(get.Modifiers, []string{"final"}) || len(get.Attributes) != 1 || get.Attributes[0].Name != `App\Audit` {
		t.Fatalf("get: %+v", get)
	}
	if set.Name != "set" || set.BodyKind != "expression" || !reflect.DeepEqual(set.Params, []Param{{Name: "value", Type: `Vendor\Value`}}) {
		t.Fatalf("set: %+v", set)
	}
	promoted := propByName(s, "name")
	if promoted == nil || !promoted.Promoted || len(promoted.Hooks) != 1 || !reflect.DeepEqual(promoted.Modifiers, []string{"public", "final"}) {
		t.Fatalf("promoted: %+v", promoted)
	}
	if len(s.Methods) != 1 || s.Methods[0].Params[0].Type != `(Vendor\Left&Vendor\Right)|App\Value` || s.Methods[0].ReturnType != `(Vendor\Left&Vendor\Right)|Fallback` {
		t.Fatalf("DNF: %+v", s.Methods)
	}
}

func TestAbstractHooksAndPrivateConstructorPromotion(t *testing.T) {
	s, err := ReadStringStrict(`<?php interface Named { public string $name { get; set; } }`, "Named.php")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Properties) != 1 || len(s.Properties[0].Hooks) != 2 {
		t.Fatalf("properties: %+v", s.Properties)
	}
	for _, h := range s.Properties[0].Hooks {
		if h.BodyKind != "abstract" {
			t.Fatalf("hook: %+v", h)
		}
	}
	s, err = ReadStringStrict(`<?php class Named { private function __construct(public string $name { get => 'name'; }) {} }`, "Named.php")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.CtorDeps) != 0 || len(s.Properties) != 1 || !s.Properties[0].Promoted {
		t.Fatalf("private constructor: %+v", s)
	}
}

func TestPHP85BodiesRemainInAST(t *testing.T) {
	cases := []struct{ name, body, kind string }{
		{"pipe", `$x = 'abc' |> strlen(...);`, "Expr_BinaryOp_Pipe"},
		{"void cast", `(void) work();`, "Expr_Cast_Void"},
		{"clone updates", `$x = clone($old, ['name' => 'new']);`, "Expr_FuncCall"},
		{"new dereference", `$x = new Example()->run();`, "Expr_MethodCall"},
		{"const expression closure", `static $f = static function() { return 1; };`, "Expr_Closure"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "<?php class Example { public function run() { " + tc.body + " } }"
			nodes, diagnostics, err := parsePHP([]byte(src))
			if err != nil || len(diagnostics) > 0 {
				t.Fatalf("parse: %v %v", err, diagnostics)
			}
			kinds := map[string]int{}
			for _, n := range nodes {
				ast.Walk(n, func(n ast.Node) bool { kinds[n.NodeType()]++; return true })
			}
			if kinds[tc.kind] == 0 {
				t.Fatalf("missing %s: %v", tc.kind, kinds)
			}
			got, _, err := Slice([]byte(src), "Example::run")
			if err != nil || got != "public function run() { "+tc.body+" }" {
				t.Fatalf("slice %q: %v", got, err)
			}
		})
	}
}

func TestTypedConstantsAndMagicProperty(t *testing.T) {
	src := `<?php #[Marker] const GLOBAL_VALUE = 1;
readonly class Example {
 #[Marker] public const string NAME = 'name';
 public string $name { get => __PROPERTY__; }
}`
	s, err := ReadStringStrict(src, "Example.php")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Properties) != 1 || len(s.Properties[0].Hooks) != 1 || !reflect.DeepEqual(s.Modifiers, []string{"readonly"}) {
		t.Fatalf("symbol: %+v", s)
	}
	got, _, err := Slice([]byte(src), "$name::get")
	if err != nil || got != "get => __PROPERTY__;" {
		t.Fatalf("magic property slice %q: %v", got, err)
	}
}

func TestNamespaceImportIsolation(t *testing.T) {
	src := `<?php namespace Before { use Wrong\Foo; }
 namespace After { use Right\{Foo as Alias, function call, const FLAG};
 #[Alias] class Example extends aLIAS { public Foo $unimported; public namespace\Foo $relative; } }`
	s, err := ReadStringStrict(src, "Example.php")
	if err != nil {
		t.Fatal(err)
	}
	if s.Extends != `Right\Foo` || s.Attributes[0].Name != `Right\Foo` || s.Properties[0].Type != `After\Foo` || s.Properties[1].Type != `After\Foo` {
		t.Fatalf("imports leaked: %+v", s)
	}
}

func TestRecoveryIsExplicitAndStrictRejectsIt(t *testing.T) {
	for _, src := range []string{
		`<?php class Example { public function run() { $x = ; } public function after(): void {} }`,
		`<?php class Example { ;;; @@@ broken @@@`,
		`<?php class Example { public private(set) string $name { get => ; } public function after(): void {} }`,
	} {
		s, err := ReadString(src, "broken.php")
		if err != nil {
			t.Fatal(err)
		}
		if !s.Partial || s.Recovery == "" || len(s.Diagnostics) == 0 {
			t.Fatalf("unmarked recovery: %+v", s)
		}
		if _, err := ReadStringStrict(src, "broken.php"); err == nil {
			t.Fatal("strict accepted broken input")
		}
		if _, _, err := Slice([]byte(src), "Example"); err == nil {
			t.Fatal("slice accepted broken input")
		}
	}
}

func TestSliceOriginalPropertyAndHookBytes(t *testing.T) {
	property := "#[Marker]\r\n public private(set) string $title {\r\n  get { /* } кириллица */ return \"};\"; }\r\n  set => trim($value);\r\n }"
	src := "<?php\r\n// UTF-8: заголовок\r\nclass Example {\r\n " + property + "\r\n}"
	cases := map[string]string{
		"Example::$title":      property,
		"$title::get":          `get { /* } кириллица */ return "};"; }`,
		"Example::$title::set": "set => trim($value);",
	}
	for symbol, want := range cases {
		got, _, err := Slice([]byte(src), symbol)
		if err != nil || got != want {
			t.Errorf("%s: got %q, want %q, err %v", symbol, got, want, err)
		}
	}
	_, names, err := Slice([]byte(src), "missing")
	if err == nil || !strings.Contains(strings.Join(names, ","), "Example::$title::get") {
		t.Fatalf("available names: %v %v", names, err)
	}
}

func TestSliceRejectsAmbiguousBareNames(t *testing.T) {
	src := []byte(`<?php class A { public function run() {} } class B { public function run() { return 1; } }`)
	if _, _, err := Slice(src, "run"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguity: %v", err)
	}
	got, _, err := Slice(src, "B::run")
	if err != nil || got != `public function run() { return 1; }` {
		t.Fatalf("qualified: %q %v", got, err)
	}
}

func TestConcurrentNativeReads(t *testing.T) {
	for i := 0; i < 16; i++ {
		t.Run("reader", func(t *testing.T) {
			t.Parallel()
			s, err := ReadStringStrict(`<?php namespace Example; use Vendor\Value; class Box { public Value $value { get => new Value(); } }`, "Box.php")
			if err != nil {
				t.Fatal(err)
			}
			if len(s.Properties) != 1 || s.Properties[0].Type != `Vendor\Value` || len(s.Properties[0].Hooks) != 1 {
				t.Fatalf("concurrent summary: %+v", s)
			}
		})
	}
}

// Upstream v0.1.1 misses a closing label followed by whitespace before a comma.
// Keep the failure explicit until the backend fixes it; never hide diagnostics
// merely because the surrounding method signatures could be extracted.
func TestHeredocWhitespaceLimitationIsExplicit(t *testing.T) {
	for _, separator := range []string{",", " ,"} {
		src := "<?php\nclass Example {\n public function run() {\n  foo(<<<SQL\n   one\n   SQL" + separator + " []);\n  foo(<<<SQL\n   two\n   SQL, []);\n }\n}\n"
		s, err := ReadString(src, "Example.php")
		if err != nil {
			t.Fatal(err)
		}
		if separator == "," {
			if s.Partial {
				t.Fatalf("ordinary heredoc: %+v", s)
			}
		} else {
			if !s.Partial || len(s.Diagnostics) == 0 {
				t.Fatalf("review upstream limitation after backend update: %+v", s)
			}
			if _, err := ReadStringStrict(src, "Example.php"); err == nil {
				t.Fatal("strict accepted a recovered heredoc")
			}
		}
	}
}

func TestNumericAttributeSpelling(t *testing.T) {
	s, err := ReadStringStrict(`<?php #[Example(0x20, 1_000, 1.20e3)] class Box {}`, "Box.php")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Attributes) != 1 || !reflect.DeepEqual(s.Attributes[0].Args, []AttrArg{{Value: "0x20"}, {Value: "1_000"}, {Value: "1.20e3"}}) {
		t.Fatalf("attribute values: %+v", s.Attributes)
	}
}

func TestNormalizedRecoveryKeepsOriginalErrors(t *testing.T) {
	src := `<?php class Example { public function run((A&B|C) $x): void {} }`
	s, err := ReadString(src, "Example.php")
	if err != nil {
		t.Fatal(err)
	}
	if !s.Partial || s.Recovery != "normalized_ast" || len(s.Diagnostics) == 0 || !hasMethod(s, "run") {
		t.Fatalf("normalized recovery: %+v", s)
	}
	if _, err := ReadStringStrict(src, "Example.php"); err == nil {
		t.Fatal("normalization made strict input appear valid")
	}
	if _, _, err := Slice([]byte(src), "run"); err == nil {
		t.Fatal("slice used normalized positions")
	}
}

func TestSlicePromotedHookAndSharedDeclaration(t *testing.T) {
	src := []byte(`<?php class Example {
 public int $first, $second;
 public function __construct(public string $name { set => trim($value); }) {}
}`)
	cases := map[string]string{
		"$name":               `public string $name { set => trim($value); }`,
		"Example::$name::set": `set => trim($value);`,
		"$second":             `public int $first, $second;`,
	}
	for name, want := range cases {
		got, _, err := Slice(src, name)
		if err != nil || got != want {
			t.Errorf("%s: %q, want %q (%v)", name, got, want, err)
		}
	}
}
