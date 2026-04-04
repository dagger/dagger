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

This fixed the "unknown flag: --dir" errors in `TestModule/TestIgnore`.

### Next: CLI not resolving flag values to IDs

**The immediate problem:** After the CLI flag fix, `TestIgnore`
progresses past flag parsing but fails with:

```
parse field "ignoreAll": init arg "dir" value as dagql.DynamicOptional (ID)
using dagql.DynamicOptional: invalid ID string: failed to decode base64:
illegal base64 data at input byte 0
```

**Root cause:** The CLI is sending the raw path string as the
argument value instead of resolving it to a Directory and sending
its ID. The GraphQL query being sent is:

```
query Query {ignoreAll(dir:"."){entries}}
```

The `"."` should be a base64 Directory ID, but the CLI is passing
the raw flag value through without calling `directoryValue.Get()`
(which would load the Directory from the path and return its ID).

**Where to look:** The issue is in how the CLI's `selectFunc`
(in `cmd/dagger/functions.go`) builds the GraphQL query from flag
values. With the old per-type `DirectoryID` scalar, the arg's
TypeDef kind was `ObjectKind` and the CLI knew to call `Get()` on
the flag value to resolve the path → Directory → ID. Now that
`resolveArgTypeDef` maps `ID` → `ObjectKind(Directory)`, the flag
is registered correctly, but `selectFunc` may not be invoking the
`Get()` method on the flag value — perhaps it falls through to a
code path that just sends the raw string.

Trace the path from `selectFunc` → flag value resolution to see
where object-typed args get their `Get()` called vs where the raw
string is used. The `DaggerValue` interface and its `Get()` method
in `cmd/dagger/flags.go` is the key — `directoryValue.Get()` does
the path → Directory → ID conversion.

**Other tests likely affected:** Any test that passes object-typed
arguments via the CLI (Container, Directory, File, Secret, etc.)
will hit this same issue. Search for `invalid ID string` in test
output to find them.

**Key files:**
- `cmd/dagger/flags.go` — `DaggerValue` interface, flag value
  types (`directoryValue`, `fileValue`, etc.) and `Get()` methods
- `cmd/dagger/functions.go` — `selectFunc` builds the GraphQL
  query from flag values; look at how it decides whether to call
  `Get()` or use the raw string
- `core/typedef_from_schema.go` — `resolveArgTypeDef` (just added)

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
