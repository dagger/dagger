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

5. **`@expectedType` codegen** — Introspection query now requests
   `directives { name args { name value } }` on fields and arguments.
   `DirectiveApplication` / `DirectiveApplicationArg` structs and
   `DirectivesExt::expected_type()` helper added to introspection types.

   - **ConvertID (sync-like fields):** `CommonFunctions::convert_id()`
     detects fields returning `ID!` where `@expectedType` matches the
     parent object name. These fields become async methods returning
     the parent type (not `Id`). The body executes the query to get
     the ID, then reconstructs the parent via
     `query.root().select("node").arg("id", id).inline_fragment(name)`.
     `Selection::root()` added to the query builder.

   - **`id()` field:** Not converted (explicitly excluded by
     `convert_id`). Still returns `Result<Id, DaggerError>`.

   - **`@expectedType` on arguments:** Already works via `is_id()` →
     `impl IntoID<Id>` pattern. All objects implement `IntoID<Id>`,
     so args typed `ID @expectedType(name: "Foo")` accept `Foo`
     objects directly.

### Regenerated

`gen.rs` regenerated with unified `ID` scalar, ConvertID on sync-like
fields, interface traits, Loadable impls, and possibleTypes output.
21 codegen unit tests pass. Integration tests compile but require
the dev engine (released v0.20.3 lacks `node(id:)`).

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
| Codegen functions | `sdk/rust/crates/dagger-codegen/src/rust/functions.rs` |
| Common functions | `sdk/rust/crates/dagger-codegen/src/functions.rs` |
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
