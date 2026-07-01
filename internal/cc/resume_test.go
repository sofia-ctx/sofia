package cc

import (
	"strings"
	"testing"
	"time"
)

func TestBuildBrief(t *testing.T) {
	s := &Session{
		Project:     "myapp",
		Branch:      "main",
		ID:          "abc12345",
		Cwd:         "/tmp/myapp",
		Messages:    42,
		End:         time.Now().Add(-90 * time.Second),
		UserPrompts: []string{"first request — implement feature X", "now fix the bug"},
		LastText:    "Done.\nNext: run the tests.",
		Files: []FileTouch{
			{Path: "/tmp/myapp/a.php", Reads: 5},
			{Path: "/tmp/myapp/b.php", Reads: 1, Edits: 3},
		},
	}
	b := buildBrief(s)
	if !strings.Contains(b.Goal, "implement feature X") {
		t.Errorf("goal=%q", b.Goal)
	}
	if !strings.Contains(b.Now, "fix the bug") {
		t.Errorf("now=%q", b.Now)
	}
	if !strings.Contains(b.Next, "Next") {
		t.Errorf("next=%q", b.Next)
	}
	// Edited file outranks the read-only file in the working set.
	if len(b.Files) != 2 || !strings.HasSuffix(b.Files[0].Path, "b.php") {
		t.Errorf("expected edited b.php first, got %+v", b.Files)
	}
	// Paths are relativised to cwd.
	if strings.HasPrefix(b.Files[0].Path, "/") {
		t.Errorf("path not relativised: %q", b.Files[0].Path)
	}
}

func TestBuildBriefSinglePromptOmitsNow(t *testing.T) {
	b := buildBrief(&Session{UserPrompts: []string{"only one"}})
	if b.Now != "" {
		t.Errorf("single prompt should not set 'now', got %q", b.Now)
	}
	if !strings.Contains(b.Goal, "only one") {
		t.Errorf("goal=%q", b.Goal)
	}
}
