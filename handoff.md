# Handoff: `loadFooFromID` → `node(id:)` Migration

## Context

The `interfaces` branch replaces per-type `loadFooFromID(id:)` schema
fields with the Global Object Identification `node(id:)` pattern. All
SDKs are migrated. Now fixing integration test failures in the engine
and CLI.

## Current status: fixing integration tests

All SDK migrations are done. The focus is now on engine/CLI
integration test failures caused by the unified `ID` scalar and
`node(id:)` changes.

### Recently fixed

**CLI unified ID → object type resolution (scalar args)** —
`resolveArgTypeDef` in `core/typedef_from_schema.go` compared the
scalar name against `"ID"` but `NewScalarTypeDef` applies
`strcase.ToCamel` which converts it to `"Id"`. Fixed to use
`OriginalName` instead. This was the root cause of the CLI sending
raw path strings instead of Directory/File IDs.

**CLI unified ID → object type resolution (list args)** —
`resolveArgTypeDef` only checked the top-level TypeDef, missing list
args like `[ID!]!` with `@expectedType`. Extracted a `resolveIDScalar`
helper that recurses into list element types so e.g. `[]File` args
get proper DaggerValue flag types.

**Inline test module code using removed APIs** — Tests
`TestSecretNested/secret_by_id_leak` and
`TestContextDirectory/as_module` used `dag.LoadSecretFromID()` /
`dag.LoadDirectoryFromID()` which no longer exist. Updated to use
`dagger.Ref[*dagger.Secret](dag, id)` /
`dagger.Ref[*dagger.Directory](dag, id)`.

**Interface source map comments** — The Go codegen interface template
was missing SourceMap directive comments on generated client methods.
Added to `interface.go.tmpl`.

**TypedefSourceMaps test regex** — Updated regex to expect
`type DepMyInterface interface` (not `struct`) and method on
`*DepMyInterfaceClient` (not `*DepMyInterface`).

**TypeScript SDK tests** — `typescript-sdk:test-bunjs` and
`typescript-sdk:lint-typescript` both pass. Query builder closing
brace spacing updated in test expectations; prettier formatting
applied. A `format` generator was added to the TS SDK toolchain
(`dagger generate typescript-sdk:format -y`).

**Go codegen: pre-v0.12.0 module interfaces** — `parseGoIface` was
missing the `isDaggerGenerated` early-return that `parseGoStruct`
already had. Pre-v0.12.0 Go modules generate type aliases for all
schema types, including the new `Node` interface. When dealiased,
`Node` has an `ID()` method not in `DaggerObject`/`GraphQLMarshaller`,
causing "no decl for ID" errors. Fixed in `module_interfaces.go`.
This fixed `TestContainer/TestSystemGoProxy` (all 4 VCS subtests).

**CLI flag registration for unified ID args** — The CLI's
`introspectionRefToTypeDef` had legacy logic converting `FooID`
scalar inputs to object types by stripping the `ID` suffix. The
unified `ID` scalar matched (`strings.HasSuffix("ID", "ID")` →
empty object name), causing args to be silently skipped as
unsupported flags. Fixed in `core/typedef_from_schema.go`:
- Bare `ID` scalar excluded from legacy `FooID` stripping
- New `resolveArgTypeDef` helper reads `@expectedType` directives
  to map unified `ID` → correct object type (Directory, File, etc.)
- Applied to both object and interface TypeDef construction
- Recurses into list element types for `[ID!]!` args

### Next: broader test triage

The `TestModule$` suite passes except for flaky/infra failures
(git auth, resource contention). `TestCall/TestArgTypes/list_args`
now passes.

**Next steps:**
- Run `TestCall$` suite to find remaining CLI-related failures
- Run `TestContainer$` suite for core type issues
- Search for any remaining `Load*FromID` references in test inline
  module code (`core/integration/`) — grep for
  `dag\.Load.*FromID` in test files
- Check `TestModule/TestPrivateGitRepoArgCaching` — always fails
  with git auth error, likely environmental

**Known non-issues (flaky):**
- `TestPrivateGitRepoArgCaching` — git authentication failure
- Various tests fail under parallel load but pass in isolation
  (TestContextGitDetectDirty, TestFunctionCacheControl,
  TestPrivateField/typescript, etc.)

**Key files:**
- `core/typedef_from_schema.go` — `resolveArgTypeDef` +
  `resolveIDScalar` handle unified ID → object type conversion
- `cmd/dagger/flags.go` — `DaggerValue` interface, flag value
  types and `Get()` methods; `AddFlag` uses TypeDef kind to pick
  the right flag type
- `cmd/dagger/functions.go` — `selectFunc` builds GraphQL queries
  from flag values

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

### PHP SDK ✅

Inline fragment support added to QueryBuilderChain.
`loadObjectFromId()` helper on AbstractClient replaces dynamic
`load*FromId()` calls. DecodesValue + IdableHandler updated.
Generated code regenerated. All checks pass (223 tests, phpstan,
phpcs).

### Java SDK ✅

Inline fragment support added to `QueryBuilder` via `chainNode()`
method and `InlineFragment.on()` from SmallRye GraphQL client.
`loadObjectFromID(Class<T>, ID)` and `nodeQueryBuilder(String, ID)`
helpers generated on Client class. 35 unit tests pass.

### .NET SDK ✅

Directive types, introspection models, `@expectedType` support,
unified `Id` class, inline fragments, C# interface generation.
All 20 tests pass. Not registered in root `dagger.json` — run via
`dagger -m toolchains/dotnet-sdk-dev call`.

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

```bash
dagger generate -l              # list all generate targets
dagger check -l                 # list all check targets
dagger generate php-sdk:api -y  # regenerate
dagger check 'php-sdk:*'       # run all checks for an SDK
```
