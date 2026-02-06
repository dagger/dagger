# Part 1: Module vs. Workspace

## Table of Contents

- [Problem](#problem)
- [Core Concepts](#core-concepts)
  - [What is a Workspace?](#what-is-a-workspace)
  - [What is a Module?](#what-is-a-module)
  - [Comparison](#comparison)
  - [Dependency Model](#dependency-model)
- [Configure a Workspace](#configure-a-workspace)
  - [How Dagger Loads Configuration](#how-dagger-loads-configuration)
  - [Adding Modules](#adding-modules)
  - [Configuration Reference](#configuration-reference)
- [Develop a Module](#develop-a-module)
  - [Create a Module](#create-a-module)
  - [Write Module Code](#write-module-code)
  - [Share a Module](#share-a-module)
- [Migration](#migration)
  - [No Runtime Compat Mode](#no-runtime-compat-mode)
  - [Detection Triggers](#detection-triggers)
  - [dagger migrate](#dagger-migrate)
    - [Project Module Migration](#project-module-migration)
    - [Toolchain Migration](#toolchain-migration)
  - [Command Changes](#command-changes)
  - [Real-World Examples](#real-world-examples)
- [Status](#status)

## Problem

Dagger currently conflates two distinct concepts in a single `dagger.json` file:
- Project configuration (what tools to use, how to configure them)
- Module definition (what code to package, what dependencies it needs)

This causes confusion about what files are accessible, how dependencies work, and what appears in the CLI.

## Core Concepts

This proposal establishes **workspaces** and **modules** as two fundamentally different things, each with their own configuration file and dependency model.

### What is a Workspace?

A **workspace** is a directory used as context for configuring and using Dagger. Typically it is a git repository, or a subdirectory within a larger repo.

The main way to configure a Dagger workspace is to add modules to it, possibly with custom workspace-level configuration.

### What is a Module?

A **Dagger module** is software packaged for the Dagger platform (engine & SDK). It implements functions and types to extend the capabilities of a Dagger workspace: typically with new ways to build, check, generate, deploy, or publish artifacts within the workspace.

Modules can access Dagger's powerful API to orchestrate system primitives (containers, secrets, etc), as well as interact with the contents of the workspace. This allows deep integration with the project's existing tools by parsing their configuration files and adapting behavior - the best of both worlds between native integration and cross-platform repeatability.

### Comparison

| | Workspace | Module |
|-|-----------|--------|
| **What it is** | A directory (project context) | Packaged software |
| **Configured via** | `.dagger/config.toml` | `dagger.json` |
| **Contains** | Modules added to it | Functions and types |
| **Purpose** | Configure Dagger for this project | Extend Dagger's capabilities |
| **Analogy** | A VS Code workspace | A VS Code extension |

Modules are added *to* workspaces. Once added, a module can access and interact with the workspace's contents.

### Dependency Model

There are two distinct ways to depend on a module:

| Relationship | Meaning | Configured in | Example |
|--------------|---------|---------------|---------|
| **Workspace → Module** | "Use this module in my project" | `.dagger/config.toml` | Adding go-toolchain to build your Go code |
| **Module → Module** | "My code calls this module" | `dagger.json` | Importing a helper library in your module's source |

These serve different purposes:

- **Workspace → Module** is project configuration. The module gains access to your workspace and extends your CLI. This is how you set up your development environment.

- **Module → Module** is code dependency. One module's implementation calls another module's functions. The dependency is internal - it doesn't affect the workspace or CLI.

A module added to a workspace is *not* the same as a module dependency. They live in different config files, are installed with different commands, and have different effects.

## Configure a Workspace

### How Dagger Loads Configuration

When the CLI starts:

1. **Find workspace**: Walk up from current directory looking for a `.dagger/` directory. The directory containing `.dagger/` is the **workspace root**.

   - If not found: workspace is empty (no modules, no aliases). CLI still works — `dagger call -m` and core commands are available.
   - If a `dagger.json` with legacy triggers is found instead: fail with migration error (see [No Runtime Compat Mode](#no-runtime-compat-mode)).

2. **Parse config**: Read `.dagger/config.toml` if it exists. If `.dagger/` exists but has no `config.toml`, the workspace is valid but empty. Resolve all paths relative to the `.dagger/` directory.

3. **Load modules**: For each entry in `[modules]`:
   - Resolve the `source` to a module (local path or git reference)
   - Apply `config.*` values as constructor argument defaults
   - The module's name in the workspace is the table key (e.g., `go` from `[modules.go]`)

4. **Register aliases**: For each entry in `[aliases]`, register the array-encoded path as a top-level function alias.

5. **Build schema**: Each module's constructor is mounted as a top-level function in the GraphQL schema, keyed by its workspace name. Aliases add additional top-level entry points that resolve to the specified path.

6. **Serve**: The CLI dispatches `dagger call <name> <function>` by looking up `<name>` in the schema — either a module constructor or an alias.

The CLI always operates in workspace context. There is no "module context" at the CLI level.

### Adding Modules

```bash
# Add a module to the workspace
dagger install github.com/dagger/go-toolchain
```

The module is registered under its `name` from dagger.json. Override with `--name`:

```bash
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

### Configuration Reference

**Config file:** `.dagger/config.toml` (human-editable)

```toml
# Paths to ignore during workspace operations (extends .gitignore)
# ignore = ["docs/**", "marketing/**"]

# Modules added to this workspace
[modules.ci]
source = "modules/ci"

[modules.node]
source = "github.com/dagger/node-toolchain@v1.0"

[modules.go]
source = "github.com/dagger/go-toolchain@v1.0"
config.goVersion = "1.22"
config.lintStrict = true
```

Each module entry is a table with a required `source` and optional `config.*` keys. The table key (e.g., `go`) is the module's local name in this workspace — it determines the CLI namespace (`dagger call go build`) and is used by aliases and other references.

Paths are relative to the `.dagger/` directory.

#### Module Configuration

The `config.*` keys set default values for the module's constructor arguments:

```toml
[modules.go]
source = "github.com/dagger/go-toolchain@v1.0"
config.goVersion = "1.22"
config.lintStrict = true
config.tags = ["integration", "unit"]
```

The config keys map directly to constructor argument names. Supported types: strings, booleans, numbers, arrays.

This replaces `customizations` in `dagger.json`. Only constructor arguments can be configured - this keeps the config surface simple and encourages module authors to expose important settings as constructor parameters.

Config values are evaluated by the engine the same way as `.env` defaults — variable expansion (`${SYSTEM_VAR}`), `env://` references for secrets, and file/directory path resolution all work:

```toml
[modules.go]
source = "github.com/dagger/go-toolchain@v1.0"
config.goVersion = "1.22"
config.cacheDir = "${HOME}/.cache/go"
config.apiKey = "env://GO_API_KEY"
```

This replaces `.env`-based user defaults for constructor arguments. The `.env` mechanism for setting constructor arg defaults is **deprecated** — use `config.*` in workspace config instead. The engine provides the same evaluation behavior regardless of whether values come from `.env` or `config.toml`.

`.env` files remain supported for non-constructor function argument defaults, which cannot be expressed in workspace config.

#### Aliases

The `[aliases]` section creates top-level shortcuts for functions on workspace modules:

```toml
[aliases]
# 'dagger call deploy' resolves to 'dagger call k8s deploy'
deploy = ["k8s", "deploy"]

# 'dagger call test' resolves to 'dagger call go test'
test = ["go", "test"]

# Deeper chains are supported
scan = ["security", "source", "scan"]
```

Each alias maps a top-level name to an array-encoded path: the module name followed by one or more function/field names. This is the same chaining as `dagger call`, just pre-configured.

Aliases are a general-purpose feature (not migration-only). They're useful for project-level shortcuts and for preserving backwards compatibility during migration.

#### Environments

Module config and workspace settings can be overridden per environment (e.g., CI, staging, production). Environments are selected explicitly via `--env`:

```bash
dagger check --env=ci
```

See Part N: Environments (coming soon) for the full design.

#### Workspace Ignore

Top-level `ignore` defines paths to skip during all workspace operations (glob, search, file access). This extends `.gitignore` for tracked-but-irrelevant parts of the repo:

```toml
ignore = ["docs/**", "marketing/**", "data-science/**"]
```

The engine already respects `.gitignore` by default. The `ignore` key covers paths that are tracked in git but irrelevant to Dagger - useful in large monorepos to speed up module discovery.

For scoping which artifacts to operate on (rather than which files to see), use artifact path filtering. See [Part 3: Artifacts](https://gist.github.com/shykes/aa852c54cf25c4da622f64189924de99):

```bash
dagger check --path='./myapp'
```

**Lock file:** `.dagger/lock` (machine-managed)

```
[["version", "1"]]
["modules", "resolve", ["github.com/dagger/go-toolchain@v1.0"], "abc123..."]
["core", "git.ref", ["https://github.com/dagger/go-toolchain", "v1.0"], "abc123..."]
```

The lock file pins module versions to exact commits and caches runtime lookups (git refs, container digests, HTTP content). Modules can store their own lookups under their namespace. See [PR #11156](https://github.com/dagger/dagger/pull/11156) for the lockfile design.

## Develop a Module

Most projects eventually need custom logic that doesn't fit existing modules - custom build steps, project-specific CI/CD, glue code combining multiple tools. This is when you create a module.

### Create a Module

```bash
# From anywhere in the workspace
dagger module init --sdk=go ci
```

This:
1. Creates `.dagger/modules/ci/` with module source
2. Auto-installs in `.dagger/config.toml`:
   ```toml
   [modules.ci]
   source = "modules/ci"
   ```
3. Module is immediately callable: `dagger call ci <function>`

The `--sdk` flag is required. Options: `go`, `python`, `typescript`, `php`, or a custom SDK module reference.

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

### Write Module Code

Your module code lives in `.dagger/modules/<name>/`. Edit the generated source files to add functions:

```go
// .dagger/modules/ci/main.go
func (m *Ci) Build(ctx context.Context) *dagger.Container {
    // ...
}
```

Changes take effect immediately - just run `dagger call ci build`.

### Share a Module

To make a module installable by other projects:

**Option 1: Promote an existing module**

Move it from `.dagger/modules/foo/` to a git-accessible location (dedicated repo, monorepo subdirectory, etc.).

**Option 2: Start standalone**

Run `dagger module init` outside any workspace:

```bash
mkdir my-module && cd my-module
dagger module init --sdk=go my-module
```

When no workspace is found, the module is created in the current directory.

**Either way**, others can then install it:
```bash
dagger install github.com/you/my-module
```

To test a standalone module during development, create a workspace:
```bash
dagger install .
dagger call my-module <function>
```

## Migration

This section covers the transition from the current model. The design principle is: **no runtime compat mode**. The engine has exactly one loading path (workspace config). All backwards compatibility is handled by `dagger migrate`, which transforms legacy configurations into the new format.

### No Runtime Compat Mode

When the engine encounters a `dagger.json` with legacy triggers and no `.dagger/config.toml`, it fails with a clear message:

```
Error: this project uses a legacy module format.
Run 'dagger migrate' to update your project.
```

The engine never interprets legacy `dagger.json` at runtime. This keeps the loading path clean — one format, one behavior, no branching.

### Detection Triggers

Two independent signals in `dagger.json` indicate a legacy project needing migration. They are not mutually exclusive — a single file can match both:

| Trigger | What it signals | Example |
|---------|----------------|---------|
| `source != "."` | Legacy "project module" — a module that doubles as project config | `"source": ".dagger"` |
| Has `toolchains` | Toolchains that should become workspace modules | `"toolchains": [...]` |

Modules that match neither trigger (pure modules with `source == "."` or absent, no toolchains) are **not legacy**. They get a clean break: call them via `dagger call -m .` or install them in a workspace with `dagger install .`.

### dagger migrate

The `dagger migrate` command detects legacy triggers and transforms the project. It handles two cases: project module migration and toolchain migration.

#### Implementation

`dagger migrate` is implemented as a standalone Dagger module. It receives the project source via `+defaultPath`, performs the migration logic using Dagger's own APIs (module introspection for enumerating constructor args and functions, user defaults discovery, etc.), and returns a `Changeset`. The CLI's built-in Changeset handling shows the user a diff preview and prompts to apply.

```bash
# Run migration (shows diff, prompts to apply)
dagger call -m github.com/dagger/migrate migrate

# Or install as a toolchain first
dagger toolchain install github.com/dagger/migrate
dagger call migrate migrate
```

This keeps migration logic out of the engine, makes it independently testable, and dogfoods the module and Changeset APIs.

#### Project Module Migration

Triggered by: `source != "."` in `dagger.json`.

A "project module" is one where `dagger.json` sits at the project root with source code in a subdirectory (e.g., `source: ".dagger"`). In the new model, modules are self-contained packages — `dagger.json` lives alongside the source, and `source` is always `.`.

**Steps:**

1. **Move module source** to `.dagger/modules/<name>/`:
   ```
   .dagger/*                →  .dagger/modules/<name>/
   dagger.json (project root)  →  .dagger/modules/<name>/dagger.json
   ```

2. **Update the moved `dagger.json`:**
   - Remove `source` field (now always `.`)
   - Remove `toolchains` field (migrated separately, see below)
   - Rewrite `dependencies[].source` paths relative to new location
   - Rewrite `include` paths relative to new location

3. **Create `.dagger/config.toml`:**
   - Add module under `[modules]`
   - Enumerate the module's constructor arguments and write each as a commented-out `[modules.<name>.config]` entry (with type-appropriate example values)
   - Load the module and enumerate its top-level functions and fields. For each, add an `[aliases]` entry preserving backwards compat with `dagger call <function>`

**Example** — migrating `dagger/dagger` (name: `dagger-dev`, source: `.dagger`):

`.dagger/config.toml` (generated):
```toml
[modules.dagger-dev]
source = "modules/dagger-dev"
# Constructor arguments (uncomment to configure):
# config.someArg = "value"

[aliases]
# Migrated from project module "dagger-dev".
# These preserve 'dagger call <function>' backwards compat.
# Remove as you update scripts to 'dagger call dagger-dev <function>'.
check-generated = ["dagger-dev", "check-generated"]
generate = ["dagger-dev", "generate"]
```

`.dagger/modules/dagger-dev/dagger.json` (moved and updated):
```json
{
  "name": "dagger-dev",
  "sdk": {"source": "go"},
  "dependencies": [
    {"name": "changelog", "source": "../../toolchains/changelog"},
    {"name": "docs", "source": "../../toolchains/docs-dev"},
    ...
  ]
}
```

#### Toolchain Migration

Triggered by: `toolchains` field present in `dagger.json`.

Each toolchain becomes a workspace module. Toolchain source directories are left in place — only the configuration moves.

**Steps:**

For each toolchain entry:

1. **Add to `[modules]`** in `.dagger/config.toml`, with path relative to `.dagger/`:
   ```toml
   [modules.go]
   source = "../toolchains/go"

   [modules.ci]
   source = "../toolchains/ci"
   ```

2. **Migrate customizations** (if any):

   | Customization type | Action |
   |--------------------|--------|
   | Default value for constructor arg | Migrate to `[modules.<name>.config]` |
   | `ignore`, `defaultPath`, or other non-value customization for constructor arg | Add warning comment with original value |
   | Customization targeting a non-constructor function | Add warning comment with original value (cannot be migrated) |

   Example — `go` toolchain with constructor-level `ignore` customization:
   ```toml
   [modules.go]
   source = "../toolchains/go"
   # WARNING: constructor arg 'source' had 'ignore' customization that cannot
   # be expressed as a config value. Original:
   # {"argument":"source","ignore":["bin",".git","**/node_modules",...]}
   ```

   Example — `security` toolchain with function-level customization:
   ```toml
   [modules.security]
   source = "../toolchains/security"
   # WARNING: customization for function 'scanSource' could not be migrated
   # (non-constructor). Original:
   # {"function":["scanSource"],"argument":"source","ignore":["bin",".git","docs",...]}
   ```

3. **Remove `toolchains`** field from the migrated `dagger.json`.

#### User Defaults Migration

For each module (project module and toolchains), `dagger migrate` uses the Dagger API's existing user defaults introspection to discover `.env`-based defaults.

For each discovered default:

| Default type | Action |
|-------------|--------|
| Constructor arg with simple value | Migrate to `config.*` in `config.toml` |
| Constructor arg with variable expansion | Migrate as-is — engine evaluates `${VAR}` the same way in `config.toml` |
| Constructor arg with `env://` reference | Migrate as-is — engine handles `env://` the same way in `config.toml` |
| Non-constructor function arg | Add warning comment (cannot be expressed in workspace config) |

Example — `.env` before migration:
```bash
GO_VERSION=1.22
GO_CGO=true
GO_CACHE_DIR=${HOME}/.cache/go
SECURITY_API_KEY=env://SECURITY_KEY
```

After migration in `.dagger/config.toml`:
```toml
[modules.go]
source = "../toolchains/go"
config.version = "1.22"
config.cgo = true
config.cacheDir = "${HOME}/.cache/go"

[modules.security]
source = "../toolchains/security"
config.apiKey = "env://SECURITY_KEY"
```

The migrated `.env` entries should be removed or commented out to avoid duplicate defaults. Note: the Dagger API's user defaults introspection may not track the source file path of each default — if so, `dagger migrate` should print which `.env` entries to remove manually.

### Command Changes

| Command | Current | New |
|---------|---------|-----|
| `dagger init` | Creates a module | Deprecated; use `dagger module init` |
| `dagger call` in module dirs | Loads module from `dagger.json` | Only reads workspace config (`.dagger/config.toml`) |
| `dagger call -m` | Specifies current module | Escape hatch: loads module in isolation, no workspace context |
| `dagger install` | Adds code dependency to module | Adds module to workspace |
| `dagger module dependency add` | (new) | Adds code dependency to a module's `dagger.json` |
| `dagger migrate` | (new) | Migrates legacy project to workspace format |

### Real-World Examples

#### dagger/dagger.io

**Before** — workspace ancestor pattern (no sdk, no source, toolchains only):
```json
{"name": "dagger.io", "engineVersion": "v0.19.8",
 "toolchains": [
   {"name": "api", "source": "api"},
   {"name": "dagger-cloud", "source": "cloud"}
 ]}
```

**After** `dagger migrate`:

`.dagger/config.toml`:
```toml
[modules.api]
source = "../api"

[modules.dagger-cloud]
source = "../cloud"
```

The `dagger.json` is removed (it had no sdk, no source — it was purely config).

#### dagger/dagger

**Before** — both triggers (source != ".", has toolchains):
```json
{"name": "dagger-dev", "sdk": {"source": "go"}, "source": ".dagger",
 "toolchains": [
   {"name": "go", "source": "toolchains/go", "customizations": [...]},
   {"name": "security", "source": "toolchains/security", "customizations": [...]},
   ... (17 more)
 ],
 "dependencies": [...]}
```

**After** `dagger migrate`:

`.dagger/config.toml`:
```toml
[modules.dagger-dev]
source = "modules/dagger-dev"

[modules.changelog]
source = "../toolchains/changelog"

[modules.ci]
source = "../toolchains/ci"

[modules.cli]
source = "../toolchains/cli-dev"

[modules.docs]
source = "../toolchains/docs-dev"

# ... (15 more toolchains)

[modules.go]
source = "../toolchains/go"
# WARNING: constructor arg 'source' had 'ignore' customization that cannot
# be expressed as a config value. Original:
# {"argument":"source","ignore":["bin",".git","**/node_modules",...]}

[modules.security]
source = "../toolchains/security"
# WARNING: customization for function 'scanSource' could not be migrated
# (non-constructor). Original:
# {"function":["scanSource"],"argument":"source","ignore":["bin",".git","docs",...]}

[aliases]
# Migrated from project module "dagger-dev".
check-generated = ["dagger-dev", "check-generated"]
generate = ["dagger-dev", "generate"]
```

`.dagger/modules/dagger-dev/dagger.json`:
```json
{
  "name": "dagger-dev",
  "sdk": {"source": "go"},
  "dependencies": [
    {"name": "changelog", "source": "../../toolchains/changelog"},
    {"name": "docs", "source": "../../toolchains/docs-dev"},
    {"name": "helm", "source": "../../toolchains/helm-dev"},
    {"name": "sdks", "source": "../../toolchains/all-sdks"},
    {"name": "engine-dev", "source": "../../toolchains/engine-dev"},
    {"name": "cli", "source": "../../toolchains/cli-dev"}
  ]
}
```

## Status

Design phase. Depends on team alignment on the workspace/module distinction.

---

Next: [Part 2: Workspace API](https://gist.github.com/shykes/86c05de3921675944087cb0849e1a3be)
