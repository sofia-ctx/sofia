package adapter

import "testing"

func TestMatch(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		// ** matches everything beneath a directory.
		{"src/Domain/**", "src/Domain/User.php", true},
		{"src/Domain/**", "src/Domain/Model/User.php", true},
		{"src/Domain/**", "src/Application/x.php", false},
		// Trailing ** matches the directory itself (zero trailing segments).
		{"src/Domain/**", "src/Domain", true},
		// ** across arbitrary depth, with an intra-segment glob after it.
		{"**/*.php", "User.php", true},
		{"**/*.php", "src/User.php", true},
		{"**/*.php", "src/Domain/Model/User.php", true},
		{"**/*.php", "src/User.go", false},
		// Leading **/ before a fixed tail.
		{"**/User.php", "src/Domain/User.php", true},
		{"**/User.php", "User.php", true},
		{"**/User.php", "src/Domain/Repo.php", false},
		// ** may swallow zero middle segments.
		{"src/**/User.php", "src/User.php", true},
		{"src/**/User.php", "src/Domain/User.php", true},
		// A single * never crosses a "/".
		{"src/*", "src/User.php", true},
		{"src/*", "src/Domain/User.php", false},
		// Plain, no metacharacters.
		{"composer.json", "composer.json", true},
		{"composer.json", "src/composer.json", false},
	}
	for _, c := range cases {
		if got := Match(c.pattern, c.path); got != c.want {
			t.Errorf("Match(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}
