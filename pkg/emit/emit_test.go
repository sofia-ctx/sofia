package emit

import (
	"bytes"
	"testing"
)

func TestSmallerOfPicksRaw(t *testing.T) {
	var buf bytes.Buffer
	compact := []byte("this compact summary is in fact longer than the raw output")
	raw := []byte("short raw")
	res, err := SmallerOf(&buf, compact, raw)
	if err != nil {
		t.Fatal(err)
	}
	if !res.UsedRaw {
		t.Errorf("expected raw to win (it is smaller); res=%+v", res)
	}
	if buf.String() != "short raw" {
		t.Errorf("wrote %q, want raw", buf.String())
	}
}

func TestSmallerOfPicksCompact(t *testing.T) {
	var buf bytes.Buffer
	compact := []byte("tiny")
	raw := []byte("a much much much longer raw output than the compact form is")
	res, err := SmallerOf(&buf, compact, raw)
	if err != nil {
		t.Fatal(err)
	}
	if res.UsedRaw {
		t.Errorf("expected compact to win; res=%+v", res)
	}
	if buf.String() != "tiny" {
		t.Errorf("wrote %q, want compact", buf.String())
	}
}

func TestSmallerOfNilRawEmitsCompact(t *testing.T) {
	var buf bytes.Buffer
	res, err := SmallerOf(&buf, []byte("anything"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.UsedRaw || buf.String() != "anything" {
		t.Errorf("nil raw must emit compact; got %q res=%+v", buf.String(), res)
	}
}

// TestFooter pins the exact one-line formats: the three-field form when a
// raw comparison saves something, the passthrough note when it doesn't
// (never a negative "saved"), and the plain form without a raw baseline.
func TestFooter(t *testing.T) {
	for _, tt := range []struct {
		name string
		tok  int64
		raw  int64
		want string
	}{
		{"raw baseline with savings", 612, 3120, "# sf ≈612 tok · raw ≈3120 · saved ≈2508\n"},
		{"no raw baseline", 612, 0, "# sf ≈612 tok\n"},
		{"raw not cheaper — no negative saved", 1980, 1500, "# sf ≈1980 tok · raw passthrough\n"},
		{"exact tie counts as passthrough", 100, 100, "# sf ≈100 tok · raw passthrough\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SOFIA_FOOTER", "")
			var buf bytes.Buffer
			Footer(&buf, tt.tok, tt.raw)
			if buf.String() != tt.want {
				t.Errorf("Footer(%d, %d) = %q, want %q", tt.tok, tt.raw, buf.String(), tt.want)
			}
		})
	}
}

func TestFooterOff(t *testing.T) {
	t.Setenv("SOFIA_FOOTER", "off")
	var buf bytes.Buffer
	Footer(&buf, 10, 100)
	if buf.Len() != 0 {
		t.Errorf("SOFIA_FOOTER=off must suppress the footer, got %q", buf.String())
	}
}
