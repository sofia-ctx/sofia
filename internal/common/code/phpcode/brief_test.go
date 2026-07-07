package phpcode

import (
	"testing"

	"github.com/sofia-ctx/sofia/internal/common/php"
)

const briefClassSrc = `<?php
class Widget
{
    public string $name;
    private int $count = 0;

    public function __construct(private LoggerInterface $log) {}

    public function rename(string $name): void
    {
        $this->name = $name;
    }
}
`

// TestBrief: properties disappear entirely, but the class name/kind, ctor
// deps, and method signatures survive — those are already signature-level.
func TestBrief(t *testing.T) {
	s, err := php.ReadString(briefClassSrc, "Widget.php")
	if err != nil {
		t.Fatalf("php.ReadString: %v", err)
	}
	if len(s.Properties) == 0 {
		t.Fatal("fixture should declare properties before Brief")
	}
	Brief(s)

	if len(s.Properties) != 0 {
		t.Errorf("properties should be gone after Brief, got %+v", s.Properties)
	}
	if s.Kind != php.KindClass || s.FQCN == "" {
		t.Errorf("class kind/name should survive Brief: %+v", s)
	}
	if len(s.CtorDeps) != 1 || s.CtorDeps[0].Type != "LoggerInterface" {
		t.Errorf("ctor deps should survive Brief: %+v", s.CtorDeps)
	}
	if len(s.Methods) != 1 || s.Methods[0].Name != "rename" {
		t.Errorf("method signatures should survive Brief: %+v", s.Methods)
	}
}

// TestBriefEnumDropsCaseValues: Brief keeps every case's name but drops its
// backing value — the enum-case equivalent of a constant's value.
func TestBriefEnumDropsCaseValues(t *testing.T) {
	s, err := php.Read("testdata/Status.php")
	if err != nil {
		t.Fatalf("php.Read: %v", err)
	}
	Brief(s)

	if len(s.Cases) != 2 {
		t.Fatalf("cases = %+v, want 2 names", s.Cases)
	}
	for _, c := range s.Cases {
		if c.Value != "" {
			t.Errorf("case %q should have its value dropped after Brief, got %q", c.Name, c.Value)
		}
		if c.Name == "" {
			t.Error("case name should survive Brief")
		}
	}
}
