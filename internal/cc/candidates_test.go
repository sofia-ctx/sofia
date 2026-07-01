package cc

import "testing"

func TestDedupBash(t *testing.T) {
	cmds := []string{"git status", "git status", "grep x .", "cat f"}
	commands, summary := dedupBash(cmds)

	if len(commands) != 3 {
		t.Fatalf("unique commands = %d, want 3", len(commands))
	}
	// most frequent first
	if commands[0].Command != "git status" || commands[0].Count != 2 || commands[0].Category != CatGit {
		t.Errorf("commands[0] = %+v, want git status ×2 git", commands[0])
	}

	byCat := map[string]CategoryCount{}
	for _, c := range summary {
		byCat[c.Category] = c
	}
	if g := byCat[CatGit]; g.Calls != 2 || g.Unique != 1 {
		t.Errorf("git summary = %+v, want calls 2 unique 1", g)
	}
	if s := byCat[CatSearch]; s.Calls != 1 || s.Unique != 1 {
		t.Errorf("search summary = %+v, want calls 1 unique 1", s)
	}
}

func TestBuildCandidates(t *testing.T) {
	s1 := &Session{
		ID:               "a",
		ToolCalls:        map[string]int{"Read": 3, "Bash": 2},
		ToolResultTokens: map[string]int64{"Read": 5000, "Bash": 100},
		Bash:             []string{"git status", "git status", "grep x ."},
		Files:            []FileTouch{{Path: "a.go", Reads: 2}, {Path: "b.go", Reads: 1}},
	}
	s2 := &Session{
		ID:               "b",
		ToolCalls:        map[string]int{"Read": 1},
		ToolResultTokens: map[string]int64{"Read": 2000},
		Bash:             []string{"git status"},
		Files:            []FileTouch{{Path: "a.go", Reads: 1}},
	}

	c := buildCandidates([]*Session{s1, s2}, 2, 20)

	if c.Scanned != 2 {
		t.Errorf("Scanned = %d, want 2", c.Scanned)
	}

	// heavy_tools: Read aggregates to 4 calls / 7000 tokens and ranks first.
	if len(c.HeavyTools) == 0 || c.HeavyTools[0].Tool != "Read" {
		t.Fatalf("HeavyTools[0] = %+v, want Read first", c.HeavyTools)
	}
	if c.HeavyTools[0].Calls != 4 || c.HeavyTools[0].ResultTokens != 7000 {
		t.Errorf("Read aggregate = %+v, want calls 4 tokens 7000", c.HeavyTools[0])
	}
	if c.HeavyTools[0].Suggestion == "" {
		t.Error("Read should carry a suggestion")
	}

	// repeated_commands: only "git status" (×3 across 2 sessions); grep excluded by min-count.
	if len(c.RepeatedCommands) != 1 {
		t.Fatalf("RepeatedCommands = %d, want 1 (%+v)", len(c.RepeatedCommands), c.RepeatedCommands)
	}
	rc := c.RepeatedCommands[0]
	if rc.Command != "git status" || rc.Count != 3 || rc.Sessions != 2 || rc.Category != CatGit {
		t.Errorf("RepeatedCommands[0] = %+v, want git status ×3 in 2 sessions", rc)
	}

	// repeated_reads: only a.go (3 reads across 2 sessions).
	if len(c.RepeatedReads) != 1 {
		t.Fatalf("RepeatedReads = %d, want 1 (%+v)", len(c.RepeatedReads), c.RepeatedReads)
	}
	if rr := c.RepeatedReads[0]; rr.Path != "a.go" || rr.Reads != 3 || rr.Sessions != 2 {
		t.Errorf("RepeatedReads[0] = %+v, want a.go 3 reads 2 sessions", rr)
	}
}
