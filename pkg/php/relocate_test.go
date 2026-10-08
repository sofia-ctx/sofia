package php

import (
	"strings"
	"testing"
)

func TestRelocateNamespacePreservesExternalTypes(t *testing.T) {
	src := `<?php
namespace App\Old;
use Vendor\Base;
class Foo extends Base {
 public Peer $peer { get => new Peer(); }
 public function selfType(Foo $x): Foo { return $x; }
}`
	want := strings.Replace(src, `namespace App\Old;`, `namespace App\New;`, 1)
	want = strings.ReplaceAll(want, `Peer`, `\App\Old\Peer`)
	r, err := Relocate(src, "Foo.php", Relocation{Move: true, Namespace: `App\Old`, NewNamespace: `App\New`, Classes: map[string]string{`App\Old\Foo`: `App\New\Foo`}})
	if err != nil {
		t.Fatal(err)
	}
	if r.Source != want {
		t.Fatalf("got:\n%s\nwant:\n%s", r.Source, want)
	}
	if len(r.Edits) != 3 {
		t.Fatalf("edits: %+v", r.Edits)
	}
}

func TestRelocateReferencesAndAliases(t *testing.T) {
	src := `<?php namespace Client;
use App\Old\Foo;
use App\Old\Foo as Alias;
use function App\Old\Foo as functionAlias;
use const App\Old\Foo as CONSTANT_ALIAS;
#[\App\Old\Foo]
class Consumer {
 public function run(Foo $a, Alias $b): \App\Old\Foo {
  functionAlias(); echo CONSTANT_ALIAS;
  return new Foo();
 }
}`
	want := strings.Replace(src, `use App\Old\Foo;`, `use App\New\Renamed as Foo;`, 1)
	want = strings.Replace(want, `use App\Old\Foo as Alias;`, `use App\New\Renamed as Alias;`, 1)
	want = strings.ReplaceAll(want, `\App\Old\Foo`, `\App\New\Renamed`)
	r, err := Relocate(src, "Consumer.php", Relocation{Classes: map[string]string{`App\Old\Foo`: `App\New\Renamed`}})
	if err != nil {
		t.Fatal(err)
	}
	if r.Source != want {
		t.Fatalf("got:\n%s\nwant:\n%s", r.Source, want)
	}
}

func TestRelocatePartialGroupPreservesComments(t *testing.T) {
	src := `<?php namespace Client;
use App\Old\{ /* keep */ Foo as F, Bar, function helper, const FLAG };
class Consumer { public F $f; public Bar $b; }`
	want := `<?php namespace Client;
use App\{ /* keep */ New\Foo as F, Old\Bar, function Old\helper, const Old\FLAG };
class Consumer { public F $f; public Bar $b; }`
	r, err := Relocate(src, "Consumer.php", Relocation{Classes: map[string]string{`App\Old\Foo`: `App\New\Foo`}})
	if err != nil {
		t.Fatal(err)
	}
	if r.Source != want {
		t.Fatalf("got:\n%s\nwant:\n%s", r.Source, want)
	}
}

func TestRelocateGroupAcrossNamespaceRoots(t *testing.T) {
	src := `<?php namespace Client;
use App\Old\{Foo, Bar};
class Consumer { public Foo $f; public Bar $b; }`
	r, err := Relocate(src, "Consumer.php", Relocation{Classes: map[string]string{`App\Old\Foo`: `Other\Foo`}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.Source, "use Other\\Foo;\nuse App\\Old\\Bar;") {
		t.Fatal(r.Source)
	}
	commented := strings.Replace(src, "{Foo,", "{/* preserve */ Foo,", 1)
	if _, err := Relocate(commented, "Consumer.php", Relocation{Classes: map[string]string{`App\Old\Foo`: `Other\Foo`}}); err == nil {
		t.Fatal("split discarded comments")
	}
}

func TestRelocateDirectoryPeersAndRenameDeclaration(t *testing.T) {
	src := `<?php namespace App\Old; class Foo { public Peer $peer; public Foo $self; }`
	r, err := Relocate(src, "Foo.php", Relocation{Move: true, Namespace: `App\Old`, NewNamespace: `App\New`, Classes: map[string]string{`App\Old\Foo`: `App\New\Renamed`, `App\Old\Peer`: `App\New\Peer`}})
	if err != nil {
		t.Fatal(err)
	}
	want := `<?php namespace App\New; class Renamed { public Peer $peer; public \App\New\Renamed $self; }`
	if r.Source != want {
		t.Fatalf("got %s, want %s", r.Source, want)
	}
}

func TestRelocateWarnsWithoutRewritingRuntimeData(t *testing.T) {
	src := `<?php namespace App\Old;
/** @return Foo */
class Foo {
 public function run(): string { include __DIR__.'/config.php'; return 'App\\Old\\Foo'.trim(' x '); }
}`
	r, err := Relocate(src, "Foo.php", Relocation{Move: true, Namespace: `App\Old`, NewNamespace: `App\New`, Classes: map[string]string{`App\Old\Foo`: `App\New\Foo`}})
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]bool{}
	for _, w := range r.Warnings {
		kinds[w.Kind] = true
	}
	for _, kind := range []string{"phpdoc", "file_location", "string_reference", "namespace_fallback"} {
		if !kinds[kind] {
			t.Errorf("missing %s: %+v", kind, r.Warnings)
		}
	}
	if !strings.Contains(r.Source, `'App\\Old\\Foo'`) {
		t.Fatal("runtime string changed")
	}
}

func TestRelocateRejectsPartialInputAndUnmovedDeclaration(t *testing.T) {
	options := Relocation{Classes: map[string]string{`App\Old\Foo`: `App\New\Foo`}}
	for _, src := range []string{
		`<?php namespace App\Old; class Foo { public function run((A&B|C) $x) {} }`,
		`<?php namespace App\Old; class Foo {}`,
	} {
		if _, err := Relocate(src, "Foo.php", options); err == nil {
			t.Fatalf("accepted %s", src)
		}
	}
}

func TestRelocateDynamicCallsAndNestedReferences(t *testing.T) {
	src := `<?php namespace Client; class Consumer { function run($fn): void { $fn(new \App\Old\Foo); (fn() => new \App\Old\Foo)(); } }`
	r, err := Relocate(src, "Consumer.php", Relocation{Classes: map[string]string{`App\Old\Foo`: `App\New\Foo`}})
	if err != nil {
		t.Fatal(err)
	}
	if r.Source != strings.ReplaceAll(src, `\App\Old\Foo`, `\App\New\Foo`) {
		t.Fatal(r.Source)
	}
}

func TestRelocateModernTypeAndTraitReferences(t *testing.T) {
	src := "<?php\r\nnamespace App\\Old;\r\n" + `
#[Foo]
class Consumer implements Foo {
 use Foo, Other { Foo::run insteadof Other; Foo::run as oldRun; }
 public function __construct(public (Foo&Other)|null $value) {}
 public Foo $item { get => new Foo; set(Foo $value) { $this->item = $value; } }
 public function inspect(): void { try { throw new Foo; } catch (Foo $error) { echo Foo::class; } }
}`
	r, err := Relocate(src, "Consumer.php", Relocation{Classes: map[string]string{`App\Old\Foo`: `App\New\Foo`}})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.ReplaceAll(src, "Foo", `\App\New\Foo`)
	if r.Source != want {
		t.Fatalf("got:\n%s\nwant:\n%s", r.Source, want)
	}
}
