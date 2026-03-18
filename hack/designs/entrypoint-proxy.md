# Entrypoint Proxy: Two-Server Architecture

## Overview

Workspace entrypoint modules promote their primary type's fields to `Query`
so users can write `dagger call build` instead of `dagger call myModule
build`. These promotions are called **entrypoint proxies**.

Proxies can shadow core API fields (`container`, `file`, `directory`) or
even the module's own constructor (a module named `test` with a method
`test`). To handle this safely, the engine maintains two `dagql.Server`
instances that share a single `SessionCache`:

- **Inner (canonical)**: All core types, module types, constructors, and
  `loadXxxFromID` fields. No entrypoint proxies. IDs are loaded here.

- **Outer (client-facing)**: Everything the inner server has, PLUS
  entrypoint proxy fields on `Query`. Served to clients over HTTP and
  used for schema introspection.

When no module has `Entrypoint` set, only one server is built and inner
== outer.

## Key Invariants

1. **IDs are always loaded through the inner server.** Canonical IDs like
   `test(...).file(...)` never touch proxy fields during evaluation.

2. **Proxy resolvers always Select through the inner server.** This
   prevents infinite recursion when a proxy shadows the field it needs
   to call (e.g. proxy `test` calling constructor `test`).

3. **Both servers share one `SessionCache`.** A constructor call cached
   during proxy resolution on the outer server is a cache hit when the
   inner server evaluates the same call during ID loading.

4. **Proxy fields use `DoNotCache` and `NoTelemetry`.** They are pure
   routing — the real work (and caching) happens in the inner
   constructor and method calls.

## dagql.Server Fields

Two fields on `dagql.Server` (`dagql/server.go`) support the
architecture:

### `IDLoader`

```go
IDLoader func(ctx context.Context, id *call.ID) (AnyObjectResult, error)
```

Checked first by `Load()` and `LoadType()`. When set, all ID evaluation
is delegated to the inner server. Set to `inner.Load` on the outer
server.

### `Inner`

```go
Inner *Server
```

A direct reference to the inner server. Used by code that needs to
`Select` core API fields without risk of hitting an entrypoint proxy.
The closures in proxy resolvers capture `dag` (the outer `*Server`)
at install time; since `Inner` is set after both servers are built
but before any resolver runs, the closures see it through the pointer.

## Who Uses Inner vs. Outer

### Outer server (`servedMods.Schema(ctx)`)

| Caller | File | Purpose |
|--------|------|---------|
| `session.go` HTTP handler | `engine/server/session.go:1293` | GraphQL endpoint served to clients |
| `currentTypeDefs` | `core/schema/module.go:766` | Schema introspection — sees proxy fields so CLI discovers them |

### Inner server (`servedMods.Server(ctx)`)

| Caller | File | Purpose |
|--------|------|---------|
| `client_resources.go` | `engine/server/client_resources.go:76` | `LoadIDResults` / `LoadIDs` for secrets and sockets — needs canonical ID evaluation |

### `dag.Inner` (inner server via pointer on outer)

| Caller | File | Purpose |
|--------|------|---------|
| Proxy resolvers (functions) | `core/object.go:418` | `target.Select(ctx, target.Root(), ...)` — calls constructor→method chain without hitting the proxy |
| Proxy resolvers (fields) | `core/object.go:462` | Same pattern for field proxies |
| `ContainerRuntime.Call` | `core/sdk.go:177` | Selects `directory` from Query root to create module metadata dir — must not hit a `directory` proxy |

## Schema Build Flow

`ServedMods.lazilyLoadSchema` in `core/served_mods.go`:

```
hasEntrypoint?
├─ no  → buildSchema(mods) → single server (inner == outer)
└─ yes → buildSchema(mods with Entrypoint:false) → inner
         buildSchema(mods with real Entrypoint flags) → outer
         outer.IDLoader = inner.Load
         outer.Inner = inner
```

`buildSchema` (`core/schema_build.go`) creates a `dagql.Server`, calls
`mod.Install(ctx, dag, opts)` for each module, wires up interface
extensions, and produces the introspection JSON file. Both inner and
outer builds use the same `root.Cache(ctx)` so they share a
`SessionCache`. Module `Install` is all in-memory registration (classes,
fields, specs) — no I/O — so the double build is cheap.

## Proxy Installation

`ModuleObject.installEntrypointMethods` in `core/object.go` runs during
`Install` when `opts.Entrypoint` is true. For each function and field on
the module's primary type:

1. **Arg conflict check**: If any method arg has the same name as a
   constructor arg, the proxy is skipped (the merged flat arg list would
   be ambiguous). The function remains reachable via the namespaced
   constructor path (`dagger call myModule myMethod`).

2. **Merge args**: Constructor args + method args are merged into a
   single `InputSpecs` list.

3. **Extend Query root**: `dag.Root().ObjectType().Extend(proxySpec,
   resolver)` appends the proxy. Since `Extend` appends and field
   lookup iterates backwards, the proxy takes precedence over any
   existing field with the same name (including core fields).

4. **Resolver**: Calls `target.Select(ctx, target.Root(), &result,
   constructorSel, methodSel)` where `target` is `dag.Inner` (or `dag`
   itself if `Inner` is nil). The `Select` chains two steps: first the
   constructor, then the method/field on the constructed object. The
   result carries a canonical ID.

## Directive Reconstruction

When the CLI introspects entrypoint proxy fields, it reads arg metadata
through `introspectionObjectToTypeDef` in `core/schema/coremod.go`.
This path reconstructs `FunctionArg` from introspection fields and reads
back custom directives (`@defaultPath`, `@defaultAddress`,
`@ignorePatterns`, `@sourceMap`) so that CLI-side ignore patterns and
contextual defaults survive the round-trip through introspection.

## Conflict Handling Summary

| Conflict | Behavior |
|----------|----------|
| Method name == core field name (e.g. `container`) | Proxy shadows core field on outer server. Core field is unambiguous on inner server. IDs produced by core `container` load correctly. |
| Method name == constructor name (e.g. module `test`, method `test`) | Proxy shadows constructor on outer server. Resolver calls through inner server where constructor is unambiguous. |
| Method arg name == constructor arg name | Proxy is **skipped** — merged flat arg list would be ambiguous. Method is reachable via namespaced path. |

## Future Work

The remaining conflict-skip (ambiguous arg names) could potentially be
resolved by prefixing or namespacing constructor args in the proxy's
merged arg list, but this hasn't been implemented yet.
