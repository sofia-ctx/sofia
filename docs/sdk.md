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
| [`pkg/php`](../pkg/php) | PHP 8.5 type summaries, property hooks, DNF types and original-source slices. Native Go backend: dimasma0305/php-parser-go v0.1.1. | `php.Read(path string) (*Symbol, error)` |
| [`pkg/walker`](../pkg/walker) | Walks a directory tree with include/exclude rules, streaming paths over a channel. | `walker.Files(opts Options) (<-chan string, <-chan error)` |
| [`pkg/matcher`](../pkg/matcher) | Line-by-line search over a file — literal or regex, with word-boundary handling for non-ASCII. | `matcher.ScanFile(path string, opts Options) ([]Hit, []string, error)` |
| [`pkg/codectx`](../pkg/codectx) | Finds the nearest enclosing function/class/block around a line, without a full AST. PHP, TS, Twig, INI. | `codectx.Enclosing(lines []string, idx int, ext string) string` |
| [`pkg/toon`](../pkg/toon) | Primitives for [TOON](https://github.com/toon-format/toon) output: scalar quoting/escaping, list joining. | `toon.Scalar(s string) string` |
| [`pkg/emit`](../pkg/emit) | Enforces the "never cost more than `cat`" rule: picks whichever of a compact or raw rendering is smaller. | `emit.SmallerOf(w io.Writer, compact, raw []byte) (Result, error)`, `emit.Footer(w io.Writer, tok, rawTok int64)`, `emit.FooterFor(w, format, tok, rawTok)` (no footer after json unless `emit.KeepFooterOnJSON(true)`) |
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

## PHP parsing and recovery

`Read` and `ReadString` summarize the first named class/interface/trait/enum. Anonymous classes are skipped. Imported class names resolve through ordinary/grouped imports, aliases and namespace-relative names. Function/constant imports do not affect class names. `Properties` includes promoted constructor properties, including those in private constructors. Hook metadata records get/set, by-reference returns, modifiers, attributes, parameters and body kind (`abstract`, `expression`, `block`). `WriteVisibility` contains an explicitly declared setter visibility, not an inferred effective visibility. Modifiers use canonical order.

Supported syntax is parsed directly, preserving DNF constituents and hook bodies in the internal AST. If the original source has syntax errors, `Read` may return a summary with `Partial: true`, original `Diagnostics` and `Recovery` equal to `partial_ast`, `normalized_ast` or `source`. Partial members can be incomplete. The source-only fallback has no members and unresolved parent/interface names. Legacy normalization is isolated in `pkg/php/normalize.go`, runs only after native parse errors, and remains available for future compatibility work. Even if a normalized copy parses cleanly, the result stays partial. It must never supply source-edit offsets.

`ReadStrict` and `ReadStringStrict` reject every original-source parse error. `Slice` also requires clean original input and returns original bytes using AST positions. Selectors include a type/function/method, `Class::method`, `$property`, `Class::$property`, `$property::get` and `Class::$property::set`. Ambiguous selectors return an error and available names. These functions check parser syntax, not runtime behavior, type correctness or safe refactoring of references. The parser implementation and AST types stay outside the SDK contract.

Compatibility note for release: summary fields are additive, but promoted properties now appear in `Properties`, `Partial` covers all recovered parses, and `Slice` rejects malformed or ambiguous input previously tolerated. Consumers relying on those behaviors need migration and the version treatment required by the policy below.

Known backend limitation in v0.1.1: a heredoc/nowdoc closing label followed by a space before punctuation can be missed by the lexer. Such source may be reported as partial with indentation diagnostics, and strict reads/slices reject it. This is a parser limitation, not proof that the PHP source is invalid. The adapter does not suppress these diagnostics or silently rewrite the source. Upgrading the backend requires rerunning modern-syntax, recovery, byte-position and concurrent-reader tests.

## Semver policy

`pkg/` is sofia's public API surface: breaking a signature, a type, or an
observable behavior there is a major-version bump. `internal/` is not
covered by this — anything under `internal/` (including packages that
happen to have moved out of it in the past) can change shape between
patch releases without notice. sofia itself keeps dogfooding `pkg/` — the
`sf` binary imports these packages the same way an external consumer
would — so drift gets caught before it ships.

`walker.Files`'s two-channel shape (paths, errors) is a frozen contract:
future changes add functionality without changing that signature.
