package php

import (
	"strings"
	"testing"
)

func TestReadString_FinalReadonlyClassWithPromotedCtor(t *testing.T) {
	src := `<?php

declare(strict_types=1);

namespace App\Service;

use Override;
use Psr\Log\LoggerInterface;
use App\Repository\FlightsRepository;
use App\Contracts\ServiceInterface;

/**
 * Owns the runtime flights map; subscribes to repository changes.
 *
 * @internal
 */
final readonly class FlightsMapService implements ServiceInterface
{
    private \React\Promise\Deferred $runtime;

    public function __construct(
        private LoggerInterface $logger,
        private FlightsRepository $flightsRepository,
    ) {}

    #[Override]
    public function start(): void {}

    #[Override]
    public function stop(): void {}

    public function trapDebugInfo(): void {}

    protected function internalHelper(): void {}

    private function alsoSkip(): void {}
}
`
	sym, err := ReadString(src, "test.php")
	if err != nil {
		t.Fatalf("ReadString: %v", err)
	}

	if got, want := sym.FQCN, `App\Service\FlightsMapService`; got != want {
		t.Errorf("FQCN: got %q, want %q", got, want)
	}
	if sym.Kind != KindClass {
		t.Errorf("Kind: got %q, want class", sym.Kind)
	}
	if got, want := strings.Join(sym.Modifiers, ","), "final,readonly"; got != want {
		t.Errorf("Modifiers: got %q, want %q", got, want)
	}
	if got, want := strings.Join(sym.Implements, ","), `App\Contracts\ServiceInterface`; got != want {
		t.Errorf("Implements: got %q, want %q", got, want)
	}
	if sym.Extends != "" {
		t.Errorf("Extends: got %q, want empty", sym.Extends)
	}
	if sym.DocSummary != "Owns the runtime flights map; subscribes to repository changes." {
		t.Errorf("DocSummary: got %q", sym.DocSummary)
	}

	if len(sym.CtorDeps) != 2 {
		t.Fatalf("CtorDeps len: got %d, want 2 (%+v)", len(sym.CtorDeps), sym.CtorDeps)
	}
	wantDeps := []CtorDep{
		{Name: "logger", Type: `Psr\Log\LoggerInterface`, Promoted: true},
		{Name: "flightsRepository", Type: `App\Repository\FlightsRepository`, Promoted: true},
	}
	for i, want := range wantDeps {
		got := sym.CtorDeps[i]
		if got != want {
			t.Errorf("CtorDep[%d]: got %+v, want %+v", i, got, want)
		}
	}

	wantMethods := []struct {
		name string
		ret  string
		attr string
	}{
		{"start", "void", "Override"},
		{"stop", "void", "Override"},
		{"trapDebugInfo", "void", ""},
	}
	if len(sym.Methods) != len(wantMethods) {
		t.Fatalf("Methods len: got %d, want %d (%+v)", len(sym.Methods), len(wantMethods), sym.Methods)
	}
	for i, w := range wantMethods {
		got := sym.Methods[i]
		if got.Name != w.name {
			t.Errorf("Methods[%d].Name: got %q, want %q", i, got.Name, w.name)
		}
		if got.ReturnType != w.ret {
			t.Errorf("Methods[%d].ReturnType: got %q, want %q", i, got.ReturnType, w.ret)
		}
		if w.attr == "" {
			if len(got.Attributes) != 0 {
				t.Errorf("Methods[%d].Attributes: got %v, want none", i, got.Attributes)
			}
		} else {
			if len(got.Attributes) != 1 || got.Attributes[0] != w.attr {
				t.Errorf("Methods[%d].Attributes: got %v, want [%q]", i, got.Attributes, w.attr)
			}
		}
	}
}

func TestReadString_Interface(t *testing.T) {
	src := `<?php
namespace App\Contracts;

interface ServiceInterface extends \Countable, Stringable
{
    public function start(): void;
    public function stop(): void;
}
`
	sym, err := ReadString(src, "iface.php")
	if err != nil {
		t.Fatalf("ReadString: %v", err)
	}
	if sym.Kind != KindInterface {
		t.Errorf("Kind: %q", sym.Kind)
	}
	if sym.FQCN != `App\Contracts\ServiceInterface` {
		t.Errorf("FQCN: %q", sym.FQCN)
	}
	if got := strings.Join(sym.Implements, ","); got != `Countable,App\Contracts\Stringable` {
		t.Errorf("Implements (extends): got %q", got)
	}
	if len(sym.Methods) != 2 {
		t.Errorf("Methods: got %d, want 2", len(sym.Methods))
	}
}

func TestReadString_Trait(t *testing.T) {
	src := `<?php
namespace App\Mixin;

trait Loggable
{
    public function log(string $msg): void {}
}
`
	sym, err := ReadString(src, "trait.php")
	if err != nil {
		t.Fatalf("ReadString: %v", err)
	}
	if sym.Kind != KindTrait {
		t.Errorf("Kind: %q", sym.Kind)
	}
	if sym.FQCN != `App\Mixin\Loggable` {
		t.Errorf("FQCN: %q", sym.FQCN)
	}
	if len(sym.Methods) != 1 || sym.Methods[0].Name != "log" {
		t.Errorf("Methods: %+v", sym.Methods)
	}
	if got := sym.Methods[0].Params; len(got) != 1 || got[0].Name != "msg" || got[0].Type != "string" {
		t.Errorf("Params: %+v", got)
	}
}

func TestReadString_TraitUse(t *testing.T) {
	// A class composing traits via `use`, and a trait composing another trait.
	// Names resolve to FQCN through the file's use-imports / current namespace,
	// exactly like extends/implements.
	src := `<?php
namespace App\Service;

use App\Mixin\Loggable;

class Worker
{
    use Loggable;
    use \App\Mixin\Timed;

    public function run(): void {}
}
`
	sym, err := ReadString(src, "worker.php")
	if err != nil {
		t.Fatalf("ReadString: %v", err)
	}
	want := []string{`App\Mixin\Loggable`, `App\Mixin\Timed`}
	if len(sym.Uses) != len(want) {
		t.Fatalf("Uses: got %v, want %v", sym.Uses, want)
	}
	for i, w := range want {
		if sym.Uses[i] != w {
			t.Errorf("Uses[%d] = %q, want %q", i, sym.Uses[i], w)
		}
	}
}

func TestReadString_TraitComposesTrait(t *testing.T) {
	src := `<?php
namespace App\Mixin;

trait Auditable
{
    use Loggable;

    public function audit(): void {}
}
`
	sym, err := ReadString(src, "auditable.php")
	if err != nil {
		t.Fatalf("ReadString: %v", err)
	}
	if sym.Kind != KindTrait {
		t.Errorf("Kind: %q", sym.Kind)
	}
	if len(sym.Uses) != 1 || sym.Uses[0] != `App\Mixin\Loggable` {
		t.Errorf("Uses: %v", sym.Uses)
	}
}

func TestReadString_Enum(t *testing.T) {
	src := `<?php
namespace App\Enum;

enum Status: string implements \JsonSerializable
{
    case Active = 'active';
    case Inactive = 'inactive';

    public function label(): string { return $this->value; }
}
`
	sym, err := ReadString(src, "enum.php")
	if err != nil {
		t.Fatalf("ReadString: %v", err)
	}
	if sym.Kind != KindEnum {
		t.Errorf("Kind: %q", sym.Kind)
	}
	if sym.FQCN != `App\Enum\Status` {
		t.Errorf("FQCN: %q", sym.FQCN)
	}
	if got := strings.Join(sym.Implements, ","); got != "JsonSerializable" {
		t.Errorf("Implements: %q", got)
	}
	if len(sym.Methods) != 1 || sym.Methods[0].Name != "label" {
		t.Errorf("Methods: %+v", sym.Methods)
	}
	want := []EnumCase{{Name: "Active", Value: "active"}, {Name: "Inactive", Value: "inactive"}}
	if len(sym.Cases) != len(want) {
		t.Fatalf("Cases: %+v", sym.Cases)
	}
	for i, c := range want {
		if sym.Cases[i] != c {
			t.Errorf("Cases[%d] = %+v, want %+v", i, sym.Cases[i], c)
		}
	}
}

func TestReadString_PureEnum(t *testing.T) {
	src := `<?php
namespace App\Enum;

enum Suit
{
    case Hearts;
    case Spades;
}
`
	sym, err := ReadString(src, "suit.php")
	if err != nil {
		t.Fatalf("ReadString: %v", err)
	}
	want := []EnumCase{{Name: "Hearts"}, {Name: "Spades"}}
	if len(sym.Cases) != len(want) {
		t.Fatalf("Cases: %+v", sym.Cases)
	}
	for i, c := range want {
		if sym.Cases[i] != c {
			t.Errorf("Cases[%d] = %+v, want %+v (pure enum value must be empty)", i, sym.Cases[i], c)
		}
	}
}

func TestReadString_UnionAndIntersectionAndNullable(t *testing.T) {
	src := `<?php
namespace App;

use Foo\A;
use Foo\B;

class C
{
    public function exotic(A|B $either, A&B $both, ?A $maybe): A|null { return null; }
}
`
	sym, err := ReadString(src, "exotic.php")
	if err != nil {
		t.Fatalf("ReadString: %v", err)
	}
	m := sym.Methods[0]
	if got, want := m.Params[0].Type, `Foo\A|Foo\B`; got != want {
		t.Errorf("union: got %q, want %q", got, want)
	}
	if got, want := m.Params[1].Type, `Foo\A&Foo\B`; got != want {
		t.Errorf("intersection: got %q, want %q", got, want)
	}
	if got, want := m.Params[2].Type, `?Foo\A`; got != want {
		t.Errorf("nullable: got %q, want %q", got, want)
	}
	if got, want := m.ReturnType, `Foo\A|null`; got != want {
		t.Errorf("return union with null: got %q, want %q", got, want)
	}
}

func TestReadString_ExtendsResolution(t *testing.T) {
	src := `<?php
namespace App\Service;

use App\Base\AbstractService;

class Concrete extends AbstractService {}
`
	sym, err := ReadString(src, "ext.php")
	if err != nil {
		t.Fatalf("ReadString: %v", err)
	}
	if got, want := sym.Extends, `App\Base\AbstractService`; got != want {
		t.Errorf("Extends: got %q, want %q", got, want)
	}
}

func TestReadString_NonPromotedCtorArg(t *testing.T) {
	src := `<?php
namespace App;

class WithMixed
{
    public function __construct(
        string $name,
        private \Foo\Bar $promoted,
    ) {}
}
`
	sym, err := ReadString(src, "mixed.php")
	if err != nil {
		t.Fatalf("ReadString: %v", err)
	}
	if len(sym.CtorDeps) != 2 {
		t.Fatalf("CtorDeps len: %d", len(sym.CtorDeps))
	}
	if sym.CtorDeps[0].Promoted {
		t.Errorf("dep[0] should not be promoted: %+v", sym.CtorDeps[0])
	}
	if !sym.CtorDeps[1].Promoted {
		t.Errorf("dep[1] should be promoted: %+v", sym.CtorDeps[1])
	}
	if sym.CtorDeps[1].Type != `Foo\Bar` {
		t.Errorf("dep[1] type: %q", sym.CtorDeps[1].Type)
	}
}

func TestReadString_NoDeclarationReturnsError(t *testing.T) {
	src := `<?php
namespace App;

function helper(): void {}

const PI = 3.14;
`
	if _, err := ReadString(src, "fns.php"); err == nil {
		t.Fatal("expected error for no-class file, got nil")
	}
}

func TestReadString_NoDocblockYieldsEmptySummary(t *testing.T) {
	src := `<?php
namespace App;

class Bare {}
`
	sym, err := ReadString(src, "bare.php")
	if err != nil {
		t.Fatal(err)
	}
	if sym.DocSummary != "" {
		t.Errorf("DocSummary: got %q, want empty", sym.DocSummary)
	}
}

func TestReadString_MultiClassFileTakesFirst(t *testing.T) {
	src := `<?php
namespace App;

class First {}
class Second {}
`
	sym, err := ReadString(src, "multi.php")
	if err != nil {
		t.Fatal(err)
	}
	if sym.FQCN != `App\First` {
		t.Errorf("FQCN: %q (want App\\First)", sym.FQCN)
	}
}

func TestReadString_AliasedUse(t *testing.T) {
	src := `<?php
namespace App;

use Foo\Bar\Original as Aliased;

class C
{
    public function take(Aliased $x): void {}
}
`
	sym, err := ReadString(src, "alias.php")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := sym.Methods[0].Params[0].Type, `Foo\Bar\Original`; got != want {
		t.Errorf("aliased type: got %q, want %q", got, want)
	}
}
