# Workspace Branch - Handoff

## What was done

### 1. Workspace detection timing fix (commit 9c9e3cec2)
- **Problem**: `loadWorkspace()` ran during phase 1 of client-engine connection, before buildkit session was registered (phase 2). This caused workspace detection to silently fail over TCP.
- **Fix**: Deferred workspace loading to `serveQuery()` (phase 3) using same mutex+flag pattern as `ensureExtraModulesLoaded`.
- **Files**: `engine/server/session.go`

### 2. Replace SkipRootFieldInstalls with currentTypeDefs(includeCore) (commit 053c3c168)
- **Problem**: `SkipRootFieldInstalls` hid ALL core root fields from dagql schema, including `currentTypeDefs` which the CLI needs for introspection.
- **Fix**: Removed `SkipRootFieldInstalls` entirely. All core fields are always installed. Added `includeCore` optional boolean arg to `currentTypeDefs` — when false, filters out core TypeDefs (those with empty `SourceModuleName`). The arg exists for API clients; CLI loads all types and filters at display level.
- **Files**: `dagql/server.go`, `dagql/objects.go`, `core/moddeps.go`, `core/schema/module.go`, `cmd/dagger/typedefs.graphql`, `cmd/dagger/module_inspect.go`, `cmd/dagger/call.go`, `cmd/dagger/functions.go`, `cmd/dagger/shell_commands.go`

### 3. Fix workspace module source resolution for remote refs (in commit 053c3c168)
- **Problem**: Workspace config source paths were always prepended with `.dagger/` directory, breaking remote git refs like `github.com/dagger/dagger/modules/wolfi`.
- **Fix**: Check if source looks local (starts with `.` or `/`, or contains no dots) before prepending workspace root. Remote refs pass through to `moduleSource` as-is.
- **Files**: `engine/server/session.go`

### 4. Always include Query root type in currentTypeDefs (commit a2c7b5020)
- **Problem**: Query type has empty `SourceModuleName` (core type), so `includeCore: false` filtered it out, causing nil pointer dereference in CLI.
- **Fix**: `isCoreTypeDef` never treats Query as core. CLI filters Query root functions to hide core constructors.
- **Files**: `core/schema/module.go`, `cmd/dagger/functions.go`, `cmd/dagger/call.go`

### 5. CLI loads all TypeDefs for pipeline navigation (commit 50f34b9fa)
- **Problem**: `includeCore: false` filtered out core types like Container, breaking `dagger call wolfi container with-exec`. CLI needs core types for navigating return types.
- **Fix**: CLI always loads with `includeCore: true`. Root-level filter hides core constructors from display, but types remain available for pipeline navigation.
- **Files**: `cmd/dagger/module_inspect.go`, `cmd/dagger/shell_commands.go`

### 6. Migration error message fix (commit 2db2d87a3)
- **Fix**: No longer references non-existent `dagger migrate` command.
- **Files**: `core/workspace/detect.go`

### 7. Migration module alias format fix (commit 25613e9a3)
- **Fix**: Generates per-module `alias = true` instead of global `[aliases]` section.
- **Files**: `modules/migrate/toml.go`, `modules/migrate/report.go`

### 8. Add Ignore field to Config struct (commit 80031d299)
- **Fix**: `Config.Ignore []string` field for parsing workspace ignore patterns. Enforcement not yet implemented.
- **Files**: `core/workspace/config.go`

### 9. Apply workspace config defaults as constructor args (commit f2e3bd7cd)
- **Problem**: `[modules.NAME.config]` entries in config.toml were parsed into `ModuleEntry.Config` but never applied to the module's constructor arguments.
- **Fix**: After loading a workspace module via dagql pipeline, `applyWorkspaceConfigDefaults()` iterates over the module's ObjectDefs, finds the constructor, and sets `DefaultValue` on matching arguments (case-insensitive match). Values are parsed as JSON if valid, otherwise treated as string literals.
- **Files**: `engine/server/session.go`

## Test results

| Test | Status | Notes |
|------|--------|-------|
| Empty workspace (no modules) | PASS | Clean empty output |
| Empty .dagger/ (no config.toml) | PASS | Clean empty output |
| Legacy dagger.json migration | PASS | Error fires with correct message |
| Workspace with wolfi module - functions | PASS | Shows `alpine`, `wolfi` only |
| Workspace with wolfi module - call --help | PASS | Pipeline help works |
| Pipeline execution: wolfi container with-exec stdout | PASS | Prints "hello" |
| dagger version | PASS | Engine starts correctly |
| Config defaults in config.toml | PASS | `dagger functions` shows `alpine`, `wolfi` with config entries |
| Malformed config.toml | PASS | Clean error: `failed to parse config.toml: was expecting token =` |
| Workspace call --help with config | PASS | `dagger call wolfi --help` shows `container` function |

## Implementation gaps (not yet addressed)

1. **dagger install**: Doesn't update `.dagger/config.toml` (still only updates `dagger.json`)
2. **dagger module init**: Not implemented (no way to create a new module in a workspace)
3. **IncludeCoreModule**: Not yet a connect-time client control parameter
4. **Workspace ignore enforcement**: `Ignore` field parses but patterns aren't applied during operations
5. **.dagger/lock file**: Not implemented
