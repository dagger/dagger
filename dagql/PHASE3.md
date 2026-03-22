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
the interface get an `AsTestCustomIface() TestCustomIface` method
as a **client-side adapter** (no schema field) for covariant return
narrowing. The interface itself declares `AsImpl() (*Impl, bool)`
methods for each possible type, enabling type-safe narrowing.

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
// plus narrowing methods for each possibleType.
type Duck interface {
    DaggerObject

    ID(ctx context.Context) (DuckID, error)
    Quack(ctx context.Context) (string, error)
    WithName(name string) Duck  // covariant: returns interface

    // Narrowing — one per possibleType.
    // Returns (concrete, true) if the underlying value IS that type,
    // or (nil, false) if it isn't.
    AsMallard() (*Mallard, bool)
    AsRubberDuck() (*RubberDuck, bool)
}

// The ID scalar (unchanged).
type DuckID string
```

#### Query-builder struct for the interface

When a field returns an interface type (or `loadFooFromID` is
called), the SDK needs a concrete struct to build queries against.
This struct satisfies the `Duck` Go interface. Since it doesn't
know the concrete type (it's a lazy query builder), its narrowing
methods all return `(nil, false)`:

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

// Narrowing — lazy client doesn't know the concrete type.
func (r *duckClient) AsMallard() (*Mallard, bool)       { return nil, false }
func (r *duckClient) AsRubberDuck() (*RubberDuck, bool)  { return nil, false }

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

// Narrowing — this IS a Mallard, so AsMallard succeeds.
func (w *mallardAsDuck) AsMallard() (*Mallard, bool)      { return w.Mallard, true }
func (w *mallardAsDuck) AsRubberDuck() (*RubberDuck, bool) { return nil, false }

// DaggerObject methods delegate to inner Mallard.
func (w *mallardAsDuck) XXX_GraphQLType() string { return w.Mallard.XXX_GraphQLType() }
// etc.
```

#### Narrowing from interface to concrete type

Given a `Duck` value, the caller narrows using the methods on the
interface:

```go
var duck Duck = getDuck()
if mallard, ok := duck.AsMallard(); ok {
    // use mallard-specific fields
    mallard.MallardOnlyField()
}
```

This works regardless of how the `Duck` was obtained:
- From `AsDuck()` adapter → `AsMallard()` returns the inner `*Mallard`.
- From `duckClient` (lazy query builder) → `AsMallard()` returns
  `(nil, false)`. The caller must have loaded the concrete type
  first (e.g., via `loadMallardFromID`).

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
type resolves lazily when fields are queried. Since the query-
builder doesn't know the concrete type, `AsMallard()` etc. return
false. To narrow after loading by ID, the caller should use
`loadMallardFromID` directly instead.

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
}

// Query-builder class for loadFooFromID and interface-typed returns.
export class DuckClient extends BaseClient implements Duck {
    async quack(): Promise<string> {
        // query builder
    }
    withName(name: string): Duck {
        return new DuckClient(this._ctx, ...)
    }
}
```

Concrete classes like `Mallard` already structurally satisfy `Duck`
in TypeScript (structural typing). No adapter needed.

#### Narrowing to concrete type

```typescript
const duck: Duck = getDuck()
if (duck instanceof Mallard) {
    // narrowed to Mallard
}
```

For values returned through `DuckClient` (lazy query builder),
`instanceof Mallard` returns false. The caller should load the
concrete type directly or use `__typename` to determine the type.

### Python

**Current state**: INTERFACE types generate the same `class` as
OBJECT types. `asFoo` is generated as a method.

**Target state**: INTERFACE types generate a Python class (same
as current, but without `asFoo` methods). Python's duck typing
means concrete objects are structurally compatible without any
special mechanism.

#### Generated types

```python
class Duck(BaseClient):
    """A duck that can quack."""

    async def quack(self) -> str:
        ...

    def with_name(self, name: str) -> "Duck":
        ...
```

Concrete classes like `Mallard` have the same method signatures and
are assignable to `Duck`-typed variables.

#### Narrowing to concrete type

```python
duck: Duck = get_duck()
if isinstance(duck, Mallard):
    # mallard-specific methods
```

## Implementation Order

1. **Remove `asFoo` from schema** (`core/moddeps.go`).

2. **Remove dead code**: `wrapIface`, `InterfaceAnnotatedValue`,
   `InterfaceValue`, and the dead branches that reference them.

3. **Go codegen**: Generate Go interface + query-builder struct for
   INTERFACE kinds. Generate `AsFoo()` adapter on implementing
   objects. Generate narrowing methods (`AsMallard`, etc.) on the
   interface. Fix `loadFooFromID` and interface-returning fields to
   return the Go interface.

4. **TypeScript codegen**: Generate TS interface + query-builder
   class for INTERFACE kinds. Remove `asFoo` references. Fix
   interface-returning fields.

5. **Python codegen**: Remove `asFoo` references. Interface classes
   stay as regular generated classes (already structurally
   compatible).

6. **Update integration tests**: Rewrite test modules and test
   assertions. Go tests use `AsFoo()` adapter and narrowing methods.
   TS/Python tests pass concrete objects directly.
