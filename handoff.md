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

### 10. Check migration triggers when .dagger/ exists without config.toml (commit adc527962)
- **Problem**: `Detect()` found `.dagger/` (step 1), failed to read `config.toml`, and immediately returned an empty workspace — skipping `dagger.json` migration check (step 2). Projects like the dagger repo (`.dagger/` as Go module source dir + `dagger.json` with triggers) silently got an empty workspace instead of the migration error.
- **Fix**: When `.dagger/` exists but has no `config.toml`, also check for `dagger.json` migration triggers before returning empty workspace.
- **Files**: `core/workspace/detect.go`

### 11. `dagger install` writes to workspace .dagger/config.toml (commit 1381a95ad)
- **Problem**: `dagger install` added dependencies to `dagger.json` (legacy module system). Per workspace design, it should add modules to `.dagger/config.toml`.
- **Fix**: Rewrote `moduleInstallCmd` to use workspace flow:
  1. `workspace.DetectLocal(cwd)` — CLI-side workspace detection using `os.Stat` (new function, no buildkit session needed)
  2. `dag.ModuleSource().ModuleName()` — engine resolves module name
  3. `dag.ModuleSource().Kind()` — determines local vs git for source path formatting
  4. Loads existing config or creates empty, adds module entry
  5. `workspace.SerializeConfig()` — new function, hand-built TOML with dotted-key format
  6. Writes `.dagger/config.toml` (creates `.dagger/` dir if needed)
- Idempotent: "already installed" if name+source match existing entry
- `--name` flag overrides module name
- **Files**: `cmd/dagger/module.go`, `core/workspace/config.go`, `core/workspace/detect.go`

### 12. CLI build context fix for embedded markdown (commit 1381a95ad)
- **Problem**: `core/prompts/fs.go` uses `//go:embed *.md` but CLI build context excluded `.md` files, causing build failure.
- **Fix**: Added `"!core/prompts/*.md"` to `toolchains/cli-dev/main.go` ignore patterns.
- **Files**: `toolchains/cli-dev/main.go`

### 13. `dagger module init` command (commit cacc78b10)
- **Problem**: No way to create a new module inside a workspace.
- **Fix**: Added `dagger module` parent command with `init` subcommand:
  - `dagger module init --sdk=go ci` creates module at `.dagger/modules/ci/`
  - Auto-installs in `.dagger/config.toml` with `ci.source = "modules/ci"`
  - `--sdk` is required (go, python, typescript)
  - Delegates to engine for SDK scaffolding (dagger.json + source files)
  - Workspace detection: checks if `.dagger/` or `.git` exists at root
  - Outside workspace: creates module at `<cwd>/<name>/` (standalone mode)
  - Old `dagger init` kept intact for backwards compatibility
- **Files**: `cmd/dagger/module.go`, `cmd/dagger/main.go`

### 14. `dagger workspace info` command
- **What**: Added `dagger workspace` parent command with `info` subcommand.
- **Output**: Shows workspace root path and config path (or `none` if no config.toml).
- **Pure CLI-side**: Uses `workspace.DetectLocal()`, no engine connection needed.
- **Files**: `cmd/dagger/module.go`, `cmd/dagger/main.go`

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
| .dagger/ without config.toml + dagger.json with triggers | PASS | Migration error fires (dagger repo scenario) |
| Legacy dagger.json without .dagger/ | PASS | Migration error fires |
| `dagger install` remote module | PASS | `Installed module "wolfi"`, config.toml written |
| `dagger install --name=mywolfi` | PASS | Both entries in config.toml |
| Idempotent re-install | PASS | `Module "wolfi" is already installed` |
| `dagger functions` after install | PASS | Shows `alpine`, `wolfi` |
| `dagger module init --sdk=go ci` in workspace | PASS | Creates `.dagger/modules/ci/`, auto-installs in config.toml |
| Module files generated (dagger.json, main.go) | PASS | SDK scaffolding created |
| `dagger functions` after module init | PASS | Shows `ci` |
| `dagger module init` without --sdk | PASS | Error: `--sdk is required` |
| `dagger workspace info` with config | PASS | Shows root path and config path |
| `dagger workspace info` empty .dagger/ | PASS | Shows root path, `Config: none` |
| `dagger workspace info` bare directory | PASS | Falls back to cwd, `Config: none` |

## Implementation gaps (not yet addressed)

1. **Workspace ignore enforcement**: `Ignore` field parses but patterns aren't applied during operations
2. **.dagger/lock file**: Not implemented
