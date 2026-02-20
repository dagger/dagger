# dagql API Server

dagql is Dagger's custom GraphQL server implementation that sits on top of standard GraphQL. It enables the immutable DAG model described in the skill overview and is the main consumer of the cache layer.

## What Makes dagql Different

Standard GraphQL only passes scalars between operations. dagql extends this by:

1. **ID-able objects**: Every installed object type gets:
   - an `id: <TypeName>ID` field
   - a `<TypeName>ID` scalar type
   - a `load<TypeName>FromID(id: <TypeName>ID): <TypeName>` field on Query

2. **Object arguments become IDs**: When a field takes an object argument, the GraphQL schema uses `<TypeName>ID` as the type. The ID encodes the full call chain.

3. **Automatic caching around call identity**: Results are keyed by call ID recipe digest. If a content digest exists, cache can also reuse by content match.

## Core Types

### Server

`Server` (`dagql/server.go`) owns installed types, schema generation, query resolution, and the session cache wrapper used during execution.

Key methods: `InstallObject`, `InstallScalar`, `Resolve`, `Load`, `Select`.

### Typed / ScalarType / Input

- `Typed` (`dagql/types.go`): anything with a GraphQL type (`Type() *ast.Type`)
- `ScalarType`: scalar + input decoder
- `Input`: typed argument value that can encode to call literal

Most of dagql's runtime value model builds on these interfaces.

### Class and Field

- `Class[T]` (`dagql/objects.go`): object type implementation and field registry
- `Field[T]`: field resolver + `FieldSpec`
- `FieldSpec`: field metadata (`Args`, `Type`, `DoNotCache`, `TTL`, `GetCacheConfig`, etc.)

Fields are created with `Func`, `NodeFunc`, `FuncWithCacheKey`, `NodeFuncWithCacheKey`.

### ObjectType

`ObjectType` (`dagql/types.go`) is the runtime interface for selectable object types. `Class[T]` implements it.

### Result Types

- `AnyResult` (`dagql/types.go`): interface for typed value + constructor ID + cache/lifecycle metadata
- `AnyObjectResult` (`dagql/types.go`): `AnyResult` plus selection/call methods
- `Result[T]` (`dagql/cache.go`): concrete result wrapper
- `ObjectResult[T]` (`dagql/cache.go`): selectable result wrapper with object class

Current `Result[T]` model is split into:
- shared immutable payload (`sharedResult`) reused across cache references
- per-call metadata (`hitCache`, `hitContentDigestCache`, optional per-call ID override)

That split is important for content-digest cache hits where payload is reused but caller-facing ID stays the requested recipe ID.

### ID[T]

`ID[T]` (`dagql/types.go`) is the typed ID scalar wrapping `*call.ID`.

Key methods: `Encode`, `Decode`, `Load`, `Display`.

### Enum / InputObject / Wrappers

- `EnumValues[T]` and `EnumValueName` for enums
- `InputObject[T]` + `InputObjectSpec` for structured inputs
- `Wrapper` and `UnwrapAs[T]` for working through wrapper layers (`Result`, `Nullable`, dynamic wrappers, etc.)

### Optional, Nullable, Dynamic Variants

- `Optional[I]`: optional argument values
- `Nullable[T]`: optional return values
- `DynamicOptional` / `DynamicNullable`: runtime-typed variants

These interact heavily with selection/call normalization and result dereferencing.

### Arrays and Enumerable

- `Array[T]`, `ArrayInput[I]`, `ResultArray[T]`, `ObjectResultArray[T]`
- `DynamicArrayOutput`, `DynamicArrayInput`
- `Enumerable` for indexed access and nth selection behavior

## Defining Fields

Fields are typically defined in schema installers under `core/schema/*`.

### Func vs NodeFunc

```go
// Func: receives unwrapped self value
dagql.Func("fieldName", func(ctx context.Context, self *core.Container, args MyArgs) (*core.Container, error) {
    return self, nil
})

// NodeFunc: receives ObjectResult[T], so you can inspect ID and metadata
dagql.NodeFunc("fieldName", func(ctx context.Context, self dagql.ObjectResult[*core.Container], args MyArgs) (*core.Container, error) {
    _ = self.ID()
    return self.Self(), nil
})
```

Use `NodeFunc` when you need ID-aware behavior.

### Returning Object Results With Custom Identity

By default, returned values get the field call ID (`receiver + field + args`).

When needed, you can return a result with different identity:
1. **Recipe identity override**: return `ObjectResult[T]`/`Result[T]` built from a different ID or digest.
2. **Content identity hint**: keep recipe ID but attach content digest (`WithContentDigest`) for content-based cache reuse.

This is the core tool for sharing work across different query shapes that produce equivalent results.

### Custom Cache Keys (`GetCacheConfig`)

`FuncWithCacheKey` / `NodeFuncWithCacheKey` install a `GetCacheConfig` callback:

```go
func(ctx context.Context, self ObjectResult[T], args A, req GetCacheConfigRequest) (*GetCacheConfigResponse, error)
```

Prebuilt helpers (`dagql/cachekey.go`):

| Function | Behavior |
|----------|----------|
| `CachePerClient` | Mixes client ID into cache key digest |
| `CachePerSession` | Mixes session ID into cache key digest |
| `CachePerCall` | Uses random digest (effectively no reuse for that call identity) |
| `CachePerSchema` | Mixes schema digest |
| `CachePerClientSchema` | Mixes client + schema digests |

Important behavior in `preselect`:
- Callback returns a full `CacheKey`.
- If callback leaves `CacheKey.ID` nil, dagql uses the original computed ID.
- If callback rewrites ID, dagql re-decodes execution args from that final ID.

This keeps execution args, telemetry, and cache identity aligned.

## Resolution Flow

When a query executes:

```text
Server.Resolve
  -> ObjectResult.Select
    -> preselect (build ID + cache key)
    -> call (SessionCache.GetOrInitCall)
```

### `preselect`

`ObjectResult.preselect` (`dagql/objects.go`):
1. Resolve field and arguments
2. Build new call ID (`receiver.Append(...)`)
3. Build default `CacheKey` from ID + field spec (`TTL`, `DoNotCache`, `ConcurrencyKey`)
4. Apply `GetCacheConfig` if configured
5. If ID changed, decode final args from final ID

### `call`

`ObjectResult.call` (`dagql/objects.go`):
1. Attach current ID to context
2. Execute through `SessionCache.GetOrInitCall`
3. On miss, run field resolver
4. Normalize nullable/enumerable wrappers
5. Always run `PostCall` before returning

## Cache Integration

`SessionCache` (`dagql/session_cache.go`) wraps base cache (`dagql/cache.go`).

Base cache key input is:

```go
type CacheKey struct {
    ID             *call.ID
    ConcurrencyKey string
    TTL            int64
    DoNotCache     bool
}
```

Derived behavior:
- call lookup key comes from `ID.Digest()`
- optional content fallback lookup uses `ID.ContentDigest()`
- in-flight dedupe uses `(callKey, ConcurrencyKey)`

Session wrapper responsibilities:
- keep references alive for session lifetime
- release on session close
- dedupe telemetry emission
- handle error retry via forced one-shot `DoNotCache` (`noCacheNext`)

For content-digest hits, cache reuses payload but preserves the caller's requested recipe ID in the returned result metadata.

## Field Installation Pattern

Typical pattern:

```go
func (s *containerSchema) Install(srv *dagql.Server) {
    dagql.Fields[*core.Query]{
        dagql.Func("container", s.container),
    }.Install(srv)

    dagql.Fields[*core.Container]{
        dagql.Func("withEnvVariable", s.withEnvVariable).Args(...),
        dagql.NodeFunc("from", s.from),
    }.Install(srv)
}
```

## Context Utilities

Useful helpers while debugging/exploring execution:

| Function | Purpose |
|----------|---------|
| `CurrentID(ctx)` | ID currently being evaluated |
| `CurrentDagqlServer(ctx)` | Current server |
| `NewResultForCurrentID(ctx, val)` | Wrap value in `Result` using current ID |
| `NewObjectResultForCurrentID(ctx, srv, val)` | Wrap value in `ObjectResult` using current ID |

## Code Locations

| File | Contents |
|------|----------|
| `dagql/server.go` | `Server`, `Resolve`, `Load`, `Select`, `InputObject` |
| `dagql/objects.go` | `Class`, `Field`, `FieldSpec`, `preselect`, field call wiring |
| `dagql/cache.go` | base cache, `CacheKey`, `Result`, `ObjectResult` |
| `dagql/types.go` | core interfaces/types (`Typed`, `Input`, `AnyResult`, `ID[T]`, arrays, enums) |
| `dagql/nullables.go` | optional/nullable wrappers |
| `dagql/builtins.go` | builtin conversions, dynamic wrappers |
| `dagql/cachekey.go` | cache key rewrite helpers |
| `dagql/session_cache.go` | session cache wrapper |
| `core/schema/*.go` | concrete API implementations |

## Gotchas

- `Func` vs `NodeFunc`: default to `Func`; use `NodeFunc` when ID-aware logic is needed.
- `GetCacheConfig` ID rewrites are authoritative: returned ID controls cache identity and argument decode.
- Content-digest cache hit does not mean recipe identity changes for callers; returned result preserves requested recipe ID.
- `DoNotCache` skips reuse for that call path but still returns a normal result value.
- In-flight dedupe is intentionally scoped by client via `ConcurrencyKey`, not global across clients.
