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
