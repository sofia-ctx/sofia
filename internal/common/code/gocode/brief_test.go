package gocode

import (
	"strings"
	"testing"
)

// TestBrief: a struct's field list/tags disappear, but its inventory row
// stays (kind+name), interface method signatures stay untouched, free
// func/method signatures are unaffected, and consts/vars collapse to bare
// names (no type column).
func TestBrief(t *testing.T) {
	g := readSample(t)
	g.Brief()

	types := map[string]GoType{}
	for _, ty := range g.Types {
		types[ty.Name] = ty
	}
	if s := types["Server"]; s.Kind != "struct" || s.Detail != "" {
		t.Errorf("Server after Brief = %+v, want struct with empty detail", s)
	}
	if c := types["ContactSource"]; c.Kind != "interface" || !strings.Contains(c.Detail, "Fetch(ctx context.Context, inn string) ([]Contact, error)") {
		t.Errorf("interface method signature should survive Brief: %+v", c)
	}
	if r := types["RegionCode"]; r.Kind != "alias" || r.Detail != "string" {
		t.Errorf("alias detail should survive Brief: %+v", r)
	}

	funcs := map[string]GoFunc{}
	for _, fn := range g.Funcs {
		funcs[fn.Name] = fn
	}
	if r := funcs["Routes"]; r.Sig != "() http.Handler" {
		t.Errorf("func signature should survive Brief: %+v", r)
	}

	if len(g.Consts) != 1 || g.Consts[0].Name != "defaultLimit" || g.Consts[0].Type != "" {
		t.Errorf("consts after Brief = %+v, want [defaultLimit] with no type", g.Consts)
	}
	if len(g.Vars) != 1 || g.Vars[0].Name != "ErrNotFound" || g.Vars[0].Type != "" {
		t.Errorf("vars after Brief = %+v, want [ErrNotFound] with no type", g.Vars)
	}
}

// TestBriefComposesExported: Brief and FilterExported apply independently —
// calling both drops unexported symbols AND the struct's field detail.
func TestBriefComposesExported(t *testing.T) {
	g := readSample(t)
	g.FilterExported()
	g.Brief()

	for _, ty := range g.Types {
		if ty.Name == "Server" && ty.Detail != "" {
			t.Errorf("Server detail should be empty after Brief: %+v", ty)
		}
	}
	for _, fn := range g.Funcs {
		if !fn.Exported {
			t.Errorf("unexported func leaked after FilterExported+Brief: %s", fn.Name)
		}
	}
}
