# Workspace-Based Module Loading: Implementation Handoff

Branch: `workspace`

## Original Implementation Plan

Replace CLI-driven module loading (`ModuleSource().Serve()`, `initializeDefaultModule()`) with automatic workspace detection at engine connect time. Modules are loaded using the existing dagql pipeline. No more "main module" concept.

### Architecture: Before and After

**Before:**
```
CLI: withEngine(ctx, params, callback)
  → client.Connect(ctx, params)
  → callback(ctx, engineClient)
    → initializeDefaultModule(ctx, dag)
      → dag.ModuleSource(".").AsModule().Serve(ctx)    // CLI serves module
    → inspectModule() + loadTypeDefs()                  // CLI queries engine
      → finds MainObject by SourceModuleName == module.Name
```

**After:**
```
CLI: withEngine(ctx, params, callback)
  → client.Connect(ctx, params)
  → engine: workspace detection via FindUp from bk.AbsPath(ctx, ".")
  → engine: reads .dagger/config.toml
  → engine: for each module, resolves via dagql pipeline + serveModule()
    → Module.Install() adds constructor as Query root field (existing mechanism)
  → callback(ctx, engineClient)
    → CLI calls loadTypeDefs() (no Serve needed — modules already loaded)
    → MainObject = Query root type
    → filter Query fields: only show SourceModuleName != "" as subcommands
```

### Workspace Detection Fallback Chain

At connect time, from the client's working directory:

1. **FindUp `.dagger/`** → stat `.dagger/config.toml` → parse config → workspace root = parent of `.dagger/`
2. **No `.dagger/`** → FindUp `dagger.json` → check migration triggers:
   - `toolchains` present OR `source != "."` → **fail: "needs migration"**
   - Neither trigger → **ignore the dagger.json**, fall through
3. **FindUp `.git`** → workspace root = directory containing `.git` (empty workspace)
4. **No `.git`** → cwd is workspace root (empty workspace)

### Key Reuse Points

| What | Where | Used for |
|------|-------|----------|
| `bk.AbsPath(ctx, ".")` | `engine/buildkit/filesync.go:61` | Client cwd |
| `core.Host{}.FindUpAll()` | `core/host.go:73` | Multi-name find-up |
| `core.NewCallerStatFS(bk)` | `core/modulesource.go:1222` | Stat files on client host |
| `bk.ReadCallerHostFile()` | `engine/buildkit/filesync.go:47` | Read config from client host |
| `dagql.Selector` chains | `core/modulerefs.go:260` | Internal dagql queries |
| `Module.Install()` | `core/module.go:303` | Adds constructor as Query root field |
| `serveModule()` | `engine/server/session.go:1325` | Adds module to client.deps |
| `currentTypeDefs` | `core/schema/module.go:751` | Returns types from all served modules |

## What Was Implemented

### Commit 1: `core/workspace/config.go` + `detect.go`

New `core/workspace` package:
- `ParseConfig(data []byte) (*Config, error)` — TOML parser using `pelletier/go-toml` v1
- `Detect(ctx, statFS, readFile, cwd) (*Workspace, error)` — 4-step fallback chain
- `checkMigrationTriggers(data []byte) error` — fails on toolchains or source != "."

### Commit 2: `engine/server/session.go`

Inserted into `initializeDaggerClient()` after core module setup, before telemetry:

```go
if opts.EncodedModuleID == "" {
    srv.loadWorkspace(ctx, client, coreMod.Dag)
}
```

Two new methods:
- `loadWorkspace()` — detects workspace, iterates config.Modules, calls loadWorkspaceModule
- `loadWorkspaceModule()` — resolves via `dag.Select(moduleSource → asModule)`, calls `serveModule()`

Only runs for the main CLI client (not nested module function calls which have `EncodedModuleID`).

### Commit 3: CLI changes (`cmd/dagger/`)

- **`module_inspect.go`**: Added `initializeWorkspace()` (identical to `initializeCore()` — engine did the heavy lifting). Removed `initializeDefaultModule()`.
- **`functions.go`**: `execute()` branches on `getExplicitModuleSourceRef()`: explicit → `initializeModule()`, none → `initializeWorkspace()`. Added `isWorkspaceRoot()` filter in `addSubCommands()`.
- **`call.go`**: Same pattern in `funcListCmd`. `functionListRun()` gains `workspaceMode bool` param for filtering.
- **`mcp.go`**: Same pattern in `mcpStart()`.

### Commit 4: Build fix

`dagql.Instance[T]` does not exist. Correct type is `dagql.ObjectResult[T]`.

### Commit 5: Design doc update

Updated Part 1 gist (`01-module-vs-workspace.md`) with `-m` flag semantics (see below).

## Follow-Up Decisions and Clarifications

### `-m` Flag Semantics (decided after initial implementation)

The initial plan said "-m loads module ON TOP of workspace". After discussion, the agreed design is:

**`-m` is a connect-time engine parameter.** The engine handles it, not the CLI.

- **Workspace detection always runs** — the engine always knows the workspace root and config, regardless of `-m`. This is important for the future workspace API.
- **Without `-m`**: engine loads workspace modules from `config.toml`.
- **With `-m <ref>`**: engine skips workspace module loading, loads only the explicit module. Its constructor becomes the sole Query root field (besides core). CLI sets MainObject to it — functions appear top-level (backwards compat).
- **CLI is unified**: always just `loadTypeDefs()`. No `Serve()`, no `inspectModule()`, no branching. The only `-m`-specific code is passing the flag to the engine at connect time.

**Rationale:**
- No duplicate code paths (both modes use engine-side loading)
- No name collisions (workspace modules aren't loaded alongside `-m` module)
- No wasted work (workspace modules aren't resolved for nothing)
- Backwards compatible (existing `dagger call -m ./ci test` works unchanged)
- Forward compatible (when modules get workspace API, `-m` modules can access workspace context)

### Breaking Change

Simple `dagger.json` projects (no toolchains, `source="."`) see an empty workspace. `dagger.json` without migration triggers is ignored. Users must:
- Run `dagger migrate` to create `.dagger/config.toml`, or
- Use `dagger call -m .` to explicitly load the module

This is intentional — it forces migration and removes the ambiguity.

## Follow-Up Tasks

### 1. Move `-m` to engine connect-time parameter

**What:** Pass the `-m` ref from CLI to engine at connect time. Engine handles it in `loadWorkspace()`.

**How:**
- Add `ExplicitModuleRef string` to `ClientInitOpts` (or `client.Params`)
- In `loadWorkspace()`:
  ```go
  if explicitModRef != "" {
      srv.loadWorkspaceModule(ctx, client, dag, "", explicitModRef)
  } else if ws.Config != nil {
      for name, entry := range ws.Config.Modules { ... }
  }
  ```
- Remove CLI-side `initializeModule()` calls from `execute()`, `funcListCmd`, `mcpStart()`
- Remove `inspectModule()` dependency — get module name from `loadTypeDefs()` (`SourceModuleName` match)
- CLI becomes fully unified: always `initializeWorkspace()`, never branches on `-m`

**Files:** `engine/server/session.go`, `engine/client/`, `cmd/dagger/functions.go`, `cmd/dagger/call.go`, `cmd/dagger/mcp.go`

### 2. Aliases

**What:** `[aliases]` in config.toml → extra Query root fields that resolve to array-encoded paths.

**Why:** Needed for migration backwards compat (`dagger call lint` → `dagger call dagger-dev lint`).

**How:** TBD — likely engine-side, register alias as a synthetic Query field that delegates to the path.

### 3. Module `config.*` values

**What:** Constructor argument defaults from `config.toml`.

**How:** TBD — engine passes them when resolving module source, or as args to the constructor call.

### 4. End-to-end testing

**What:** Generate `.dagger/config.toml` via the migrate module, then verify:
```bash
dagger call                    # shows workspace modules
dagger call go lint            # call function on workspace module
dagger functions               # lists workspace modules
dagger call -m ./path func     # loads explicit module
```

### 5. `dagger install` updates

**What:** `dagger install` should create/update `.dagger/config.toml` instead of adding deps to `dagger.json`.

### 6. `dagger module init`

**What:** New command to create a module in `.dagger/modules/<name>/` and auto-install it in the workspace.

### 7. Shell integration

**What:** `shell.go` still uses `initializeCore()`. Could benefit from workspace mode for the interactive shell.
