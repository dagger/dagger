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

**Not yet tested:** SDK integration tests (`sdkTest`) require the dev
engine which is blocked by Dang SDK `@expectedType` support.

### PHP SDK ✅

Inline fragment support added to QueryBuilderChain.
`loadObjectFromId()` helper on AbstractClient replaces dynamic
`load*FromId()` calls. DecodesValue + IdableHandler updated.
Generated code regenerated. All checks pass (223 tests, phpstan,
phpcs).

## Remaining SDKs

These still reference `loadFooFromID` and need the same migration
pattern: add inline fragment support to the query builder, update
codegen, regenerate, fix tests.

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

## Regenerating and checking SDKs

Use `dagger generate -l` and `dagger check -l` to list available
targets. Then run specific ones:

```bash
dagger generate php-sdk:api -y    # regenerate
dagger check 'php-sdk:*'           # run all checks for an SDK
```

For the dev engine: `./hack/dev` builds and starts it,
`./bin/dagger` runs commands against it.

**Note:** The outer `dagger` CLI (stable release) works for running
toolchain functions. `./bin/dagger` (dev engine) is needed for
integration tests but is currently blocked for modules with Dang
SDK dependencies due to missing `@expectedType` support in Dang.
