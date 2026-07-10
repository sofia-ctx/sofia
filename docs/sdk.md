# Go SDK

`sf` is built from a handful of small, dependency-light Go packages — a PHP
symbol reader, a tree walker, a line matcher, a token estimator, and so on.
Nine of them are also useful on their own, so they're exported under `pkg/`
as a public SDK: Tier 3 of the [three-tier plugin
design](../internal/plugin/plugin.go) (Tier 1 is a declarative adapter, see
[docs/adapters.md](adapters.md); Tier 2 is a subprocess plugin, see
[docs/plugins.md](plugins.md)). Where a subprocess plugin shells out and
talks JSON, a Go SDK consumer imports these packages directly and compiles
them in.

[`sofia-ctx/pfranken`](https://github.com/sofia-ctx/pfranken) is the
reference consumer: a plugin whose PHP-parsing commands are built entirely
on this SDK.

## The packages

| Package | What it does | Entry point |
|---|---|---|
| [`pkg/php`](../pkg/php) | Reads a PHP source file into a structured symbol: namespace, FQCN, modifiers, parent/implements, methods, constructor deps, docblock. VKCOM/php-parser backend, PHP 8.2–8.5. | `php.Read(path string) (*Symbol, error)` |
| [`pkg/walker`](../pkg/walker) | Walks a directory tree with include/exclude rules, streaming paths over a channel. | `walker.Files(opts Options) (<-chan string, <-chan error)` |
| [`pkg/matcher`](../pkg/matcher) | Line-by-line search over a file — literal or regex, with word-boundary handling for non-ASCII. | `matcher.ScanFile(path string, opts Options) ([]Hit, []string, error)` |
| [`pkg/codectx`](../pkg/codectx) | Finds the nearest enclosing function/class/block around a line, without a full AST. PHP, TS, Twig, INI. | `codectx.Enclosing(lines []string, idx int, ext string) string` |
| [`pkg/toon`](../pkg/toon) | Primitives for [TOON](https://github.com/toon-format/toon) output: scalar quoting/escaping, list joining. | `toon.Scalar(s string) string` |
| [`pkg/emit`](../pkg/emit) | Enforces the "never cost more than `cat`" rule: picks whichever of a compact or raw rendering is smaller. | `emit.SmallerOf(w io.Writer, compact, raw []byte) (Result, error)`, `emit.Footer(w io.Writer, tok, rawTok int64)` |
| [`pkg/tokens`](../pkg/tokens) | Sub-microsecond heuristic LLM token estimate (no tokenizer dependency). | `tokens.Estimate(s string) int64` |
| [`pkg/cliflags`](../pkg/cliflags) | Cobra helpers shared across `sf` subcommands: format flags, arg-count validators with agent-friendly hints, dir-only completion. | `cliflags.AttachFormatFlags`, `cliflags.MinArgs`, `cliflags.ExactArgsHint` |
| [`pkg/strdist`](../pkg/strdist) | Levenshtein distance and "did you mean" suggestion for typo-tolerant CLIs. | `strdist.Nearest(target string, candidates []string) (best string, ok bool)` |

Each package's doc comment is the fuller reference — `go doc
github.com/sofia-ctx/sofia/pkg/<name>` from a checkout, or read the source
directly.

## Using it

```go
import "github.com/sofia-ctx/sofia/pkg/php"

func main() {
    sym, err := php.Read("src/Domain/Order.php")
    if err != nil {
        panic(err)
    }
    fmt.Println(sym.FQCN, len(sym.Methods))
}
```

Add it the normal way:

```bash
go get github.com/sofia-ctx/sofia
```

## Semver policy

`pkg/` is sofia's public API surface: breaking a signature, a type, or an
observable behavior there is a major-version bump. `internal/` is not
covered by this — anything under `internal/` (including packages that
happen to have moved out of it in the past) can change shape between
patch releases without notice. sofia itself keeps dogfooding `pkg/` — the
`sf` binary imports these packages the same way an external consumer
would — so drift gets caught before it ships.

`php`'s visitor methods (`StmtClass`, `StmtFunction`, …) satisfy VKCOM's
AST-visitor interface and are exported only because that interface
requires it; they hang off unexported types, so nothing outside the
package can reach them. They're API noise, not API — left as-is rather
than hidden behind an internal wrapper.

`walker.Files`'s two-channel shape (paths, errors) is a frozen contract:
future changes add functionality without changing that signature.
