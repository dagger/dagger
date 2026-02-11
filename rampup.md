# Workspace Branch Rampup

## What this branch does

This branch implements the **workspace** concept from [the design proposal](https://gist.github.com/shykes/e4778dc5ec17c9a8bbd3120f5c21ce73). The core idea: separate **workspaces** (project configuration) from **modules** (packaged software). A workspace is configured via `.dagger/config.toml` and determines which modules are loaded when the CLI connects to the engine.

## Why

Today `dagger.json` conflates project config with module definition. This causes confusion about file access, dependency models, and CLI behavior. The workspace model gives each concept its own file and clear semantics:

- **Workspace** (`.dagger/config.toml`): "what modules does my project use?"
- **Module** (`dagger.json`): "what functions does this package expose?"

## Architecture: how workspace loading works

### The loading pipeline

1. **CLI** connects to engine, sending `ClientMetadata` as HTTP headers
2. **Engine** (`ensureWorkspaceLoaded` in `engine/server/session.go`) runs workspace detection:
   - Walks up from CWD looking for `.dagger/`, `.git`, or falls back to CWD
   - Reads `.dagger/config.toml` if present
3. **Engine** loads each module from the config via `loadWorkspaceModule()` — this calls `moduleSource(refString).asModule` through the dagql pipeline, same machinery that `-m` uses
4. **Engine** loads any extra modules from `-m` flag via `loadExtraModule()`
5. **CLI** builds commands from whatever the engine served in the GraphQL schema

### Connect-time parameters (`engine/opts.go` → `ClientMetadata`)

| Parameter | Effect |
|-----------|--------|
| `ExtraModules` | Modules from `-m` flag. Loaded after workspace modules. |
| `SkipWorkspaceModules` | Skip loading from config.toml (set by `-m`). |
| `RemoteWorkdir` | Git ref for remote workspace. Engine clones instead of using local FS. |

### Local vs remote workdir

- **Local** (`-C /path`): CLI does `os.Chdir`, engine detects workspace from CWD as usual
- **Remote** (`-C github.com/foo/bar@v1.0`): CLI stores in `remoteWorkdir` global, engine clones repo via `git().ref().tree()`, reads config.toml from cloned tree, constructs full git refs for module entries

The CLI distinguishes these by trying `os.Chdir` — if it fails, it's remote. This is in `cmd/dagger/main.go` `PersistentPreRunE`.

## Key files

### Config & detection
- `core/workspace/config.go` — `Config` struct, `ParseConfig()`, `SerializeConfig()` (TOML format)
- `core/workspace/detect.go` — `Detect()` walks up from CWD, checks `.dagger/`, `.git`, legacy `dagger.json`

### Engine-side loading
- `engine/server/session.go` — **the heart of it all**:
  - `ensureWorkspaceLoaded()` (~line 1416) — orchestrates workspace detection + module loading
  - `loadRemoteWorkspace()` (~line 1617) — clones git repo, reads config, loads modules
  - `loadWorkspaceModule()` (~line 1564) — loads a single module via dagql pipeline
  - `loadExtraModule()` (~line 1518) — loads `-m` modules
  - `applyWorkspaceConfigDefaults()` (~line 1710) — maps `config.*` entries to constructor arg defaults
- `engine/opts.go` — `ClientMetadata` struct with `ExtraModules`, `SkipWorkspaceModules`, `RemoteWorkdir`

### CLI
- `cmd/dagger/main.go` — `--workdir`/`-C` flag, `remoteWorkdir` global, `NormalizeWorkdir()`
- `cmd/dagger/engine.go` — `withEngine()` passes `remoteWorkdir` to `params.RemoteWorkdir`
- `cmd/dagger/workspace.go` — `dagger workspace info`, `dagger install`
- `cmd/dagger/module.go` — `dagger module init`
- `cmd/dagger/function_name.go` — `initModuleParams()` maps `-m` to `params.Module`

### Schema (engine-side API for workspace operations)
- `core/workspace.go` — `Workspace` type (dagql representation)
- `core/schema/workspace.go` — resolvers for `workspace()`, `install()`, `moduleInit()`
- `core/env.go` — hides `Workspace` from module SDKs

### Module ref parsing (important for understanding remote refs)
- `core/modulerefs.go` — `ParseRefString()`, `fastModuleSourceKindCheck()`, `ParseGitRefString()`
  - Determines if a ref is local path or git URL
  - Extracts repo root, version, subdir from git refs

## Config format

```toml
[modules.ci]
source = "modules/ci"
alias = true              # promote functions to top-level commands

[modules.go]
source = "github.com/dagger/go-toolchain@v1.0"
config.goVersion = "1.22"  # constructor arg defaults
```

Paths in `source` are relative to `.dagger/` directory. If a source contains dots and slashes (like a git URL), it's treated as a remote ref.

## What's been implemented

- Workspace detection (`.dagger/` → `.git` → CWD fallback)
- Config parsing/serialization (TOML)
- Module loading from config at connect time
- Auto-aliases (`alias = true`)
- Constructor arg defaults (`config.*` keys)
- `dagger install` — adds modules to config.toml
- `dagger module init` — scaffolds module + auto-installs
- `dagger workspace info` — shows workspace root and config path
- `-C`/`--workdir` with remote git refs
- Migration detection (legacy `dagger.json` → error with migration prompt)
- Engine-side `Workspace` type in dagql schema

## What's NOT implemented yet (known gaps)

- `dagger migrate` — the migration module exists at `modules/migrate/` but is a prototype
- `IncludeCoreModule` connect-time parameter (expose core API at Query root)
- Lock file (`.dagger/lock`)
- Workspace `ignore` patterns (field exists in config but not wired)
- `.env` deprecation / migration of user defaults

## Testing

Use the playground script for manual e2e testing. Load the `engine-dev-testing` skill for instructions. The basic flow is: build a dev engine from source, run commands inside it.

Key test scenarios:
1. `dagger call` in a directory with `.dagger/config.toml` — should load workspace modules
2. `dagger call -m ./some/module func` — should skip workspace, load explicit module
3. `dagger -C github.com/someone/repo@version call func` — should clone and load remote workspace
4. `dagger install github.com/some/module` — should write to config.toml
5. `dagger module init --sdk=go mymod` — should create `.dagger/modules/mymod/` and update config

## Common pitfalls

- **Can't `go build ./engine/server/...` on macOS** — pre-existing issue, Linux-only deps. Use `GOOS=linux GOARCH=amd64 go build ./engine/server/...` to verify types, or just build the CLI with `go build ./cmd/dagger/...`.
- **dagql `Select` types** — use `dagql.ObjectResult[*core.Foo]` for object results, `dagql.String`/`dagql.Boolean` for scalars. There is no `dagql.Instance`.
- **Workspace detection vs module loading** — detection always runs (even with `-m`). Loading modules from config can be skipped via `SkipWorkspaceModules`.
