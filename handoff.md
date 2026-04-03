# Handoff: `loadFooFromID` → `node(id:)` Migration

## Context

The `interfaces` branch replaces per-type `loadFooFromID(id:)` schema
fields with the Global Object Identification `node(id:)` pattern. The
Go, Python, TypeScript, and Rust SDKs are fully migrated. The CLI,
Dang SDK, codegen, integration tests, and module dependencies are also
fixed. Codegen introspection test fixtures updated.

## Remaining SDKs

These still reference `loadFooFromID` and need the same migration
pattern: add inline fragment support to the query builder, update
codegen, regenerate, fix tests.

### Elixir SDK

**Codegen:** `sdk/elixir/dagger_codegen/lib/dagger/codegen/elixir_generator/object_renderer.ex`
**Generated:** `sdk/elixir/lib/dagger/gen/*.ex`
Query builder likely needs `inline_fragment` added.

### PHP SDK ✅

**Done.** Inline fragment support added to QueryBuilderChain.
`loadObjectFromId()` helper on AbstractClient replaces dynamic
`load*FromId()` calls. DecodesValue + IdableHandler updated.
Generated code regenerated. All checks pass (223 tests, phpstan, phpcs).

### Java SDK

**Codegen:** `sdk/java/dagger-codegen-maven-plugin/src/main/java/io/dagger/codegen/introspection/ObjectVisitor.java`
**Test:** `sdk/java/dagger-java-sdk/src/test/java/io/dagger/client/ClientIT.java`

### .NET SDK (test data only)

**Test data:** `sdk/dotnet/sdk/Dagger.SDK.SourceGenerator.Tests/TestData.cs`

## Codegen introspection test fixtures

`schema.json` captured from dev engine via `go/namespacing` test
module. Golden files regenerated with `go test -update`. Tests use
`sub1`/`sub2`/`test` module names. Could use a `go:generate` script
to keep it from going stale.

## Architecture reference

Each SDK migration follows the same pattern:

1. **Query builder** — add `inline_fragment(type_name)` support
2. **Codegen** — generate native interface types, `Loadable` impls
3. **Codegen** — handle `@expectedType` for sync/id fields
4. **Generated client** — regenerate
5. **Tests** — update to use new API

The Go SDK's `selectNode` helper is the canonical reference:
```go
func selectNode(q *Selection, id any, typeName string) *Selection {
    return q.Select("node").Arg("id", id).InlineFragment(typeName)
}
```

## Regenerating SDKs

Use `dagger generate <module>:apiclient` where possible (e.g. Rust
SDK uses `dagger generate rust-sdk:apiclient`). This runs against the
dev engine from this branch, producing the correct unified ID schema.

For the dev engine: `./hack/dev` builds and starts it,
`./bin/dagger` runs commands against it.
