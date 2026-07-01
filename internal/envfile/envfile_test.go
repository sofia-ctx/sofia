package envfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingFile(t *testing.T) {
	m, err := Load(filepath.Join(t.TempDir(), "missing.env"))
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 0 {
		t.Errorf("expected empty map, got %v", m)
	}
}

func TestLoadParse(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	content := `# this is a comment
KEY=value
QUOTED="with space"
SINGLE='single quoted'

EMPTY=
TRAILING=trail_value
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	m, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"KEY":      "value",
		"QUOTED":   "with space",
		"SINGLE":   "single quoted",
		"EMPTY":    "",
		"TRAILING": "trail_value",
	}
	for k, want := range cases {
		if got := m[k]; got != want {
			t.Errorf("%s: got %q, want %q", k, got, want)
		}
	}
}

func TestSaveRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", ".env") // also tests MkdirAll
	in := map[string]string{
		"PLAIN":   "value",
		"SPACED":  "with space",
		"SPECIAL": `has"quotes#hash`,
	}
	if err := Save(path, in); err != nil {
		t.Fatal(err)
	}
	out, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range in {
		if got := out[k]; got != want {
			t.Errorf("%s roundtrip: got %q, want %q", k, got, want)
		}
	}
}

func TestResolve_UsesExisting(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	_ = Save(path, map[string]string{"FOO": "bar"})
	out, err := Resolve(path, []Field{{Key: "FOO", Required: true}})
	if err != nil {
		t.Fatal(err)
	}
	if out["FOO"] != "bar" {
		t.Errorf("expected bar, got %q", out["FOO"])
	}
}

func TestResolve_FallsBackToProcessEnv(t *testing.T) {
	t.Setenv("RESOLVE_TEST_KEY", "from_env")
	path := filepath.Join(t.TempDir(), ".env") // missing
	out, err := Resolve(path, []Field{{Key: "RESOLVE_TEST_KEY", Required: true}})
	if err != nil {
		t.Fatal(err)
	}
	if out["RESOLVE_TEST_KEY"] != "from_env" {
		t.Errorf("got %q", out["RESOLVE_TEST_KEY"])
	}
}

func TestResolve_UsesDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	out, err := Resolve(path, []Field{{Key: "ZZ_UNSET", Default: "fallback"}})
	if err != nil {
		t.Fatal(err)
	}
	if out["ZZ_UNSET"] != "fallback" {
		t.Errorf("got %q", out["ZZ_UNSET"])
	}
}

func TestResolve_ValidatorRejects(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	_ = Save(path, map[string]string{"X": "bad"})
	_, err := Resolve(path, []Field{{
		Key:       "X",
		Required:  true,
		Validator: func(v string) error { return errStr("nope") },
	}})
	if err == nil {
		t.Error("expected validator failure")
	}
}

type errStr string

func (e errStr) Error() string { return string(e) }

func TestMigrateOnce(t *testing.T) {
	t.Run("noop when current exists", func(t *testing.T) {
		dir := t.TempDir()
		current := filepath.Join(dir, "new", ".env")
		legacy := filepath.Join(dir, "old", ".env")
		if err := os.MkdirAll(filepath.Dir(current), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(current, []byte("KEEP=me\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(legacy, []byte("OVERWRITE=bad\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		MigrateOnce(current, legacy)
		got, err := os.ReadFile(current)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "KEEP=me\n" {
			t.Errorf("current overwritten: %q", string(got))
		}
	})

	t.Run("copies when current absent and legacy present", func(t *testing.T) {
		dir := t.TempDir()
		current := filepath.Join(dir, "new", "sub", ".env") // also exercises MkdirAll
		legacy := filepath.Join(dir, "old", ".env")
		if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
			t.Fatal(err)
		}
		body := []byte("KEY=value\nOTHER=x\n")
		if err := os.WriteFile(legacy, body, 0o644); err != nil {
			t.Fatal(err)
		}
		MigrateOnce(current, legacy)

		got, err := os.ReadFile(current)
		if err != nil {
			t.Fatalf("current missing after migration: %v", err)
		}
		if string(got) != string(body) {
			t.Errorf("content mismatch: got %q, want %q", string(got), string(body))
		}
		if _, err := os.Stat(legacy); err != nil {
			t.Errorf("legacy disappeared: %v", err)
		}
	})

	t.Run("noop when neither exists", func(t *testing.T) {
		dir := t.TempDir()
		current := filepath.Join(dir, "new", ".env")
		legacy := filepath.Join(dir, "old", ".env")
		MigrateOnce(current, legacy)
		if _, err := os.Stat(current); !os.IsNotExist(err) {
			t.Errorf("current was created from nothing: %v", err)
		}
	})

	t.Run("noop when legacy empty string", func(t *testing.T) {
		dir := t.TempDir()
		current := filepath.Join(dir, "new", ".env")
		MigrateOnce(current, "")
		if _, err := os.Stat(current); !os.IsNotExist(err) {
			t.Errorf("current was created with no legacy: %v", err)
		}
	})
}
