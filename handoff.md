# Handoff: First-Class Interfaces + node(id:) — Go SDK Integration

## Context

See the design doc: https://gist.github.com/vito/158619f941f529244dda00b0d45a8a1c

We're on the `interfaces` branch, implementing first-class interfaces, unified
IDs, and the `node(id:)` Global Object Identification pattern. The SDK codegen
(especially Go) needs to work with these engine changes.

## What's been done (this session)

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

## Current blocker: `TestInterface/TestIfaceBasic/go`

**Error:** `Call: Query has no such field: "impl"`

**What's happening:** The `test` module's runtime receives an `Impl` object (from
the `impl` module) as a `TestCustomIface` interface value. When the test module's
runtime tries to reconstruct this object, the generated Go SDK code builds a query
that references the `impl` module's constructor field on the root Query — but the
`test` module's schema view doesn't include the `impl` module (it's a sibling
dependency via `caller`, not a direct dependency of `test`).

**Root cause:** The old `loadImplFromID` fields are gone, replaced by `node(id:)`.
But somewhere in the generated code path, the SDK is still constructing queries
that go through module-specific root fields rather than through `node(id:)`.

**Where to look:**

The `AsTestCustomIface()` adapter on objects does a simple local query rewrap:
```go
func (r *ImplImpl) AsTestCustomIface() TestCustomIface {
    return &_TestCustomIfaceClient{query: r.query}
}
```
This preserves the original query chain, which starts from the `impl` module's
root constructor: `Query.impl(...)`. When this query is later evaluated in the
`test` module's server context, `impl` doesn't exist as a field.

The fix likely needs to go through `node(id:)` instead of preserving the
original query chain. When crossing module boundaries (i.e., the interface
is from a different module than the concrete type), the adapter should
resolve the ID and use `SelectNode` to rebuild the query via `node(id:)`.

Alternatively, the engine's `parseASTSelections` / `typeConditionMatches`
may need to handle the case where the concrete type behind an interface
fragment isn't in the current schema view but is resolvable via the ID.

**Repro:**
```
dagger call --progress=plain engine-dev test \
  --pkg ./core/integration/ \
  --run TestInterface/TestIfaceBasic/go
```

## Other tests not yet checked

- `TestInterface/TestIfaceBasic/typescript`
- `TestInterface/TestIfaceBasic/python`
- Other `TestInterface/` subtests
- The full `TestInterface` suite

The TypeScript and Python SDK codegen may need similar schema-name fixes
(using module-prefixed names in SelectNode / node queries).

## Key files

| Area | Files |
|------|-------|
| Go codegen — module interface impl | `cmd/codegen/generator/go/templates/module_interfaces.go` |
| Go codegen — template functions | `cmd/codegen/generator/go/templates/functions.go` |
| Go codegen — interface template | `cmd/codegen/generator/go/templates/src/_types/interface.go.tmpl` |
| Go codegen — object template (AsFoo) | `cmd/codegen/generator/go/templates/src/_types/object.go.tmpl` |
| Go codegen — defs template (SelectNode, Load) | `cmd/codegen/generator/go/templates/src/_dagger.gen.go/defs.go.tmpl` |
| Engine — interface install + schema | `dagql/server.go` |
| Engine — interface type | `dagql/interfaces.go` |
| Engine — module schema building | `core/schema_build.go` |
| Engine — interface install | `core/interface.go` |
| Engine — type namespacing | `core/gqlformat.go` |
| Test module source | `core/integration/testdata/modules/go/ifaces/` |
| Test file | `core/integration/module_iface_test.go` |
