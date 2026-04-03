# Handoff: `loadFooFromID` → `node(id:)` Migration

## Context

The `interfaces` branch replaces per-type `loadFooFromID(id:)` schema
fields with the Global Object Identification `node(id:)` pattern. The
Go, Python, and TypeScript SDKs are fully migrated. The CLI, Dang SDK,
codegen, integration tests, and module dependencies are also fixed.

## Rust SDK interface support — status

### Done

1. **Inline fragments in the query builder** —
   `Selection::inline_fragment(type_name)` emits `... on TypeName` in
   queries and skips the fragment during response unpacking (no extra
   nesting level). Unit tests in `querybuilder::tests`.

2. **Rust traits for interfaces** — Codegen generates `pub trait Node`
   with async method signatures, a `NodeClient` struct for
   query-building, `impl Node for NodeClient`, and `impl Node for
   Container` / etc. for every object that declares the interface.
   Visitor split into phases so interfaces generate before objects.
   `format_kind_interface` ensures return types use `FooClient`.

3. **`Loadable` trait + `ref`/`load` helpers** — `Loadable` trait in
   `sdk/rust/crates/dagger-sdk/src/lib.rs::loadable` provides
   `graphql_type()` and `from_query()`. Codegen generates
   `impl Loadable` on every object/interface client with an `id` field.
   `Query::r#ref<T>(id)` returns a lazy reference via
   `node(id).inline_fragment(T::graphql_type())`. `Query::load<T>(id)`
   verifies the node exists first.

4. **`possibleTypes` wiring** — Codegen reads `interfaces` on each
   object and generates `impl InterfaceTrait for Object` forwarding
   methods for every declared interface.

### Regenerated

`gen.rs` has been regenerated with trait, Loadable, and possibleTypes
output. Integration tests in `tests/mod.rs` (`test_node_load_container`,
`test_node_load_directory`, `test_node_load_file`) compile. 60 types
implement `Loadable`.

### Remaining: `@expectedType` codegen

`sync()` still returns `Id` instead of the parent object type. Fixing
this requires:

1. **Introspection query must request field-level directives.** The
   Rust introspection types (`FullTypeFields`) don't have a
   `directives` field. Add `directives { name args { name value } }`
   to the introspection query's field selections. Add a `directives`
   field to `FullTypeFields` in
   `sdk/rust/crates/dagger-sdk/src/core/introspection.rs`.

   Reference: PHP had to rewrite introspection parsing entirely
   (`42a2e83ab`). The Rust SDK uses `graphql_client` which may or may
   not preserve custom directives — check before assuming they're
   available. May need to parse raw introspection JSON.

   Introspection query files:
   `sdk/rust/crates/dagger-sdk/src/core/graphql/introspection_query.graphql`
   `sdk/rust/crates/dagger-sdk/src/core/graphql/introspection_schema.graphql`

2. **Read `@expectedType` in codegen.** When a field's return type is
   the `ID` scalar, check for `@expectedType(name: "Foo")`. If
   `expectedType == parentObject`, treat it as a sync-like field:
   query for the ID, then reconstruct the parent via
   `selectNode(q.Root(), id, typeName)`. See Go's `Container.Sync`
   pattern in `sdk/go/dagger.gen.go`.

3. **Read `@expectedType` on arguments.** Arguments typed `ID` with
   `@expectedType(name: "Foo")` should accept `Foo` (via `IntoID`)
   instead of raw `Id`. The Rust codegen already does this for the
   old `FooID` pattern — it just needs to check the directive instead
   of the type name suffix.

### Key files

| What | Where |
|------|-------|
| Query builder | `sdk/rust/crates/dagger-sdk/src/querybuilder.rs` |
| Loadable trait | `sdk/rust/crates/dagger-sdk/src/lib.rs` (loadable module) |
| ref/load methods | `sdk/rust/crates/dagger-sdk/src/client.rs` |
| Codegen visitor | `sdk/rust/crates/dagger-codegen/src/visitor.rs` |
| Interface template | `sdk/rust/crates/dagger-codegen/src/rust/templates/interface_tmpl.rs` |
| Object template | `sdk/rust/crates/dagger-codegen/src/rust/templates/object_tmpl.rs` |
| Format helpers | `sdk/rust/crates/dagger-codegen/src/rust/format.rs` |
| Introspection types | `sdk/rust/crates/dagger-sdk/src/core/introspection.rs` |
| Introspection query | `sdk/rust/crates/dagger-sdk/src/core/graphql/introspection_query.graphql` |
| Generated client | `sdk/rust/crates/dagger-sdk/src/gen.rs` |
| Integration tests | `sdk/rust/crates/dagger-sdk/tests/mod.rs` |
| Codegen tests | `sdk/rust/crates/dagger-codegen/src/lib.rs` (tests module) |

## Other remaining SDKs

These still reference `loadFooFromID` and need the same migration
pattern: add inline fragment support to the query builder, update
codegen, regenerate, fix tests.

### Codegen introspection test fixtures (trivial)

JSON schema fixtures with `loadDepFromID` / `loadTestFromID` entries.
Tests filtering logic, not the API. Remove the entries, update
expected schemas, run `go test ./cmd/codegen/introspection/`.

**Files:** `cmd/codegen/introspection/testdata/schema.json`,
`keep_dep_expected_schema.json`, `keep_dep_and_test_expected_schema.json`

### Elixir SDK

**Codegen:** `sdk/elixir/dagger_codegen/lib/dagger/codegen/elixir_generator/object_renderer.ex`
**Generated:** `sdk/elixir/lib/dagger/gen/*.ex`
Query builder likely needs `inline_fragment` added.

### PHP SDK

**Codegen:** `sdk/php/src/Codegen/Introspection/NewCodegenVisitor.php`
**Generated:** `sdk/php/generated/Client.php`

### Java SDK

**Codegen:** `sdk/java/dagger-codegen-maven-plugin/src/main/java/io/dagger/codegen/introspection/ObjectVisitor.java`
**Test:** `sdk/java/dagger-java-sdk/src/test/java/io/dagger/client/ClientIT.java`

### .NET SDK (test data only)

**Test data:** `sdk/dotnet/sdk/Dagger.SDK.SourceGenerator.Tests/TestData.cs`

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
