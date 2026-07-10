package version

import "testing"

func TestNormalizeVersion(t *testing.T) {
	cases := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{"v0.17.0", "0.17.0", true},
		{"v1.2.3", "1.2.3", true},
		{"0.17.0", "0.17.0", true}, // already stripped
		{"(devel)", "", false},     // plain go build/run of a checkout
		{"", "", false},            // no module version
	}
	for _, c := range cases {
		got, ok := normalizeVersion(c.in)
		if got != c.want || ok != c.wantOK {
			t.Errorf("normalizeVersion(%q) = (%q, %v), want (%q, %v)", c.in, got, ok, c.want, c.wantOK)
		}
	}
}
