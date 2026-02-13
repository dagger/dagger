# Workspaces (aka "modules v2")

## Overview

This branch implements the **workspaces** feature ([PR #11812](https://github.com/dagger/dagger/pull/11812)) — a redesign of how Dagger projects are configured and how modules interact with the projects they're installed in. The result: reduced complexity for Dagger users, and increased control for module developers.

The design is split into two parts:
- **[Part 1: Workspaces and Modules](https://gist.github.com/shykes/e4778dc5ec17c9a8bbd3120f5c21ce73)** — introduces the workspace concept, separates project config (`.dagger/config.toml`) from module definitions (`dagger.json`), new dependency model.
- **[Part 2: Workspace API](https://gist.github.com/shykes/86c05de3921675944087cb0849e1a3be)** — a `Workspace` type that modules receive as a function argument, replacing `+defaultPath`/`+ignore` with dynamic code-level access to the project. Deprecates "toolchains" by making every module a toolchain.

## Naming

- **"Workspaces"** is the overall feature name.
- **"Modules v2"** is a nickname for the same feature — it makes modules both more powerful (Workspace API for developers) and simpler (workspace config for users).
- Part 1 and Part 2 are design document labels, not separate features.

## Key concepts

| | Workspace | Module |
|-|-----------|--------|
| **What** | A project directory | Packaged software |
| **Config** | `.dagger/config.toml` | `dagger.json` |
| **Purpose** | Configure Dagger for this project | Extend Dagger's capabilities |
| **Analogy** | VS Code workspace | VS Code extension |

**Two dependency models:**
- **Workspace → Module** (`.dagger/config.toml`): "use this module in my project"
- **Module → Module** (`dagger.json`): "my code calls this module's functions"

**Workspace API:** Any module function can declare a `Workspace` argument. The engine auto-injects the current workspace. The module can then call `ws.Directory()`, `ws.File()` to access project files dynamically. `Workspace` cannot be stored as a field — it must be a function argument.

## Config format

```toml
[modules.ci]
source = "modules/ci"
alias = true              # promote functions to top-level commands

[modules.go]
source = "github.com/dagger/go-toolchain@v1.0"
config.goVersion = "1.22"  # constructor arg default
```

Paths in `source` are relative to `.dagger/` directory.

## Key files

### Config & detection
- `core/workspace/config.go` — `Config` struct, `ParseConfig()`, `SerializeConfig()`
- `core/workspace/detect.go` — `Detect()` walks up from CWD: `.dagger/` → `.git` → CWD fallback

### Engine-side loading
- `engine/server/session.go` — the heart of it:
  - `ensureWorkspaceLoaded()` — orchestrates detection + module loading
  - `loadWorkspaceModule()` / `loadExtraModule()` — loads modules via dagql pipeline
  - `applyWorkspaceConfigDefaults()` — maps `config.*` entries to constructor arg defaults
- `engine/opts.go` — `ClientMetadata` with `ExtraModules`, `SkipWorkspaceModules`, `RemoteWorkdir`

### Workspace API (Part 2)
- `core/workspace.go` — `Workspace` struct (Root, ConfigPath, HasConfig, ClientID)
- `core/schema/workspace.go` — resolvers: `currentWorkspace()`, `directory()`, `file()`, `install()`, `moduleInit()`, `configRead()`, `configWrite()`
- `core/modfunc.go` — injection: `IsWorkspace()` detects Workspace args, `loadWorkspaceArg()` injects them, `hasWorkspaceArgs()` triggers content-addressed caching
- `core/module.go` (~line 713) — enforces "cannot be stored as field" constraint

### CLI
- `cmd/dagger/main.go` — `--workdir`/`-C` flag, remote workdir detection
- `cmd/dagger/workspace.go` — `dagger workspace info`, `dagger install`
- `cmd/dagger/module.go` — `dagger module init`

## What's implemented

**Part 1 (Workspaces and Modules):**
- Workspace detection, config parsing/serialization (TOML)
- Module loading from config at connect time
- Auto-aliases (`alias = true`), constructor arg defaults (`config.*`)
- `dagger install`, `dagger module init`, `dagger workspace info`
- `-C`/`--workdir` with remote git refs
- `dagger migrate` for legacy `dagger.json` projects
- `dagger config` for reading/writing config values

**Part 2 (Workspace API):**
- `Workspace` type in dagql schema with `directory()` and `file()` methods
- Automatic injection into module functions
- "Cannot be stored" constraint on module object fields
- Content-addressed caching (digest of workspace content in cache key)
- Client session routing (workspace carries originating ClientID for host FS access)

## Known gaps

- `IncludeCoreModule` connect-time parameter (expose core API at Query root)
- Lock file (`.dagger/lock`)
- Workspace `ignore` patterns (field exists in config but not wired)
- `.env` deprecation / migration of user defaults
- `findUp()` and `search()` methods on Workspace (in Part 2 design but not implemented)

## Testing

Use the playground script for manual e2e testing. Load the `engine-dev-testing` skill for instructions.

Key test scenarios:
1. `dagger call` in a directory with `.dagger/config.toml` — should load workspace modules
2. `dagger call -m ./some/module func` — should skip workspace, load explicit module
3. `dagger -C github.com/someone/repo@version call func` — should clone and load remote workspace
4. `dagger install github.com/some/module` — should write to config.toml
5. `dagger module init --sdk=go mymod` — should create `.dagger/modules/mymod/` and update config
6. Module with `Workspace` constructor arg — should receive injected workspace, access files

## Common pitfalls

- **Can't `go build ./engine/server/...` on macOS** — Linux-only deps. Use `GOOS=linux GOARCH=amd64 go build ./engine/server/...` to verify types, or build CLI with `go build ./cmd/dagger/...`.
- **dagql `Select` types** — use `dagql.ObjectResult[*core.Foo]` for object results, `dagql.String`/`dagql.Boolean` for scalars.
- **Workspace detection vs module loading** — detection always runs (even with `-m`). Loading modules from config can be skipped via `SkipWorkspaceModules`.
- **Workspace injection vs defaultPath** — these are separate code paths in `modfunc.go`. `IsWorkspace()` is checked first, then `isContextual()` for defaultPath/defaultAddress.
