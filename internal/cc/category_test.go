package cc

import "testing"

func TestCategorize(t *testing.T) {
	cases := map[string]string{
		"grep -rn foo .":                  CatSearch,
		"rg --files":                      CatSearch,
		"find . -name '*.go'":             CatSearch,
		"cat README.md":                   CatRead,
		"head -50 main.go | jq .":         CatRead,
		"git status":                      CatGit,
		"git commit -m x && git push":     CatGit,
		"./vendor/bin/phpunit --filter X": CatTest,
		"go test ./...":                   CatTest,
		"npm run test":                    CatTest,
		"composer install":                CatBuild,
		"go build ./...":                  CatBuild,
		"npm install && npm run build":    CatBuild,
		"mysql -u root -e 'select 1'":     CatDB,
		"php artisan migrate":             CatDB,
		"mkdir -p src && touch src/x.go":  CatFS,
		"rm -rf dist":                     CatFS,
		"export FOO=bar":                  CatOther,
		"echo hello":                      CatOther,
	}
	for cmd, want := range cases {
		if got := Categorize(cmd); got != want {
			t.Errorf("Categorize(%q) = %q, want %q", cmd, got, want)
		}
	}
}

func TestCategorizePriority(t *testing.T) {
	// A compound that both reads and tests should resolve to the more
	// specific test category (test rules precede read rules).
	if got := Categorize("cat phpunit.xml && ./vendor/bin/phpunit"); got != CatTest {
		t.Errorf("compound test cmd = %q, want %q", got, CatTest)
	}
	// git rules precede build/read so a git grep stays git.
	if got := Categorize("git log --oneline | head"); got != CatGit {
		t.Errorf("git+head = %q, want %q", got, CatGit)
	}
}
