// Package codectx extracts surrounding-scope context from source files
// without depending on a full AST. Tools (xref, grep, future inspectors)
// share these regex-based heuristics so the "nearest enclosing
// function/class/block" label stays consistent across the toolkit.
package codectx

import "regexp"

var (
	phpEnclosingRe = regexp.MustCompile(`(?:public|private|protected|static|final|abstract|readonly|\s)*\s*(function|class|enum|trait|interface)\s+([A-Za-z_][A-Za-z0-9_]*)`)
	tsEnclosingRe  = regexp.MustCompile(`(?:export\s+)?(?:default\s+)?(?:async\s+)?(function|class|const|enum|interface|type)\s+([A-Za-z_$][A-Za-z0-9_$]*)`)
	twigBlockRe    = regexp.MustCompile(`\{%\s*(block|macro)\s+([A-Za-z_][A-Za-z0-9_]*)`)
	iniSectionRe   = regexp.MustCompile(`^\s*\[([^\]]+)\]`)
)

// Enclosing returns the nearest enclosing scope label preceding lines[idx].
// `ext` is the lower-case file extension (".php", ".ts", ".twig", ".ini",
// ...). Returns "" when nothing recognisable is found or the extension
// isn't supported.
//
// The result is a short human-readable label like "function deleteUser",
// "class UserService", "block content", or "[section_a]" — designed to fit
// next to a file:line citation without inflating LLM token cost.
func Enclosing(lines []string, idx int, ext string) string {
	for j := idx - 1; j >= 0; j-- {
		l := lines[j]
		switch ext {
		case ".php":
			if m := phpEnclosingRe.FindStringSubmatch(l); m != nil {
				return m[1] + " " + m[2]
			}
		case ".ts", ".tsx", ".js", ".vue":
			if m := tsEnclosingRe.FindStringSubmatch(l); m != nil {
				return m[1] + " " + m[2]
			}
		case ".twig", ".tpl", ".html":
			if m := twigBlockRe.FindStringSubmatch(l); m != nil {
				return m[1] + " " + m[2]
			}
		case ".ini":
			if m := iniSectionRe.FindStringSubmatch(l); m != nil {
				return "[" + m[1] + "]"
			}
		}
	}
	return ""
}
