# Entrypoint Proxy: Canonical and Sugared Servers

## Overview

Workspace entrypoint modules promote their primary type's fields to `Query`
so users can write `dagger call build` instead of `dagger call myModule
build`. These promotions are called **entrypoint proxies**.

Proxies can shadow core API fields (`container`, `file`, `directory`) or
even the module's own constructor (a module named `test` with a method
`test`). To handle this safely, the engine maintains two `dagql.Server`
instances that share a single `SessionCache`:

- **Canonical**: All core types, module types, constructors, and
  `loadXxxFromID` fields. No entrypoint proxies. The real namespace.

- **Sugared (client-facing)**: Everything the canonical server has, PLUS
  entrypoint proxy fields and the `with` field on `Query`. Served to
  clients over HTTP and used for schema introspection.

The sugared server's `Canonical()` method returns the canonical server.
For servers without entrypoints, `Canonical()` returns the receiver
itself (only one server is built).

## Key Invariants

1. **IDs are always loaded through the canonical server.** `Load` and
   `LoadType` on the sugared server delegate to `Canonical()`.
   Canonical IDs like `test(...).file(...)` never touch proxy fields
   during evaluation.

2. **Proxy resolvers always Select through the canonical server.** They
   call `dag.Canonical().Select(...)`, preventing infinite recursion
   when a proxy shadows the field it needs to call (e.g. proxy `test`
   calling constructor `test`).

3. **Both servers share one `SessionCache`.** A constructor call cached
   during proxy resolution on the sugared server is a cache hit when
   the canonical server evaluates the same call during ID loading.

4. **Proxy fields use `DoNotCache` and `NoTelemetry`.** They are pure
   routing — the real work (and caching) happens in the canonical
   constructor and method calls.

## Constructor Args: The `with` Field

When an entrypoint module has a constructor with arguments, a `with`
field is installed on `Query`. It takes the constructor's args and
returns a new `Query` with those args stored in `Query.ConstructorArgs`.

### GraphQL Flow

```graphql
# CLI translates: dagger call --foo=abc gimme-foo
{ with(foo: "abc") { gimmeFoo } }
```

### How It Works

1. `with(foo: "abc")` stores `{"foo": "abc"}` on the Query object and
   returns it with a canonical ID like `with(foo: "abc")`.

2. `gimmeFoo` proxy resolver reads `Query.ConstructorArgs` from `self`,
   calls `test(foo: "abc").gimmeFoo()` through the canonical server.

3. The result carries a canonical ID like `test(foo: "abc").gimmeFoo()`.

### CLI Integration

The CLI (`cmd/dagger/functions.go`) detects the `with` field on Query
during `addConstructorLocalFlags`. It registers the `with` args as
local flags on the root `call` command. When any of these flags are
set, `selectWith` adds `with(args...)` to the query builder chain
before the proxy subcommand.

## dagql.Server: Canonical()

The `dagql.Server` has a private `canonical` field and a public
`Canonical()` method (`dagql/server.go`):

```go
func (s *Server) Canonical() *Server {
    if s.canonical != nil {
        return s.canonical
    }
    return s
}
```

This is set by `SchemaBuilder` via `SetCanonical(inner)` when building
the sugared/canonical server pair. `Load()` and `LoadType()` delegate
to `Canonical()` so that ID evaluation always runs against the
un-sugared schema.

## Who Uses Canonical vs. Sugared

### Sugared server (`schemaBuilder.Server(ctx)`)

| Caller | File | Purpose |
|--------|------|---------|
| `session.go` HTTP handler | `engine/server/session.go` | GraphQL endpoint served to clients |
| `currentTypeDefs` | `core/schema/module.go` | Schema introspection — sees proxy fields and `with` so CLI discovers them |
| `client_resources.go` | `engine/server/client_resources.go` | `LoadIDResults` for secrets and sockets — `Load` delegates to canonical via `Canonical()` |

### Canonical server (via `dag.Canonical()`)

| Caller | File | Purpose |
|--------|------|---------|
| Proxy resolvers (functions) | `core/object.go` | `canonical.Select(ctx, canonical.Root(), ...)` — calls constructor→method chain without hitting the proxy |
| Proxy resolvers (fields) | `core/object.go` | Same pattern for field proxies |
| `ContainerRuntime.Call` | `core/sdk.go` | Selects `directory` from Query root to create module metadata dir — must not hit a `directory` proxy |

## SchemaBuilder

`SchemaBuilder` (`core/moddeps.go`) is an immutable type that lazily
constructs a dagql server from a set of modules with per-module install
policy (`InstallOpts`). Builder methods (`Append`, `Prepend`, `With`,
`Clone`) return new instances; the server is computed once on first
access and cached.

### Schema Build Flow

`SchemaBuilder.lazilyLoadSchema`:

```text
hasEntrypoint?
├─ no  → buildSchema(mods) → single server (canonical == self)
└─ yes → buildSchema(mods with Entrypoint:false) → canonical
         buildSchema(mods with real Entrypoint flags) → sugared
         sugared.SetCanonical(canonical)
```

`buildSchema` (`core/schema_build.go`) creates a `dagql.Server`, calls
`mod.Install(ctx, dag, opts)` for each module, and wires up interface
extensions. Both builds use the same `root.Cache(ctx)` so they share a
`SessionCache`. Module `Install` is all in-memory registration (classes,
fields, specs) — no I/O — so the double build is cheap.

### Introspection JSON

`SchemaBuilder` does not cache introspection JSON files. JSON is
generated on-demand via `dag.Select("__schemaJSONFile", hiddenTypes)`
and dagql's `CachePerSchema` handles per-args deduplication.

Three convenience methods control what gets hidden:

| Method | Hidden types | Use case |
|--------|-------------|----------|
| `SchemaIntrospectionJSONFileForModule` | `TypesToIgnoreForModuleIntrospection` + `TypesHiddenFromModuleSDKs` (Engine, etc.) | Module SDK codegen |
| `SchemaIntrospectionJSONFileForClient` | none | Standalone client codegen |
| `SchemaIntrospectionJSONFile` | caller-specified | Custom filtering |

## Proxy Installation

`ModuleObject.installEntrypointMethods` in `core/object.go` runs during
`Install` when `opts.Entrypoint` is true:

1. **Install `with` field** (if constructor has args): Takes constructor
   args, stores them on `Query.ConstructorArgs`, returns the new Query.

2. **Install function proxies**: For each function on the primary type,
   extend Query root with a proxy that:
   - Takes only the method's own args (no constructor args)
   - Reads stored constructor args from `self` (the Query)
   - Calls `canonical.Select(canonical.Root(), constructorSel, methodSel)`
     through the canonical server

3. **Install field proxies**: Same pattern for fields (no args of their
   own — constructor args come from `self`).

Since `Extend` appends and field lookup iterates backwards, proxies take
precedence over any existing field with the same name (including core
fields).

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
| Method name == core field name (e.g. `container`) | Proxy shadows core field on sugared server. Core field is unambiguous on canonical server. IDs produced by core `container` load correctly. |
| Method name == constructor name (e.g. module `test`, method `test`) | Proxy shadows constructor on sugared server. Resolver desugars through canonical server where constructor is unambiguous. |
