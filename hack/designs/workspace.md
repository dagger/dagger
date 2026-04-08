# Dagger Design: Workspaces

*Introduce workspaces: project-level configuration separate from module definitions.*

- [Problem](#problem)
- [Solution](#solution)
- [Workspaces](#workspaces)
  - [What is a Workspace?](#what-is-a-workspace)
  - [Workspace Configuration](#workspace-configuration)
  - [Workspace Dependency Model](#workspace-dependency-model)
  - [Environments](#environments)
  - [Workspace Lockfile](#workspace-lockfile)
  - [Workspace Selection](#workspace-selection)
  - [Loading Modules](#loading-modules)
  - [Migration](#migration)
- [Modules](#modules)
  - [What is a Module?](#what-is-a-module)
  - [Create a Module](#create-a-module)
  - [Workspace API](#workspace-api)
  - [Changes to dagger.json](#changes-to-daggerjson)
  - [Module Dependency Model](#module-dependency-model)
- [CLI Changes](#cli-changes)
  - [New Commands](#new-commands)
  - [Changed Commands](#changed-commands)
  - [Removed Commands](#removed-commands)
- [Known Gaps & Future Work](#known-gaps--future-work)

## Problem

Dagger currently conflates two distinct concepts in a single `dagger.json` file:

- **Project configuration** (what tools to use, how to configure them)
- **A software package** (what code to package, what dependencies it needs)

This causes confusion about what files are accessible, how dependencies work, and what appears in the CLI.

## Solution

Separate and disambiguate the concepts of *workspace* and *module*.

- A *module* is a software package which can be loaded and executed by Dagger
- A *workspace* is a context for configuring and using Dagger

## Workspaces

### What is a Workspace?

A **workspace** is a directory used as context for configuring and using Dagger. Typically it is a git repository, or a subdirectory within a larger repo.

The main way to configure a Dagger workspace is to add modules to it, possibly with custom workspace-level configuration.

### Workspace Configuration

The workspace config file is human-editable and lives at `.dagger/config.toml`:

```toml
# Paths to ignore during workspace operations (extends .gitignore)
# ignore = ["docs/**", "marketing/**"]

# Modules added to this workspace
[modules.ci]
source = "modules/ci"
entrypoint = true

[modules.node]
source = "github.com/dagger/node-toolchain@v1.0"

[modules.go]
source = "github.com/dagger/go-toolchain@v1.0"
config.goVersion = "1.22"
config.lintStrict = true
```

Each module entry is a table with a required `source` and optional keys. The table key (e.g., `go`) is the module's local name in this workspace — it determines the CLI namespace (`dagger call go build`).

Paths are relative to the `.dagger/` directory.

#### Adding Modules

```bash
# Add a module to the workspace
dagger install github.com/dagger/go-toolchain

# Override the local name
dagger install github.com/dagger/go-toolchain --name=go
```

This updates `.dagger/config.toml`. If no workspace exists yet:
- If in a git repository, creates `.dagger/config.toml` at the repo root
- Otherwise, creates in the current directory

Once installed, module functions are available:

```bash
dagger call go build
dagger call go test
```

#### Module Configuration (`config.*`)

The `config.*` keys set default values for a module's constructor arguments:

```toml
[modules.go]
source = "github.com/dagger/go-toolchain@v1.0"
config.goVersion = "1.22"
config.lintStrict = true
config.tags = ["integration", "unit"]
```

Config keys map directly to constructor argument names. Supported types: strings, booleans, numbers, arrays.

This replaces the legacy `.env` mechanism for constructor arg defaults. The engine provides the same evaluation behavior (variable expansion with `${VAR}`, `env://` references for secrets) regardless of whether values come from `.env` or `config.toml`.

#### Workspace Ignore

Top-level `ignore` defines paths to skip during workspace operations (glob, search, file access). This extends `.gitignore` for tracked-but-irrelevant parts of the repo:

```toml
ignore = ["docs/**", "marketing/**", "data-science/**"]
```

The engine already respects `.gitignore` by default. The `ignore` key covers paths that are tracked in git but irrelevant to Dagger — useful in large monorepos to speed up module discovery.

### Workspace Dependency Model

A workspace depends on modules. This is project configuration: "use this module in my project." The module gains access to your workspace and extends your CLI.

Workspace → module dependencies are configured in `.dagger/config.toml` and installed with `dagger install`. This is distinct from module → module dependencies, which are code-level and configured in `dagger.json` (see [Module Dependency Model](#module-dependency-model)).

A module added to a workspace is *not* the same as a module dependency. They live in different config files, are installed with different commands, and have different effects.

### Environments

Module configuration and workspace settings can be overridden per environment (e.g., CI, staging, production):

```bash
dagger check --env=ci
```

Multiple environments would be defined in `config.toml`, each with their own `config.*` values for registered modules. Environment selection would be explicit via `--env`.

**Status: not yet implemented.** This section is a placeholder to ensure the design is not forgotten. The config format and module loading pipeline are designed to accommodate this feature when it is built.

### Workspace Lockfile

Dagger's lockfile system is a general-purpose mechanism for pinning symbolic lookups (container image refs, git branches, module sources) to exact resolved values. It is designed and implemented separately — see the `lockfile` branch for the full primitive specification.

In a workspace, the lockfile is the **source of truth for dependency pins**. Unlike `dagger.json` (which carries its own version pins inline), `config.toml` does not embed resolved versions — those live in `.dagger/lock`.

Key workspace-specific behaviors:

- **Enabled by default**: Workspaces use lockfiles by default (`--lock=pinned`), unlike the lockfile primitive's default of `disabled`. This means workspace module resolution, container base images, and git refs are all pinned on first run and reused on subsequent runs.
- **Lock file location**: `.dagger/lock`, derived from the workspace root.
- **Update flow**: `dagger lock update` refreshes entries in the lockfile. Running with `--lock=live` refreshes entries as they are encountered during execution.

`dagger.json` already carries inline version pins for module dependencies. How these interact with workspace-level lockfile pins is an open design question. Today, both mechanisms coexist independently.

### Workspace Selection

#### Automatic Detection

When the CLI connects to the engine, the engine detects the workspace before any queries are served:

1. **Find workspace**: Walk up from the client's working directory looking for a `.dagger/` directory. The directory containing `.dagger/` is the **workspace root**.

2. **Parse config**: Read `.dagger/config.toml` if it exists. If `.dagger/` exists but has no `config.toml`, the workspace is valid but empty.

3. **Legacy fallback**: If no `.dagger/` is found, check for a `dagger.json` with legacy triggers. If eligible, a compatibility workspace is inferred (see [Migration](#migration)).

Workspace detection always runs, regardless of other flags. The engine always knows the workspace root and config.

#### Explicit Selection: `--workspace` / `-W`

The `--workspace` flag (shorthand `-W`) explicitly selects a workspace for the session. It accepts either a local path or a remote git ref:

```bash
dagger -W ../other-project functions
dagger -W github.com/acme/project check
```

The flag is an opaque string passed to the engine, which resolves it (local path first, remote git ref second). The CLI does not classify or interpret the ref.

`--workspace` does not change the process working directory. The hidden legacy `--workdir` flag changes cwd but carries no workspace semantics. When both are present, `--workdir` applies first, then `--workspace` is interpreted relative to the resulting cwd.

Commands that accept `--workspace`: `call`, `functions`, `check`, `generate`, `workspace info`, `workspace list`, `init`, `install`, `update`, `lock update`, `workspace config`.

Commands that are module-centric or otherwise unrelated (`config`, `migrate`, `module ...`) reject `--workspace`.

#### Remote Workspaces

`--workspace` accepts remote git references, allowing Dagger to run against a remote project without cloning it locally:

```bash
dagger -W github.com/dagger/dagger@main call ci build
dagger -W github.com/dagger/dagger/subproject@v1.0 call build
```

When the engine receives a remote workspace ref:

1. **Parse the git ref**: Extract the repo URL, version (tag/branch/commit), and optional subdirectory.
2. **Clone the repo**: Use the engine's existing git pipeline to fetch the repo at the specified version.
3. **Detect workspace**: Look for `.dagger/config.toml` within the cloned tree (starting from the subdirectory if specified).
4. **Load modules**: Local sources (e.g. `source = "modules/ci"`) are resolved as paths within the cloned repo. Remote sources (e.g. `source = "github.com/other/module@v1.0"`) are loaded as-is.

**Limitations:**
- **Read-only**: Mutating commands (`install`, `module init`) are not supported.
- **No local host access**: The workspace context is the cloned git tree, not the local machine. Modules cannot access the local filesystem.
- **Config value resolution**: `config.*` entries that reference environment variables (`${VAR}`) or secrets (`env://KEY`) resolve against the *local* environment where Dagger runs, not the remote repo's environment.

### Loading Modules

Configuration loading happens in the **engine**, not the CLI. When the CLI connects to the engine, the engine detects the workspace and loads modules into the GraphQL schema before serving any queries. The CLI has a single initialization path — it never manages module state. It simply reads whatever the engine served and builds commands from the schema's type definitions.

#### Workspace Modules

For each entry in `[modules]` in the config, the engine resolves the source and installs the module's constructor as a top-level function in the GraphQL schema:

```bash
dagger call go build     # calls the 'go' module's constructor, then 'build'
dagger call ci test      # calls the 'ci' module's constructor, then 'test'
```

`config.*` values are injected as constructor argument defaults.

#### CWD Module

After workspace detection, the engine separately detects the **CWD module** — the nearest `dagger.json` found by walking up from the caller's working directory.

The CWD module is a permanent convenience, not a migration-specific behavior. It is detected separately from workspace context.

If the CWD module is distinct from modules already loaded by the workspace, it is loaded as an additional module and becomes the active entrypoint for the invocation. If it refers to a module already loaded, nothing extra happens — see [Deduplication](#deduplication).

If explicit extra modules (`-m`) are present, the CWD module is suppressed entirely.

#### Extra Modules (`-m`)

The `-m` flag bypasses workspace module loading and loads an explicit module instead:

```bash
dagger call -m ./my-module build
dagger call -m github.com/foo/bar build
```

When `-m` is used:
- Workspace modules from `config.toml` are skipped
- The CWD module is suppressed
- The specified module is loaded with its functions promoted to the Query root
- Workspace detection still runs (the engine still knows the workspace root)

If explicit extra modules nominate more than one distinct entrypoint, that is an error.

Why `-m` exists:

- **Backwards compatibility**: Existing CI scripts using `dagger call -m ./ci test` continue to work unchanged.
- **Ad-hoc module usage**: Run any module by reference without installing it: `dagger call -m github.com/foo/bar build`.
- **Single code path**: Both `-m` and workspace loading use the same engine-side module loading pipeline.

#### Deduplication

Multiple loading paths can nominate the same module (workspace config, CWD module, `-m`). The engine deduplicates by resolved source identity:

- Local modules: absolute source root + source subpath
- Git modules: clone ref + source subpath (plus pin, if present)

If multiple paths nominate the same module, the engine loads it once.

After deduplication, the engine performs a separate entrypoint arbitration pass.

#### Entrypoint Module

A workspace can designate one module as the **entrypoint**. The entrypoint module's methods are promoted to the Query root, so users can write:

```bash
dagger call build        # instead of 'dagger call ci build'
```

Set `entrypoint = true` on a module in `config.toml`:

```toml
[modules.ci]
source = "modules/ci"
entrypoint = true
```

At most one module can be the active entrypoint. After deduplication, entrypoint arbitration runs with this precedence:

1. Extra modules (`-m`)
2. CWD module
3. Ambient workspace modules

Cross-tier conflicts are resolved by precedence. Same-tier conflicts are errors. In particular:
- More than one distinct ambient workspace entrypoint is an invalid workspace configuration.
- More than one distinct extra-module entrypoint is an invalid request.

The engine installs proxy fields at the Query root for each of the entrypoint module's methods. These proxies are pure routing — there is no ambiguity even when method names overlap with core fields.

If the entrypoint module's constructor has arguments, a `with` field is installed on `Query` to accept them:

```bash
dagger call --foo=abc build    # translates to: with(foo: "abc") { build }
```

Core fields and existing constructors always win conflicts. If an entrypoint method name conflicts with an existing Query root field, the proxy is skipped — the method remains accessible through the namespaced constructor (`dagger call ci build`). This behavior comes from schema construction, so all clients see the same shape through introspection.

### Migration

#### Runtime Compatibility: CompatWorkspace

When the engine encounters a legacy `dagger.json` with no `.dagger/config.toml`, it does not fail immediately. Instead, it infers a **compat workspace** (`CompatWorkspace`) — an in-memory workspace-shaped projection of the legacy configuration.

The engine detects ambient workspace context in this order:

```
find-up .dagger/config.toml   → normal workspace
else find-up eligible legacy dagger.json   → CompatWorkspace
else   → no workspace
```

A legacy `dagger.json` is eligible for compat if any of these are true:
- `source != "."`
- `toolchains` is non-empty
- `blueprint` is set

If a legacy `dagger.json` is found but is not eligible, it does not create ambient workspace context (it is a plain module).

Legacy fields (`blueprint`, `toolchains`) are interpreted **only** while building the compat workspace. Generic module loading does not honor them outside this context; direct module load fails, and workspace module sources that still point at a legacy workspace fail with an explicit migration error.

Inside the compat workspace:
- If a legacy `blueprint` exists, the blueprint module is the compat entrypoint.
- Otherwise, the projected main module is the compat entrypoint.

If there is also a distinct CWD module (see [CWD Module](#cwd-module)), the CWD module wins as the active entrypoint for the invocation. The compat workspace remains the ambient workspace context. If explicit extra modules (`-m`) are present, the CWD module is suppressed instead.

#### Migration Equivalence

This guarantee must hold:

```
compat mode  ==  migrate in memory, then load
```

Explicit migration persists the same `CompatWorkspace` that runtime compat mode would have built in memory. This ensures that migrating a project does not change its runtime behavior.

#### API: `Workspace.migrate()`

Migration is engine-owned. The API returns one migration plan for the current workspace:

```graphql
extend type Workspace {
  migrate: WorkspaceMigration!
}

type WorkspaceMigration {
  changes: Changeset!
  steps: [WorkspaceMigrationStep!]!
}

type WorkspaceMigrationStep {
  code: String!           # stable identifier (e.g. "legacy-dagger-json")
  description: String!    # human-readable summary
  warnings: [String!]!    # non-fatal issues
  changes: Changeset!     # filesystem changes for this step
}
```

The engine guarantees:
- `changes` and all `steps` are based on the same pre-migration state
- `changes` is equivalent to merging the step changesets in order
- `steps = []` and an empty `changes` means "no migration needed"
- Warnings are informational and do not block application

Returning one plan is intentional. Future migration expansion happens inside `steps`, not by returning multiple top-level migrations.

#### CLI: `dagger migrate`

`dagger migrate` is a thin wrapper over `Workspace.migrate()`:

```
$ dagger migrate
Migrated to workspace format
WARNING: 2 migration gaps need manual review; see .dagger/migration-report.md

[changeset preview]

Apply changes? [y/N]
```

The CLI calls `Workspace.migrate()`, uses the combined `changes` as the source of truth, and reuses the standard changeset preview/apply flow. Human-readable progress and warnings are emitted by the engine during migration planning. With `-y`, changes are auto-applied.

#### What Gets Migrated

The migration handles three cases, which can co-occur in a single project:

**Project module** (triggered by `source != "."`): A "project module" is one where `dagger.json` sits at the project root with source code in a subdirectory (e.g., `source: ".dagger"`). In the new model, modules are self-contained — `dagger.json` lives alongside the source, and `source` is always `.`.

Steps:

1. **Move module source** to `.dagger/modules/<name>/`:
   ```
   .dagger/*                        →  .dagger/modules/<name>/
   dagger.json (project root)       →  .dagger/modules/<name>/dagger.json
   ```

2. **Update the moved `dagger.json`:**
   - Remove `source` field (now always `.`)
   - Remove `toolchains` field (migrated separately)
   - Rewrite `dependencies[].source` paths relative to new location
   - Rewrite `include` paths relative to new location

3. **Register in `.dagger/config.toml`** with `entrypoint = true` to preserve existing `dagger call <function>` commands.

**Toolchains** (triggered by `toolchains` field): Each toolchain becomes a workspace module entry. Source directories are left in place — only the configuration moves.

For each toolchain:

1. Add to `[modules]` in `config.toml`, with path relative to `.dagger/`.

2. Migrate customizations where possible:

   | Customization type | Action |
   |--------------------|--------|
   | Default value for constructor arg | Migrate to `config.*` |
   | `ignore`, `defaultPath`, or other non-value customization | Emit a warning and record it in `.dagger/migration-report.md` |
   | Customization targeting a non-constructor function | Emit a warning and record it in `.dagger/migration-report.md` |

3. Remove `toolchains` from the migrated `dagger.json`.

**User defaults**: `.env`-based constructor arg defaults are migrated to `config.*` entries in `config.toml`. The engine evaluates `${VAR}` expansion and `env://` references the same way in both formats. Non-constructor function arg defaults cannot be expressed in workspace config and produce warnings.

#### Real-World Examples

**dagger/dagger.io** — workspace ancestor pattern (no sdk, no source, toolchains only):

Before:
```json
{"name": "dagger.io", "engineVersion": "v0.19.8",
 "toolchains": [
   {"name": "api", "source": "api"},
   {"name": "dagger-cloud", "source": "cloud"}
 ]}
```

After `dagger migrate` — `.dagger/config.toml`:
```toml
[modules.api]
source = "../api"

[modules.dagger-cloud]
source = "../cloud"
```

The `dagger.json` is removed (it had no sdk, no source — it was purely config).

**dagger/dagger** — both triggers (source != ".", has toolchains):

Before:
```json
{"name": "dagger-dev", "sdk": {"source": "go"}, "source": ".dagger",
 "toolchains": [
   {"name": "go", "source": "toolchains/go", "customizations": [...]},
   {"name": "security", "source": "toolchains/security", "customizations": [...]},
   ...
 ],
 "dependencies": [...]}
```

After `dagger migrate` — `.dagger/config.toml`:
```toml
[modules.dagger-dev]
source = "modules/dagger-dev"
entrypoint = true  # preserves 'dagger call <function>' backwards compat

[modules.changelog]
source = "../toolchains/changelog"

[modules.ci]
source = "../toolchains/ci"

[modules.go]
source = "../toolchains/go"

[modules.security]
source = "../toolchains/security"
```

After `dagger migrate` — `.dagger/migration-report.md`:
```md
# Migration Report

## Module `go`

- Constructor arg `source` had `ignore` customization that cannot be expressed as a workspace config value.

## Module `security`

- Function `scanSource` had argument customization that could not be migrated because it does not target the constructor.
```

After `dagger migrate` — `.dagger/modules/dagger-dev/dagger.json`:
```json
{
  "name": "dagger-dev",
  "sdk": {"source": "go"},
  "dependencies": [
    {"name": "changelog", "source": "../../toolchains/changelog"},
    {"name": "docs", "source": "../../toolchains/docs-dev"},
    {"name": "helm", "source": "../../toolchains/helm-dev"}
  ]
}
```

## Modules

### What is a Module?

A **Dagger module** is a software package that extends Dagger with new functions and types. Modules are developed with a Dagger SDK and can call both the *system API* (containers, secrets, filesystem snapshots) and the *workspace API* (project files and configuration).

### Create a Module

#### Project-Specific Modules

For modules that are part of your project, `dagger module init` creates them inside the workspace and auto-registers them:

```bash
dagger module init --sdk=go ci
```

This:
1. Creates `.dagger/modules/ci/` with module source files
2. Auto-registers in `.dagger/config.toml`:
   ```toml
   [modules.ci]
   source = "modules/ci"
   ```
3. Module is immediately callable: `dagger call ci <function>`

**Directory structure:**

```
repo/
├── .dagger/
│   ├── config.toml
│   └── modules/
│       └── ci/
│           ├── dagger.json
│           └── main.go
└── src/
```

Edit the generated source files to add functions:

```go
// .dagger/modules/ci/main.go
func (m *Ci) Build(ctx context.Context) *dagger.Container {
    return dag.Container().From("golang:1.22").
        WithDirectory("/src", m.Source).
        WithWorkdir("/src").
        WithExec([]string{"go", "build", "./..."})
}
```

Changes take effect immediately — just run `dagger call ci build`.

When run without a path argument inside an initialized workspace, `dagger module init` defaults to `.dagger/modules/<name>/` and auto-registers the module.

#### Reusable Modules

For modules intended to be shared across projects, pass an explicit path:

```bash
dagger module init --sdk=go ./my-toolchain
```

This creates a standalone module at the specified location. It is **not** auto-registered in any workspace — it's a self-contained package meant to be published and installed elsewhere.

To test a standalone module during development, install it into a workspace:

```bash
dagger install ./my-toolchain
dagger call my-toolchain <function>
```

To share it, push to a git-accessible location. Others can then install it:

```bash
dagger install github.com/you/my-toolchain
```

To promote an existing project-specific module to a reusable one, move it from `.dagger/modules/foo/` to a git-accessible location and update the `source` path in `config.toml` accordingly.

### Workspace API

#### What Shipped (PR #11874)

The Workspace API was merged and released separately. Modules can already interact with their workspace via the `Workspace` type:

```graphql
type Workspace {
  directory(path: String!, include: [String!], exclude: [String!]): Directory!
  file(path: String!): File!
}
```

Key properties already in `main`:

- **Automatic injection**: When a function declares a `Workspace` argument, the engine injects the current workspace. No user action needed.
- **Always optional**: `Workspace` arguments are registered as optional in the schema, regardless of how they're declared in code.
- **Cannot be stored**: `Workspace` cannot be a field on module objects — it must be a function argument. This keeps workspace access visible in function signatures.
- **Content-addressed caching**: Functions with `Workspace` arguments use content-based cache keys. Same workspace content = cached result. Changed files = re-execution.

Example:

```go
func New(ws dagger.Workspace) *Ci {
    return &Ci{
        Source: ws.Directory(".", dagger.DirectoryOpts{
            Include: []string{"**/*.go", "go.mod", "go.sum"},
        }),
    }
}
```

#### What This Branch Adds

This branch refines the **path contract** for `Workspace.directory()` and `Workspace.file()`. Paths encode scope of intent via two styles:

- **Relative paths** (`.`, `src`, `./src`): resolve from the **workspace directory** (where `.dagger/` lives). This is the "within my jurisdiction" scope.
- **Absolute paths** (`/`, `/libs/shared`): resolve from the **workspace boundary** (typically the git root, with fallback to workspace directory). This is the "within the whole repo" scope.

The distinction matters for discovery operations. A Go toolchain scanning for modules:

```go
// "Find go modules in the user's workspace" — respects their jurisdiction
ws.Directory(".").Glob("**/go.mod")

// "Find go modules across the whole repo" — explicit wider scope
ws.Directory("/").Glob("**/go.mod")
```

A team with a workspace at `apps/frontend/` expects the first to find *their* Go modules, not every Go module across 50 teams. The path style makes the intent visible.

| Workspace at `repo/apps/frontend/` | Path | Resolves to | Scope |
|-------------------------------------|------|-------------|-------|
| Relative | `src` | `repo/apps/frontend/src` | Workspace |
| Relative | `.` | `repo/apps/frontend/` | Workspace |
| Absolute | `/libs/shared` | `repo/libs/shared` | Repo |
| Absolute | `/` | `repo/` | Repo |
| Traversal | `../backend` | **error** | — |

Edge cases degrade cleanly:
- No git root → absolute = relative (scope collapses to workspace directory)
- Workspace at git root → absolute = relative (same scope, no surprise)

**Convention for module developers:** default to relative paths. A well-behaved module respects the user's workspace scope. Use absolute paths only when you deliberately need repo-wide reach (CI modules, cross-project dependency scanners, etc.).

Metadata:
- `ws.path` — the workspace directory path relative to the workspace boundary
- `ws.address` — the canonical Dagger address of the workspace

### Changes to `dagger.json`

The workspace split clarifies what `dagger.json` is: purely a module definition file. It no longer carries project configuration. Key changes:

- **`source` is deprecated**: Modules are self-contained packages. `dagger.json` lives alongside the module source, so `source` is always `.` and no longer needs to be specified.
- **`sdk` is mandatory**: Every module must declare its SDK. The implicit detection from project context is removed.
- **`toolchains` is removed**: Toolchain configuration moves to workspace `config.toml` as module entries.
- **`blueprint` is removed**: The entrypoint concept moves to workspace `config.toml` as `entrypoint = true`.
- **Dependencies stay**: Module → module dependencies (`dependencies` field) remain in `dagger.json`. This is code dependency, not project configuration.
- **Pins stay**: Inline version pins in `dagger.json` remain. Workspace-level pinning uses the lockfile instead (see [Workspace Lockfile](#workspace-lockfile)).

### Module Dependency Model

A module depends on other modules as code dependencies: "my code calls this module." This is configured in `dagger.json` and is internal — it doesn't affect the workspace or CLI.

```json
{
  "name": "ci",
  "sdk": {"source": "go"},
  "dependencies": [
    {"name": "golang", "source": "github.com/dagger/go-toolchain@v1.0"}
  ]
}
```

This is distinct from workspace → module dependencies, which are project configuration (see [Workspace Dependency Model](#workspace-dependency-model)).

## CLI Changes

### New Commands

| Command | Purpose |
|---------|---------|
| `dagger workspace init` | Initialize a workspace (creates `.dagger/config.toml`) |
| `dagger workspace info` | Show workspace info |
| `dagger workspace config [key] [value]` | Read/write workspace config |
| `dagger module init --sdk=<sdk> <name>` | Create a new module |
| `dagger module dependency add` | Add a code dependency to a module's `dagger.json` |
| `dagger migrate` | Migrate legacy project to workspace format |
| `dagger lock update` | Refresh lockfile entries |
| `-W` / `--workspace` | Select workspace (local path or remote git ref) |

### Changed Commands

| Command | Before | After |
|---------|--------|-------|
| `dagger init` | Creates a module | Deprecated; use `dagger module init` |
| `dagger install` | Adds code dependency to a module | Adds a module to the workspace |
| `dagger call` | Loads module from `dagger.json` | Loads modules from workspace config |
| `dagger call -m` | Specifies the current module | Loads an explicit module, skips workspace modules |

### Removed Commands

| Command | Replacement |
|---------|-------------|
| `dagger toolchain install` | `dagger install` (toolchains are now regular workspace modules) |
| `dagger toolchain ...` | Subsumed by workspace module management |

## Known Gaps & Future Work

- Workspace grants/access-control policy is not fully implemented.
- Workspace `ignore` config is not fully wired through all loading paths.
- `.env` deprecation/migration work remains.
- Core lockfile coverage is incomplete outside current hooks (`container.from`, `modules.resolve`, `git.*`).
- Remote workspace lockfile read/write is not yet enabled.
- Environments feature is not yet implemented.
- `dagger.json` pin overlap with workspace lockfiles is unresolved.
