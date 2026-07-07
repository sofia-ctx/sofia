package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sofia-ctx/sofia/internal/pack"
)

func TestClassifyStaleness(t *testing.T) {
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name      string
		bin, head time.Time
		dirtyGo   bool
		want      string
	}{
		{"head newer than build → fail", base, base.Add(time.Hour), false, statusFail},
		{"build newer, clean → ok", base.Add(time.Hour), base, false, statusOK},
		{"build newer, uncommitted go → warn", base.Add(time.Hour), base, true, statusWarn},
		{"equal, clean → ok", base, base, false, statusOK},
		{"equal, dirty go → warn", base, base, true, statusWarn},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, detail := classifyStaleness(tc.bin, tc.head, tc.dirtyGo)
			if got != tc.want {
				t.Fatalf("status = %q, want %q", got, tc.want)
			}
			if detail == "" {
				t.Fatal("detail must not be empty")
			}
		})
	}
}

func TestCompareSkill(t *testing.T) {
	same := []byte("# sf-context\n")
	other := []byte("# sf-context (edited)\n")

	if status, _ := compareSkill(same, same); status != statusOK {
		t.Errorf("identical → status = %q, want %q", status, statusOK)
	}
	status, detail := compareSkill(other, same)
	if status != statusWarn {
		t.Errorf("differing → status = %q, want %q", status, statusWarn)
	}
	if detail == "" {
		t.Error("detail must not be empty")
	}
}

func TestCheckMCP(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		t.Chdir(t.TempDir())
		if c := checkMCP(); c.Status != statusWarn {
			t.Errorf("status = %q, want %q", c.Status, statusWarn)
		}
	})
	t.Run("registered", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)
		mcp := `{"mcpServers":{"sofia":{"command":"sf","args":["mcp"]}}}`
		if err := os.WriteFile(filepath.Join(dir, ".mcp.json"), []byte(mcp), 0o644); err != nil {
			t.Fatal(err)
		}
		if c := checkMCP(); c.Status != statusOK {
			t.Errorf("status = %q, want %q", c.Status, statusOK)
		}
	})
	t.Run("configured without sofia", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)
		mcp := `{"mcpServers":{"other":{"command":"foo"}}}`
		if err := os.WriteFile(filepath.Join(dir, ".mcp.json"), []byte(mcp), 0o644); err != nil {
			t.Fatal(err)
		}
		if c := checkMCP(); c.Status != statusWarn {
			t.Errorf("status = %q, want %q", c.Status, statusWarn)
		}
	})
}

func TestCheckCodex(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		t.Setenv("CODEX_HOME", filepath.Join(t.TempDir(), "nonexistent"))
		if c := checkCodex(); c.Status != statusOK {
			t.Errorf("status = %q, want %q", c.Status, statusOK)
		}
	})
	t.Run("both wired", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("CODEX_HOME", dir)
		content := "sf hook pre\n[mcp_servers.sofia]\n"
		if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if c := checkCodex(); c.Status != statusOK {
			t.Errorf("status = %q, want %q", c.Status, statusOK)
		}
	})
	t.Run("partial: hook only", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("CODEX_HOME", dir)
		if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("sf hook pre\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if c := checkCodex(); c.Status != statusWarn {
			t.Errorf("status = %q, want %q", c.Status, statusWarn)
		}
	})
	t.Run("neither", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("CODEX_HOME", dir)
		if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("model = \"gpt-5\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if c := checkCodex(); c.Status != statusWarn {
			t.Errorf("status = %q, want %q", c.Status, statusWarn)
		}
	})
}

func TestCheckPacks(t *testing.T) {
	t.Run("no packs", func(t *testing.T) {
		t.Setenv("XDG_DATA_HOME", t.TempDir())
		t.Setenv("CLAUDE_DIR", t.TempDir())
		if c := checkPacks(); c.Status != statusOK {
			t.Errorf("status = %q, want %q", c.Status, statusOK)
		}
	})

	t.Run("clean then drifted", func(t *testing.T) {
		t.Setenv("XDG_DATA_HOME", t.TempDir())
		claudeDir := t.TempDir()
		t.Setenv("CLAUDE_DIR", claudeDir)

		src := t.TempDir()
		writeTestPack(t, src, "testpack")
		if _, err := pack.Install(pack.InstallOptions{Src: src}); err != nil {
			t.Fatalf("Install: %v", err)
		}

		if c := checkPacks(); c.Status != statusOK {
			t.Errorf("fresh install: status = %q, detail %q, want %q", c.Status, c.Detail, statusOK)
		}

		skillPath := filepath.Join(claudeDir, "skills", "my-skill", "SKILL.md")
		if err := os.WriteFile(skillPath, []byte("hand-edited\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		c := checkPacks()
		if c.Status != statusWarn {
			t.Errorf("status = %q, want %q", c.Status, statusWarn)
		}
		if !strings.Contains(c.Detail, "testpack") || !strings.Contains(c.Detail, "modified") {
			t.Errorf("detail = %q, want it to name the pack and the modified count", c.Detail)
		}
	})
}

// writeTestPack writes the smallest pack.yaml that exercises drift
// detection: a single claude skill, no plugins or project files needed.
func writeTestPack(t *testing.T, dir, name string) {
	t.Helper()
	mustWriteTestFile(t, filepath.Join(dir, "pack.yaml"),
		"schema: 1\nname: "+name+"\ndescription: test pack\nclaude:\n  skills: [ { src: skills/my-skill } ]\n")
	mustWriteTestFile(t, filepath.Join(dir, "skills", "my-skill", "SKILL.md"), "# my skill\n")
}

func mustWriteTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPorcelainHasGo(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want bool
	}{
		{"modified go", " M internal/common/doctor/doctor.go\n", true},
		{"staged go", "A  cmd/common/doctor/main.go\n", true},
		{"untracked go", "?? foo/bar.go\n", true},
		{"rename to go", "R  old.txt -> new.go\n", true},
		{"rename from go to txt", "R  old.go -> new.txt\n", false},
		{"only docs/config", " M README.md\n M go.mod\n?? notes.txt\n", false},
		{"empty", "", false},
		{"short line ignored", "M\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := porcelainHasGo(tc.out); got != tc.want {
				t.Fatalf("porcelainHasGo(%q) = %v, want %v", tc.out, got, tc.want)
			}
		})
	}
}
