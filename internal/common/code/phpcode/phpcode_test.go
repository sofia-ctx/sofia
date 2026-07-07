package phpcode

import (
	"bytes"
	"strings"
	"testing"

	"github.com/sofia-ctx/sofia/internal/common/php"
)

// TestEffectiveSurface checks the flattened public surface of a class that
// composes two traits (one of which composes a third) and inherits from a
// two-level parent chain: order is own → traits → parents, dedup honours
// PHP precedence (own > trait > parent), protected members are dropped, and
// each method is tagged with the trait/parent that defines it.
func TestEffectiveSurface(t *testing.T) {
	s, err := php.Read("testdata/api/Thing.php")
	if err != nil {
		t.Fatalf("php.Read: %v", err)
	}
	methods, notes := effectiveSurface(s, "testdata/api/Thing.php")
	if len(notes) != 0 {
		t.Errorf("notes = %v, want none (all resolvable)", notes)
	}
	want := []methodVia{
		{php.Method{Name: "own"}, ""},
		{php.Method{Name: "shared"}, ""}, // Thing's own overrides TraitA + Base
		{php.Method{Name: "aa"}, "TraitA"},
		{php.Method{Name: "bb"}, "TraitB"},
		{php.Method{Name: "cc"}, "TraitC"}, // recursion: TraitC via TraitB
		{php.Method{Name: "base"}, "Base"},
		{php.Method{Name: "grand"}, "GrandBase"}, // recursion: GrandBase via Base
	}
	if len(methods) != len(want) {
		t.Fatalf("surface = %s, want %d methods", names(methods), len(want))
	}
	for i, w := range want {
		if methods[i].M.Name != w.M.Name || methods[i].Via != w.Via {
			t.Errorf("method[%d] = (%s, via=%q), want (%s, via=%q)",
				i, methods[i].M.Name, methods[i].Via, w.M.Name, w.Via)
		}
	}
	// protected Base::hidden must not leak into the public surface.
	for _, m := range methods {
		if m.M.Name == "hidden" {
			t.Error("protected method 'hidden' leaked into the surface")
		}
	}
}

func TestSummarizeAPI_TOON(t *testing.T) {
	var buf bytes.Buffer
	if _, err := Summarize(&buf, "testdata/api/Thing.php", "toon", false, true, false); err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "methods[7]{name,sig,attrs,via}:") {
		t.Errorf("missing api methods header in:\n%s", out)
	}
	if !strings.Contains(out, "aa,\"(): void\",\"\",TraitA") {
		t.Errorf("missing trait-sourced row in:\n%s", out)
	}
	if !strings.Contains(out, "grand,\"(): void\",\"\",GrandBase") {
		t.Errorf("missing inherited row in:\n%s", out)
	}
}

func TestSummarizeAPI_Unresolved(t *testing.T) {
	var buf bytes.Buffer
	if _, err := Summarize(&buf, "testdata/api/Orphan.php", "toon", false, true, false); err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "orphan,") {
		t.Errorf("own method missing in:\n%s", out)
	}
	if !strings.Contains(out, "# unresolved: extends Base (unresolved)") {
		t.Errorf("missing unresolved note in:\n%s", out)
	}
}

// Without --api the public view keeps its original shape but advertises the
// fuller surface via a single hint line (only under --exported).
func TestSummarizeExportedHint(t *testing.T) {
	var buf bytes.Buffer
	if _, err := Summarize(&buf, "testdata/api/Thing.php", "toon", true, false, false); err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, ",via}") {
		t.Errorf("non-api output must not carry the via column:\n%s", out)
	}
	if !strings.Contains(out, "# +api: traits(TraitA,TraitB) extends(Base)") {
		t.Errorf("missing api hint in:\n%s", out)
	}
}

// An enum summary must carry its cases (name + backing value) — without them
// the agent has to re-read the file to learn the allowed values.
func TestSummarizeEnum_TOON(t *testing.T) {
	var buf bytes.Buffer
	if _, err := Summarize(&buf, "testdata/Status.php", "toon", false, false, false); err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"kind: enum", "cases[2]{name,value}:", "Active,active", "Inactive,inactive"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestSummarizeEnum_Markdown(t *testing.T) {
	var buf bytes.Buffer
	if _, err := Summarize(&buf, "testdata/Status.php", "md", false, false, false); err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"## cases", "- `Active` = `active`", "- `Inactive` = `inactive`"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

// A pure enum (no backing values) drops the value column entirely — a noisy
// `Name,""` per case would only waste tokens.
func TestSummarizePureEnum_TOON(t *testing.T) {
	var buf bytes.Buffer
	if _, err := Summarize(&buf, "testdata/Suit.php", "toon", false, false, false); err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "cases[4]{name}:") {
		t.Errorf("pure enum should use single-column cases header in:\n%s", out)
	}
	for _, name := range []string{"Hearts", "Diamonds", "Clubs", "Spades"} {
		if !strings.Contains(out, "\n  "+name+"\n") { // own line, no value column
			t.Errorf("case %q should render bare (no value column) in:\n%s", name, out)
		}
	}
}

func names(ms []methodVia) string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.M.Name + "/" + m.Via
	}
	return strings.Join(out, " ")
}
