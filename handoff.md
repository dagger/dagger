# Workspace Branch - Handoff

## What was done

### 1. Workspace detection timing fix (commit 9c9e3cec2)
- **Problem**: `loadWorkspace()` ran during phase 1 of client-engine connection, before buildkit session was registered (phase 2). This caused workspace detection to silently fail over TCP.
- **Fix**: Deferred workspace loading to `serveQuery()` (phase 3) using same mutex+flag pattern as `ensureExtraModulesLoaded`.
- **Files**: `engine/server/session.go`

### 2. Replace SkipRootFieldInstalls with currentTypeDefs(includeCore) (commit 053c3c168)
- **Problem**: `SkipRootFieldInstalls` hid ALL core root fields from dagql schema, including `currentTypeDefs` which the CLI needs for introspection.
- **Fix**: Removed `SkipRootFieldInstalls` entirely. All core fields are always installed. Added `includeCore` optional boolean arg to `currentTypeDefs` — when false, filters out core TypeDefs (those with empty `SourceModuleName`). CLI passes `includeCore: false` for workspace mode.
- **Files**: `dagql/server.go`, `dagql/objects.go`, `core/moddeps.go`, `core/schema/module.go`, `cmd/dagger/typedefs.graphql`, `cmd/dagger/module_inspect.go`, `cmd/dagger/call.go`, `cmd/dagger/functions.go`, `cmd/dagger/shell_commands.go`

### 3. Fix workspace module source resolution for remote refs (in commit 053c3c168)
- **Problem**: Workspace config source paths were always prepended with `.dagger/` directory, breaking remote git refs like `github.com/dagger/dagger/modules/wolfi`.
- **Fix**: Check if source looks local (starts with `.` or `/`, or contains no dots) before prepending workspace root. Remote refs pass through to `moduleSource` as-is.
- **Files**: `engine/server/session.go`

### 4. Always include Query root type in currentTypeDefs (commit a2c7b5020)
- **Problem**: Query type has empty `SourceModuleName` (core type), so `includeCore: false` filtered it out, causing nil pointer dereference in CLI.
- **Fix**: `isCoreTypeDef` never treats Query as core. CLI filters Query root functions to hide core constructors.
- **Files**: `core/schema/module.go`, `cmd/dagger/functions.go`, `cmd/dagger/call.go`

### 5. Root-level filter fix for dagger functions (commit e45400347)
- **Problem**: Core function filter in `functionListRun` applied unconditionally, which would break navigating into module types (`dagger functions wolfi`).
- **Fix**: Only apply at root level. Also suppress noisy "skipped functions" message when filtering core functions.
- **Files**: `cmd/dagger/call.go`

## Test results

| Test | Status | Notes |
|------|--------|-------|
| Empty workspace detection | PASS | `.dagger/` dir detected, workspace loads |
| Legacy dagger.json migration (with source) | PASS | Error fires: "run `dagger migrate` to update it" |
| Workspace with wolfi module - functions | PASS | Shows `alpine`, `wolfi` only, no core noise |
| Workspace with wolfi module - call --help | PASS | `dagger call wolfi --help` shows wolfi functions |
| dagger version | PASS | Engine starts and reports version |

## Implementation gaps (not yet addressed)

1. config.* constructor defaults parsed but not applied
2. `dagger install` doesn't update `.dagger/config.toml`
3. `dagger module init` not implemented
4. `IncludeCoreModule` not a client control (connect-time parameter)
5. Migration module generates old `[aliases]` format
6. Workspace `ignore` field not in config struct
7. Migration error message references wrong command (`dagger migrate` doesn't exist)
8. `.dagger/lock` file not implemented
