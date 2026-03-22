# Phase 3: Interface Cleanup & SDK Codegen

## Context

Phase 1 added dagql infrastructure for first-class interfaces. Phase 2
switched module interfaces from `Class[*InterfaceAnnotatedValue]` to
`dagql.Interface`. The schema now emits `ast.Interface` definitions,
objects declare `implements`, and `possibleTypes` is populated. The
deprecated `asFoo` fields are no-op identity functions.

This phase removes the deprecated `asFoo` schema fields and legacy
plumbing (`InterfaceAnnotatedValue`, `DynamicID`, `InterfaceValue`),
and updates SDK codegen so that interfaces are represented
idiomatically in each language.

## What Stays

- `dagql.Interface` and its field specs, `ParseField`, `Satisfies`.
- `Class.Implements` / `ImplementInterfaceUnchecked`.
- `loadFooFromID` for interface types (accepts any implementing
  object's ID).
- `DynamicID` as the scalar type for `loadFooFromID` arguments — it
  accepts any encoded ID string and validates at load time.
- `__typename` support (added in this phase).

## What Goes

### 1. `asFoo` fields

Remove from `ModDeps.lazilyLoadSchema`. These are currently deprecated
no-op identity functions (`return self`). After removal, SDKs that
still reference `asFoo` fields will get schema validation errors —
hence the codegen changes below.

### 2. `InterfaceAnnotatedValue`

Used in three places:

- **`InterfaceType.Install`**: as the `Type` of the `loadFooFromID`
  field spec. Replace with a thin `Typed` wrapper that returns the
  interface's `ast.Type` (just needs `Type()` and
  `TypeDefinition()`).
- **`InterfaceType.CollectContent`**: to unwrap an interface value
  and find the underlying `ModuleObject` for content collection.
  After Phase 2 the value reaching `CollectContent` is either a
  `*ModuleObject` directly or loaded via `loadImpl` — the
  `InterfaceAnnotatedValue` path is dead. Remove it and keep only
  the `*ModuleObject` path.
- **`ModuleObjectType.ConvertToSDKInput`**: handles the case where
  a `DynamicID` wrapping an `InterfaceAnnotatedValue` is received.
  After Phase 2, interface-typed inputs arrive as `DynamicID`
  wrapping a bare ID (no `InterfaceAnnotatedValue`). The
  `ObjectResult[*InterfaceAnnotatedValue]` case is dead. Remove it.

### 3. `dagql.InterfaceValue` interface

Only used in `toSelectable` to unwrap `InterfaceAnnotatedValue` and
find the underlying object. Once `InterfaceAnnotatedValue` is gone,
remove `InterfaceValue` from `dagql/types.go` and the unwrapping
path in `toSelectable`.

### 4. `wrapIface`

Already dead code (nobody calls it). Delete.

## SDK Codegen Changes

### Guiding Principle

In every SDK language, `loadFooFromID` and any field returning an
interface should return the **interface type**, not a concrete type.
The caller can then duck-type against the interface. To narrow to a
concrete type when needed, each language provides an idiomatic
mechanism.

### Go

**Current state**: INTERFACE types generate the same Go struct as
OBJECT types (via `_types/object.go.tmpl`). The `asFoo` method is
generated as a regular field method backed by a schema field.

**Target state**: INTERFACE types generate a **Go interface** plus
a **query-builder implementation struct** (for `loadFooFromID` and
fields returning the interface). Concrete objects that `implements`
the interface get an `AsDuck() Duck` method as a **client-side
adapter** (no schema field) for covariant return narrowing. The
interface declares a `Concrete(ctx) (DaggerObject, error)` method
for narrowing to the underlying concrete type.

#### Why `AsFoo()` is still needed in Go

Go does not support covariant return types on interface methods. If
interface `Duck` declares `WithName(string) Duck`, concrete struct
`*Mallard` cannot satisfy `Duck` — its `WithName` returns `*Mallard`,
not `Duck`. The `AsDuck()` adapter wraps `*Mallard` in a generated
struct whose methods return `Duck`.

Note: `AsFoo()` is purely SDK-side. It is NOT a schema field. It does
no GraphQL calls — it's a local type conversion.

#### Generated types for interface `Duck`

Suppose the interface `Duck` has two possible types: `Mallard` and
`RubberDuck`.

```go
// The Go interface — matches the GraphQL interface's fields,
// plus Concrete() for narrowing.
type Duck interface {
    DaggerObject

    ID(ctx context.Context) (DuckID, error)
    Quack(ctx context.Context) (string, error)
    WithName(name string) Duck  // covariant: returns interface

    // Resolve the concrete type behind this interface value.
    // The returned DaggerObject is one of the possibleTypes
    // (*Mallard, *RubberDuck) and can be type-switched.
    Concrete(ctx context.Context) (DaggerObject, error)
}

// The ID scalar (unchanged).
type DuckID string
```

#### Query-builder struct for the interface

When a field returns an interface type (or `loadFooFromID` is
called), the SDK needs a concrete struct to build queries against.
This struct satisfies the `Duck` Go interface:

```go
// duckClient is the query-builder for the interface.
// Unexported — users interact via the Duck interface.
type duckClient struct {
    query *querybuilder.Selection
}

func (r *duckClient) Quack(ctx context.Context) (string, error) {
    q := r.query.Select("quack")
    var response string
    q = q.Bind(&response)
    return response, q.Execute(ctx)
}

func (r *duckClient) WithName(name string) Duck {
    return &duckClient{query: r.query.Select("withName").Arg("name", name)}
}

// Concrete resolves __typename + ID to return the actual concrete type.
func (r *duckClient) Concrete(ctx context.Context) (DaggerObject, error) {
    // Query __typename to determine the concrete type.
    q := r.query.Select("__typename")
    var typeName string
    q = q.Bind(&typeName)
    if err := q.Execute(ctx); err != nil {
        return nil, err
    }
    // Get the ID so we can load the concrete object.
    id, err := r.ID(ctx)
    if err != nil {
        return nil, err
    }
    // Type switch generated from possibleTypes.
    switch typeName {
    case "Mallard":
        return dag.LoadMallardFromID(MallardID(id)), nil
    case "RubberDuck":
        return dag.LoadRubberDuckFromID(RubberDuckID(id)), nil
    default:
        return nil, fmt.Errorf("unknown Duck implementation: %s", typeName)
    }
}

// DaggerObject methods
func (r *duckClient) XXX_GraphQLType() string  { return "Duck" }
func (r *duckClient) XXX_GraphQLIDType() string { return "DuckID" }
func (r *duckClient) XXX_GraphQLID(ctx context.Context) (string, error) { ... }
func (r *duckClient) MarshalJSON() ([]byte, error) { ... }
```

#### Generated `AsDuck` on implementing objects

For each object `Mallard` whose schema `Interfaces` includes `Duck`:

```go
// Adapter — wraps *Mallard so it satisfies the Duck Go interface.
type mallardAsDuck struct {
    *Mallard
}

// AsDuck returns this Mallard as a Duck.
// This is a local type conversion — no GraphQL call.
func (r *Mallard) AsDuck() Duck {
    return &mallardAsDuck{r}
}

// Forwarding methods — same logic as Mallard but returns Duck.
func (w *mallardAsDuck) Quack(ctx context.Context) (string, error) {
    return w.Mallard.Quack(ctx)
}
func (w *mallardAsDuck) WithName(name string) Duck {
    return &mallardAsDuck{w.Mallard.WithName(name)}
}

// Concrete returns the inner *Mallard — no call needed.
func (w *mallardAsDuck) Concrete(ctx context.Context) (DaggerObject, error) {
    return w.Mallard, nil
}

// DaggerObject methods delegate to inner Mallard.
func (w *mallardAsDuck) XXX_GraphQLType() string { return w.Mallard.XXX_GraphQLType() }
// etc.
```

#### Usage: narrowing from interface to concrete type

```go
duck := test.GetDuck()

// Type switch to handle each possible concrete type.
obj, err := duck.Concrete(ctx)
if err != nil {
    return err
}
switch v := obj.(type) {
case *Mallard:
    v.MallardOnlyMethod()
case *RubberDuck:
    v.Squeak()
}
```

This works regardless of how the `Duck` was obtained:
- From `AsDuck()` adapter → `Concrete()` returns immediately with
  the inner `*Mallard`, no network call.
- From `duckClient` (lazy query builder) → `Concrete()` queries
  `__typename` and `id`, then loads the concrete type via
  `loadFooFromID`.

#### `loadFooFromID`

Returns `Duck` (the Go interface). The implementation uses the
query-builder struct:

```go
func (c *Client) LoadDuckFromID(id DuckID) Duck {
    return &duckClient{
        query: querybuilder.Query().
            Client(c.GraphQLClient()).
            Select("loadDuckFromID").
            Arg("id", id),
    }
}
```

The caller gets a `Duck` they can call methods on. The concrete
type resolves lazily when fields are queried. To get the concrete
type, call `Concrete(ctx)`.

#### Fields returning an interface

Any field whose return type is an INTERFACE kind uses the
query-builder struct:

```go
func (r *Test) GetDuck() Duck {
    return &duckClient{query: r.query.Select("getDuck")}
}
```

#### Data available in codegen

- `Type.Kind == "INTERFACE"` — identifies interface types
- `Type.Fields` — the interface's field definitions
- `Type.PossibleTypes` — which objects implement it (**newly added**)
- `Type.Interfaces` (on objects) — which interfaces an object
  implements

#### Module runtime codegen (`dagger.gen.go` + `module_interfaces.go`)

The module-side codegen already generates `customIfaceImpl` structs
for interface-typed arguments/returns. This continues to work as-is.
The module-side `LoadCustomIfaceFromID` already returns the Go
interface type.

### TypeScript

**Current state**: INTERFACE types generate the same `class` as
OBJECT types. `asFoo` is generated as a method backed by a schema
field.

**Target state**: INTERFACE types generate a TypeScript `interface`
for type-checking, plus a concrete query-builder `class` (like Go's
`duckClient`) for `loadFooFromID` and interface-typed field returns.
The `asFoo` schema-backed methods are removed.

TypeScript supports covariant return types, so concrete classes can
directly extend or implement the interface without adapter wrappers.

#### Generated types for interface `Duck`

```typescript
// TypeScript interface for type-checking.
export interface Duck {
    quack(): Promise<string>
    withName(name: string): Duck

    // Resolve the concrete type.
    concrete(): Promise<Mallard | RubberDuck>
}

// Query-builder class for loadFooFromID and interface-typed returns.
export class DuckClient extends BaseClient implements Duck {
    async quack(): Promise<string> {
        // query builder
    }
    withName(name: string): Duck {
        return new DuckClient(this._ctx, ...)
    }
    async concrete(): Promise<Mallard | RubberDuck> {
        const typeName = await this.__typename()
        const id = await this.id()
        switch (typeName) {
            case "Mallard": return dag.loadMallardFromID(id)
            case "RubberDuck": return dag.loadRubberDuckFromID(id)
            default: throw new Error(`unknown: ${typeName}`)
        }
    }
}
```

Concrete classes like `Mallard` already structurally satisfy `Duck`
(TypeScript structural typing). They implement `concrete()` by
returning `this`.

#### Narrowing to concrete type

```typescript
const duck: Duck = getDuck()
const obj = await duck.concrete()
if (obj instanceof Mallard) {
    obj.mallardOnlyMethod()
}
```

### Python

**Current state**: INTERFACE types generate the same `class` as
OBJECT types. `asFoo` is generated as a method.

**Target state**: INTERFACE types generate a Python class (same
as current, but without `asFoo` methods). Add a `concrete()` method
that resolves to the underlying type. Python's duck typing means
concrete objects are structurally compatible without any special
mechanism.

#### Generated types

```python
class Duck(BaseClient):
    """A duck that can quack."""

    async def quack(self) -> str:
        ...

    def with_name(self, name: str) -> "Duck":
        ...

    async def concrete(self) -> "Mallard | RubberDuck":
        type_name = await self.__typename()
        id = await self.id()
        match type_name:
            case "Mallard":
                return dag.load_mallard_from_id(id)
            case "RubberDuck":
                return dag.load_rubber_duck_from_id(id)
            case _:
                raise ValueError(f"unknown: {type_name}")
```

#### Narrowing to concrete type

```python
duck: Duck = get_duck()
obj = await duck.concrete()
if isinstance(obj, Mallard):
    obj.mallard_only_method()
```

## Implementation Order

1. **Remove `asFoo` from schema** (`core/moddeps.go`).

2. **Remove dead code**: `wrapIface`, `InterfaceAnnotatedValue`,
   `InterfaceValue`, and the dead branches that reference them.

3. **Go codegen**: Generate Go interface + query-builder struct for
   INTERFACE kinds. Generate `AsFoo()` adapter on implementing
   objects. Generate `Concrete()` method with type switch from
   `PossibleTypes`. Fix `loadFooFromID` and interface-returning
   fields to return the Go interface.

4. **TypeScript codegen**: Generate TS interface + query-builder
   class for INTERFACE kinds. Remove `asFoo` references. Generate
   `concrete()` with type switch. Fix interface-returning fields.

5. **Python codegen**: Remove `asFoo` references. Generate
   `concrete()` with type switch. Interface classes stay as regular
   generated classes.

6. **Update integration tests**: Rewrite test modules and test
   assertions. Go tests use `AsFoo()` adapter and `Concrete()`.
   TS/Python tests use `concrete()`.
