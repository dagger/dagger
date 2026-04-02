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
   had no concrete implementors in the module's schema view. Fixed by:
   - Auto-implementing `Node` for interfaces with an `id` field in `InstallInterface`
   - Registering interfaces as their own possible types in `SchemaForView`

3. **Compile errors from interface PossibleTypes** (`functions.go`, `interface.go.tmpl`):
   The self-registration caused `Concrete()` to try creating composite literals of
   interface types. Fixed `possibleTypes()` to filter out interface-kind entries, and
   updated the template to use the function instead of the raw field.

### Commit `a1640117c` — resolve node(id:) through module-aware loader

**Root cause:** The `node(id:)` resolver replayed ID call chains on the
*current module's* dagql server. When the test module received an `ImplImpl`
ID (from a sibling `impl` module it doesn't depend on), `LoadType` tried to
resolve `Query.impl(...)` on the test module's server, which doesn't have
the `impl` field → `Query has no such field: "impl"`.

On `main`, `loadFromID` used `IDDeps` to build a server with all modules
referenced in the ID before loading. `node(id:)` was missing this.

**Fix (two parts):**

1. **`dagql/server.go` + `core/schema_build.go`** — Added a `nodeLoader` hook
   to `dagql.Server`. The Dagger core sets it in `buildSchema` to use `IDDeps`:
   get the current `Query` from context, call `query.IDDeps(ctx, id)` to collect
   all modules the ID depends on, build a server with those deps, then load
   through that server. Falls back to `srv.Load` if no query in context.

2. **`defs.go.tmpl` + `sdk/go/dagger.gen.go`** — Fixed `Load[T]` to query
   `__typename` *through the inline fragment*. Before, it compared `__typename`
   directly against the expected type name, which fails for interfaces:
   `__typename` returns the concrete type (`Impl`), not the interface name
   (`DepCustomIface`). Now the `__typename` query goes through
   `SelectNode(c.query, id, expectedType)` so the fragment only matches if the
   concrete type implements the expected interface. Empty result → node doesn't
   satisfy the type → clean error.

**Result:** `TestInterface/TestIfaceBasic/go` passes (all 32 subtests).

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
| Engine — dagql server (node loader hook) | `dagql/server.go` |
| Engine — schema building (nodeLoader setup) | `core/schema_build.go` |
| Engine — interface install + schema | `dagql/server.go` (InstallInterface) |
| Engine — interface type | `dagql/interfaces.go` |
| Engine — module schema building | `core/schema_build.go` |
| Engine — interface install | `core/interface.go` |
| Engine — type namespacing | `core/gqlformat.go` |
| Test module source | `core/integration/testdata/modules/go/ifaces/` |
| Test file | `core/integration/module_iface_test.go` |

## Architecture: How node(id:) loads cross-module IDs

```
SDK runtime (test module)
  → UnmarshalJSON: SelectNode(dag.query, id, "TestCustomIface")
  → executes: { node(id: "...") { ... on TestCustomIface { str } } }
  → dagql node(id:) resolver
    → nodeLoader hook (set in core/schema_build.go)
      → CurrentQuery(ctx) → query.IDDeps(ctx, id)
        → collects modules referenced in the ID (e.g. impl module)
      → deps.Server(ctx) → builds server with impl + test + core
      → idServer.Load(ctx, id)
        → replays ID call chain on a server that HAS the impl field
    → returns result, inline fragment scopes to interface fields
```
