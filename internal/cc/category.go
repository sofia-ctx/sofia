package cc

import (
	"regexp"
	"strings"
)

// Category labels for bash commands. They answer "what was the agent doing
// at the shell" — a classifier for commands instead of file paths.
const (
	CatSearch = "search" // grep/rg/find/fd — locating things
	CatRead   = "read"   // cat/head/tail/less — reading file content
	CatGit    = "git"    // version control
	CatTest   = "test"   // running test suites
	CatBuild  = "build"  // compile / install / bundle
	CatDB     = "db"     // database & migrations
	CatFS     = "fs"     // create/move/delete/inspect files
	CatOther  = "other"
)

// catRule maps a regexp against the (already lowercased) command to a
// category. Rules are evaluated in order; first match wins, so put the
// more specific patterns first.
type catRule struct {
	re  *regexp.Regexp
	cat string
}

var catRules = []catRule{
	{regexp.MustCompile(`\b(phpunit|pest|jest|vitest|pytest|go test|cargo test|rspec|mocha|playwright)\b`), CatTest},
	{regexp.MustCompile(`\b(npm|yarn|pnpm|composer) (run )?test\b`), CatTest},
	{regexp.MustCompile(`\bmake test\b|/vendor/bin/(phpunit|pest|phpstan|psalm)`), CatTest},
	{regexp.MustCompile(`\b(mysql|psql|sqlite3|mysqldump|pg_dump|mongosh|redis-cli)\b`), CatDB},
	{regexp.MustCompile(`\b(artisan|doctrine|migrate|sequelize|prisma|alembic|flyway)\b`), CatDB},
	{regexp.MustCompile(`\bgit\b`), CatGit},
	{regexp.MustCompile(`\b(go build|go install|go run|cargo build|tsc|vite build|webpack|rollup|esbuild|docker build|make\b)`), CatBuild},
	{regexp.MustCompile(`\b(npm|yarn|pnpm|composer|pip|poetry|bundle|gradle|mvn)\b`), CatBuild},
	{regexp.MustCompile(`\b(grep|rg|ag|ack|fd|find)\b`), CatSearch},
	{regexp.MustCompile(`\b(cat|head|tail|less|bat|sed|awk|jq|wc)\b`), CatRead},
	{regexp.MustCompile(`\b(mkdir|rm|mv|cp|touch|ln|chmod|chown|ls|tree|stat)\b`), CatFS},
}

// Categorize classifies a single shell command. Compound commands
// (`cd x && grep ...`) are matched against the whole string, so the most
// specific rule still wins.
func Categorize(cmd string) string {
	low := strings.ToLower(cmd)
	for _, r := range catRules {
		if r.re.MatchString(low) {
			return r.cat
		}
	}
	return CatOther
}
