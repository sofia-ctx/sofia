package php

import "testing"

const entitySrc = `<?php
namespace App\User\Entity;

use Doctrine\ORM\Mapping as ORM;
use Symfony\Bridge\Doctrine\Types\UuidType;
use Symfony\Component\Uid\Uuid;

#[ORM\Entity]
#[ORM\Table(name: 'users')]
#[ORM\UniqueConstraint(name: 'uniq_users_email', columns: ['email'])]
class User
{
    #[ORM\Id]
    #[ORM\Column(type: UuidType::NAME, unique: true)]
    private Uuid $id;

    #[ORM\Column(length: 180, unique: true, nullable: true)]
    private string $email;

    #[ORM\Column(type: 'json')]
    private array $roles = [];

    #[ORM\Column]
    private bool $approved = false;
}
`

func TestExtractAttributesAndProperties(t *testing.T) {
	sym, err := ReadString(entitySrc, "User.php")
	if err != nil {
		t.Fatalf("ReadString: %v", err)
	}

	// class-level attributes resolved to FQCN with args
	byName := map[string]Attr{}
	for _, a := range sym.Attributes {
		byName[a.Name] = a
	}
	tbl, ok := byName[`Doctrine\ORM\Mapping\Table`]
	if !ok {
		t.Fatalf("missing Table attr; got %v", keysOf(byName))
	}
	if v, _ := tbl.Get("name"); v != "users" {
		t.Errorf("Table name = %q, want users", v)
	}
	uniq, ok := byName[`Doctrine\ORM\Mapping\UniqueConstraint`]
	if !ok {
		t.Fatal("missing UniqueConstraint attr")
	}
	if v, _ := uniq.Get("columns"); v != "[email]" {
		t.Errorf("UniqueConstraint columns = %q, want [email]", v)
	}

	// properties with type + attribute args
	props := map[string]Property{}
	for _, p := range sym.Properties {
		props[p.Name] = p
	}
	if len(props) != 4 {
		t.Fatalf("properties = %d, want 4 (%v)", len(props), keysOfProps(props))
	}

	id := props["id"]
	if id.Type != `Symfony\Component\Uid\Uuid` {
		t.Errorf("id.Type = %q, want resolved Uuid FQCN", id.Type)
	}
	if !hasAttr(id, `\Id`) {
		t.Error("id should carry an Id attribute")
	}
	col := attrOf(id, `\Column`)
	if v, _ := col.Get("type"); v != "Symfony\\Bridge\\Doctrine\\Types\\UuidType::NAME" {
		t.Errorf("id Column type = %q, want UuidType::NAME (resolved)", v)
	}

	email := attrOf(props["email"], `\Column`)
	if v, _ := email.Get("length"); v != "180" {
		t.Errorf("email length = %q, want 180", v)
	}
	if v, _ := email.Get("unique"); v != "true" {
		t.Errorf("email unique = %q, want true", v)
	}
	if v, _ := email.Get("nullable"); v != "true" {
		t.Errorf("email nullable = %q, want true", v)
	}

	roles := attrOf(props["roles"], `\Column`)
	if v, _ := roles.Get("type"); v != "json" {
		t.Errorf("roles type = %q, want json", v)
	}
	if props["roles"].Type != "array" {
		t.Errorf("roles php type = %q, want array", props["roles"].Type)
	}

	// approved: bare #[ORM\Column], no args, bool type
	if props["approved"].Type != "bool" {
		t.Errorf("approved php type = %q, want bool", props["approved"].Type)
	}
	if !hasAttr(props["approved"], `\Column`) {
		t.Error("approved should carry a Column attribute")
	}
}

const controllerSrc = `<?php
namespace App\User\Features\Me\EntryPoint\Http;

use Symfony\Component\Routing\Attribute\Route;
use Symfony\Component\Security\Http\Attribute\IsGranted;

#[IsGranted('ROLE_OWNER')]
final class MeController
{
    #[Route('/api/v1/me', name: 'api_v1_me', methods: ['GET'])]
    public function __invoke(): void {}
}
`

func TestMethodAttributesWithArgs(t *testing.T) {
	sym, err := ReadString(controllerSrc, "MeController.php")
	if err != nil {
		t.Fatalf("ReadString: %v", err)
	}
	// class-level IsGranted with positional role
	ig, ok := findAttrBySuffix(sym.Attributes, `\IsGranted`)
	if !ok {
		t.Fatal("missing class IsGranted")
	}
	if len(ig.Args) == 0 || ig.Args[0].Value != "ROLE_OWNER" {
		t.Errorf("IsGranted arg = %v, want positional ROLE_OWNER", ig.Args)
	}
	// method-level Route with args
	if len(sym.Methods) != 1 {
		t.Fatalf("methods = %d, want 1 (__invoke)", len(sym.Methods))
	}
	route, ok := findAttrBySuffix(sym.Methods[0].Attrs, `\Route`)
	if !ok {
		t.Fatal("missing method Route attr")
	}
	if route.Args[0].Value != "/api/v1/me" {
		t.Errorf("Route positional path = %q, want /api/v1/me", route.Args[0].Value)
	}
	if v, _ := route.Get("name"); v != "api_v1_me" {
		t.Errorf("Route name = %q, want api_v1_me", v)
	}
	if v, _ := route.Get("methods"); v != "[GET]" {
		t.Errorf("Route methods = %q, want [GET]", v)
	}
}

func findAttrBySuffix(attrs []Attr, suffix string) (Attr, bool) {
	for _, a := range attrs {
		if endsWith(a.Name, suffix) {
			return a, true
		}
	}
	return Attr{}, false
}

func keysOf(m map[string]Attr) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func keysOfProps(m map[string]Property) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func hasAttr(p Property, suffix string) bool {
	for _, a := range p.Attributes {
		if endsWith(a.Name, suffix) {
			return true
		}
	}
	return false
}

func attrOf(p Property, suffix string) Attr {
	for _, a := range p.Attributes {
		if endsWith(a.Name, suffix) {
			return a
		}
	}
	return Attr{}
}

func endsWith(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}
