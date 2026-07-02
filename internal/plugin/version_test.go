package plugin

import (
	"strings"
	"testing"
)

func TestParseSemver(t *testing.T) {
	cases := []struct {
		in   string
		want semver
		ok   bool
	}{
		{"1.2.3", semver{1, 2, 3}, true},
		{"v2.0.0", semver{2, 0, 0}, true},
		{"2", semver{2, 0, 0}, true},
		{"2.1", semver{2, 1, 0}, true},
		{"1.2.0-rc1", semver{1, 2, 0}, true},
		{"1.2.0+build.7", semver{1, 2, 0}, true},
		{"", semver{}, false},
		{"x.y", semver{}, false},
		{"1.2.3.4", semver{}, false},
		{"-1.0.0", semver{}, false},
	}
	for _, c := range cases {
		got, err := parseSemver(c.in)
		if (err == nil) != c.ok {
			t.Errorf("parseSemver(%q) ok=%v, want %v (err=%v)", c.in, err == nil, c.ok, err)
			continue
		}
		if c.ok && got != c.want {
			t.Errorf("parseSemver(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

func TestSemverCompare(t *testing.T) {
	mustLess := func(a, b string) {
		av, _ := parseSemver(a)
		bv, _ := parseSemver(b)
		if av.compare(bv) >= 0 {
			t.Errorf("expected %s < %s", a, b)
		}
	}
	mustLess("1.0.0", "2.0.0")
	mustLess("1.1.0", "1.2.0")
	mustLess("1.2.3", "1.2.4")
	eq, _ := parseSemver("2")
	full, _ := parseSemver("2.0.0")
	if eq.compare(full) != 0 {
		t.Errorf("2 should equal 2.0.0")
	}
}

// The four compatibility quadrants plus the min_sf gate. hostProto is fixed at
// 2.3.0 here so a "too old" (major below the N-1 window) is representable — the
// shipped HostProtocol constant is separately exercised below.
func TestCompatible_Quadrants(t *testing.T) {
	const host = "2.3.0"

	t.Run("exactly compatible (same major)", func(t *testing.T) {
		ok, reason := Compatible("2.0.0", "", host)
		if !ok || reason != "" {
			t.Errorf("want enabled, got ok=%v reason=%q", ok, reason)
		}
	})

	t.Run("N-1 compatible (one major below)", func(t *testing.T) {
		ok, reason := Compatible("1.5.0", "", host)
		if !ok || reason != "" {
			t.Errorf("want enabled, got ok=%v reason=%q", ok, reason)
		}
	})

	t.Run("too old (below N-1 window) → disabled with reason", func(t *testing.T) {
		ok, reason := Compatible("0.9.0", "", host)
		if ok {
			t.Fatal("want disabled")
		}
		if !strings.Contains(reason, "older") || !strings.Contains(reason, "0.9.0") {
			t.Errorf("reason must state it is too old and name the version: %q", reason)
		}
	})

	t.Run("too new (higher major) → disabled with reason", func(t *testing.T) {
		ok, reason := Compatible("3.0.0", "", host)
		if ok {
			t.Fatal("want disabled")
		}
		if !strings.Contains(reason, "newer") || !strings.Contains(reason, "3.0.0") {
			t.Errorf("reason must state it is too new and name the version: %q", reason)
		}
	})
}

func TestCompatible_MinSF(t *testing.T) {
	const host = "2.3.0"

	t.Run("min_sf satisfied", func(t *testing.T) {
		ok, reason := Compatible("2.0.0", "2.3.0", host)
		if !ok || reason != "" {
			t.Errorf("want enabled, got ok=%v reason=%q", ok, reason)
		}
	})
	t.Run("min_sf too high → disabled with reason", func(t *testing.T) {
		ok, reason := Compatible("2.0.0", "2.5.0", host)
		if ok {
			t.Fatal("want disabled")
		}
		if !strings.Contains(reason, "requires host protocol >= 2.5.0") {
			t.Errorf("reason must state the required host protocol: %q", reason)
		}
	})
}

func TestCompatible_UndeclaredProtocol(t *testing.T) {
	ok, reason := Compatible("", "", "2.3.0")
	if ok || !strings.Contains(reason, "does not declare a protocol") {
		t.Errorf("want disabled with a missing-protocol reason, got ok=%v reason=%q", ok, reason)
	}
}

// A plugin declaring exactly the shipped host protocol is compatible.
func TestCompatible_ShippedHostProtocol(t *testing.T) {
	if ok, reason := Compatible(HostProtocol, "", HostProtocol); !ok {
		t.Errorf("a plugin at the host protocol must be compatible, got %q", reason)
	}
}
