# dagql API Server

dagql is Dagger's custom GraphQL server implementation that sits on top of standard GraphQL. It enables the immutable DAG model described in the skill overview.

## What Makes dagql Different

Standard GraphQL only passes scalars between operations. dagql extends this by:

1. **ID-able objects**: Every installed object type gets:
   - An `id: <TypeName>ID` field that returns the ID (the call chain that created it)
   - A `<TypeName>ID` scalar type
   - A `load<TypeName>FromID(id: <TypeName>ID): <TypeName>` field on Query

2. **Object arguments become IDs**: When a field takes an object argument, the GraphQL schema uses `<TypeName>ID` as the type. The ID encodes the full call chain.

3. **Automatic caching**: Results are cached by their ID digest. Same inputs → same digest → cache hit.

## Core Types

### Server

`Server` (`server.go`) is the GraphQL server. It holds installed types (objects, scalars, typedefs, directives), manages schema generation, and handles query resolution. Each server has a `Cache` (`SessionCache`) and a root object (typically `Query`).

Key methods: `InstallObject`, `InstallScalar`, `Resolve`, `Load`, `Select`. The server also holds a `View` which affects which fields are visible (used for API versioning).

### Typed / ScalarType / Input

**`Typed`** (`types.go:20`) is the fundamental interface - any value that knows its GraphQL type via `Type() *ast.Type`. Almost everything in dagql implements `Typed`.

**`ScalarType`** (`types.go:146`) represents a GraphQL scalar. It combines `Type` (has a name) with `InputDecoder` (can decode values). Built-in scalars: `Int`, `Float`, `Boolean`, `String`.

**`Input`** (`types.go:152`) represents values passable as arguments. All Inputs are `Typed`, can be converted to a `call.Literal` (for ID encoding), and have a `Decoder()` to create new instances. Inputs are the "leaves" of the argument tree.

### Class and Field

**`Class[T]`** (`objects.go:28`) represents a GraphQL Object type. It holds a map of `Field[T]` by name and provides methods to install fields, look them up, and call them. When an object is installed, if it's ID-able (default), the class auto-installs an `id` field and registers the `<TypeName>ID` scalar.

**`Field[T]`** (`objects.go:1332`) defines a field on an object. It has a `Spec` (metadata: name, args, return type, cache config) and a `Func` (the resolver function). Fields are created via `Func`, `NodeFunc`, or their `WithCacheKey` variants.

**`FieldSpec`** (`objects.go:935`) contains field metadata including `Name`, `Args` (InputSpecs), `Type` (return type), `DoNotCache`, `TTL`, and `GetCacheConfig` (custom cache key function).

### ObjectType

**`ObjectType`** (`types.go:37`) is the interface for GraphQL object types. `Class[T]` implements this. Key methods: `Typed()` (returns a zero value for type info), `IDType()` (returns the ID scalar type), `New(val)` (creates an instance), `ParseField` (parses a field selection), `Extend` (adds fields dynamically).

### Result Types

**`AnyResult`** (`types.go:77`) is a `Typed` value wrapped with its constructor ID. This is the interface - it doesn't know the concrete type at compile time. Key methods: `ID()`, `Unwrap()` (get inner value), `DerefValue()` (unwrap nullables), `NthValue(int)` (index into arrays).

**`AnyObjectResult`** (`types.go:103`) extends `AnyResult` for selectable objects. Adds `ObjectType()`, `Call(ctx, srv, id)`, and `Select(ctx, srv, selector)`.

**`Result[T]`** (`objects.go:332`) is the generic implementation of `AnyResult`. It wraps a typed value `self` with its `constructor` ID. Immutable - methods return new instances. Also carries optional `postCall` callback and `safeToPersistCache` flag.

**`ObjectResult[T]`** (`objects.go:424`) extends `Result[T]` with a `class` reference, enabling field selection. This is what field resolvers receive when using `NodeFunc`. Key methods: `Self()` (get unwrapped value), `ID()` (get constructor ID), `Select()`, `Call()`.

### ID[T]

**`ID[T]`** (`types.go:711`) is the typed ID scalar. It wraps a `*call.ID` and knows its expected type. Used as the GraphQL scalar type for object IDs (e.g., `ContainerID`). Implements both `Input` (can be passed as argument) and `IDable` (has an ID).

Key methods: `Encode()` / `Decode()` (base64 string conversion), `Load(ctx, server)` (evaluate the ID to get the object), `Display()` (human-readable representation).

### Enum

**`EnumValues[T]`** (`types.go:1169`) represents a GraphQL enum type. It's a slice of `EnumValue[T]` with methods to register values, look them up, and generate schema definitions. Enums can have aliases (multiple names for the same underlying value) and view filters.

**`EnumValueName`** (`types.go:1311`) is a runtime representation of an enum value when the concrete type isn't known at compile time.

### InputObject

**`InputObject[T]`** (`server.go:1208`) represents a GraphQL input object. Unlike regular objects, input objects are passed as arguments (not returned). They decode from maps and have fields defined via struct tags.

**`InputObjectSpec`** (`types.go:1378`) is the schema definition for an input object, holding name, description, and fields.

### Wrapper and UnwrapAs

**`Wrapper`** (`types.go:181`) is an interface for types that wrap another type. Implementations have `Unwrap() Typed`. Many dagql types implement this: `Result`, `Optional`, `Nullable`, `DynamicArrayOutput`, etc.

**`UnwrapAs[T](val)`** (`types.go:190`) recursively unwraps a value until it finds type `T` or fails. Essential for extracting concrete types from wrapped results:

```go
// UnwrapAs checks val.(T) first, then unwraps if needed
if container, ok := dagql.UnwrapAs[*core.Container](val); ok {
    // use container
}
```

Note: The order matters - it checks `val.(T)` before unwrapping, since sometimes `T` itself implements `Wrapper`.

### Optional, Nullable, and Derefable

**`Derefable`** (`nullables.go:18`) is an interface for types that may or may not have a value. Method: `Deref() (Typed, bool)`. Used for optional/nullable handling.

**`DerefableResult`** (`nullables.go:24`) extends `Derefable` with `DerefToResult()` which returns an `AnyResult` with proper ID construction.

**`Optional[I Input]`** (`nullables.go:31`) wraps an `Input` type and allows it to be null. Used for optional **arguments**. Has `Value` and `Valid` fields. Helper: `Opt(v)` creates a set optional, `NoOpt[T]()` creates empty.

**`Nullable[T Typed]`** (`nullables.go:254`) wraps any `Typed` value and allows it to be null. Used for optional **return values**. Implements `DerefableResult` so the resolution flow can unwrap it properly. Helpers: `NonNull(v)`, `Null[T]()`.

**`DynamicOptional`** / **`DynamicNullable`** (`nullables.go:162`, `317`) are non-generic versions used when the element type isn't known at compile time. Used internally by reflection-based code.

### Array Types

**`Array[T Typed]`** (`types.go:987`) is an array of typed values for **return values**. Implements `Enumerable` (has `Len()`, `Nth(int)`, `NthValue()`). Helper constructors: `NewStringArray`, `NewIntArray`, etc.

**`ArrayInput[I Input]`** (`types.go:899`) is an array of input values for **arguments**. Implements both `Input` and `Enumerable`.

**`ResultArray[T]`** / **`ObjectResultArray[T]`** (`types.go:1064`, `1110`) are arrays of `Result[T]` / `ObjectResult[T]`. Used when returning arrays of objects that already have their IDs.

**`DynamicArrayOutput`** / **`DynamicArrayInput`** (`builtins.go:78`, `259`) are non-generic array types used by reflection-based code when element type isn't known at compile time.

**`Enumerable`** (`types.go:886`) is the interface for array-like types. Methods: `Element()` (element type), `Len()`, `Nth(int)` (1-indexed!), `NthValue(int, *call.ID)` (returns `AnyResult` with proper ID).

### Builtin Conversion Utilities

**`builtinOrTyped(val)`** (`builtins.go:14`) converts Go primitives and slices to dagql `Typed` values. Handles `string→String`, `int→Int`, `[]T→DynamicArrayOutput`, `*T→DynamicNullable`.

**`builtinOrInput(val)`** (`builtins.go:196`) same but for `Input` types. Used when processing arguments.

## Defining Fields

Fields are defined using `Func`, `NodeFunc`, and their cache-key variants.

### Func vs NodeFunc

```go
// Func: receives the unwrapped value (T)
dagql.Func("fieldName", func(ctx context.Context, self *core.Container, args MyArgs) (*core.Container, error) {
    // self is the unwrapped *core.Container
})

// NodeFunc: receives ObjectResult[T] - access to ID
dagql.NodeFunc("fieldName", func(ctx context.Context, self dagql.ObjectResult[*core.Container], args MyArgs) (*core.Container, error) {
    // self.ID() gives the current ID
    // self.Self() gives the unwrapped value
})
```

Use `NodeFunc` when you need:
- Access to the current ID (`self.ID()`)
- To build IDs for returned values

### Returning ObjectResult with a Different ID

**This is crucial for cache optimization.** By default, a field's return value gets the operation's ID (parent + field + args). But when you return an `ObjectResult[T]`, the returned object can have a **completely different ID**. Subsequent field calls chain off that new ID, not the original operation.

This enables **cache sharing** across different query paths that arrive at the same underlying object.

**Example**: Consider these two query paths:
```
# Path A: extract directory from container, then modify it
container
  .withDirectory("/dir", directory.withNewFile("foo", "foo"))
  .directory("/dir")
  .withNewFile("bar", "bar")

# Path B: modify directory directly
directory
  .withNewFile("foo", "foo")
  .withNewFile("bar", "bar")
```

When `container.directory("/dir")` returns, it can return an `ObjectResult[*core.Directory]` whose ID is just the directory's own operation chain (`directory.withNewFile("foo", "foo")`), not the container operation chain. Then `.withNewFile("bar", "bar")` appends to the directory's ID.

Result: Both paths produce objects with the **same ID**, so they share cache.

**Implementation**: Return `ObjectResult[T]` (or `dagql.Result[T]`) instead of just `T`:

```go
// Returns T - ID will be the operation itself
dagql.Func("directory", func(ctx context.Context, self *core.Container, args dirArgs) (*core.Directory, error) {
    dir := self.GetDirectory(args.Path)
    return dir, nil  // ID = container.directory(path: "/dir")
})

// Returns ObjectResult - can have different ID
dagql.NodeFunc("directory", func(ctx context.Context, self dagql.ObjectResult[*core.Container], args dirArgs) (dagql.ObjectResult[*core.Directory], error) {
    dir := self.Self().GetDirectory(args.Path)
    // Return with the directory's own ID, not the container operation
    return dagql.NewObjectResultForID(dir, srv, dir.ID())
})
```

The resolution flow handles this in `call()` (`objects.go:752-776`): if the returned value is `IDable` and has a different digest than the operation, it adds a secondary cache entry under that digest.

### Custom Cache Keys

```go
// FuncWithCacheKey: Func + cache customization
dagql.FuncWithCacheKey("currentModule", s.currentModule, dagql.CachePerClient)

// NodeFuncWithCacheKey: NodeFunc + cache customization
dagql.NodeFuncWithCacheKey("cacheVolume", s.cacheVolume, s.cacheVolumeCacheKey)
```

Pre-built cache key functions (in `dagql/cachekey.go`):

| Function | Behavior |
|----------|----------|
| `CachePerClient` | Mixes client ID into cache key |
| `CachePerSession` | Mixes session ID into cache key |
| `CachePerCall` | Uses random key - never caches, but result is cacheable |
| `CachePerSchema` | Mixes schema digest into cache key |
| `CachePerClientSchema` | Combines client + schema |

Custom cache key function signature:
```go
func(ctx context.Context, inst ObjectResult[T], args A, req GetCacheConfigRequest) (*GetCacheConfigResponse, error)
```

## Resolution Flow

When a GraphQL query is executed:

```
Server.Resolve(ctx, root, selections...)
    └── resolvePath(ctx, self, selection)
            └── ObjectResult.Select(ctx, server, selector)
                    └── preselect()  // Build ID, compute cache key
                    └── call()       // Cache lookup + execution
```

### preselect (`objects.go:497`)

1. Look up field spec from class
2. Build arguments (from selector + defaults)
3. Create new ID via `receiver.Append(...)`
4. Compute default cache key from ID digest
5. **If `GetCacheConfig` is set**: call it to customize cache key

### call (`objects.go:689`)

1. Set current ID in context (`idToContext`)
2. Call `Cache.GetOrInitializeWithCallbacks`:
   - On cache hit: return cached result
   - On miss: execute `Class.Call` → field's `Func`
3. Run any `PostCall` callback
4. If returned value has different digest, add secondary cache entry

## Server Key Methods

| Method | Purpose |
|--------|---------|
| `Server.Resolve` | Resolve selections on an object |
| `Server.Load` | Load object from ID (evaluates the call chain) |
| `Server.LoadType` | Load result from ID (may not be selectable) |
| `Server.Select` | Evaluate selector chain, assign to dest |
| `Server.toSelectable` | Convert `AnyResult` to `AnyObjectResult` |

### Load vs Select

- **Load**: Takes an ID, evaluates the entire call chain, returns the object
- **Select**: Takes selectors, evaluates them in sequence from a starting object

## Cache Integration

The `SessionCache` (`session_cache.go`) wraps the underlying cache:

```go
type SessionCache struct {
    cache cache.Cache[CacheKeyType, CacheValueType]
    // ...
}
```

Key type is `string` (the digest), value type is `AnyResult`.

### CacheKey Structure

```go
type CacheKey struct {
    CallKey        string  // The cache lookup key (usually ID digest)
    TTL            int64   // Optional TTL in seconds
    DoNotCache     bool    // Skip caching entirely
    ConcurrencyKey string  // For deduping concurrent calls
}
```

## Field Installation Pattern

Fields are installed on the server during init:

```go
// In core/schema/container.go
func (s *containerSchema) Install(srv *dagql.Server) {
    dagql.Fields[*core.Query]{
        dagql.Func("container", s.container).Doc("..."),
    }.Install(srv)

    dagql.Fields[*core.Container]{
        dagql.Func("withEnvVariable", s.withEnvVariable).Doc("...").Args(...),
        dagql.NodeFunc("from", s.from).Doc("..."),
    }.Install(srv)
}
```

## Context Utilities

| Function | Purpose |
|----------|---------|
| `CurrentID(ctx)` | Get the ID being evaluated |
| `CurrentDagqlServer(ctx)` | Get the server |
| `NewResultForCurrentID(ctx, val)` | Wrap value with current ID |

## Code Locations

| File | Contents |
|------|----------|
| `dagql/server.go` | `Server`, `Resolve`, `Load`, `Select`, `InputObject` |
| `dagql/objects.go` | `Class`, `Field`, `FieldSpec`, `Result`, `ObjectResult`, `preselect`, `call` |
| `dagql/types.go` | `Typed`, `Input`, `ScalarType`, `ID[T]`, scalars, `EnumValues`, `Array`, `ArrayInput`, `UnwrapAs` |
| `dagql/nullables.go` | `Derefable`, `Optional`, `Nullable`, `DynamicOptional`, `DynamicNullable` |
| `dagql/builtins.go` | `builtinOrTyped`, `builtinOrInput`, `DynamicArrayOutput`, `DynamicArrayInput` |
| `dagql/cachekey.go` | `CachePerClient`, `CachePerSession`, etc. |
| `dagql/session_cache.go` | `SessionCache` |
| `core/schema/*.go` | Actual API implementations |

## Gotchas

- **Func vs NodeFunc choice**: Use `Func` by default. Only use `NodeFunc` when you need `self.ID()`.

- **Cache key affects ID**: When `GetCacheConfig` returns a different `CallKey`, the ID's digest is updated to match (`objects.go:599`). This ensures the returned object's ID reflects its actual cache key.

- **DoNotCache still returns cacheable results**: `CachePerCall` makes every call execute, but the returned `AnyResult` can still be cached under its own digest if passed around.

- **Field spec DoNotCache**: Set via `.DoNotCache("reason")` on field definition. Different from cache key's `DoNotCache` - this is permanent for the field.
