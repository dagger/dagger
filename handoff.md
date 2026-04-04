# Handoff: `loadFooFromID` → `node(id:)` Migration

## Context

The `interfaces` branch replaces per-type `loadFooFromID(id:)` schema
fields with the Global Object Identification `node(id:)` pattern. The
Go, Python, TypeScript, and Rust SDKs are fully migrated. The CLI,
Dang SDK, codegen, integration tests, and module dependencies are also
fixed. Codegen introspection test fixtures updated.

## Completed SDKs

### Elixir SDK ✅

`inline_fragment/2` added to QueryBuilder. Codegen updated:
`@expectedType` directive support on args and fields, unified `ID`
scalar, `INTERFACE` kind handling, `node(id:)` + inline fragment
replaces all `loadFooFromID` calls. 52 per-type `FooID` scalar files
removed. `Node` interface type generated. Nestru.Decoder uses
`node(id:)` directly. Test fixtures regenerated from
`cmd/codegen/introspection/testdata/schema.json`. All 19 codegen
tests pass.

**Toolchain:** `updateCodegenTests` function added to
`elixir-sdk.dang` for auto-accepting Mneme snapshot changes.
`codegenTest` sets `CI=true` to reject mode.

**Regenerating:** Use the introspection JSON from the test fixtures
or the dev engine:
```bash
python3 -c "import json; s=json.load(open('cmd/codegen/introspection/testdata/schema.json')); json.dump({'__schema':s}, open('/tmp/introspection.json','w'))"
cd toolchains/elixir-sdk-dev && dagger call generate --introspection-json /tmp/introspection.json -y
```

**Updating codegen test snapshots:**
```bash
dagger call elixir-sdk update-codegen-tests -y
```

**SDK integration tests (`sdkTest`):** Run via `dagger check elixir-sdk:sdk-test`.

### PHP SDK ✅

Inline fragment support added to QueryBuilderChain.
`loadObjectFromId()` helper on AbstractClient replaces dynamic
`load*FromId()` calls. DecodesValue + IdableHandler updated.
Generated code regenerated. All checks pass (223 tests, phpstan,
phpcs).

### Java SDK ✅

Inline fragment support added to `QueryBuilder` via `chainNode()`
method and `InlineFragment.on()` from SmallRye GraphQL client.
`executeObjectListQuery` now takes a GraphQL type name string and
uses `node(id:)` + inline fragment instead of `loadFooFromID`.

`loadObjectFromID(Class<T>, ID)` and `nodeQueryBuilder(String, ID)`
helpers generated on Client class. Deserializers use
`nodeQueryBuilder` to construct typed objects from IDs.
Annotation processor test fixture updated (import ordering).

35 unit tests pass (23 SDK + 12 annotation processor). Codegen test
fixture (`cmd/codegen/introspection/testdata/schema.json`) required
for maven build:
```bash
python3 -c "import json; s=json.load(open('cmd/codegen/introspection/testdata/schema.json')); json.dump({'__schema':s}, open('/tmp/introspection.json','w'))"
cd sdk/java && ./mvnw test -Ddaggerengine.version=local -Ddaggerengine.schema=/tmp/introspection.json
```

**Integration tests (`ClientIT`):** Run via `dagger check java-sdk:sdk-test`.

## Remaining SDKs

None — all SDKs are migrated.

### .NET SDK ✅

Directive types added to `Types/Directive.cs`. Introspection models
updated: `Field`, `InputValue`, and `Type` now deserialize
`directives`, `interfaces`, and `possibleTypes`. `@expectedType`
resolved on args (including lists) and fields (sync-like fields
execute and return parent via `node(id:)`). Per-type `FooId` scalars
replaced by unified `Id` class in base SDK. `QueryBuilder` supports
inline fragments for `node(id:)` pattern. `INTERFACE` schema types
generate C# `interface` + `*Client` class pairs. `IId` simplified
to unified interface. `IdValue` no longer generic.

`QueryExecutor` now checks for GraphQL errors and handles null
JSON traversal. `TestReturnArray` test fixed to use async/await
properly. Three new `QueryBuilder` tests for inline fragments.

**Toolchain:** `test` in `main.dang` uses `dagger run` instead of
`experimentalPrivilegedNesting` so integration tests use the dev
engine with `node(id:)` support. All 20 tests pass (3 codegen +
17 integration).

**Note:** The .NET SDK is intentionally not registered in root
`dagger.json`. Run it directly via `dagger -m toolchains/dotnet-sdk-dev call`.

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

## Regenerating and checking SDKs

Use `dagger generate -l` and `dagger check -l` to list available
targets. Then run specific ones:

```bash
dagger generate php-sdk:api -y    # regenerate
dagger check 'php-sdk:*'           # run all checks for an SDK
```

The `dagger` CLI handles running the correct engine internally.
