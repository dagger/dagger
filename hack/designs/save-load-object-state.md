# Stable Save/Load State for Dagger Objects

## Status: Draft (`codegen-moduletypes`)

## Summary

Dagger already has one generic object reload primitive:

```graphql
type Query {
  loadModuleFromID(id: ModuleID!): Module!
}
```

That primitive reloads a live engine object from an engine ID.

It does **not** produce a portable artifact. IDs are runtime objects. They are
not suitable for source control.

This proposal adds a second engine-owned primitive:

```graphql
type Query {
  __saveModuleState(id: ModuleID!): String!
  __loadModuleFromState(state: String!): Module!
}
```

The returned string is a versioned JSON payload containing the stable public
state of the object. In v1, the only supported type is `Module`.

This is enough to solve the `moduleTypes` persistence problem without adding a
new SDK-specific codegen target.

## Problem

Today the engine has two relevant capabilities:

1. It can load objects from IDs.
2. It can rebuild modules from semantic typedef state.

The first capability is already exposed by DAGQL automatically. Every object
type with IDs gets:

```graphql
loadFooFromID(id: FooID!): Foo!
```

This is installed in [`dagql/server.go`](../../dagql/server.go):

```go
spec := FieldSpec{
	Name: fmt.Sprintf("load%sFromID", class.TypeName()),
	...
}
```

The second capability exists, but only in narrow engine code paths. For
example, module loading already rebuilds a module from semantic typedef data in
[`core/schema/modulesource.go`](../../core/schema/modulesource.go):

```go
mod.Description = initialized.Description
for _, obj := range initialized.ObjectDefs {
	mod, err = mod.WithObject(ctx, obj)
}
for _, iface := range initialized.InterfaceDefs {
	mod, err = mod.WithInterface(ctx, iface)
}
for _, enum := range initialized.EnumDefs {
	mod, err = mod.WithEnum(ctx, enum)
}
err = mod.Patch()
```

What is missing is a portable, versioned, engine-owned serialization boundary
for that semantic state.

Without that boundary:

- `moduleTypes` can only return a live `ModuleID`
- the result cannot be checked into git
- each SDK must keep its own live introspection/codegen path
- `dagger functions` cannot reuse a checked-in artifact

## Non-Goals

This proposal does **not** do the following in v1:

- expose a public user-facing save/load API for every Dagger object
- infer stable state from raw Go struct fields
- make all current object behavior available on state-loaded objects
- add automatic save/load support for every module-defined object

The v1 goal is smaller:

```text
add one internal engine primitive for stable object state
register it for Module
use it to persist module typedef state
```

## Why V1 Is `Module`-Only

The `Module` use case is the one we need immediately, and it has a clean,
engine-defined semantic state boundary:

- `Description`
- `ObjectDefs`
- `InterfaceDefs`
- `EnumDefs`

It also already has a consumer that uses exactly those fields:
[`runModuleDefInSDK`](../../core/schema/modulesource.go#L2875).

By contrast, most Dagger objects today are runtime handles. Existing SDK JSON
marshaling for objects like `Container`, `Directory`, `File`, `Secret`, and
`Service` serializes their IDs, not their public state:

- [`Container.MarshalJSON`](../../sdk/go/dagger.gen.go#L2051)
- [`Directory.MarshalJSON`](../../sdk/go/dagger.gen.go#L4047)
- [`File.MarshalJSON`](../../sdk/go/dagger.gen.go#L6955)
- [`Secret.MarshalJSON`](../../sdk/go/dagger.gen.go#L13241)
- [`Service.MarshalJSON`](../../sdk/go/dagger.gen.go#L13395)

Even `Module` itself is not safe to dump directly with `encoding/json`.
[`core.Module`](../../core/module.go) mixes:

- semantic state we want
- runtime/source/dependency fields we do not want

So the state boundary must be explicit and engine-owned.

## API

### Internal GraphQL Fields

V1 adds two hidden top-level fields:

```graphql
type Query {
  __saveModuleState(id: ModuleID!): String!
  __loadModuleFromState(state: String!): Module!
}
```

Why hidden:

- the loaded `Module` is a typedef carrier, not a fully live module runtime
- this primitive is needed immediately by engine internals
- we should not freeze a public surface for all object types before we prove the
  state model on `Module`

The names mirror the existing `loadFooFromID` pattern and can be made public
later if we broaden support.

### Internal Go API

Add a generic state codec registry in `dagql`.

New file:

- [`dagql/stable_state.go`](../../dagql/stable_state.go) (new)

New types:

```go
type StableStateCodec interface {
	Version() int
	Save(ctx context.Context, value AnyObjectResult) (json.RawMessage, error)
	Load(ctx context.Context, srv *Server, state json.RawMessage) (AnyObjectResult, error)
}

func (s *Server) InstallStableState(class ObjectType, codec StableStateCodec)
```

`InstallStableState` does four things:

1. verifies that `class` is already installed
2. verifies that `class` has an ID type
3. stores the codec in a server map keyed by `class.TypeName()`
4. installs two hidden root fields:
   - `__save<TypeName>State`
   - `__load<TypeName>FromState`

The new `Server` field:

```go
type Server struct {
	...
	stableStateCodecs map[string]StableStateCodec
}
```

Initialize it in [`dagql.NewServer`](../../dagql/server.go).

## Generic Wire Format

The root fields exchange UTF-8 JSON text in a `String!`.

The generic envelope is:

```go
type stableStateEnvelope struct {
	Type    string          `json:"type"`
	Version int             `json:"version"`
	State   json.RawMessage `json:"state"`
}
```

Example:

```json
{
  "type": "Module",
  "version": 1,
  "state": {
    "description": "Test module",
    "objects": [
      {
        "kind": "OBJECT_KIND",
        "optional": false,
        "asObject": {
          "name": "Test",
          "originalName": "Test",
          "functions": [
            {
              "name": "hello",
              "originalName": "Hello",
              "description": "doc for hello",
              "returnType": {
                "kind": "STRING_KIND",
                "optional": false
              }
            }
          ]
        }
      }
    ]
  }
}
```

Rules:

1. `Type` must match the target type exactly.
2. `Version` is owned by the codec for that type.
3. Unknown top-level keys are ignored on load.
4. Unknown nested keys inside `State` are also ignored on load.
5. Save always writes the latest supported version.

## `InstallStableState` Behavior

### `__save<Type>State`

`__save<Type>State` is implemented generically.

Resolver flow:

1. decode the input ID
2. call existing [`Server.Load`](../../dagql/server.go#L696)
3. pass the resulting object to `codec.Save`
4. wrap the payload in `stableStateEnvelope`
5. return the JSON bytes as `string`

### `__load<Type>FromState`

`__load<Type>FromState` is also implemented generically.

Resolver flow:

1. parse the incoming JSON string into `stableStateEnvelope`
2. verify `Type == class.TypeName()`
3. verify `Version == codec.Version()`
4. call `codec.Load`
5. return the loaded object

The resolver must return an error on:

- invalid JSON
- wrong `type`
- unsupported `version`
- codec decode failure

## `Module` Codec

New file:

- [`core/module_state.go`](../../core/module_state.go) (new)

New entry points:

```go
type moduleStableStateCodec struct{}

func ModuleStableStateCodec() dagql.StableStateCodec
func (moduleStableStateCodec) Version() int
func (moduleStableStateCodec) Save(ctx context.Context, value dagql.AnyObjectResult) (json.RawMessage, error)
func (moduleStableStateCodec) Load(ctx context.Context, srv *dagql.Server, state json.RawMessage) (dagql.AnyObjectResult, error)
```

Register it in [`core/schema/module.go`](../../core/schema/module.go) after the
`Module` object type is installed:

```go
class, ok := dag.ObjectType("Module")
if !ok {
	panic("Module object type not installed")
}
dag.InstallStableState(class, core.ModuleStableStateCodec())
```

### What `Module` State Includes

`Module` state v1 saves only the fields consumed by
[`runModuleDefInSDK`](../../core/schema/modulesource.go#L2875):

- `Description`
- `ObjectDefs`
- `InterfaceDefs`
- `EnumDefs`

It does **not** save:

- `Source`
- `NameField`
- `OriginalName`
- `SDKConfig`
- `Deps`
- `Runtime`
- `ResultID`
- workspace config fields

Those remain owned by the surrounding module load path.

### State DTOs

The saved JSON must be produced from explicit DTOs, not from raw `core.Module`
or raw `core.TypeDef` values.

New DTOs in [`core/module_state.go`](../../core/module_state.go):

```go
type moduleStateV1 struct {
	Description string           `json:"description,omitempty"`
	Objects     []typeDefStateV1 `json:"objects,omitempty"`
	Interfaces  []typeDefStateV1 `json:"interfaces,omitempty"`
	Enums       []typeDefStateV1 `json:"enums,omitempty"`
}
```

`typeDefStateV1` mirrors the semantic subset of [`core.TypeDef`](../../core/typedef.go#L525).
It must include:

- `Kind`
- `Optional`
- `AsList`
- `AsObject`
- `AsInterface`
- `AsInput`
- `AsScalar`
- `AsEnum`

The nested DTOs must preserve the following fields exactly.

From [`core.ObjectTypeDef`](../../core/typedef.go#L845):

- `Name`
- `Description`
- `SourceMap`
- `Fields`
- `Functions`
- `Constructor`
- `Deprecated`
- `SourceModuleName`
- `OriginalName`
- `IsMainObject`

From [`core.FieldTypeDef`](../../core/typedef.go#L995):

- `Name`
- `Description`
- `TypeDef`
- `SourceMap`
- `Deprecated`
- `OriginalName`

From [`core.InterfaceTypeDef`](../../core/typedef.go#L1044):

- `Name`
- `Description`
- `SourceMap`
- `Functions`
- `SourceModuleName`
- `OriginalName`

From [`core.Function`](../../core/typedef.go#L20):

- `Name`
- `Description`
- `Args`
- `ReturnType`
- `Deprecated`
- `SourceMap`
- `SourceModuleName`
- `CachePolicy`
- `CacheTTLSeconds`
- `IsCheck`
- `IsGenerator`
- `ParentOriginalName`
- `OriginalName`

From [`core.FunctionArg`](../../core/typedef.go#L332):

- `Name`
- `Description`
- `SourceMap`
- `TypeDef`
- `DefaultValue`
- `DefaultPath`
- `DefaultAddress`
- `Ignore`
- `Deprecated`
- `OriginalName`

From [`core.EnumTypeDef`](../../core/typedef.go#L1226):

- `Name`
- `Description`
- `Members`
- `SourceMap`
- `SourceModuleName`
- `OriginalName`

From [`core.EnumMemberTypeDef`](../../core/typedef.go#L1274):

- `Name`
- `Value`
- `Description`
- `SourceMap`
- `Deprecated`
- `OriginalName`

From [`core.SourceMap`](../../core/typedef.go#L1540) when present:

- `Module`
- `Filename`
- `Line`
- `Column`
- `URL`

The DTOs must not use `dagql.Nullable`.
Represent optional values in plain JSON form.

### Save Algorithm

`moduleStableStateCodec.Save`:

1. assert that the input is a `*core.Module`
2. copy semantic fields into DTOs
3. marshal the DTO with `encoding/json`
4. return the raw JSON bytes

### Load Algorithm

`moduleStableStateCodec.Load`:

1. decode `moduleStateV1`
2. convert DTOs back into fresh `*core.TypeDef`, `*core.Function`, and nested
   values
3. construct a fresh `*core.Module` with:

```go
mod := &core.Module{
	Description:   state.Description,
	ObjectDefs:    decodedObjects,
	InterfaceDefs: decodedInterfaces,
	EnumDefs:      decodedEnums,
}
```

4. return it as a DAGQL object result

The loaded object is intentionally **state-only**.
It is only guaranteed to support consumers that read:

- `Description`
- `ObjectDefs`
- `InterfaceDefs`
- `EnumDefs`

That is enough for the `moduleTypes` use case.

## Why This Is Better Than Saving `ModuleID`

Current `moduleTypes` writes a JSON-encoded `ModuleID` and the engine loads it
through [`loadModuleFromID`](../../core/sdk/module_typedefs.go#L114).

That `ModuleID` is:

- runtime-specific
- session-specific in practice
- not suitable for source control

By contrast, saved `Module` state is:

- engine-owned
- versioned
- structurally portable
- readable in git

## Tests

Add tests in three layers.

### 1. DAGQL state registry

New tests in [`dagql/dagql_test.go`](../../dagql/dagql_test.go):

- registering a state codec installs `__saveFooState`
- registering a state codec installs `__loadFooFromState`
- `__saveFooState` round-trips a test object through `__loadFooFromState`
- wrong `type` is rejected
- wrong `version` is rejected
- invalid JSON is rejected

Use the existing `Point` test object as a small codec smoke test.

### 2. Module codec

New tests in [`core/integration/module_test.go`](../../core/integration/module_test.go)
or a focused unit test file:

- save/load round-trip preserves:
  - object names and original names
  - function names and original names
  - arg defaults
  - `+check`
  - `+generate`
  - cache policy
  - source maps
  - enum values
- saved JSON does not contain runtime-only fields like `ResultID`

### 3. `moduleTypes` consumer

This is covered in the follow-on design, but the codec must be exercised by a
module-loading test that uses saved state instead of SDK `moduleTypes`.

## Rollout

V1 rollout:

1. add the generic `dagql` state codec registry
2. register `Module`
3. add module-state roundtrip tests
4. consume the new primitive from the `moduleTypes` persistence path

Future work, not part of this change:

- add codecs for other engine-owned semantic objects
- add structural save/load support for module-defined objects whose field graph
  contains only saveable types
- decide whether any of the hidden fields should become public API
