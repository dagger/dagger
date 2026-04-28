---
name: sdk-migration
description: |
  Migrate a Dagger SDK to unified IDs and first-class interfaces. Use when updating
  any SDK's codegen for the unified ID scalar (@expectedType), removing per-type FooID
  scalars, generating native interface types, or adapting module runtime code.
  Keywords: SDK, codegen, unified ID, @expectedType, FooID, interface, migration
---

# SDK Migration: Unified IDs and First-Class Interfaces

## When to Load This Skill

- Migrating an SDK to the unified `ID` scalar with `@expectedType`
- Removing per-type `FooID` scalars from an SDK
- Adding native interface type generation to an SDK
- Updating an SDK's module runtime for unified IDs
- Debugging `@expectedType` directive access in codegen

## Schema Changes

Two breaking changes to the introspection schema:

### 1. Unified `ID` scalar

All per-type `FooID` scalars replaced with one `ID` scalar.
Type information conveyed via `@expectedType(name: "Foo")` directive.

```graphql
# Before
container(id: ContainerID!): Container!

# After
container(id: ID! @expectedType(name: "Container")): Container!
```

### 2. First-class `INTERFACE` types

Interface types now have `kind: "INTERFACE"` (not `"OBJECT"`), with
`fields`, `possibleTypes`, and objects declaring `interfaces`.

## Migration Checklist

Work through these in order. Each section has a reference commit.

### Step 1: Introspection query — access directives

The SDK's introspection query must request directives on fields, args,
and enum values. Add a `DirectiveApplication` fragment:

```graphql
fragment DirectiveApplication on _DirectiveApplication {
  name
  args {
    name
    value
  }
}
```

Add `directives { ...DirectiveApplication }` to:
- Field selections (in `FullType`)
- `InputValue` fragment (for args)
- Enum values (if the SDK uses them)

**Gotcha (PHP):** If the SDK's GraphQL library uses `BuildClientSchema`
or similar to parse introspection results, it likely **drops custom
directives**. The PHP SDK had to abandon `graphql-php`'s
`BuildClientSchema` and parse raw introspection JSON directly via
new `IntrospectionSchema` data structures. Check whether the SDK's
GraphQL library preserves directives before assuming they're available.

| Reference | Commit |
|-----------|--------|
| PHP introspection rewrite | `42a2e83ab` |
| Java directive support | `280de883f` |
| Go/TS (already had directives) | `cmd/codegen/introspection/introspection.go` |

### Step 2: Read `@expectedType` on arguments

When generating method signatures, if an argument's schema type is the
`ID` scalar, check for `@expectedType`. If present, generate the
parameter as the **named type** instead of a raw `ID`/`string`.

```
Argument: directory: ID! @expectedType(name: "Directory")
Generate: directory: Directory  (not: directory: ID)
```

For **list arguments** like `[ID!]! @expectedType(name: "Directory")`,
the element type is `Directory`, parameter is `list[Directory]` / `Directory[]` / etc.

**How each SDK accesses it:**

| SDK | Pattern |
|-----|---------|
| Go/TS | `arg.Directives.ExpectedType()` via shared introspection types |
| Python | `expected_type_name(ctx.schema, graphql.ast_node)` helper |
| PHP | `IntrospectionDirective::getExpectedType()` on raw JSON |
| Java | `field.getExpectedType()` wrapping directive data |

| Reference | Commit |
|-----------|--------|
| Go `formatArgType` | `cmd/codegen/generator/go/templates/functions.go` |
| Python `format_input_type` | `sdk/python/codegen/src/codegen/generator.py` |
| PHP `resolveArgType` | `42a2e83ab` then `7a82827ad` |
| Java `formatInput(expectedType)` | `280de883f` |

### Step 3: Handle `id` and `sync`-like fields (ConvertID)

Fields returning `ID!` where `@expectedType` matches the parent object
are "sync-like" — they execute the query to get an ID string, then
return the parent object type (re-loaded from the ID).

```
Container.sync: ID! @expectedType(name: "Container")
→ returns Container, not string
```

The `id` field is similar: `@expectedType` tells you the type. For `id`,
the parent type name can be inferred if `@expectedType` is missing.

The common Go/TS `ConvertID()` function checks:
`field.Directives.ExpectedType() == field.ParentObject.Name`

| Reference | File |
|-----------|------|
| Go/TS ConvertID | `cmd/codegen/generator/functions.go` |

### Step 4: Remove per-type ID scalars

Stop generating per-type `FooID` classes/types. Generate one `ID` type.

**Approaches taken:**

| SDK | Approach |
|-----|----------|
| Go | Single `type ID string`, no aliases |
| PHP (initial) | Generated per-type `FooId` classes extending `AbstractId`, then realized they were unnecessary |
| PHP (final) | Single `Id` scalar class. `IdAble::id()` returns `Id`. Args with `@expectedType` accept the object type directly (QueryBuilder calls `->id()` automatically) |
| Rust | Removed `ID` from visitor ignore list. Generated `Id` struct with `IntoID<Id>` identity impl. All objects get `IntoID<Id>` impl so they can be passed to ID-typed args |
| Java | Removed `ID` from scalar exclude list. `IDAble<ID>` uses unified ID type |

**Lesson (PHP):** The initial migration generated per-type `FooId` classes
for backward compatibility. A follow-up commit (`7a82827ad`) removed them
entirely — they weren't needed since `@expectedType` already resolved
args to the correct object types. Don't over-engineer the transition.

**Lesson (Rust):** The `ID` scalar was previously in an ignore list
because per-type ID scalars handled everything. After removing it from
the ignore list, the scalar template's ID suffix-stripping logic
produced an empty name for bare `"ID"`. Special-case the bare `ID`
scalar in the template. Also generate `IntoID<Id>` impls on all
objects with an `id` field so the ergonomic `fn foo(container: Container)`
API keeps working.

| Reference | Commit |
|-----------|--------|
| PHP initial (per-type FooId) | `42a2e83ab` |
| PHP final (unified Id) | `7a82827ad` |
| Rust unified Id | `789db3723` |
| Java unified ID | `280de883f` |

### Step 5: Generate native interface types

For types with `kind: "INTERFACE"`, generate the language's native
interface mechanism plus a concrete client class:

| Language | Interface | Client class | Adapters needed? |
|----------|-----------|-------------|-----------------|
| Go | `type Foo interface { ... }` | `FooClient` struct | Yes — `AsFoo()` on implementing objects (Go lacks covariant returns) |
| Python | `@runtime_checkable class Foo(Protocol)` | `_FooClient(Type)` | No — structural typing. `as_foo()` returns `Self` |
| TypeScript | `export interface Foo { ... }` | `_FooClient extends BaseClient` | No — structural typing |
| PHP | `interface Foo { ... }` | `FooClient implements Foo` | No — PHP supports covariant returns |
| Java | `public interface Foo { ... }` | `FooClient implements Foo` | No — Java supports covariant returns |
| Rust | `trait Foo { ... }` (not yet implemented) | — | TBD |

The client class is needed for:
- `loadFooFromID` return values
- Fields returning interface types
- List unmarshalling (instantiate from IDs)

**Data available from introspection:**

```json
{
  "name": "CustomIface",
  "kind": "INTERFACE",
  "fields": [ ... ],
  "possibleTypes": [ {"name": "Impl"}, {"name": "OtherImpl"} ]
}
```

Objects declare which interfaces they implement:
```json
{
  "name": "Impl",
  "kind": "OBJECT",
  "interfaces": [ {"name": "CustomIface"} ]
}
```

**Gotcha (Go codegen):** Interface types from dependency modules must
be emitted in the per-dependency file (`_dep.gen.go.tmpl`), not just
in the core `dagger.gen.go`. The dep template initially only handled
`SCALAR`, `INPUT_OBJECT`, `ENUM`, and `OBJECT` — missing `INTERFACE`
caused undefined type errors. Fixed in `31172e00b`.

| Reference | Commit |
|-----------|--------|
| Go interface template | `cmd/codegen/generator/go/templates/src/_types/interface.go.tmpl` |
| Go AsFoo adapters | `cmd/codegen/generator/go/templates/src/_types/object.go.tmpl:245` |
| Python Protocol | `4c357b4d9` |
| TypeScript interface | `b074b4f43` |
| PHP interface | `42a2e83ab` |
| Java InterfaceVisitor | `280de883f` |
| Go dep file fix | `31172e00b` |

### Step 6: `XXX_GraphQLIDType` returns `"ID"`

Any method/property returning the GraphQL scalar type name for IDs
must return `"ID"` (not `"FooID"`). This affects:
- Object classes
- Interface client classes
- Query builder variable type declarations

### Step 7: Update module runtime

If the SDK's module runtime references `ModuleID`, `FunctionID`, etc.
as explicit types, change to the unified `ID` type:

| SDK | Change |
|-----|--------|
| Go | `module_interfaces.go` uses `dagger.ID` everywhere |
| Python | `_module.py` uses `str` instead of `dagger.ModuleID` |
| TypeScript | `register.ts` uses `ID` instead of `ModuleID` |

| Reference | Commit |
|-----------|--------|
| Go module interfaces | `08f381eb7` |
| Python module runtime | `0850b8958` |
| TypeScript register | `88b762186` |

### Step 8: Update GraphQL query strings

Any hardcoded GraphQL query strings using `$var: FooID!` must become
`$var: ID!`. Check:
- `.graphql` files
- Inline query strings in tests
- SDK-internal queries

| Reference | Commit |
|-----------|--------|
| GraphQL files | `69c1d9700` |
| Integration tests | `f025d8662`, `33f541202` |

### Step 9: Regenerate

Run codegen against the new engine and verify the output.
Check that:
- No `FooID` types remain in generated code
- Interface types generate with the correct language mechanism
- `id()` methods return the unified ID type
- `loadFooFromID` methods accept the unified ID type
- `sync()`-like methods return the parent object type

## Introspection JSON Shape

### Argument with `@expectedType`

```json
{
  "name": "directory",
  "type": { "kind": "NON_NULL", "ofType": { "kind": "SCALAR", "name": "ID" } },
  "directives": [
    { "name": "expectedType", "args": [{ "name": "name", "value": "\"Directory\"" }] }
  ]
}
```

### Field with `@expectedType`

```json
{
  "name": "id",
  "type": { "kind": "NON_NULL", "ofType": { "kind": "SCALAR", "name": "ID" } },
  "directives": [
    { "name": "expectedType", "args": [{ "name": "name", "value": "\"Container\"" }] }
  ]
}
```

### Interface type

```json
{
  "name": "CustomIface",
  "kind": "INTERFACE",
  "fields": [
    { "name": "id", "type": { "kind": "NON_NULL", "ofType": { "kind": "SCALAR", "name": "ID" } } },
    { "name": "quack", "type": { "kind": "NON_NULL", "ofType": { "kind": "SCALAR", "name": "String" } } }
  ],
  "possibleTypes": [
    { "name": "Mallard" },
    { "name": "RubberDuck" }
  ]
}
```

## Key Files (engine side)

| What | Where |
|------|-------|
| `@expectedType` on Go struct args | `dagql/objects.go` — `findExpectedTypeName()` |
| `@expectedType` on module function args | `core/typedef.go` — `FunctionFieldSpec` |
| `@expectedType` on interface field args | `core/interface.go` |
| `@expectedType` on field return types | `dagql/server.go` — field definition generation |
| `@expectedType` directive definition | `dagql/server.go:346` |
| `AnyID` scalar type | `dagql/types.go:717` |
| Interface type | `dagql/interfaces.go` |
| Common codegen (ConvertID, FormatReturnType) | `cmd/codegen/generator/functions.go` |
| Shared introspection types | `cmd/codegen/introspection/introspection.go` |

## Reference Implementations

For a complete migration, study these SDKs in order of complexity:

1. **Rust** (`789db3723`) — Minimal: unified ID scalar only, no interface codegen yet
2. **PHP** (`42a2e83ab` + `7a82827ad`) — Full migration including introspection rewrite and interface codegen
3. **Java** (`280de883f`) — Full migration with InterfaceVisitor
4. **Go** (various) — Most complete, but also most complex due to `AsFoo` adapters and `Concrete()`
