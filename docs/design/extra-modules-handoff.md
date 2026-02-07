# Move `-m` to Engine Connect-Time Parameter: Implementation Handoff

Branch: `workspace`

## What This Is

Pass the `-m` (explicit module) flag to the engine at connect time instead of handling it client-side. The engine loads the module, installs its constructor on the Query root, and optionally auto-aliases its functions to the Query root so the CLI doesn't need to branch on `-m`.

## What's Done

### Committed

**`b0149bb7e` — engine: move -m to engine connect-time parameter with auto-aliases**

Core implementation:

- `engine/opts.go`: Added `ExtraModule` type (`Ref`, `Name`, `Alias` fields) and `ExtraModules`/`SkipWorkspaceModules` fields to `ClientMetadata`
- `engine/client/client.go:1404`: CLI wires `c.Module` to `ExtraModules: [{Ref: c.Module, Alias: true}], SkipWorkspaceModules: true`
- `core/module.go:77`: Added `AutoAlias bool` field to `*Module`
- `core/object.go:362-391`: `installAutoAliases()` — extends Query root with alias fields that delegate via `dag.Select(ctx, self, &result, Selector{constructorName}, Selector{funcName, args})`
- `core/object.go:261-265`: `ModuleObject.Install` calls `installAutoAliases` when `mod.AutoAlias` is true
- `engine/server/session.go`: `loadWorkspace()` stores extra modules as `client.pendingExtraModules`, `loadExtraModule()` resolves module via dagql pipeline and calls `serveModule()`
- `engine/server/client_resources.go:67-77`: Fix for "no query in context" — sets `schemaCtx = core.ContextWithQuery(schemaCtx, srcClient.dagqlRoot)` before calling `deps.Schema()`
- `cmd/dagger/functions.go:291-293`: CLI always calls `initializeWorkspace()` — no `-m` branching
- `cmd/dagger/call.go`: Same simplification
- `cmd/dagger/mcp.go`: Same simplification

**`5454853b1` — fix: defer extra module loading to serveQuery to fix nested execution deadlock**

Module loading was deadlocking because `initializeDaggerClient` holds `sess.stateMu` + `client.stateMu`, and module loading needs the buildkit session which registers via the same locks. Fix: store modules as `pendingExtraModules`, load them lazily in `serveQuery` via `ensureExtraModulesLoaded`.

### Uncommitted (session.go only)

Four bug fixes in `engine/server/session.go`:

1. **Nested execution propagation**: `ServeHTTPToNestedClient` now copies `ExtraModules` and `SkipWorkspaceModules` from HTTP headers into the nested client's `ClientMetadata`
2. **Workspace module deadlock**: Removed redundant `client.stateMu.Lock()` in `loadWorkspaceModule` — caller already holds the lock
3. **Panic recovery timing**: Moved the `defer recover()` to the top of `serveQuery`, before `ensureExtraModulesLoaded`
4. **sync.Once retry**: Replaced `sync.Once` with `sync.Mutex` + `extraModulesLoaded` bool flag so transient failures (session not yet registered) can be retried

## What's Not Done

### Not yet tested end-to-end

The dev engine was stale (from Feb 2) throughout testing. All "local" tests (`./bin/dagger -m ...`) were running against a dev engine without any of the new code. The playground (`dagger call engine-dev playground`) does build a fresh dev engine from source — that's the correct test path.

**To test:**
```bash
# Basic: does -m load the module and show its functions?
dagger call engine-dev playground \
  with-exec --args 'dagger','-m','github.com/dagger/dagger/modules/wolfi','functions' \
  stdout

# Expected: should show wolfi constructor + auto-aliased functions + core functions

# Nested: does -m work inside a container?
dagger call engine-dev playground \
  with-exec --args 'dagger','-m','github.com/dagger/dagger/modules/wolfi','call','container','default-packages' \
  stdout
```

Previous playground tests failed with:
```
get or init client: initialize client: failed to add client resources from ID:
failed to get source schema: failed to select introspection JSON file:
failed to get current query: no query in context
```
This occurs during alpine dependency codegen. The `client_resources.go` fix (setting query context from `srcClient.dagqlRoot`) should address this, but it hasn't been validated against the fresh dev engine.

### Config.toml aliases (plan item 6)

Not implemented. The plan calls for `[aliases]` in `config.toml` to create Query root fields that resolve via `dag.Select` through a constructor path (e.g. `build = ["go", "build"]`). Same underlying mechanism as auto-aliases but with explicit paths from config. Should be installed during `loadWorkspace()` after all workspace modules are served.

### Auto-alias name collisions

If a module function has the same name as a core function (e.g. wolfi's `container` vs core `container`), `Extend` appends both to `class.fields[name]`. dagql uses views for field resolution, so this may work correctly, but it hasn't been tested. May need conflict resolution strategy.

### CLI filtering for auto-aliased functions

`loadTypeDefs` in `module_inspect.go` identifies constructors by checking `fn.ReturnType.AsObject.SourceModuleName != ""`. Auto-aliased functions return core types (e.g. `Container`), not module types, so they won't be identified as module functions. The CLI might need updated filtering to distinguish auto-aliases from core functions — or this might be fine (they intentionally look like core functions at the root level).

## Architecture

### Data flow

```
CLI                          Engine
 |                            |
 |  ClientMetadata{           |
 |    ExtraModules: [{        |
 |      Ref: "./ci",          |
 |      Alias: true           |
 |    }],                     |
 |    SkipWorkspaceModules:   |
 |      true                  |
 |  }                         |
 |  ────HTTP headers────────> |
 |                            | getOrInitClient()
 |                            |   initializeDaggerClient()
 |                            |     loadWorkspace()
 |                            |       stores pendingExtraModules
 |                            |
 |  ────GraphQL query───────> |
 |                            | serveQuery()
 |                            |   ensureExtraModulesLoaded()
 |                            |     loadExtraModule()
 |                            |       dag.Select: moduleSource → asModule
 |                            |       sets mod.AutoAlias = true
 |                            |       serveModule: client.deps.Append(mod)
 |                            |   client.deps.Schema(ctx)
 |                            |     lazilyLoadSchema creates dagql.Server
 |                            |     Module.Install → installAutoAliases
 |                            |       dag.Root().ObjectType().Extend(spec, resolver)
 |                            |   serve GraphQL against this schema
```

### Why deferred loading?

`initializeDaggerClient` holds `sess.stateMu` + `client.stateMu`. Module loading needs the buildkit session, which is registered by the session attachables request — but that request is blocked waiting for the same locks. Deferred loading to `serveQuery` runs after locks are released and the session is registered.

### Key files

| File | Role |
|------|------|
| `engine/opts.go` | `ExtraModule` type, `ClientMetadata` fields |
| `engine/client/client.go` | CLI → engine metadata wiring |
| `engine/server/session.go` | `loadWorkspace`, `ensureExtraModulesLoaded`, `loadExtraModule`, `loadWorkspaceModule` |
| `engine/server/client_resources.go` | Query context fix for `addClientResourcesFromID` |
| `core/module.go` | `AutoAlias` field |
| `core/object.go` | `installAutoAliases` method |
| `cmd/dagger/functions.go` | CLI simplification (always `initializeWorkspace`) |
| `cmd/dagger/module_inspect.go` | `initializeWorkspace`, `loadTypeDefs` |

## Known Issues

1. **"no query in context" in nested codegen**: The fix in `client_resources.go` adds query context from `srcClient.dagqlRoot`, but hasn't been validated against the fresh dev engine. If it persists, investigate whether `srcClient.dagqlRoot` is nil for some client types, or if there's a different code path hitting the same error.

2. **`optionalModCmdWrapper` in query.go**: The `dagger query` command has its own module loading path (`modSrc.AsModule().Serve()`) that bypasses the engine-side loading entirely. This should be updated to use the engine-side path too, or left alone if `dagger query` is intended to work differently.

3. **Shell commands**: `initializeModule()` is still used by shell code (`shell_commands.go`, `shell_exec.go`, `shell_fs.go`). Not removed.
