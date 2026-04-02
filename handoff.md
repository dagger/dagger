# Handoff: First-Class Interfaces + node(id:) — Go SDK Integration

## Context

See the design doc: https://gist.github.com/vito/158619f941f529244dda00b0d45a8a1c

We're on the `interfaces` branch, implementing first-class interfaces, unified
IDs, and the `node(id:)` Global Object Identification pattern. The SDK codegen
(especially Go) needs to work with these engine changes.

## What's been done

### Commit `bd961043c` — querybuilder dependency for codegen

When `querybuilder` was extracted to `github.com/dagger/querybuilder` (separate
module), `sdk/go/go.mod` wasn't updated and `ensureDaggerPackage()` only fetched
`dagger.io/dagger`. Fixed both:

- **`sdk/go/go.mod`** + **`sdk/go/go.sum`** — added `github.com/dagger/querybuilder`
- **`cmd/codegen/generator/go/generate_typedefs.go`** — `ensureDaggerPackage()`
  now also `go get`s `github.com/dagger/querybuilder`

### Commit `36a9958fc` — interface inline fragments on node(id:)

Three interrelated issues with module-defined interfaces and `node(id:)`:

1. **Schema name mismatch in module codegen** (`module_interfaces.go`):
   `XXX_GraphQLType()`, `UnmarshalJSON`, and `SelectNode` used Go source names
   (`CustomIface`) instead of schema-namespaced names (`TestCustomIface`). Added
   `gqlSchemaName()` helper that mirrors `core/gqlformat.go`'s `namespaceObject()`.

2. **PossibleFragmentSpreads validation failure** (`dagql/server.go`):
   `... on TestCustomIface` within `node(id:)` was rejected because the interface
   had no concrete implementors in the module's schema view. Fixed by
   auto-implementing `Node` for interfaces with an `id` field in
   `InstallInterface`.

3. **Compile errors from interface PossibleTypes** (`functions.go`, `interface.go.tmpl`):
   Fixed `possibleTypes()` to filter out interface-kind entries, and updated the
   template to use the function instead of the raw field.

### Commit `a1640117c` — resolve node(id:) through module-aware loader

The `node(id:)` resolver replayed ID call chains on the *current module's*
dagql server. When the test module received an `ImplImpl` ID (from a sibling
`impl` module it doesn't depend on), `LoadType` tried to resolve
`Query.impl(...)` on the test module's server → `Query has no such field: "impl"`.

On `main`, `loadFromID` used `IDDeps` to build a server with all modules
referenced in the ID before loading. `node(id:)` was missing this.

**Fix (two parts):**

1. **`dagql/server.go` + `core/schema_build.go`** — Added a `nodeLoader` hook
   to `dagql.Server`. The Dagger core sets it in `buildSchema` to use `IDDeps`:
   get the current `Query` from context, call `query.IDDeps(ctx, id)` to collect
   all modules the ID depends on, build a server with those deps, then load
   through that server.

2. **`defs.go.tmpl` + `sdk/go/dagger.gen.go`** — Fixed `Load[T]` to query
   `__typename` *through the inline fragment*. Before, it compared `__typename`
   directly against the expected type name, which fails for interfaces:
   `__typename` returns the concrete type (`Impl`), not the interface name
   (`DepCustomIface`). Now the `__typename` query goes through
   `SelectNode(c.query, id, expectedType)` so the fragment only matches if the
   concrete type implements the expected interface.

### Commit `7654a77bb` — register TypeScript interface template

The `interface.ts.gtpl` template file existed but wasn't listed in
`templateDeps` in `templates.go`, causing `template "interface" not defined`.

### Commit `8b84ebd6c` — Python SDK: node(id:) instead of loadFromID

The Python runtime used the removed `loadFooFromID` fields.

- **`sdk/python/src/dagger/client/_core.py`** — Added `inline_type` slot to
  `Field` so `to_dsl` wraps children in `DSLInlineFragment`. Added
  `Context.select_id` to build `Query.node(id:)` with the type condition.
- **`sdk/python/src/dagger/mod/_converter.py`** — Changed `dagger_type_structure`
  to call `dag._ctx.select_id` instead of `dag._select("load...FromID")`.

### Commit `5e681548a` — TypeScript SDK: node(id:) instead of loadFromID

- **`sdk/typescript/src/common/graphql/compute_query.ts`** — Added `inlineType`
  to `QueryTree` and inline fragment support to `buildQuery`.
- **`sdk/typescript/src/common/context.ts`** — Added `Context.selectNode`.
- **`cmd/codegen/generator/typescript/templates/src/method_solve.ts.gtpl`** —
  Changed codegen to emit `selectNode` instead of `loadFromID`.
- **`sdk/typescript/src/api/client.gen.ts`** — Regenerated: `loadFooFromID`
  methods now use `selectNode` internally.
- **`sdk/typescript/src/module/executor.ts`** — Interface loading uses
  `selectNode`.

### Commit `582ede9b1` — fix PossibleFragmentSpreads for interface-implements-interface

The GraphQL spec (§5.5.2.3) says `... on A` is valid in a `B` context when
interface A declares `implements B`, because any concrete type implementing A
must also implement B. Both gqlparser and Python's graphql-core only check
possibleTypes overlap, which fails when an interface has no concrete
implementors in the current schema view (the implementors live in other
modules).

- **`dagql/server.go`** — Replaced gqlparser's `PossibleFragmentSpreadsRule`
  with one that also checks the `implements` relationship for
  interface-on-interface spreads.
- **`sdk/python/src/dagger/client/_session.py`** — Disabled client-side query
  validation since graphql-core has the same bug and the server validates
  correctly.

## Tests status

| Test | Status |
|------|--------|
| `TestInterface/TestIfaceBasic/go` | ✅ PASS (all 32 subtests) |
| `TestInterface/TestIfaceBasic/typescript` | ✅ PASS (all subtests) |
| `TestInterface/TestIfaceBasic/python` | ✅ PASS (all subtests) |
| Other `TestInterface/` subtests | ❓ Not yet checked |

## Key files

| Area | Files |
|------|-------|
| Go codegen — module interface impl | `cmd/codegen/generator/go/templates/module_interfaces.go` |
| Go codegen — template functions | `cmd/codegen/generator/go/templates/functions.go` |
| Go codegen — interface template | `cmd/codegen/generator/go/templates/src/_types/interface.go.tmpl` |
| Go codegen — object template (AsFoo) | `cmd/codegen/generator/go/templates/src/_types/object.go.tmpl` |
| Go codegen — defs template (SelectNode, Load) | `cmd/codegen/generator/go/templates/src/_dagger.gen.go/defs.go.tmpl` |
| TS codegen — method template | `cmd/codegen/generator/typescript/templates/src/method_solve.ts.gtpl` |
| Engine — dagql server (node loader, validation) | `dagql/server.go` |
| Engine — schema building (nodeLoader setup) | `core/schema_build.go` |
| Engine — interface type | `dagql/interfaces.go` |
| Engine — interface install | `core/interface.go` |
| Engine — type namespacing | `core/gqlformat.go` |
| Python SDK — query builder (inline fragments) | `sdk/python/src/dagger/client/_core.py` |
| Python SDK — module converter | `sdk/python/src/dagger/mod/_converter.py` |
| Python SDK — session (validation) | `sdk/python/src/dagger/client/_session.py` |
| TS SDK — query builder (inline fragments) | `sdk/typescript/src/common/graphql/compute_query.ts` |
| TS SDK — context (selectNode) | `sdk/typescript/src/common/context.ts` |
| TS SDK — module executor | `sdk/typescript/src/module/executor.ts` |
| TS SDK — generated client | `sdk/typescript/src/api/client.gen.ts` |
| Test module source | `core/integration/testdata/modules/go/ifaces/` |
| Test file | `core/integration/module_iface_test.go` |

## Architecture: How node(id:) loads cross-module IDs

```
SDK runtime (test module)
  → UnmarshalJSON / select_id / selectNode
  → builds query: { node(id: "...") { ... on TestCustomIface { str } } }
  → server validates with fixed PossibleFragmentSpreads rule
    → TestCustomIface implements Node → spread is valid
  → dagql node(id:) resolver
    → nodeLoader hook (set in core/schema_build.go)
      → CurrentQuery(ctx) → query.IDDeps(ctx, id)
        → collects modules referenced in the ID (e.g. impl module)
      → deps.Server(ctx) → builds server with impl + test + core
      → idServer.Load(ctx, id)
        → replays ID call chain on a server that HAS the impl field
    → returns result, inline fragment scopes to interface fields
```
