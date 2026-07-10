package tokens

import "testing"

func TestEstimate_Empty(t *testing.T) {
	if got := Estimate(""); got != 0 {
		t.Errorf("Estimate(\"\") = %d, want 0", got)
	}
}

func TestEstimate_AsciiBytesOverFour(t *testing.T) {
	// 8 ASCII bytes → 2 tokens (8/4).
	if got := Estimate("12345678"); got != 2 {
		t.Errorf("got %d, want 2", got)
	}
}

func TestEstimate_RoundingHalfUp(t *testing.T) {
	// 10 ASCII bytes → 2.5 → 3 tokens.
	if got := Estimate("1234567890"); got != 3 {
		t.Errorf("got %d, want 3 (half-up rounding)", got)
	}
}

func TestEstimate_CyrillicOnePerRune(t *testing.T) {
	// 6 Cyrillic runes → 6 tokens.
	if got := Estimate("привет"); got != 6 {
		t.Errorf("got %d, want 6", got)
	}
}

func TestEstimate_Mixed(t *testing.T) {
	// 12 ASCII bytes (hello world,) → 3 tokens
	// 7 Cyrillic runes (привет!) → 7 tokens
	// total ≈ 10
	in := "hello world,привет!"
	got := Estimate(in)
	if got < 8 || got > 12 {
		t.Errorf("got %d, want around 10 (8-12 acceptable)", got)
	}
}

func TestEstimate_Monotonic(t *testing.T) {
	// Doubling input should roughly double token count (trend property
	// matters more than absolute numbers for analytics).
	a := Estimate("hello world! привет, мир!")
	b := Estimate("hello world! привет, мир!hello world! привет, мир!")
	if b < a*2-2 || b > a*2+2 {
		t.Errorf("non-monotonic doubling: %d → %d", a, b)
	}
}

func TestScan_AsciiOnly(t *testing.T) {
	ascii, runes := scan("plain ascii")
	if ascii != 11 || runes != 0 {
		t.Errorf("ascii=%d, runes=%d; want 11, 0", ascii, runes)
	}
}

func TestScan_NonAsciiOnly(t *testing.T) {
	ascii, runes := scan("Тест")
	if ascii != 0 || runes != 4 {
		t.Errorf("ascii=%d, runes=%d; want 0, 4", ascii, runes)
	}
}
