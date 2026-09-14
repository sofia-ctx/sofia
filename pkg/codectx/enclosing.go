// Package codectx extracts surrounding-scope context from source files
// without depending on a full AST. Tools (xref, grep, future inspectors)
// share these regex-based heuristics so the "nearest enclosing
// function/class/block" label stays consistent across the toolkit.
package codectx

import "regexp"

var (
	phpEnclosingRe = regexp.MustCompile(`(?:public|private|protected|static|final|abstract|readonly|\s)*\s*(function|class|enum|trait|interface)\s+([A-Za-z_][A-Za-z0-9_]*)`)
	tsEnclosingRe  = regexp.MustCompile(`(?:export\s+)?(?:default\s+)?(?:async\s+)?(function|class|const|enum|interface|type)\s+([A-Za-z_$][A-Za-z0-9_$]*)`)
	pyEnclosingRe  = regexp.MustCompile(`^(\s*)(?:async\s+)?(def|class)\s+([A-Za-z_][A-Za-z0-9_]*)`)
	twigBlockRe    = regexp.MustCompile(`\{%\s*(block|macro)\s+([A-Za-z_][A-Za-z0-9_]*)`)
	iniSectionRe   = regexp.MustCompile(`^\s*\[([^\]]+)\]`)
)

// Enclosing returns the nearest enclosing scope label preceding lines[idx].
// `ext` is the lower-case file extension (".php", ".ts", ".py", ".twig",
// ".ini", ...). Returns "" when nothing recognisable is found or the
// extension isn't supported.
//
// Python is the one indentation-scoped case: the label is the nearest
// preceding def/class indented *less* than the line itself, so a hit inside
// a method is attributed to that method, not to a sibling that merely
// appears earlier in the file.
//
// The result is a short human-readable label like "function deleteUser",
// "class UserService", "block content", or "[section_a]" — designed to fit
// next to a file:line citation without inflating LLM token cost.
func Enclosing(lines []string, idx int, ext string) string {
	if ext == ".py" {
		return enclosingPy(lines, idx)
	}
	for j := idx - 1; j >= 0; j-- {
		l := lines[j]
		switch ext {
		case ".php":
			if m := phpEnclosingRe.FindStringSubmatch(l); m != nil {
				return m[1] + " " + m[2]
			}
		case ".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs", ".vue":
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

// enclosingPy walks back from lines[idx] to the nearest def/class whose
// indentation is shallower than the line's own (a top-level line has no
// enclosing scope at all). idx out of range yields "".
func enclosingPy(lines []string, idx int) string {
	if idx < 0 || idx >= len(lines) {
		return ""
	}
	own := indentOf(lines[idx])
	for j := idx - 1; j >= 0; j-- {
		m := pyEnclosingRe.FindStringSubmatch(lines[j])
		if m == nil {
			continue
		}
		if len(m[1]) < own {
			return m[2] + " " + m[3]
		}
	}
	return ""
}

// indentOf counts leading whitespace columns, a tab counting as 8 like
// CPython's tokenizer does.
func indentOf(l string) int {
	n := 0
	for _, r := range l {
		switch r {
		case ' ':
			n++
		case '\t':
			n += 8
		default:
			return n
		}
	}
	return n
}
