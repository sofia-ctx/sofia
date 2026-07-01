package matcher

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "input.txt")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestScanFile_SingleMatch(t *testing.T) {
	path := writeTemp(t, "TECH_RADAR is here\nbut TECH_RADARS is broader\n")
	hits, lines, err := ScanFile(path, Options{
		Patterns:  []string{"TECH_RADAR"},
		Case:      true,
		WordBound: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(hits))
	}
	if hits[0].Line != 1 || hits[0].Pattern != "TECH_RADAR" {
		t.Errorf("hit = %+v", hits[0])
	}
	if len(lines) != 2 {
		t.Errorf("expected 2 lines, got %d", len(lines))
	}
}

func TestScanFile_WordBoundary(t *testing.T) {
	path := writeTemp(t, "USER_TECH_RADAR\nTECH_RADAR_X\nTECH_RADAR.\n_TECH_RADAR\nplain TECH_RADAR end\n")
	hits, _, err := ScanFile(path, Options{
		Patterns:  []string{"TECH_RADAR"},
		Case:      true,
		WordBound: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Only line 3 ("TECH_RADAR." → period is non-word) and line 5 ("plain TECH_RADAR end") match.
	if len(hits) != 2 {
		t.Errorf("expected 2 hits, got %d: %+v", len(hits), hits)
	}
}

func TestScanFile_UTF8WordBoundary(t *testing.T) {
	// "вижутехнологияя" — Cyrillic before & after "технология"; under the
	// byte-level boundary check this used to match (false positive).
	path := writeTemp(t, "вижутехнологияя\nтехнология обнаружения\n")
	hits, _, err := ScanFile(path, Options{
		Patterns:  []string{"технология"},
		Case:      true,
		WordBound: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Errorf("expected 1 hit (line 2 only), got %d: %+v", len(hits), hits)
	}
	if hits[0].Line != 2 {
		t.Errorf("hit should be on line 2, got %d", hits[0].Line)
	}
}

func TestScanFile_CaseInsensitive(t *testing.T) {
	path := writeTemp(t, "tech_radar\nTECH_RADAR\n")
	hits, _, err := ScanFile(path, Options{
		Patterns:  []string{"TECH_RADAR"},
		Case:      false,
		WordBound: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Errorf("expected 2 hits with case=false, got %d", len(hits))
	}
}

func TestScanFile_MultiPattern(t *testing.T) {
	path := writeTemp(t, "TECH_RADAR\nTECH_GRAVITON\nother\n")
	hits, _, err := ScanFile(path, Options{
		Patterns:  []string{"TECH_RADAR", "TECH_GRAVITON"},
		Case:      true,
		WordBound: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("expected 2 hits, got %d", len(hits))
	}
	gotPatterns := []string{hits[0].Pattern, hits[1].Pattern}
	want := map[string]bool{"TECH_RADAR": true, "TECH_GRAVITON": true}
	for _, p := range gotPatterns {
		if !want[p] {
			t.Errorf("unexpected pattern %q", p)
		}
	}
}

func TestScanFile_MultipleMatchesPerLine(t *testing.T) {
	path := writeTemp(t, "TECH_RADAR plus TECH_RADAR again\n")
	hits, _, err := ScanFile(path, Options{
		Patterns:  []string{"TECH_RADAR"},
		Case:      true,
		WordBound: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Errorf("expected 2 hits on same line, got %d", len(hits))
	}
}

func TestScanFile_EmptyPatternIgnored(t *testing.T) {
	path := writeTemp(t, "anything\n")
	hits, _, err := ScanFile(path, Options{
		Patterns:  []string{""},
		Case:      true,
		WordBound: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Errorf("expected 0 hits for empty pattern, got %d", len(hits))
	}
}

func TestScanFile_NoBoundaryAllowsSubstring(t *testing.T) {
	path := writeTemp(t, "USER_TECH_RADAR_NEW\n")
	hits, _, err := ScanFile(path, Options{
		Patterns:  []string{"TECH_RADAR"},
		Case:      true,
		WordBound: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Errorf("expected 1 hit with WordBound=false, got %d", len(hits))
	}
}

func TestScanFile_RegexMode(t *testing.T) {
	path := writeTemp(t, "foo123\nbar456\nfoo789\n")
	hits, _, err := ScanFile(path, Options{
		Patterns: []string{`foo\d+`},
		Case:     true,
		Regex:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("expected 2 regex hits, got %d", len(hits))
	}
	if hits[0].Line != 1 || hits[1].Line != 3 {
		t.Errorf("hits on wrong lines: %+v", hits)
	}
}

func TestScanFile_RegexCaseInsensitive(t *testing.T) {
	path := writeTemp(t, "FOO\nfoo\nFoo\n")
	hits, _, err := ScanFile(path, Options{
		Patterns: []string{"foo"},
		Case:     false,
		Regex:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 3 {
		t.Errorf("expected 3 case-insensitive regex hits, got %d", len(hits))
	}
}

func TestScanFile_RegexInvalid(t *testing.T) {
	path := writeTemp(t, "anything\n")
	_, _, err := ScanFile(path, Options{
		Patterns: []string{"[invalid"},
		Regex:    true,
	})
	if err == nil {
		t.Error("expected error for invalid regex")
	}
}

func TestWordBoundaryViolated(t *testing.T) {
	cases := []struct {
		s        string
		start    int
		length   int
		violated bool
	}{
		{"abc", 0, 3, false},     // both edges
		{"abc def", 0, 3, false}, // space after
		{"abc def", 4, 3, false}, // space before
		{"abcdef", 0, 3, true},   // word char after
		{"xabc", 1, 3, true},     // word char before
		{"технология X", 0, len("технология"), false},
		{"yтехнология", len("y"), len("технология"), true}, // ASCII before
	}
	for _, c := range cases {
		if got := wordBoundaryViolated(c.s, c.start, c.length); got != c.violated {
			t.Errorf("wordBoundaryViolated(%q, %d, %d) = %v, want %v",
				c.s, c.start, c.length, got, c.violated)
		}
	}
}
