package cc

import "testing"

func TestCapPromptsCapsAtMax(t *testing.T) {
	in := make([]string, maxPrompts+7)
	for i := range in {
		in[i] = "p"
	}
	shown, extra := capPrompts(in)
	if len(shown) != maxPrompts {
		t.Errorf("shown=%d, want %d", len(shown), maxPrompts)
	}
	if extra != 7 {
		t.Errorf("extra=%d, want 7", extra)
	}
}

func TestCapPromptsUnderCap(t *testing.T) {
	shown, extra := capPrompts([]string{"a", "b"})
	if len(shown) != 2 || extra != 0 {
		t.Errorf("shown=%d extra=%d, want 2/0", len(shown), extra)
	}
}

func TestCapCommands(t *testing.T) {
	mk := func(n int) []BashCmd {
		out := make([]BashCmd, n)
		for i := range out {
			out[i] = BashCmd{Command: "c"}
		}
		return out
	}
	if kept, om := capCommands(mk(50), 30); len(kept) != 30 || om != 20 {
		t.Errorf("limit 30 on 50: kept=%d omitted=%d, want 30/20", len(kept), om)
	}
	if kept, om := capCommands(mk(10), 30); len(kept) != 10 || om != 0 {
		t.Errorf("limit 30 on 10: kept=%d omitted=%d, want 10/0", len(kept), om)
	}
	if kept, om := capCommands(mk(50), 0); len(kept) != 50 || om != 0 {
		t.Errorf("limit 0 (all) on 50: kept=%d omitted=%d, want 50/0", len(kept), om)
	}
}
