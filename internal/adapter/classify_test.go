package adapter

import (
	"path/filepath"
	"testing"
)

func classifyCfg() Config {
	return Config{
		RootMarkers: []string{"composer.json"},
		Layers: []Layer{
			{Name: "Domain", Match: []string{"src/Domain/**"}},
			{Name: "Application", Match: []string{"src/Application/**"}},
			{Name: "Infrastructure", Match: []string{"src/Infrastructure/**"}},
		},
	}
}

func TestClassify_FirstMatch(t *testing.T) {
	cfg := classifyCfg()
	cases := map[string]string{
		"src/Domain/User.php":                   "Domain",
		"src/Application/RegisterUser.php":      "Application",
		"src/Infrastructure/UserRepository.php": "Infrastructure",
	}
	for path, want := range cases {
		if got := Classify(cfg, path); got != want {
			t.Errorf("Classify(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestClassify_Unclassified(t *testing.T) {
	if got := Classify(classifyCfg(), "tests/UserTest.php"); got != "" {
		t.Errorf("Classify(tests/…) = %q, want \"\"", got)
	}
}

func TestClassify_NormalizesPath(t *testing.T) {
	cfg := classifyCfg()
	// A leading "./" must not defeat the glob. (Backslash→slash normalization is
	// exercised on Windows, where filepath.ToSlash is not a no-op; on a POSIX
	// host a backslash is a valid filename character, not a separator.)
	if got := Classify(cfg, "./src/Domain/User.php"); got != "Domain" {
		t.Errorf("leading ./ not stripped: got %q", got)
	}
	if got := Classify(cfg, filepath.FromSlash("src/Domain/User.php")); got != "Domain" {
		t.Errorf("OS-path not normalized: got %q", got)
	}
}

// Declared order decides precedence: an earlier layer wins an overlap.
func TestClassify_DeclaredOrderWins(t *testing.T) {
	cfg := Config{
		RootMarkers: []string{"go.mod"},
		Layers: []Layer{
			{Name: "First", Match: []string{"src/**"}},
			{Name: "Second", Match: []string{"src/Domain/**"}},
		},
	}
	if got := Classify(cfg, "src/Domain/User.php"); got != "First" {
		t.Errorf("declared order should win, got %q", got)
	}
}
