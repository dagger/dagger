# Handoff: `loadFooFromID` → `node(id:)` Migration

## Context

The `interfaces` branch replaces per-type `loadFooFromID(id:)` schema
fields with the Global Object Identification `node(id:)` pattern. The
Go, Python, and TypeScript SDKs are fully migrated. The CLI, Dang SDK,
codegen, integration tests, and module dependencies are also fixed.

## Next focus: Rust SDK interface support

The Rust SDK compiles and generates, but its interface support is
cosmetic. Interface types (like `Node`) are generated as plain structs
identical to objects. `Query.node(id)` returns a `Node` with only
`.id()` — you can't access type-specific fields, making `node(id:)`
unusable for loading typed objects.

### What's missing

1. **Inline fragments in the query builder** —
   `Selection` needs an `inline_fragment(type_name)` method so queries
   can build `node(id:...){... on Container{...}}`. Without this,
   `node()` is a dead end. Reference: Go's `querybuilder.InlineFragment`
   in `github.com/dagger/querybuilder`. Key detail: inline fragments
   don't add a nesting level when unpacking the response — the Go
   implementation's `unpack` skips them.

2. **Rust traits for interfaces** — Other SDKs generate native
   interface types (Go `interface`, Python `Protocol`, TS `interface`).
   The Rust SDK should generate `trait Node { fn id(...); }` and impl
   it on all types that declare the interface. The introspection data
   has `possibleTypes` on interfaces and `interfaces` on objects.

3. **Downcasting / typed load** — The Go SDK has `Ref[T]` and
   `Load[T]` generics that use `selectNode` + inline fragments. Rust
   equivalent could be a generic `fn ref_<T: Loadable>(client, id) -> T`
   that constructs a `T` with `selection.select("node").arg("id", id)
   .inline_fragment(T::graphql_type())`.

4. **`possibleTypes` wiring** — The codegen ignores which objects
   implement which interfaces. No `impl Node for Container` or enum
   dispatch is generated.

### Implementation order

1. Add `inline_fragment` to `Selection` in `querybuilder.rs`
2. Generate Rust traits for INTERFACE types in codegen
3. Add `Loadable` trait + `ref_`/`load` helpers
4. Wire `possibleTypes` → trait impls in codegen

### Key files

| What | Where |
|------|-------|
| Query builder | `sdk/rust/crates/dagger-sdk/src/querybuilder.rs` |
| Codegen visitor | `sdk/rust/crates/dagger-codegen/src/visitor.rs` |
| Codegen functions | `sdk/rust/crates/dagger-codegen/src/functions.rs` |
| Object template | `sdk/rust/crates/dagger-codegen/src/rust/templates/object_tmpl.rs` |
| Format helpers | `sdk/rust/crates/dagger-codegen/src/rust/format.rs` |
| Generated client | `sdk/rust/crates/dagger-sdk/src/gen.rs` |
| Go query builder ref | `github.com/dagger/querybuilder` (see `InlineFragment`, `unpack`) |
| Go `selectNode` | `sdk/go/dagger.gen.go:15291` |
| Go `Ref`/`Load` | `sdk/go/dagger.gen.go` (search `Loadable`) |

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
2. **Codegen** — change `loadFooFromID` → `node(id:) { ... on Foo }`
3. **Generated client** — regenerate
4. **Tests** — update to use new API

The Go SDK's `selectNode` helper is the canonical reference:
```go
func selectNode(q *Selection, id any, typeName string) *Selection {
    return q.Select("node").Arg("id", id).InlineFragment(typeName)
}
```
