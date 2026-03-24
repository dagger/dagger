# SDK Migration Guide: Unified IDs and First-Class Interfaces

This document describes the schema changes introduced by the unified ID
and first-class interfaces work, and what each SDK needs to do to support
them. It is based on the changes already made to the Go, Python, and
TypeScript SDKs.

## Schema Changes Summary

### 1. Unified `ID` scalar

All per-type `FooID` scalars (`ContainerID`, `DirectoryID`, etc.) have
been replaced with a single `ID` scalar. Type information is conveyed
via the `@expectedType(name: "Foo")` directive on arguments and field
definitions.

**Before:**
```graphql
type Query {
  loadContainerFromID(id: ContainerID!): Container!
}

type Container {
  id: ContainerID!
  sync: ContainerID!
  withDirectory(path: String!, directory: DirectoryID!): Container!
}
```

**After:**
```graphql
type Query {
  loadContainerFromID(id: ID! @expectedType(name: "Container")): Container!
}

type Container {
  id: ID! @expectedType(name: "Container")
  sync: ID! @expectedType(name: "Container")
  withDirectory(path: String!, directory: ID! @expectedType(name: "Directory")): Container!
}
```

### 2. First-class `INTERFACE` types

GraphQL INTERFACE types now appear in the schema with `kind: "INTERFACE"`.
They have:
- `fields` — the interface's field definitions (same structure as object fields)
- `possibleTypes` — which object types implement the interface
- Objects have `interfaces` — which interfaces they implement

### 3. `@expectedType` directive

Available on both arguments and field definitions. Accessed via:
- **Introspection JSON**: `arg.directives` / `field.directives`
- **Go codegen**: `arg.Directives.ExpectedType()` / `field.Directives.ExpectedType()`
- **Python codegen**: `graphql.get_directive_values(schema.get_directive("expectedType"), node.ast_node)`

## What Each SDK Needs to Change

### A. Client-side codegen (the generated API bindings)

#### A1. Argument types: read `@expectedType`

When generating method signatures, if an argument's type is the `ID`
scalar, check for `@expectedType`. If present, use the named type
instead of a raw string/ID type.

**Example:** An argument `directory: ID! @expectedType(name: "Directory")`
should generate a parameter of type `Directory`, not `ID`/`string`.

For list arguments like `[ID!]! @expectedType(name: "Directory")`,
the element type is `Directory` and the parameter is `Directory[]`/`list[Directory]`/etc.

**How Go does it:** `formatArgType()` in `cmd/codegen/generator/go/templates/functions.go`
checks `arg.Directives.ExpectedType()`. If set, formats as the named type
(with list wrapping if the TypeRef is a list).

**How Python does it:** `format_input_type()` accepts an optional `expected_type`
parameter. `_InputField.__init__` reads `expected_type_name(ctx.schema, graphql.ast_node)`
and passes it through.

**How TypeScript does it:** `FormatArgType` template function checks
`arg.Directives.ExpectedType()`.

#### A2. `ConvertID` fields (e.g. `sync`)

Fields that return `ID!` where the `@expectedType` matches the parent
object are "sync-like" fields. The SDK should:
1. Execute the query to get the ID string
2. Return the parent object type (not a raw string)

**Example:** `Container.sync: ID! @expectedType(name: "Container")` should
return `Container` (re-loaded from the ID), not a string.

The common `ConvertID()` function already handles this by checking
`field.Directives.ExpectedType() == field.ParentObject.Name`.

#### A3. Interface types: generate native language interfaces

For types with `kind: "INTERFACE"`, generate the language's native
interface mechanism:

| Language   | Interface representation | Concrete client class |
|------------|------------------------|-----------------------|
| Go         | `type Foo interface { ... }` | `FooClient` struct (exported, named via `InterfaceClientName`) |
| Python     | `@runtime_checkable class Foo(Protocol)` | `_FooClient(Type)` class |
| TypeScript | `export interface Foo { ... }` | `_FooClient extends BaseClient` class |
| PHP        | `interface Foo { ... }` | `FooClient` class implementing `Foo` |
| Elixir     | `@behaviour` or protocol | Module with default implementations |
| Java       | `public interface Foo { ... }` | `FooClient implements Foo` class |

The concrete client class is needed for:
- `loadFooFromID` return values (need to instantiate something)
- Fields returning interface types (need a query-builder object)
- `execute_object_list` / list returns (need to instantiate from IDs)

The client class has the same methods as the interface, implemented
via query-builder selections (same as regular object classes).

#### A4. Object `implements` — structural subtyping

Objects that implement interfaces are listed in `type.interfaces` (from
introspection). In languages with structural typing (Python, TypeScript),
no explicit `implements` declaration is needed — the object already has
the same methods. In languages with nominal typing (Go, Java, PHP),
you need adapter methods or explicit `implements`:

- **Go**: Generate `AsFoo() Foo` methods on implementing objects
  (wraps the object in the interface client struct with the same query).
  Needed because Go doesn't support covariant return types.
- **Java/PHP**: Can use `implements Foo` on the class directly, since
  method return types can be covariant in these languages.
- **Python/TypeScript/Elixir**: No adapters needed. Pass objects directly.

#### A5. No more `asFoo` schema fields

The `asFoo` fields have been removed from the schema. SDKs must NOT
generate calls to `asFoo` fields. Instead, use the mechanisms above
(structural subtyping or `AsFoo()` adapters).

#### A6. `XXX_GraphQLIDType` returns `"ID"`

Any method/property that returns the GraphQL scalar type name for IDs
should return `"ID"` instead of `"FooID"`. This affects:
- Object classes: `XXX_GraphQLIDType() -> "ID"`
- Interface client classes: same
- Any query-builder variable type declarations

#### A7. `Node` interface and `Concrete()` (Go only for now)

Go interfaces get a `Concrete(ctx) (Node, error)` method that queries
`__typename` + `id` and type-switches on `possibleTypes` to load the
concrete object. `Node` is a base interface for all typed objects.

This is only needed for Go (nominal typing requires explicit narrowing).
Python/TS can use `isinstance()` / `instanceof` directly since they have
structural typing.

### B. Module-side codegen (the module runtime)

#### B1. Module runtime ID types

If the module runtime references `ModuleID`, `FunctionID`, etc. as
explicit types, change them to the unified `ID` type (or `str`/`string`
in the language):

- **Go**: `module_interfaces.go` now uses `dagger.ID` everywhere
- **Python**: `_module.py` uses `str` instead of `dagger.ModuleID`
- **TypeScript**: `register.ts` uses `ID` instead of `ModuleID`

#### B2. Interface impl codegen (Go-specific)

Go modules generate concrete implementations for interfaces they
consume. The `module_interfaces.go` codegen was updated:
- `idTypeName()` returns `"dagger.ID"` (not `"CustomIfaceID"`)
- No `type CustomIfaceID string` definition generated
- `LoadCustomIfaceFromID` takes `dagger.ID` parameter
- Struct fields use `*dagger.ID` for id
- `XXX_GraphQLIDType` returns `"ID"`
- `UnmarshalJSON` uses `dagger.ID` for id variable

### C. GraphQL query strings

Any hardcoded GraphQL query strings that use `$var: FooID!` must be
updated to `$var: ID!`. This includes:
- `.graphql` files (e.g. `cmd/dagger/*.graphql`)
- Inline query strings in test files
- Any SDK-internal queries

### D. `@expectedType` on the engine side

The engine adds `@expectedType` directives in two places:

1. **`dagql/objects.go` — `InputSpecsForType`**: For Go struct-based args,
   `findExpectedTypeName()` walks through `Optional`/`ArrayInput`/`ID[T]`
   wrappers to extract the type name from `ID[T]` generics.

2. **`core/typedef.go` — `FunctionFieldSpec`**: For module function args,
   walks through `TypeDefKindList` wrappers to find the underlying
   object/interface type and adds `ExpectedType(name)`.

3. **`core/interface.go` — interface field args**: Same list-walking logic.

4. **`dagql/server.go` — field definitions**: When a field's return type
   is `ID[T]`, the `@expectedType` directive is added to the field
   definition itself (for `sync`, `id`, etc.).

## Introspection Data Available

For each type in the schema:

```json
{
  "name": "TestCustomIface",
  "kind": "INTERFACE",
  "fields": [ /* same as object fields */ ],
  "possibleTypes": [
    { "name": "Impl" },
    { "name": "OtherImpl" }
  ]
}
```

For objects:
```json
{
  "name": "Impl",
  "kind": "OBJECT",
  "interfaces": [
    { "name": "TestCustomIface" }
  ]
}
```

For arguments with `@expectedType`:
```json
{
  "name": "directory",
  "type": { "kind": "NON_NULL", "ofType": { "kind": "SCALAR", "name": "ID" } },
  "directives": [
    { "name": "expectedType", "args": { "name": "Directory" } }
  ]
}
```

## Key Files Changed (reference)

### Common codegen
- `cmd/codegen/generator/functions.go` — `ConvertID`, `FormatReturnType`
- `cmd/codegen/introspection/introspection.go` — `TypeRef.IsInterface()`, `Directives.ExpectedType()`

### Go SDK
- `cmd/codegen/generator/go/templates/functions.go` — `formatArgType` reads `@expectedType`
- `cmd/codegen/generator/go/templates/src/_types/interface.go.tmpl` — interface + client struct + `Concrete()`
- `cmd/codegen/generator/go/templates/src/_types/object.go.tmpl` — `AsFoo()` adapters, `XXX_GraphQLIDType` returns `"ID"`
- `cmd/codegen/generator/go/templates/src/_dagger.gen.go/defs.go.tmpl` — `Node` interface
- `cmd/codegen/generator/go/templates/module_interfaces.go` — module-side interface impl

### Python SDK
- `sdk/python/codegen/src/codegen/generator.py` — `InterfaceProtocol` handler, `expected_type_name()`, `format_input_type()`
- `sdk/python/src/dagger/mod/_module.py` — `str` instead of `dagger.ModuleID`
- `sdk/python/src/dagger/client/_core.py` — `_graphql_name()` instead of `__name__`

### TypeScript SDK
- `cmd/codegen/generator/typescript/templates/functions.go` — `FormatArgType`, `IsInterface`
- `cmd/codegen/generator/typescript/templates/src/interface.ts.gtpl` — TS interface + `_FooClient`
- `cmd/codegen/generator/typescript/templates/src/objects.ts.gtpl` — routes to interface template
- `cmd/codegen/generator/typescript/templates/src/method.ts.gtpl` — `_FooClient` instantiation
- `cmd/codegen/generator/typescript/templates/format.go` — `FormatKindScalarID`
- `sdk/typescript/src/module/entrypoint/register.ts` — `ID` instead of `ModuleID`
