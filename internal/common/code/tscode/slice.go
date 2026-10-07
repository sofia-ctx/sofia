package tscode

import (
	"fmt"
	"regexp"
	"strings"
)

// Slice extracts source ranges using lexical delimiters, not type resolution.
// Nested functions are not indexed. Unsupported or unbalanced syntax is an
// explicit error rather than an apparently complete but cut-off body.
func Slice(src []byte, symbol string) (string, []string, error) {
	return sliceScripts(string(src), symbol, false)
}

// SliceVue reads both inline script blocks, keeping source byte offsets intact.
func SliceVue(src []byte, symbol string) (string, []string, error) {
	return sliceScripts(string(src), symbol, true)
}

type sourceSymbol struct {
	name, qualified string
	start, end      int
	block           int
}

var htmlComment = regexp.MustCompile(`(?s)<!--.*?-->`)
var scriptSource = regexp.MustCompile(`\bsrc\s*=`)
var scriptLanguage = regexp.MustCompile(`\blang\s*=\s*["']([^"']+)["']`)

func sliceScripts(src, requested string, vue bool) (string, []string, error) {
	ranges := [][2]int{{0, len(src)}}
	if vue {
		ranges = nil
		masked := htmlComment.ReplaceAllStringFunc(src, func(s string) string { return strings.Repeat(" ", len(s)) })
		for _, m := range reScript.FindAllStringSubmatchIndex(masked, -1) {
			attrs := src[m[2]:m[3]]
			if scriptSource.MatchString(attrs) {
				return "", nil, fmt.Errorf("external Vue script: read the referenced source file")
			}
			if lang := scriptLanguage.FindStringSubmatch(attrs); lang != nil && lang[1] != "ts" && lang[1] != "js" && lang[1] != "javascript" && lang[1] != "typescript" {
				return "", nil, fmt.Errorf("unsupported Vue script language %q", lang[1])
			}
			ranges = append(ranges, [2]int{m[4], m[5]})
		}
	}
	var symbols []sourceSymbol
	for block, span := range ranges {
		text := src[span[0]:span[1]]
		tokens, err := lexScript(text)
		if err != nil {
			return "", nil, fmt.Errorf("cannot slice script: %w", err)
		}
		index := scriptIndex{src: text, tokens: tokens}
		index.topLevel()
		for _, s := range index.symbols {
			s.start += span[0]
			s.end += span[0]
			s.block = block
			symbols = append(symbols, s)
		}
	}
	var names []string
	seen := map[string]bool{}
	var matches []sourceSymbol
	var exact []sourceSymbol
	for _, s := range symbols {
		if !seen[s.qualified] {
			names = append(names, s.qualified)
			seen[s.qualified] = true
		}
		if requested == s.qualified || requested == s.name {
			matches = append(matches, s)
		}
		if requested == s.qualified {
			exact = append(exact, s)
		}
	}
	if len(exact) > 0 {
		matches = exact
	}
	if len(matches) == 0 {
		return "", names, fmt.Errorf("symbol %q not found in inline script declarations", requested)
	}
	var parts []string
	for _, s := range matches {
		if s.qualified != matches[0].qualified || s.block != matches[0].block {
			return "", names, fmt.Errorf("symbol %q is ambiguous; use Class.member / object.member or read the individual script block", requested)
		}
		parts = append(parts, src[s.start:s.end])
	}
	return strings.Join(parts, "\n"), names, nil
}

type scriptIndex struct {
	src     string
	tokens  []scriptToken
	symbols []sourceSymbol
}

func (p *scriptIndex) at(i int) string {
	if i < 0 || i >= len(p.tokens) {
		return ""
	}
	return p.tokens[i].text
}

func (p *scriptIndex) add(name, qualified string, start, end int) {
	if name == "" || end <= start || end > len(p.tokens) {
		return
	}
	a := p.tokens[start].start
	// Keep adjacent JSDoc and indentation, but never another declaration.
	line := strings.LastIndex(p.src[:a], "\n") + 1
	if strings.TrimSpace(p.src[line:a]) == "" {
		a = line
	}
	before := strings.TrimRight(p.src[:a], " \t\r\n")
	if strings.HasSuffix(before, "*/") {
		if doc := strings.LastIndex(before, "/**"); doc >= 0 && !strings.Contains(before[doc:len(before)-2], "*/") {
			a = doc
		}
	}
	p.symbols = append(p.symbols, sourceSymbol{name: name, qualified: qualified, start: a, end: p.tokens[end-1].end})
}

func (p *scriptIndex) prefixes(i int) int {
	for i < len(p.tokens) {
		if p.at(i) == "@" {
			i += 2 // decorator name
			for p.at(i) == "." {
				i += 2
			}
			if p.at(i) == "(" {
				i = p.tokens[i].pair + 1
			}
			continue
		}
		switch p.at(i) {
		case "export", "default", "declare", "abstract", "async", "public", "private", "protected", "static", "readonly", "override", "accessor":
			i++
		default:
			return i
		}
	}
	return i
}

func (p *scriptIndex) topLevel() {
	for i := 0; i < len(p.tokens); {
		start := i
		j := p.prefixes(i)
		kind := p.at(j)
		nameAt := j + 1
		if p.at(nameAt) == "*" {
			nameAt++
		}
		name := p.at(nameAt)
		if !scriptIdentifier(name) {
			name = ""
		}
		switch kind {
		case "function":
			if body := p.body(nameAt+1, true); body >= 0 {
				i = p.tokens[body].pair + 1
				p.add(name, name, start, i)
				continue
			}
			i = p.statementEnd(start, len(p.tokens))
			p.add(name, name, start, i)
			continue
		case "class", "interface", "enum":
			if body := p.body(nameAt+1, false); body >= 0 {
				i = p.tokens[body].pair + 1
				p.add(name, name, start, i)
				if kind == "class" && name != "" {
					p.classMembers(name, body+1, i-1)
				}
				continue
			}
		case "const", "let", "var", "type":
			i = p.statementEnd(j, len(p.tokens))
			p.add(name, name, start, i)
			if kind != "type" && name != "" {
				for k := nameAt + 1; k < i; k++ {
					if p.at(k) == "=" && p.at(k+1) == "{" {
						p.objectMembers(name, k+2, p.tokens[k+1].pair)
						break
					}
					if p.tokens[k].pair > k {
						k = p.tokens[k].pair
					}
				}
			}
			continue
		}
		// Skip unknown top-level statements as units, including their nested scopes.
		i = p.statementEnd(start, len(p.tokens))
	}
}

// body skips generic constraints, parameter defaults and structural return
// types, all of which may contain braces before the implementation body.
func (p *scriptIndex) body(i int, callable bool) int {
	angles := 0
	params := !callable
	first := i
	for ; i < len(p.tokens); i++ {
		if callable && params && angles == 0 && i > first && strings.Contains(p.src[p.tokens[i-1].end:p.tokens[i].start], "\n") && !continuesStatement(p.at(i-1), p.at(i)) {
			return -1 // semicolon-free overload/abstract signature ends here
		}
		switch p.at(i) {
		case ";":
			return -1
		case "=":
			if angles == 0 {
				return -1
			}
		case "<":
			angles++
		case ">":
			if angles > 0 {
				angles--
			}
		case "(", "[":
			if p.at(i) == "(" && angles == 0 {
				params = true
			}
			i = p.tokens[i].pair
		case "{":
			prev := p.at(i - 1)
			if angles == 0 && params && prev != ":" && prev != "|" && prev != "&" && prev != "is" && prev != "=>" && prev != "?" && prev != "extends" && prev != "keyof" && prev != "readonly" {
				return i
			}
			i = p.tokens[i].pair
		case "}":
			return -1
		}
	}
	return -1
}

// Object literals are a common module API in JS (e.g. const API = {load()}).
// Only direct members are indexed; callbacks and nested objects stay inside
// their owning property, so identical local names never become global hits.
func (p *scriptIndex) objectMembers(object string, first, limit int) {
	for i := first; i < limit; {
		start := i
		j := i
		if (p.at(j) == "async" || p.at(j) == "get" || p.at(j) == "set") && scriptIdentifier(p.at(j+1)) {
			j++
		}
		if p.at(j) == "*" {
			j++
		}
		name := p.at(j)
		// Method signatures can have commas in generic return types outside
		// parentheses. Locate their body before considering property commas.
		if p.at(j+1) == "(" || p.at(j+1) == "<" {
			if body := p.body(j+1, true); body >= 0 && body < limit {
				i = p.tokens[body].pair + 1
				p.add(name, object+"."+name, start, i)
				if p.at(i) == "," {
					i++
				}
				continue
			}
		}
		if p.at(j+1) == ":" {
			value := j + 2
			if p.at(value) == "async" {
				value++
			}
			if p.at(value) == "function" {
				if body := p.body(value+1, true); body >= 0 && body < limit {
					i = p.tokens[body].pair + 1
					p.add(name, object+"."+name, start, i)
					if p.at(i) == "," {
						i++
					}
					continue
				}
			}
			// An arrow's parameter and return-type signature may also contain
			// top-level generic commas before its => token.
			params := p.afterGenerics(value, limit)
			if p.at(params) == "(" {
				arrow := p.tokens[params].pair + 1
				if p.at(arrow) == ":" {
					for arrow++; arrow < limit && p.at(arrow) != "=>" && p.at(arrow) != ","; arrow++ {
						if end := p.afterGenerics(arrow, limit); end > arrow {
							arrow = end - 1
						} else if p.tokens[arrow].pair > arrow {
							arrow = p.tokens[arrow].pair
						}
					}
				}
				if p.at(arrow) == "=>" {
					i = arrow + 1
					for i < limit && p.at(i) != "," {
						if end := p.afterGenerics(i, limit); end > i && p.at(end) == "(" {
							i = end
						}
						if p.tokens[i].pair > i {
							i = p.tokens[i].pair
						}
						i++
					}
					p.add(name, object+"."+name, start, i)
					if p.at(i) == "," {
						i++
					}
					continue
				}
			}
		}
		i = j + 1
		for i < limit && p.at(i) != "," {
			if p.tokens[i].pair > i {
				i = p.tokens[i].pair
			}
			i++
		}
		if scriptIdentifier(name) {
			p.add(name, object+"."+name, start, i)
		}
		if p.at(i) == "," {
			i++
		}
	}
}

func (p *scriptIndex) afterGenerics(i, limit int) int {
	if p.at(i) != "<" {
		return i
	}
	depth := 0
	for j := i; j < limit; j++ {
		switch p.at(j) {
		case "<":
			depth++
		case ">":
			depth--
			if depth == 0 {
				return j + 1
			}
		case ";", "=>":
			return i
		}
		if p.tokens[j].pair > j {
			j = p.tokens[j].pair
		}
	}
	return i
}

func (p *scriptIndex) classMembers(class string, first, limit int) {
	for i := first; i < limit; {
		start := i
		j := p.prefixes(i)
		if (p.at(j) == "get" || p.at(j) == "set") && scriptIdentifier(p.at(j+1)) {
			j++
		}
		if p.at(j) == "*" {
			j++
		}
		name := p.at(j)
		if !scriptIdentifier(name) {
			i = p.statementEnd(start, limit)
			continue
		}
		next := j + 1
		if p.at(next) == "?" || p.at(next) == "!" {
			next++
		}
		if p.at(next) == "(" || p.at(next) == "<" {
			if body := p.body(next, true); body >= 0 && body < limit {
				i = p.tokens[body].pair + 1
				p.add(name, class+"."+name, start, i)
				continue
			}
		}
		i = p.statementEnd(start, limit)
		p.add(name, class+"."+name, start, i)
	}
}

// statementEnd honors balanced groups and ordinary automatic semicolon
// insertion. Continuation punctuation keeps multiline initializers together.
func (p *scriptIndex) statementEnd(start, limit int) int {
	for i := start; i < limit; i++ {
		if i > start && strings.Contains(p.src[p.tokens[i-1].end:p.tokens[i].start], "\n") && !continuesStatement(p.at(i-1), p.at(i)) {
			return i
		}
		if p.at(i) == ";" {
			return i + 1
		}
		if p.tokens[i].pair > i {
			i = p.tokens[i].pair
		}
	}
	return limit
}

func continuesStatement(prev, next string) bool {
	const operators = " = => + - * / % & | ^ ! ~ ? : . , < > ?? ?. && || ** "
	return strings.Contains(operators, " "+prev+" ") || strings.Contains(operators, " "+next+" ") || next == "(" || next == "[" || next == "{" || prev == "const" || prev == "let" || prev == "var" || prev == "return" || prev == "export" || prev == "async"
}

type scriptToken struct {
	text       string
	start, end int
	pair       int
}

type scriptLexer struct {
	src  string
	pos  int
	prev string
}

func lexScript(src string) ([]scriptToken, error) {
	l := scriptLexer{src: src}
	var tokens []scriptToken
	var stack []int
	for {
		tok, err := l.next()
		if err != nil {
			return nil, err
		}
		if tok.text == "" {
			break
		}
		index := len(tokens)
		tokens = append(tokens, tok)
		switch tok.text {
		case "(", "[", "{":
			stack = append(stack, index)
		case ")", "]", "}":
			if len(stack) == 0 || !strings.Contains("() [] {}", tokens[stack[len(stack)-1]].text+tok.text) {
				return nil, fmt.Errorf("unbalanced delimiter at byte %d", tok.start)
			}
			open := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			tokens[open].pair = index
			tokens[index].pair = open
			if tok.text == ")" && open > 0 {
				switch tokens[open-1].text {
				case "if", "while", "for", "with", "switch", "catch":
					l.prev = "return" // a regexp can begin the following statement
				}
			}
		}
	}
	if len(stack) != 0 {
		return nil, fmt.Errorf("unclosed delimiter at byte %d", tokens[stack[len(stack)-1]].start)
	}
	return tokens, nil
}

func scriptIdentifier(s string) bool {
	return len(s) > 0 && (s[0] == '_' || s[0] == '$' || s[0] == '#' || s[0] >= 128 || s[0] >= 'a' && s[0] <= 'z' || s[0] >= 'A' && s[0] <= 'Z') && s[0] != '\'' && s[0] != '"'
}

func (l *scriptLexer) next() (scriptToken, error) {
	for l.pos < len(l.src) {
		s := l.src[l.pos:]
		if strings.ContainsRune(" \t\r\n", rune(s[0])) {
			l.pos++
			continue
		}
		if strings.HasPrefix(s, "//") || l.pos == 0 && strings.HasPrefix(s, "#!") {
			if end := strings.IndexByte(s, '\n'); end >= 0 {
				l.pos += end + 1
			} else {
				l.pos = len(l.src)
			}
			continue
		}
		if strings.HasPrefix(s, "/*") {
			end := strings.Index(s[2:], "*/")
			if end < 0 {
				return scriptToken{}, fmt.Errorf("unclosed comment at byte %d", l.pos)
			}
			l.pos += end + 4
			continue
		}
		break
	}
	start := l.pos
	if start == len(l.src) {
		return scriptToken{}, nil
	}
	c := l.src[l.pos]
	l.pos++
	switch {
	case c == '\'' || c == '"':
		if err := l.quoted(c); err != nil {
			return scriptToken{}, err
		}
	case c == '`':
		if err := l.template(); err != nil {
			return scriptToken{}, err
		}
	case c == '/' && regexAfter(l.prev):
		if err := l.regexp(); err != nil {
			return scriptToken{}, err
		}
	case scriptIdentifier(string(c)) || c >= '0' && c <= '9':
		for l.pos < len(l.src) && (scriptIdentifier(string(l.src[l.pos])) || l.src[l.pos] >= '0' && l.src[l.pos] <= '9') {
			l.pos++
		}
	default:
		for _, operator := range []string{"=>", "?.", "??", "&&", "||", "**", "++", "--", "==", "!=", "<=", ">="} {
			if strings.HasPrefix(l.src[start:], operator) {
				l.pos = start + len(operator)
				break
			}
		}
		if strings.HasPrefix(l.src[start:], "</") || strings.HasPrefix(l.src[start:], "/>") {
			return scriptToken{}, fmt.Errorf("JSX bodies are not supported by symbol slicing")
		}
	}
	text := l.src[start:l.pos]
	l.prev = text
	return scriptToken{text: text, start: start, end: l.pos, pair: -1}, nil
}

func regexAfter(prev string) bool {
	return prev == "" || strings.Contains(" ( [ { = => : , ; ! ? && || ?? return throw yield case void typeof delete else do ", " "+prev+" ")
}

func (l *scriptLexer) quoted(quote byte) error {
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		l.pos++
		if c == '\\' {
			l.pos++
		} else if c == quote {
			return nil
		} else if c == '\n' || c == '\r' {
			break
		}
	}
	return fmt.Errorf("unclosed string")
}

func (l *scriptLexer) regexp() error {
	inClass := false
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		l.pos++
		switch c {
		case '\\':
			l.pos++
		case '[':
			inClass = true
		case ']':
			inClass = false
		case '/':
			if !inClass {
				for l.pos < len(l.src) && scriptIdentifier(string(l.src[l.pos])) {
					l.pos++
				}
				return nil
			}
		case '\n', '\r':
			return fmt.Errorf("unclosed regexp")
		}
	}
	return fmt.Errorf("unclosed regexp")
}

func (l *scriptLexer) template() error {
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		l.pos++
		if c == '\\' {
			l.pos++
		} else if c == '`' {
			return nil
		} else if c == '$' && l.pos < len(l.src) && l.src[l.pos] == '{' {
			l.pos++
			l.prev = "{"
			depth := 1
			for depth > 0 {
				tok, err := l.next()
				if err != nil {
					return err
				}
				switch tok.text {
				case "":
					return fmt.Errorf("unclosed template interpolation")
				case "{":
					depth++
				case "}":
					depth--
				}
			}
		}
	}
	return fmt.Errorf("unclosed template string")
}
