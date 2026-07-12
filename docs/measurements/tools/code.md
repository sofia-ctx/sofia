# Token economy: `sf code`

**Status: measured** (on real Go files from a private Go service).

## Methodology

- Heuristic — `internal/tokens.Estimate`.
- Baseline — `cat`-ing a `.go` source file: that's how a model normally
  learns a file's API/shape (types, signatures, fields), and it does this
  **repeatedly** — session analysis showed the same files re-read 5–14
  times.
- `sf code` — a structural summary via the stdlib `go/parser` (syntax-only,
  no build or type-check): package, imports, types (struct fields + tags,
  interface methods, alias/defined types), function/method signatures,
  const/var — **without function bodies**.

## Scenario: "what's in this file — types, signatures, API"

| File (private Go service) | lines | `cat` | `sf code` | ratio |
|---|---|---|---|---|
| `internal/server/server.go` | 686 | 4894 | **613** | **8.0×** |
| `cmd/importer/main.go` | 630 | 6939 | **302** | **23×** |
| `internal/contacts/client.go` | 347 | 2550 | **405** | **6.3×** |
| `internal/db/repo/companies.go` | 323 | 3370 | **304** | **11.1×** |

`--exported` (public API only) is even more compact: `server.go` →
**204** tokens (≈24×).

The ratio grows with how much of a file is "body": `main.go` is almost
entirely cobra-command wiring (huge bodies, tiny structure) → 23×. Factoring
in re-reads, the effect compounds: across two sessions on this project, these
four files were read via `cat` for **~143k tokens** total; via `sf code` that
would be ~17k (≈8×).

## What it returns

- `package`, `imports` (with aliases);
- `types{kind,name,detail}` — struct (fields + tags), interface (method
  signatures), `alias` (`type X = Y`), `defined` (`type X Y`);
- `funcs{recv,name,sig}` — free functions and methods (with receiver),
  signatures without bodies;
- `consts`, `vars`.

## PHP (`.php`)

Backed by the shared `internal/common/php` (VKCOM AST). The VKCOM grammar is
pinned at 8.1, so before parsing, any 8.2–8.5 syntax it doesn't accept is
normalised down to an 8.1-equivalent form: typed constants (8.3), asymmetric
visibility and parenless `new` (8.4), **property hooks** (8.4,
`public T $x { get => … }` → `public T $x;`), DNF types (8.2, `(A&B)|C` →
`A|C`), first-class callables (`f(...)` → `f()`). A retry is accepted under
a "more recovered members" rule (not "fewer errors"), so a normalisation
that saved the whole class isn't discarded over residual errors inside
method bodies. If that still doesn't work, it degrades to a partial
regex-based skeleton (namespace/kind/FQCN/extends) rather than a hard error.
Residual errors **inside** method bodies are tolerated (the structure of the
declarations themselves stays intact).

**PHP 8.5 — no dedicated rule needed (measured).** New 8.5 syntax didn't
need a distinct normalisation rule: the pipe operator `|>` and `clone(...)`
-with live inside method **bodies** — VKCOM throws a parse error on them,
but tolerant recovery plus the "more members" rule still returns the full
surface (a method with `|>` in its body, and every member after it, with
signatures and types intact); measured on a class using
`… |> trim(...) |> strtoupper(...)`: both methods recovered, types intact.
Declaration-level 8.5 syntax (closures/first-class callables in const
expressions, attributes on constants, `#[\NoDiscard]`) is accepted natively
by the 8.1 grammar (0 errors). Adding a downgrade rule for `|>` would have
been dead code (YAGNI) — this is pinned down by a regression test
(`TestNormalizeModern`, "pipe operator (8.5)"), not a normalisation rule.

**Reliability measurement (property hooks).** Before the fix, 8 of 25 entity
files in a production Symfony/Doctrine codebase (`Deal`, `User`, `Task`,
`Contact`, `PlayerProfile`, …) used property hooks (8.4) and **silently
dumped the entire raw file** through `--api` (the compact-or-raw fallback) —
token bloat instead of structure; a Doctrine-schema tool built on the same
parser hard-failed on them (35% of its agent-side calls). After the fix:
**0 raw dumps, 0 parse failures** on those files (`sf code --api` returns
the surface; the schema tool returns the schema).

| File | `cat` | `sf code` | ratio |
|---|---|---|---|
| `User.php` (entity, production codebase) | 796 | **368** | **2.2×** |
| `ApproveUserController.php` (same codebase) | 357 | **147** | **2.4×** |
| `RegisterHandler.php` (same codebase) | 350 | **141** | **2.5×** |
| `BuildFactoryService.php` (business logic, separate legacy PHP codebase) | 2080 | **104** | **20×** |

The ratio tracks how much of the file is "body": entities/handlers are dense
(little body → 2–2.5×), services/controllers with real logic run much
higher. It returns: namespace, kind, extends/implements, **class
attributes** (including `#[ORM\…]`, `#[Route]`, `#[IsGranted]` with their
arguments), constructor dependencies, properties (visibility/type/
attributes), **enum cases** (name/value — value is empty for a pure enum),
method signatures.

### `--api` — the effective public surface (traits + inheritance)

`sf code <Class>.php` on its own shows only **locally declared** methods.
When a class's public API is spread across traits (`use`) and parents
(`extends`), that's not enough: a fluent-assertions helper trait library
shows one method, `for`, locally — the other 53 live across 7 traits. To
find out what's actually callable, a model would otherwise do an expensive
discovery dance: `find vendor/.../src -name '*.php'` plus grep (which
returns bare names **without signatures**, inviting hallucinated methods),
or `cat` every trait and parent.

`--api` expands the surface in one call: own methods plus the methods of
every `use`-d trait (recursively) plus everything inherited via the
`extends` chain, deduped by PHP's own precedence (own > trait > parent),
with a `via` column naming each method's source. `--api` implies
public-only.

| Target | baseline (`cat` of the relevant context) | `--api` | ratio |
|---|---|---|---|
| A fluent-assertions trait library (7 traits) | 10,266 (`cat` of 7 traits) | **992** (54 methods) | **≈10.3×** |
| Symfony `ListCommand` (`extends Command`) | 6,307 (`cat` class+parent) | **635** (34 methods) | **≈9.9×** |

Trait/parent file resolution follows PSR-4: first from the class's own
namespace (covers package-local siblings without reading anything extra),
then via `vendor/composer/autoload_psr4.php` (cross-package parents).
Anything unresolvable (PHP built-in classes, non-PSR-4 legacy code) becomes
a marker line, `# unresolved: …`, not an error.

The cheap path advertises the full one: `--exported` on a class with traits
or a parent adds a one-line hint, `# +api: traits(…) extends(…) — re-run
with --api`.

## TS/Vue (`.ts` / `.tsx` / `.vue`)

There's no good pure-Go TS parser, so the extractor is line/block-based
(regex), and honestly approximate: imports, top-level declarations
(`const`/`function`/`class`), **members** of `interface`/`type`/`enum`
(name: type), and for `.vue` — the component name,
`defineProps`/`defineEmits`/`defineModel`, stores it uses (`useXStore`) and
API calls (`client.*`/axios), and the components referenced from
`<template>`.

| File (production Vue frontend) | `cat` | `sf code` | ratio |
|---|---|---|---|
| `views/ProductsView.vue` | 8,235 | **600** | **~13.7×** |
| `api/types.ts` (35 interfaces) | 2,280 | **354** | **~6.4×** |

Extending the digest to TS/Vue eliminated hot re-reads of this frontend (per
`sf cc candidates`: `ProductsView.vue` was re-read ×27, `api/types.ts` ×10
across sessions).

## Architecture (router + per-language libraries + multiple files)

`sf code` is a thin router (`internal/common/code`): dispatch by extension
to per-language libraries under `code/{gocode,phpcode,pycode,tscode}` (each tested
in isolation), a **parallel** run across multiple files, and aggregation.
The compact-or-raw invariant, call logging, and slice mode all live in the
router. Multiple files per call:
`sf code a.go b.php c.vue` (output order matches argument order).

## Boundary of applicability

### Boundary update (2026-07-03)

Below **8192 B** the tool now returns the raw file by design (behind a
one-line `# raw: …` header) instead of a summary: the project's own A/B
measured structural round-trips **losing** to a plain read on small files —
[`2026-07-02-t1-composer.md`](https://github.com/sofia-ctx/evaluation/blob/main/results/2026-07-02-t1-composer.md)
(7.0 KB single file: +29% $, +45% tokens) and
[`2026-07-02-t2-pricing.md`](https://github.com/sofia-ctx/evaluation/blob/main/results/2026-07-02-t2-pricing.md)
(2.9 KB single file: +9% $, +29% tokens). `SOFIA_CODE_RAW_BELOW` moves the
threshold (0 disables). The ratios measured above are unaffected — those
files are all well past this floor.

- **Structure of one file, not its logic.** Function bodies,
  implementation, the values of complex expressions aren't shown — for
  those you need the file itself.
- **Go** — `go/parser`, syntax-only (works even on a file that doesn't
  compile); doesn't resolve types across files or build a "where is this
  used" view.
- **PHP** — VKCOM plus 8.2–8.5 normalisation (property hooks, DNF types,
  first-class callables, typed constants, asymmetric visibility, parenless
  `new`); anything it can't parse degrades to a regex skeleton (`Partial`)
  rather than an error. Public methods only, same as project-specific
  structural tools built on the same parser; `--exported` also filters
  properties down to public. Known limitation: a heredoc/nowdoc inside a
  property hook's body isn't tracked by the brace matcher (a rare case).
- **`--api`** — expands methods (not properties) from traits and parents;
  resolution is PSR-4 (derived namespace plus `autoload_psr4.php`); a
  classmap/legacy autoload isn't parsed in v1, so such a parent shows as
  `unresolved`. Trait adaptations (`insteadof`/`as`) aren't modelled — the
  original method set is used as-is.
- **TS/Vue** — line-based, not an AST: multi-line `defineProps`,
  re-exports, and unusual formatting can be missed. Accurate TS parsing
  would need an external parser (ruled out to avoid a CGO dependency).

## Reproduce

```
sf code internal/server/server.go
sf code src/User/Entity/User.php --exported   # public API only
sf code vendor/acme/lib/src/FluentThing.php --api  # full surface: traits+inheritance
sf history --tool code --stats
```
