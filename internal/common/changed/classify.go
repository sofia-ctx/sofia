package changed

import (
	"path/filepath"
	"strings"
)

// classify maps a path to a coarse category and language — a generic,
// cross-language classifier, enough to group a diff at a glance.
func classify(path string) (category, lang string) {
	lang = langOf(path)
	switch {
	case isTest(path):
		return "test", lang
	case strings.Contains(path, "migrations/") || strings.Contains(strings.ToLower(filepath.Base(path)), "migration"):
		return "migration", lang
	case isDocs(path):
		return "docs", lang
	case isBuild(path):
		return "build", lang
	case isConfig(path):
		return "config", lang
	case lang != "":
		return "source", lang
	default:
		return "other", lang
	}
}

func langOf(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return "go"
	case ".php":
		return "php"
	case ".ts", ".tsx":
		return "ts"
	case ".js", ".mjs", ".cjs":
		return "js"
	case ".vue":
		return "vue"
	case ".py":
		return "python"
	case ".rs":
		return "rust"
	case ".sql":
		return "sql"
	case ".css", ".scss":
		return "css"
	case ".html", ".twig":
		return "template"
	default:
		return ""
	}
}

func isTest(path string) bool {
	base := filepath.Base(path)
	if strings.HasSuffix(base, "_test.go") || strings.HasSuffix(base, "Test.php") {
		return true
	}
	if strings.Contains(base, ".test.") || strings.Contains(base, ".spec.") {
		return true
	}
	return strings.Contains(path, "/tests/") || strings.Contains(path, "/test/") || strings.HasPrefix(path, "tests/")
}

func isDocs(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".rst", ".adoc", ".txt":
		return true
	}
	return strings.Contains(path, "/docs/") || strings.HasPrefix(path, "docs/")
}

func isBuild(path string) bool {
	switch filepath.Base(path) {
	case "Makefile", "Dockerfile", "go.mod", "go.sum",
		"composer.json", "composer.lock", "package.json", "package-lock.json",
		"yarn.lock", "Cargo.toml", "Cargo.lock":
		return true
	}
	return strings.HasSuffix(path, ".lock") || strings.HasPrefix(filepath.Base(path), "Dockerfile")
}

func isConfig(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".yaml", ".yml", ".json", ".xml", ".toml", ".ini", ".env", ".neon", ".conf", ".dist":
		return true
	}
	base := filepath.Base(path)
	return strings.HasPrefix(base, ".env") || strings.Contains(path, "/config/") || strings.HasPrefix(path, "config/")
}
