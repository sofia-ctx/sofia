package toon

import "testing"

func TestScalar(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", `""`},
		{"plain", "hello", "hello"},
		{"with_comma", "with,comma", `"with,comma"`},
		{"with_colon", "k:v", `"k:v"`},
		{"with_quote", `with"quote`, `"with\"quote"`},
		{"with_backslash", `a\b`, `"a\\b"`},
		{"with_newline", "a\nb", `"a\nb"`},
		{"with_tab", "a\tb", `"a\tb"`},
		{"with_cr", "a\rb", `"a\rb"`},
		{"leading_space", " leading", `" leading"`},
		{"trailing_space", "trailing ", `"trailing "`},
		{"bracket", "[x]", `"[x]"`},
		{"brace", "{x}", `"{x}"`},
		{"unicode_clean", "Технология", "Технология"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Scalar(c.in); got != c.want {
				t.Errorf("Scalar(%q) = %q; want %q", c.in, got, c.want)
			}
		})
	}
}

func TestNeedsQuote(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", true},
		{"abc", false},
		{"a,b", true},
		{"a:b", true},
		{" leading", true},
		{"trailing ", true},
		{"with\nlf", true},
		{"with\\bs", true},
		{`with"q`, true},
		{"normal", false},
	}
	for _, c := range cases {
		if got := NeedsQuote(c.in); got != c.want {
			t.Errorf("NeedsQuote(%q) = %v; want %v", c.in, got, c.want)
		}
	}
}

func TestJoinList(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want string
	}{
		{"nil", nil, ""},
		{"empty", []string{}, ""},
		{"single", []string{"a"}, "a"},
		{"pair", []string{"a", "b"}, "a,b"},
		{"with_comma", []string{"a,b", "c"}, `"a,b",c`},
		{"with_unicode", []string{"Технология", "Detection"}, "Технология,Detection"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := JoinList(c.in); got != c.want {
				t.Errorf("JoinList(%q) = %q; want %q", c.in, got, c.want)
			}
		})
	}
}
