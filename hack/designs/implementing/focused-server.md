# Focused Server: Scoped Module Serving

## Status: Implementing (prototype)

## Problem

When a workspace has a default module (e.g. a blueprint), the CLI needs special logic
to "skip" the module's constructor and present its functions as top-level commands. Today
this works via `Workspace.defaultModule` — the engine tells the CLI which module to focus
on, and the CLI prepends the constructor when building GraphQL queries:

```
dagger call foo bar  →  { myModule { foo { bar } } }
```

This leaks the module-as-a-constructor abstraction into the CLI. The CLI must understand
that `foo` lives behind `myModule`, match constructor names to module names, handle
constructor flags at the top level, etc. The engine serves a schema where the module is
behind a constructor, and the CLI compensates.

## Design

### Core Insight

Instead of the CLI being aware of a "default module", the engine serves a schema that
already matches the expected user interface. A focused `*dagql.Server` substitutes the
module type for Query:

```graphql
# Real server (unchanged):
type Query { myModule: MyModule! }
type MyModule { foo: Foo!; bar: String! }

# Focused server (served to CLI):
type MyModule { foo: Foo!; bar: String! }  # ← this IS Query
```

The CLI sends `{ foo { bar } }` and it resolves directly.

### Server.Refocus

`dagql/server.go` gains two new methods:

```go
// Create a nil-ID root suitable for Refocus.
func (s *Server) NewRootObject(val Typed) (AnyObjectResult, error)

// Create a focused server with a different root type.
func (s *Server) Refocus(root AnyObjectResult) *Server
```

`Refocus` creates a new `*Server` that:
- Shares `objects`, `scalars`, `typeDefs`, `directives`, `Cache` with the original
- Has the module instance as its root (nil ID, base of the ID chain)
- Points `fallback` at the original server for fields not on the focused type

The focused and real servers are the same Go type (`*dagql.Server`). No wrapper, no
interface — just a server with a different root and a fallback pointer.

### Fallback Resolution

The focused server's `ExecOp` splits top-level fields:

1. Try each field against the focused root (module type)
2. If not found, try the fallback's root (real Query)
3. Resolve each group against the appropriate root
4. Merge results

This lets infrastructure fields (`currentTypeDefs`, `loadXFromID`, etc.) remain
accessible without polluting the module type. The schema merges both sets of fields
so gqlgen validation passes for mixed queries.

### Building the Focused Root

In `engine/server/session.go`, when a workspace has a `DefaultModule`:

1. Get the real schema from `client.deps.Schema(ctx)`
2. Call the constructor (default args) on the real server: `Select(root, "myModule")`
3. Use the constructor result directly as the focused root (preserving its ID)
4. `realSchema.Refocus(root)` → focused server
5. Serve the focused server instead of the real one

If the constructor has required args without defaults, focusing is silently skipped
and the old behavior is preserved.

### currentTypeDefs Rewriting

`currentTypeDefs` resolves via the fallback (it's a Query field, not a module field).
A new `focusTypeDefs` function rewrites the response:

- Finds the Query type def and the module's MAIN object type def (matched by
  `SourceModuleName` and name equality with `strcase.ToCamel(moduleName)`)
- Removes the constructor from Query's functions
- Removes any Query functions whose names collide with promoted functions
- Adds the module's functions to Query's functions, setting `SourceModuleName`
  on each promoted function so the CLI can identify them

This makes the CLI see the module's functions as Query-level commands without any
special `DefaultModule` awareness.

### Context Server

When the focused server resolves fields, module function execution needs access to
the full Query root (for core API fields like `directory`, `container`, etc.). The
`contextServer()` method ensures that `CurrentDagqlServer(ctx)` returns the fallback
(real) server, not the focused server.

### CLI Adaptation

The CLI's `loadTypeDefs` detects the focused schema:

- `DefaultModule` is still queried (fallback for unfocused case)
- When no constructor is found but module functions exist on Query, `MainObject`
  falls back to Query itself
- `filterCore` updated to match functions by `fn.SourceModuleName` (not just return
  type), so promoted functions returning core types or scalars aren't filtered out

## Current State

### What Works

- `Server.Refocus` creates a focused server with shared types and fallback
- `execQueryWithFallback` splits resolution between focused and fallback roots
- Schema merges focused + fallback root fields for validation
- Constructor called with defaults to build the focused root; result used directly
  (preserving its ID for valid cache chains)
- `currentTypeDefs` rewritten to promote module functions (main object only,
  matched by name; collisions resolved in favor of promoted functions)
- `contextServer()` ensures module function execution sees the real Query root
- CLI detects focused schema and uses Query as MainObject
- Graceful fallback when constructor requires args
- Tests passing: `TestModule/TestFloat`, `TestWorkspace/TestBlueprint`, `TestBlueprint`

### Known Limitations

1. **Constructor args sacrificed.** The focused root is built with default args only.
   Modules whose constructor sets important state from required args will not work
   correctly in focused mode. When the constructor fails (required args), focusing is
   skipped entirely and the old `DefaultModule` path kicks in.

2. **ID chains share the constructor prefix.** The focused root preserves the
   constructor's ID (e.g. `Query.myModule`), so field IDs form valid chains
   (`Query.myModule.foo(x:1)`). These are the same IDs the real server would
   produce, so caching works correctly.

3. **Schema Query type naming.** The focused schema's Query operation type is named
   after the module (e.g. `MyModule`), not `Query`. Standard GraphQL introspection
   would show this. The CLI works because `currentTypeDefs` keeps the "Query" name in
   its response, but other tooling (GraphiQL, codegen) would see the module name.

4. **Shell and completion.** The shell (`dagger shell`) and tab-completion code paths
   have their own module resolution logic (`IsDefaultModule`, etc.) that hasn't been
   updated for the focused server approach.

5. **Single module only.** Only one module can be focused. Workspaces with multiple
   modules and no blueprint have no default module and won't focus.

6. **`focusTypeDefs` mutates response.** The rewriting modifies the ObjectTypeDef
   in-place. This is safe today because `TypeDefs()` returns fresh clones per call,
   but it's fragile.

## What's Next

### Constructor Args

The biggest gap. Options:

- **Accept the limitation.** Constructor args are a module-level UX concern. When a
  module is served as a workspace default, maybe it shouldn't have required constructor
  args — the workspace config should provide defaults.
- **Workspace config defaults.** The workspace `config.toml` already has
  `[modules.<name>.config]` for providing default values. These could be used to
  satisfy constructor args when building the focused root.
- **Lazy root.** Don't call the constructor at focus time. Instead, call it lazily on
  first field access, with args supplied from workspace config.

### Removing DefaultModule

Once the constructor args story is solid, `Workspace.defaultModule` can be removed from
the API. The CLI would no longer need to query it — the focused server's schema IS the
interface. The `focusTypeDefs` rewriting in `currentTypeDefs` could also be removed if
the CLI switches to standard GraphQL introspection against the focused schema.
