package php

import "regexp"

// The VKCOM grammar tops out at PHP 8.1. These rewrites turn the PHP
// 8.2–8.5 declaration syntax that the 8.1 parser rejects into an equivalent
// 8.1-parseable form, preserving structure (names, signatures, members).
// Each pattern matches only syntax that is *already invalid* in 8.1, so
// valid ≤8.1 code is never touched — normalization is applied only as a
// retry after a real parse failure (see ReadString).
var (
	// PHP 8.3 typed class constants: `const TYPE NAME =` → `const NAME =`.
	// Two tokens before `=` (type + name) distinguish it from the 8.1 form
	// `const NAME =` (one token), so this never matches valid 8.1 consts.
	reTypedConst = regexp.MustCompile(`\bconst\s+[?\w\\|&]+\s+([A-Za-z_]\w*)(\s*=)`)

	// PHP 8.4 "new without parentheses": `new Foo->`/`::`/`?->` →
	// `new Foo()->` etc. (lives in method bodies — irrelevant to structure,
	// but must stay parseable so the whole file isn't rejected).
	reNewNoParens = regexp.MustCompile(`\bnew\s+(\\?[\w\\]+)\s*(\?->|->|::)`)

	// PHP 8.4 asymmetric visibility: `public private(set)` → `public`,
	// standalone `private(set)` → `public`. Applied before the property-hook
	// pass so a hook head reads as a plain `public TYPE $name {`.
	reAsymPair = regexp.MustCompile(`\b(public|protected|private)\s+(?:public|protected|private)\(set\)`)
	reAsymSolo = regexp.MustCompile(`\b(?:public|protected|private)\(set\)`)

	// PHP 8.2 DNF types: collapse a parenthesised intersection group to its
	// first member so a disjunctive-normal-form type becomes a plain 8.1
	// union — `(A&B)|C` → `A|C`, standalone `(A&B)` → `A`. The group must
	// start with a bare type name (`?`, `\`, word chars), never `$`, so this
	// matches type positions and not boolean expressions like `($a & $b)`.
	reDNFGroup = regexp.MustCompile(`\(\s*([?\\\w]+)\s*&[^)]*\)`)

	// First-class callable syntax `f(...)` / `$o->m(...)` / `Cls::m(...)`.
	// The literal `(...)` (a bare spread, no following arg) only ever appears
	// in first-class-callable position, so this never touches variadics
	// (`...$args` carries a `$`). Lives in bodies; collapse to `()`.
	reFirstClassCallable = regexp.MustCompile(`\(\s*\.\.\.\s*\)`)

	// Property-hook head: a property declaration whose body is a `{…}` block
	// instead of `;`. Anchored on a visibility/`var` keyword, then the type
	// and `$name` (no `(){};` in between, so a method's `function …(` head
	// can never match — its `(` blocks the class before any `$`), then `{`.
	rePropHookHead = regexp.MustCompile(`\b(?:public|protected|private|var)\b[^;{}()\n]*\$\w+[^;{}()\n]*\{`)
)

// normalizeModern returns a copy of src with modern PHP declaration syntax
// downgraded to an 8.1-parseable equivalent. The source file is never
// modified — the result feeds the parser only.
func normalizeModern(src []byte) []byte {
	src = reAsymPair.ReplaceAll(src, []byte("$1"))
	src = reAsymSolo.ReplaceAll(src, []byte("public"))
	src = downgradePropertyHooks(src)
	src = reTypedConst.ReplaceAll(src, []byte("const $1$2"))
	src = reNewNoParens.ReplaceAll(src, []byte("new $1()$2"))
	src = reDNFGroup.ReplaceAll(src, []byte("$1"))
	src = reFirstClassCallable.ReplaceAll(src, []byte("()"))
	return src
}

// downgradePropertyHooks rewrites PHP 8.4 property hooks
//
//	public bool $isUnassigned { get => $this->…; }
//	public string $password   { get => …; set => …; }
//
// into a plain typed property (`public bool $isUnassigned;`) so the 8.1
// grammar accepts the declaration and parsing of the rest of the class
// continues. A hook body is irrelevant to a class's declared shape — only
// the property's name/type/visibility matter downstream. The body is located
// by a string/comment-aware brace match so a `}` inside a string literal
// (e.g. `get => '}';`) does not confuse the scan.
func downgradePropertyHooks(src []byte) []byte {
	var out []byte
	i := 0
	for i < len(src) {
		loc := rePropHookHead.FindIndex(src[i:])
		if loc == nil {
			out = append(out, src[i:]...)
			break
		}
		brace := i + loc[1] - 1 // index of the head's opening '{'
		end := matchBrace(src, brace)
		if end < 0 {
			// Unbalanced (e.g. a brace inside an untracked heredoc): leave the
			// text as-is and step past this '{' so we never loop forever.
			out = append(out, src[i:brace+1]...)
			i = brace + 1
			continue
		}
		out = append(out, src[i:brace]...) // declaration head, up to the '{'
		out = append(out, ';')             // replaces the whole hook block
		i = end + 1
	}
	return out
}

// matchBrace returns the index of the '}' that closes the '{' at open, or -1
// if unbalanced. String literals and comments are skipped so their braces are
// not counted. Heredoc/nowdoc bodies are not tracked (a known, rare limit).
func matchBrace(src []byte, open int) int {
	depth := 0
	for i := open; i < len(src); {
		switch c := src[i]; c {
		case '{':
			depth++
			i++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
			i++
		case '\'', '"':
			i = skipString(src, i)
		case '/':
			switch {
			case i+1 < len(src) && src[i+1] == '/':
				i = skipLineComment(src, i)
			case i+1 < len(src) && src[i+1] == '*':
				i = skipBlockComment(src, i)
			default:
				i++
			}
		case '#':
			if i+1 < len(src) && src[i+1] == '[' { // attribute, not a comment
				i++
			} else {
				i = skipLineComment(src, i)
			}
		default:
			i++
		}
	}
	return -1
}

// skipString returns the index just past the closing quote of the string
// literal starting at i (src[i] is the opening quote). Backslash escapes are
// honoured, which covers both single- and double-quoted forms for the purpose
// of brace counting.
func skipString(src []byte, i int) int {
	q := src[i]
	for i++; i < len(src); i++ {
		switch src[i] {
		case '\\':
			i++ // skip the escaped char
		case q:
			return i + 1
		}
	}
	return i
}

// skipLineComment returns the index just past the next newline at/after i.
func skipLineComment(src []byte, i int) int {
	for ; i < len(src); i++ {
		if src[i] == '\n' {
			return i + 1
		}
	}
	return i
}

// skipBlockComment returns the index just past the closing `*/` at/after i
// (src[i:i+2] is `/*`).
func skipBlockComment(src []byte, i int) int {
	for i += 2; i+1 < len(src); i++ {
		if src[i] == '*' && src[i+1] == '/' {
			return i + 2
		}
	}
	return len(src)
}
