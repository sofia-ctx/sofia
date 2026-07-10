package strdist

import "testing"

func TestLevenshtein(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "", 3},
		{"", "abc", 3},
		{"abc", "abc", 0},
		{"Contact", "Contract", 1}, // one insertion
		{"Deals", "Deal", 1},       // one deletion
		{"kitten", "sitting", 3},   // classic
		{"Игрок", "Игра", 2},       // non-ASCII (runes, not bytes)
	}
	for _, c := range cases {
		if got := Levenshtein(c.a, c.b); got != c.want {
			t.Errorf("Levenshtein(%q,%q)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestNearest(t *testing.T) {
	ents := []string{"Contract", "Deal", "PlayerProfile", "MarketplaceListing"}
	cases := []struct {
		name     string
		target   string
		cands    []string
		wantBest string
		wantOK   bool
	}{
		{"close typo", "Contact", ents, "Contract", true}, // dist 1 ≤ ceil(7/3)=3
		{"plural", "Deals", ents, "Deal", true},           // dist 1 ≤ ceil(5/3)=2
		{"case-insensitive", "deal", ents, "Deal", true},  // dist 0
		{"too far", "Invoice", ents, "", false},           // nothing within tolerance
		{"empty target", "", ents, "", false},             //
		{"empty candidates", "Deal", nil, "", false},      //
		{"exact", "PlayerProfile", ents, "PlayerProfile", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			best, ok := Nearest(c.target, c.cands)
			if ok != c.wantOK || best != c.wantBest {
				t.Errorf("Nearest(%q)=(%q,%v) want (%q,%v)", c.target, best, ok, c.wantBest, c.wantOK)
			}
		})
	}
}
