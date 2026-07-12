package pack

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestRenderList(t *testing.T) {
	infos := []Info{
		{
			Description: "Acme agent pack",
			Receipt: Receipt{
				Name:    "acme",
				Plugins: []string{"widget", "deploy-tools"},
				Projects: map[string]ProjectInstall{
					"/home/u/www/myproj": {InstalledAt: time.Now()},
				},
			},
		},
	}

	var buf bytes.Buffer
	if err := RenderList(&buf, "toon", infos); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "packs[1]{name,description,plugins,projects}:") {
		t.Errorf("header missing:\n%s", out)
	}
	if !strings.Contains(out, "acme") || !strings.Contains(out, "Acme agent pack") {
		t.Errorf("row missing fields:\n%s", out)
	}

	var jsonBuf bytes.Buffer
	if err := RenderList(&jsonBuf, "json", infos); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(jsonBuf.String(), `"plugins": 2`) {
		t.Errorf("json output missing plugin count:\n%s", jsonBuf.String())
	}
}

func TestRenderInfo(t *testing.T) {
	info := Info{
		Description: "Acme agent pack",
		Receipt: Receipt{
			Name:    "acme",
			Source:  Source{URL: "git@github.com:o/r.git", Ref: "main", Commit: "abc1234567"},
			Plugins: []string{"widget"},
			Claude:  []ClaudeFile{{Dest: "/home/u/.claude/skills/my-skill/SKILL.md", SHA256: "deadbeef"}},
			Projects: map[string]ProjectInstall{
				"/home/u/www/myproj": {Files: []ProjectFile{{Dest: "AGENTS.md", SHA256: "deadbeef"}}},
			},
		},
	}

	var buf bytes.Buffer
	if err := RenderInfo(&buf, "toon", info); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"name: acme", "Acme agent pack", "git@github.com:o/r.git @ main (abc1234)",
		"plugins: widget", "claude[1]", "projects[1]",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("info toon missing %q:\n%s", want, out)
		}
	}

	var jsonBuf bytes.Buffer
	if err := RenderInfo(&jsonBuf, "json", info); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(jsonBuf.String(), `"acme"`) {
		t.Errorf("json info missing name:\n%s", jsonBuf.String())
	}
}

func TestRenderStatus(t *testing.T) {
	var buf bytes.Buffer
	if err := RenderStatus(&buf, "toon", PackStatus{Name: "acme", Ok: 14}); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(buf.String()); got != "ok (14 files)" {
		t.Errorf("clean status = %q, want %q", got, "ok (14 files)")
	}

	buf.Reset()
	if err := RenderStatus(&buf, "toon", PackStatus{Name: "acme", Ok: 11, Modified: 2, Missing: 1}); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(buf.String()); got != "2 modified, 1 missing" {
		t.Errorf("drifted status = %q, want %q", got, "2 modified, 1 missing")
	}
}
