# Token economy: `sf vue routes`

**Status: measured** (on a production frontend, vue-router 5).

## Methodology

- Heuristic — `internal/tokens.Estimate`.
- Baseline — reading `router/index.ts` in full to learn the route map
  (path/name/component/meta), plus mentally resolving nested `children`.
- `sf vue routes` — parses `createRouter({ routes: [...] })` (bracket
  matching) into a flat table with fully resolved paths.

## Scenario: "the frontend's route map"

| | `cat router/index.ts` | `sf vue routes` |
|---|---|---|
| tokens | 1,033 | **165** |
| full paths (children→parent) | by hand | ✅ |
| name / component / meta | mixed in with code | ✅ columns |

Against reading the file — **~6.3×**. Per `sf cc candidates`,
`router/index.ts` was re-read ×8 across sessions on that project — a direct
sink.

## What it returns

`routes[N]{path,name,component,meta}` — the path (nested `children` folded
onto the parent; an index route `''` becomes the parent's own path), name,
component name (from `() => import('…')`), and a compact `meta`. The file
is found via `**/router/index.ts` under `--root`, or given explicitly.
`--md/--json`.

## Boundary of applicability

- A text-based heuristic (there's no pure-Go TS parser): built for a
  regular router DSL (`{path,name,component,meta,children}` object
  literals). Unusual forms (computed paths, dynamically built `routes`,
  spreads) can be missed.
- Only the first `routes: [...]` is used; routes added dynamically
  (`router.addRoute`) aren't visible.

## Reproduce

```
sf vue routes --root /path/to/your/project
sf vue routes frontend/src/router/index.ts --md
sf history --tool "vue routes" --stats
```
