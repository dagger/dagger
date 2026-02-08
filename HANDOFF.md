# Workspace Module Loading: Implementation Handoff

Branch: `workspace`

## What This Is

Engine-side workspace module loading. The engine loads modules at connect time — from `.dagger/config.toml` (workspace modules) or from `-m` flag (extra modules) — installs their constructors on the Query root, and optionally auto-aliases their functions to the root so the CLI doesn't need module-specific branching.

## Commit History

All work is committed on the `workspace` branch. Listed oldest to newest (post-infrastructure commits only):

### Foundation

**`b0149bb7e` — engine: move -m to engine connect-time parameter with auto-aliases**

Core implementation of engine-side module loading:

- `engine/opts.go`: `ExtraModule` type (`Ref`, `Name`, `Alias` fields), `ExtraModules`/`SkipWorkspaceModules` fields on `ClientMetadata`
- `engine/client/client.go`: CLI wires `-m` flag to `ExtraModules: [{Ref: c.Module, Alias: true}], SkipWorkspaceModules: true`
- `core/module.go:77`: `AutoAlias bool` field on `*Module`
- `core/object.go:362-391`: `installAutoAliases()` — extends Query root with alias fields that delegate via `dag.Select(ctx, self, &result, Selector{constructorName}, Selector{funcName, args})`
- `core/object.go:261-265`: `ModuleObject.Install` calls `installAutoAliases` when `mod.AutoAlias` is true
- `engine/server/session.go`: `loadWorkspace()` loads workspace modules and stores extra modules as `client.pendingExtraModules`; `loadExtraModule()` resolves module via dagql pipeline and calls `serveModule()`
- `engine/server/client_resources.go:67-77`: Fix for "no query in context" — sets `schemaCtx = core.ContextWithQuery(schemaCtx, srcClient.dagqlRoot)` before `deps.Schema()`
- `cmd/dagger/functions.go`, `cmd/dagger/call.go`, `cmd/dagger/mcp.go`: CLI always calls `initializeWorkspace()` — no `-m` branching

### Bug fixes

**`5454853b1` — fix: defer extra module loading to serveQuery to fix nested execution deadlock**

Module loading was deadlocking because `initializeDaggerClient` holds `sess.stateMu` + `client.stateMu`, and module loading needs the buildkit session which registers via the same locks. Fix: store modules as `pendingExtraModules`, load them lazily in `serveQuery` via `ensureExtraModulesLoaded`.

**`7027674b9` — fix: resolve deadlock, panic, and propagation issues in extra module loading**

Four fixes in `engine/server/session.go`:
1. Nested execution propagation: `ServeHTTPToNestedClient` copies `ExtraModules`/`SkipWorkspaceModules` from HTTP headers into nested client's `ClientMetadata`
2. Removed redundant `client.stateMu.Lock()` in `loadWorkspaceModule` — caller already holds the lock
3. Moved `defer recover()` to top of `serveQuery`, before `ensureExtraModulesLoaded`
4. Replaced `sync.Once` with `sync.Mutex` + `extraModulesLoaded` bool for retryable failures

**`e9585af52` — fix: use srcClient context when building schema in addClientResourcesFromID**

Sets query context from `srcClient.dagqlRoot` before calling `deps.Schema()` in `client_resources.go`.

### CLI schema filtering

**`0cc610b4c` — fix: show core functions when no workspace modules are loaded**

When `dagger functions` runs without `-m` and no workspace modules exist, falls back to showing core functions instead of empty output.

**`f1296a0ed` — fix: hide core functions from schema when user modules are loaded**

Skip core Query root fields (`container`, `directory`, `git`, etc.) when user modules are present, so `dagger functions`/`call` only shows module functions. Core functions remain accessible via `dagger core`. Changes in `cmd/dagger/call.go`, `core/moddeps.go`, `dagql/objects.go`, `dagql/server.go`.

### Per-module alias in config.toml

**`d0be2440a` — feat: support per-module alias=true in workspace config**

Wires `alias = true` from `.dagger/config.toml` module entries through `loadWorkspaceModule` to set `mod.Self().AutoAlias = true`, reusing the same mechanism as `-m`. Changes:
- `core/workspace/config.go`: Added `Alias bool` field to `ModuleEntry`, removed unused `Aliases map[string][]string` from `Config`
- `engine/server/session.go`: `loadWorkspaceModule` accepts `alias bool` param, sets `mod.Self().AutoAlias = true` before serving

Config example:
```toml
[modules.go]
source = "modules/go"
alias = true
```
Result: `dagger call build` works (aliased from `go.build`), alongside `dagger call go build` (explicit).

## Architecture

### Data flow — extra modules (`-m` flag)

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

### Data flow — workspace modules (config.toml)

```
Engine startup
  getOrInitClient()
    initializeDaggerClient()
      loadWorkspace()
        workspace.Detect() finds .dagger/config.toml
        for each module entry:
          loadWorkspaceModule(name, sourcePath, entry.Alias)
            dag.Select: moduleSource(disableFindUp) → asModule
            if alias: mod.Self().AutoAlias = true
            serveModule: client.deps.Append(mod)
```

Workspace modules load synchronously during `initializeDaggerClient` (they don't need deferred loading because they resolve from the local filesystem, not from the network).

### Why deferred loading for extra modules?

`initializeDaggerClient` holds `sess.stateMu` + `client.stateMu`. Extra module loading needs the buildkit session, which is registered by the session attachables request — but that request is blocked waiting for the same locks. Deferred loading to `serveQuery` runs after locks are released and the session is registered.

### Key files

| File | Role |
|------|------|
| `engine/opts.go` | `ExtraModule` type, `ClientMetadata` fields |
| `engine/client/client.go` | CLI → engine metadata wiring |
| `engine/server/session.go` | `loadWorkspace`, `ensureExtraModulesLoaded`, `loadExtraModule`, `loadWorkspaceModule` |
| `engine/server/client_resources.go` | Query context fix for `addClientResourcesFromID` |
| `core/module.go` | `AutoAlias` field |
| `core/object.go` | `installAutoAliases` method |
| `core/workspace/config.go` | `Config`, `ModuleEntry` (with `Alias` field), `ParseConfig` |
| `core/workspace/detect.go` | `Detect` — finds `.dagger/` root and reads `config.toml` |
| `core/moddeps.go` | Schema filtering (hide core fields when user modules loaded) |
| `dagql/objects.go`, `dagql/server.go` | `ObjectType.FieldsWithoutCore()` for schema filtering |
| `cmd/dagger/functions.go` | CLI `initializeWorkspace`, function listing |
| `cmd/dagger/call.go` | CLI call command, workspace filter logic |
| `cmd/dagger/module_inspect.go` | `loadTypeDefs` |

## What's Not Done

### Not yet tested end-to-end

The dev engine was stale throughout testing. All "local" tests (`./bin/dagger -m ...`) were running against a dev engine without the new code. The playground (`dagger call engine-dev playground`) builds a fresh dev engine from source — that's the correct test path.

**To test:**
```bash
# Basic: does -m load the module and show its functions?
dagger call engine-dev playground \
  with-exec --args 'dagger','-m','github.com/dagger/dagger/modules/wolfi','functions' \
  stdout

# Expected: should show wolfi constructor + auto-aliased functions (no core functions)

# Nested: does -m work inside a container?
dagger call engine-dev playground \
  with-exec --args 'dagger','-m','github.com/dagger/dagger/modules/wolfi','call','container','default-packages' \
  stdout

# Workspace alias: does alias=true promote functions to root?
# (Needs a test project with .dagger/config.toml containing alias = true)
```

Previous playground tests failed with:
```
get or init client: initialize client: failed to add client resources from ID:
failed to get source schema: failed to select introspection JSON file:
failed to get current query: no query in context
```
The `client_resources.go` fix (`e9585af52`) should address this, but hasn't been validated against the fresh dev engine.

### Auto-alias name collisions

If a module function has the same name as a core function (e.g. wolfi's `container` vs core `container`), `Extend` appends both to `class.fields[name]`. dagql uses views for field resolution, so this may work correctly, but it hasn't been tested. May need a conflict resolution strategy.

### CLI filtering for auto-aliased functions

`loadTypeDefs` in `module_inspect.go` identifies constructors by checking `fn.ReturnType.AsObject.SourceModuleName != ""`. Auto-aliased functions return core types (e.g. `Container`), not module types, so they won't be identified as module functions. The CLI might need updated filtering to distinguish auto-aliases from core functions — or this might be fine (they intentionally look like core functions at the root level).

## Known Issues

1. **"no query in context" in nested codegen**: The fix in `client_resources.go` (`e9585af52`) adds query context from `srcClient.dagqlRoot`, but hasn't been validated against the fresh dev engine. If it persists, investigate whether `srcClient.dagqlRoot` is nil for some client types, or if there's a different code path hitting the same error.

2. **`optionalModCmdWrapper` in query.go**: The `dagger query` command has its own module loading path (`modSrc.AsModule().Serve()`) that bypasses engine-side loading entirely. Should be updated to use the engine-side path, or left alone if `dagger query` is intended to work differently.

3. **Shell commands**: `initializeModule()` is still used by shell code (`shell_commands.go`, `shell_exec.go`, `shell_fs.go`). Not removed.
