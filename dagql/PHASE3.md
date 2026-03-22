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
mechanism (Go: type assertion via `AsFoo`; TS: type guard +
structural compatibility; Python: `isinstance` on the generated
class).

### Go

**Current state**: INTERFACE types generate the same Go struct as
OBJECT types (via `_types/object.go.tmpl`). The `asFoo` method is
generated as a regular field method backed by a schema field.

**Target state**: INTERFACE types generate a **Go interface** plus
a **query-builder implementation struct** (for `loadFooFromID` and
fields returning the interface). Concrete objects that `implements`
the interface get an `AsFoo() Foo` method as a **client-side
adapter** (no schema field) for covariant return narrowing.

#### Why `AsFoo()` is still needed in Go

Go does not support covariant return types on interface methods. If
interface `Duck` declares `WithName(string) Duck`, concrete struct
`*Mallard` cannot satisfy `Duck` — its `WithName` returns `*Mallard`,
not `Duck`. The `AsDuck()` adapter wraps `*Mallard` in a generated
struct whose methods return `Duck`.

Note: `AsFoo()` is purely SDK-side. It is NOT a schema field. It does
no GraphQL calls — it's a local type conversion.

#### Generated types for interface `TestCustomIface`

```go
// The Go interface — matches the GraphQL interface's fields.
type TestCustomIface interface {
    DaggerObject

    ID(ctx context.Context) (TestCustomIfaceID, error)
    Str(ctx context.Context) (string, error)
    WithStr(strArg string) TestCustomIface  // covariant: returns interface
    Obj() *Directory
    SelfIface() TestCustomIface
    OtherIface() TestOtherIface
    // ...
}

// The ID scalar (unchanged).
type TestCustomIfaceID string
```

#### Query-builder struct for the interface

When a field returns an interface type (or `loadFooFromID` is
called), the SDK needs a concrete struct to build queries against.
This is the same pattern already used for objects, but the struct's
methods that return the same interface type return the **interface**
rather than a struct pointer:

```go
// testCustomIfaceClient is the query-builder for the interface.
// Unexported — users interact via the TestCustomIface interface.
type testCustomIfaceClient struct {
    query *querybuilder.Selection
}

func (r *testCustomIfaceClient) ID(ctx context.Context) (TestCustomIfaceID, error) { ... }
func (r *testCustomIfaceClient) Str(ctx context.Context) (string, error) { ... }
func (r *testCustomIfaceClient) WithStr(strArg string) TestCustomIface {
    // returns testCustomIfaceClient (satisfies TestCustomIface)
    return &testCustomIfaceClient{query: r.query.Select("withStr").Arg("strArg", strArg)}
}
func (r *testCustomIfaceClient) SelfIface() TestCustomIface {
    return &testCustomIfaceClient{query: r.query.Select("selfIface")}
}
func (r *testCustomIfaceClient) OtherIface() TestOtherIface {
    return &testOtherIfaceClient{query: r.query.Select("otherIface")}
}

// DaggerObject methods
func (r *testCustomIfaceClient) XXX_GraphQLType() string    { return "TestCustomIface" }
func (r *testCustomIfaceClient) XXX_GraphQLIDType() string   { return "TestCustomIfaceID" }
func (r *testCustomIfaceClient) XXX_GraphQLID(ctx context.Context) (string, error) { ... }
func (r *testCustomIfaceClient) MarshalJSON() ([]byte, error) { ... }
```

#### Generated `AsFoo` on implementing objects

For each object `Impl` whose schema `Interfaces` includes
`TestCustomIface`:

```go
// Adapter — wraps *Impl so it satisfies the TestCustomIface Go interface.
type implAsTestCustomIface struct {
    *Impl
}

// AsTestCustomIface returns this Impl as a TestCustomIface.
// This is a local type conversion — no GraphQL call.
func (r *Impl) AsTestCustomIface() TestCustomIface {
    return &implAsTestCustomIface{r}
}

// Forwarding methods — same logic as Impl but returns the interface type.
func (w *implAsTestCustomIface) Str(ctx context.Context) (string, error) {
    return w.Impl.Str(ctx)
}
func (w *implAsTestCustomIface) WithStr(strArg string) TestCustomIface {
    return &implAsTestCustomIface{w.Impl.WithStr(strArg)}
}
func (w *implAsTestCustomIface) SelfIface() TestCustomIface {
    return &implAsTestCustomIface{w.Impl.SelfIface()}
}
func (w *implAsTestCustomIface) OtherIface() TestOtherIface {
    return w.Impl.OtherIface().AsTestOtherIface()
}

// DaggerObject methods delegate to inner Impl.
func (w *implAsTestCustomIface) XXX_GraphQLType() string { return w.Impl.XXX_GraphQLType() }
// etc.
```

#### Going from interface back to concrete type

Given a `TestCustomIface` value, a caller may need the underlying
concrete type. This uses standard Go type assertion:

```go
var duck TestCustomIface = getDuck()
if mallard, ok := duck.(*Impl); ok {
    // use mallard-specific fields
}
// or if it came through AsTestCustomIface:
if adapter, ok := duck.(*implAsTestCustomIface); ok {
    mallard := adapter.Impl
}
```

For ergonomics, we should also consider a helper on the interface's
query-builder struct or a top-level function, but the type assertion
pattern is standard Go and may be sufficient for v1.

#### `loadFooFromID`

Returns `TestCustomIface` (the Go interface). The implementation
uses the query-builder struct:

```go
func (c *Client) LoadTestCustomIfaceFromID(id TestCustomIfaceID) TestCustomIface {
    return &testCustomIfaceClient{
        query: querybuilder.Query().
            Client(c.GraphQLClient()).
            Select("loadTestCustomIfaceFromID").
            Arg("id", id),
    }
}
```

This is clean — the caller gets a `TestCustomIface` they can call
methods on, and the concrete type behind it is resolved lazily when
fields are actually queried.

#### Fields returning an interface

Any field whose return type is an INTERFACE kind uses the
query-builder struct:

```go
func (r *Test) GetDuck() TestCustomIface {
    return &testCustomIfaceClient{query: r.query.Select("getDuck")}
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

**Target state**: INTERFACE types generate a TypeScript `abstract
class` (or `interface`). Concrete classes that implement the
interface use `extends` (or `implements`). The `asFoo` methods are
removed.

TypeScript supports covariant return types, so concrete classes can
directly implement the interface's method signatures. No adapter
wrapper is needed.

#### Generated types for interface `Duck`

```typescript
export abstract class Duck extends BaseClient {
    abstract quack(): Promise<string>
    abstract withName(name: string): Duck
}
```

Or as a TypeScript interface (preferred for structural typing):

```typescript
export interface Duck {
    quack(): Promise<string>
    withName(name: string): Duck
}
```

#### Fields returning an interface

Use the query-builder class (similar to objects but typed as the
interface):

```typescript
export class DuckClient extends BaseClient implements Duck {
    quack = async (): Promise<string> => {
        // query builder
    }
    withName = (name: string): Duck => {
        return new DuckClient(this._ctx, ...)
    }
}
```

`loadDuckFromID` and interface-typed fields return `Duck` (the
interface) via `DuckClient`.

#### Narrowing to concrete type

Use `__typename` plus a type guard:

```typescript
const duck: Duck = getDuck()
if (duck instanceof Mallard) {
    // mallard-specific methods
}
```

### Python

**Current state**: INTERFACE types generate the same `class` as
OBJECT types. `asFoo` is generated as a method.

**Target state**: INTERFACE types generate a Python class (can use
`typing.Protocol` or a regular class). The `asFoo` methods are
removed. Python's duck typing means concrete objects are already
structurally compatible.

#### Generated types

```python
class Duck(BaseClient):
    """A duck that can quack."""

    async def quack(self) -> str:
        ...

    def with_name(self, name: str) -> "Duck":
        ...
```

This is essentially the same as the current generated class, just
without `asFoo` methods. Concrete classes like `Mallard` have the
same method signatures and are assignable to `Duck`-typed variables.

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
   objects. Fix `loadFooFromID` and interface-returning fields to
   return the Go interface.

4. **TypeScript codegen**: Generate interface or abstract class for
   INTERFACE kinds. Remove `asFoo` references. Fix interface-
   returning fields.

5. **Python codegen**: Remove `asFoo` references. Interface classes
   stay as regular generated classes (already structurally compatible).

6. **Update integration tests**: Rewrite test modules and test
   assertions. Go tests use `AsFoo()` adapter. TS/Python tests pass
   concrete objects directly.
