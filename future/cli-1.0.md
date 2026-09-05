# CLI 1.0: module-max SDK UX

Related issue: [dagger/dagger#13912](https://github.com/dagger/dagger/issues/13912)

Status: implementation handoff

## Purpose

This design gives module users one command group: `dagger module`.

The design also defines a new SDK-module interface. The interface is not a runtime interface.

The engine owns workspace state and command lifecycle. An SDK module owns deterministic file generation.

## Terms

Use these terms with the exact meanings in this table.

| Term | Meaning |
| --- | --- |
| Module | A Dagger API extension that users can install or develop. |
| Module client | Generated code that calls one target module. |
| SDK module | An installed module that generates module source or module clients. |
| Runtime | Engine code that generates runtime bindings and executes a module. |
| Scope | A persisted workspace path that identifies one SDK generation unit and sets its generation CWD. |
| Provider | The installed module that implements an SDK module. |

Do not call a runtime an SDK. Old engine code uses that name, but the concepts are different.

## Goals

1. Give module installation and module development one command group.
2. Keep SDK and scope metadata visible through a small `dagger sdk` command group.
3. Replace module dependencies with generated module clients.
4. Store all explicit generation inputs in `dagger.toml`.
5. Use the same generation path during initialization and later generation.
6. Let each SDK module select its project layout and module runtime.
7. Keep SDK-module code separate from legacy runtime code.

## Non-goals

1. Do not keep compatibility with the beta SDK-module interface.
2. Do not change the legacy runtime interface in this work.
3. Do not require the new code-facing client format in this work.
4. Do not add per-client SDK settings in this work.
5. Do not add a dedicated module dependency command.
6. Do not add a dedicated module engine command.
7. Do not let one SDK scope contain more than one module.
8. Do not hide a module source path as an SDK implementation detail.

## Outstanding work

The design in this document is locked unless an item below says that a decision is open. The branch does not yet implement all locked behavior.

### Final SDK-module interface

- [x] Replace `clientScope(ws)` with `findClientRoot(ws)` in the provider loader, engine, demo SDK, and tests.
- [x] Add optional `defaultModulePath(ws, name)` detection and validation.
- [x] Apply the scope and module-path lifecycles in this document.
- [x] Integration-test an SDK that implements `defaultModulePath`. Both in-tree SDKs omit it, so only the engine-default branch runs end to end.

### Workspace output validation

- [x] Remove the rule that rejects SDK changes outside the persisted scope.
- [x] Keep protection for the active engine-owned `dagger.toml`.
- [x] Keep generation-CWD and module-manifest validation.
- [x] Add a test in which an SDK updates a project file above the persisted scope.

### Update lifecycle decisions

- [x] `dagger workspace update` regenerates client scopes unless `--no-generate` is set.
- [x] `dagger module update` regenerates a client scope when it targets an updated module.

### Merge readiness

- [x] Update the PR description after the interface work is complete.
- [x] Refresh generated APIs and schema fixtures.
- [ ] Resolve all remaining CI failures.
- [x] Rebase on the current base branch.
- [x] Run the final focused and full validation sets.
- [x] Fix DCO and push the final branch.

## Command tree

The CLI must expose this command tree.

```text
dagger
├── setup
├── agent
├── check
├── generate
├── up
├── api
│
├── module (alias: mod)
│   ├── install <REF> [--name=NAME] [--here]
│   ├── uninstall <MODULE> [--here]
│   ├── list [--source=local|remote|all]
│   │        [--installed=true|false|all]
│   │        [--sdk=SDK]
│   ├── update [MODULE...]
│   ├── search [QUERY] [--sdk]
│   ├── settings [MODULE] [KEY] [VALUE...]
│   │            [--global] [--here] [--unset]
│   ├── init <SDK> [--name=NAME] [--path=PATH] [SDK SETTINGS]
│   └── client
│       ├── add <SDK> <MODULE> [SDK SETTINGS]
│       ├── update [MODULE...] [--all] [--sdk=SDK]
│       ├── rm <MODULE>
│       ├── list [--all] [--sdk=SDK]
│       └── scope --sdk=SDK
│
├── sdk
│   ├── list
│   └── scope
│       ├── list [--is-module=BOOL] [--name=NAME] [--sdk=SDK]
│       ├── is-module [--path=PATH] [-u] [BOOL]
│       ├── name [--path=PATH] [-u] [NAME]
│       └── sdk [--path=PATH] [-u] [SDK]
│
├── workspace (alias: ws)
│   ├── activity [--all]
│   ├── update [--no-generate]
│   ├── config [KEY] [VALUE] [--global] [--here] [--unset]
│   ├── config-file
│   ├── cwd
│   ├── remote
│   ├── remotes
│   └── root
│
├── cloud
├── llm
├── help
└── version
```

Do not add top-level aliases for `dagger module` subcommands.

### `dagger module install`

This command installs one module in the workspace.

The command accepts a local path, a module address, or another supported module reference.

The engine inspects the module interface. If the module implements the complete SDK-module interface, the engine also adds an `sdks.<SDK>` entry.

The engine derives the default SDK name from the installed module name.

The command replaces the old top-level `dagger install` command.

### `dagger module uninstall`

This command removes one installed module entry.

The command does not remove module source files.

The command replaces the old top-level `dagger uninstall` command.

### `dagger module list`

This command lists installed modules.

### `dagger module search`

This command searches registered modules and SDK modules. The `--sdk` flag filters the results to SDK modules.

### `dagger module update`

This command refreshes lock entries for installed modules only.

The command does not refresh client targets or runtime targets.

If an SDK client scope targets an updated module, the engine regenerates that scope. The generation uses the refreshed installed-module lock entries. It does not refresh other client-target entries.

### `dagger module client update`

This command updates the lock entries for module clients.

The command uses this form:

```console
dagger module client update [MODULE...] [--all] [--sdk=SDK]
```

If you give no argument, the command updates all client targets in the current scope. `MODULE` selects one client target or more. `--all` selects the clients in all scopes. `--sdk` selects the clients of one SDK module. The command `dagger module client list` uses the same selectors.

The engine reads the recorded ref of each selected client target. The engine finds the current version and writes the new pin to `dagger.lock`. A local target has no pin, and the engine does not read it again.

The engine resolves the selected targets into an empty lock, then merges the result. Therefore the command writes only the lock entries that the selected targets reach.

A new pin can change the API of the target module. Because of this, the engine calls `generateScope` for each scope that has a changed client. The command uses the same generation path, graph order, cycle rules, and atomic changeset as `dagger module client add`.

### `dagger workspace update`

This command refreshes all entries in `dagger.lock`.

The command refreshes installed modules, client targets, and runtime targets.

The command regenerates every SDK client scope. `--no-generate` updates only `dagger.lock`.

### Update commands compared

These three commands update different lock entries and SDK client scopes.

| Command | Installed modules | Client targets | Runtime targets | Generates again |
| --- | --- | --- | --- | --- |
| `dagger module update` | yes | shared refs only | shared refs only | Scopes that target an updated module |
| `dagger module client update` | shared refs only | yes | shared refs only | yes |
| `dagger workspace update` | yes | yes | yes | All client scopes, unless `--no-generate` is set |

A lock entry has a ref for its key. It does not have an owner. Two different entries in `dagger.toml` can therefore use one lock entry. If they do, an update of one entry also moves the other. "Shared refs only" in the table shows this effect.

`dagger module update` and `dagger module client update` do not read all lock entries. Each command resolves only its own selection into an empty lock, then merges the result. An entry that no selected item reaches stays unchanged.

The pin of a client sets the content of the generated code. Therefore, a command that can change an effective client pin also regenerates the affected scope. `dagger workspace update` can change every client pin, so it regenerates every client scope by default. The opt-out supports workflows that need only the lockfile change.

### `dagger module settings`

This command reads and writes settings for installed modules.

An SDK provider is a normal installed module. Its global settings use the same command.

For example:

```console
$ dagger module settings
MODULE          KEY                   VALUE  DESCRIPTION
go-sdk          legacy-module-compat  false  Generate the legacy module bundle
typescript-sdk  runtime               node   JavaScript runtime for generated code
```

### `dagger module init`

This command creates one local module scope.

The command uses this form:

```console
dagger module init <SDK> [--name=NAME] [--path=PATH] [SDK SETTINGS]
```

`--name` and `--path` are optional. The engine first resolves an explicit `--path`, if present. A relative path starts at `Workspace.cwd`. An absolute path starts at the workspace root.

The engine then resolves the name one time and stores it with the module scope. The engine uses these rules in order:

1. Use `--name` when it is set.
2. If `--path` is set, use the final directory name from the resolved path. For example, `--path=foo/bar/baz` uses `baz`.
3. If `--path` is not set and an active config file exists, use the name of the directory that contains the config file and append `-dev`.
4. If no valid config-parent name exists, use the local workspace root directory name and append `-dev`.
5. If no local workspace root name exists, use the remote Git repository name and append `-dev`.
6. If no valid name is available, report an error that tells the user to set `--name`.

The SDK is a required argument. The command does not call `findClientRoot`, and it reports an error when the named SDK is not installed.

The engine then resolves the final module path:

1. Use the resolved explicit `--path` when it is set. Do not call `defaultModulePath`.
2. Otherwise, call `defaultModulePath` when the selected SDK implements it.
3. Use a nonempty path returned by `defaultModulePath`.
4. Otherwise, use `<active-config-parent>/.dagger/modules/<name>`.
5. If no active config file exists, use `<workspace-root>/.dagger/modules/<name>`.

The engine normalizes and persists the module scope path.

The SDK-selected module path is not private SDK state. The engine uses it as the module scope and source path. Later generation uses the persisted path and does not call `defaultModulePath` again.

Without an explicit `--path`, the command also installs the local module. When
both `--name` and `--path` are absent, it installs the module as the workspace
entrypoint. If a different entrypoint exists, the command reports an error and
tells the user to pass `--name` to initialize a namespaced module. With an
explicit `--path`, the command records the SDK scope but does not install the
module.

The command accepts SDK-setting flags. The CLI gets these flags from provider constructor settings. The named SDK is the only source of these flags, so they carry no SDK prefix.

For example:

```console
dagger module init typescript --runtime=bun
```

### `dagger module client scope`

This command prints the client-generation scope for the current workspace location.

The required `--sdk` flag selects one installed SDK module. The engine uses the current-scope lookup for that SDK.

The command prints no output when the lookup finds no recorded or detected scope.

### `dagger module client add`

This command records and generates one module client.

The module argument accepts these values:

- A local module path.
- A module address.
- An installed module name.

The engine resolves the argument to a pinned `ModuleSource`.

The required SDK subcommand selects one installed SDK module. The engine uses the current-scope lookup for that SDK.

The command accepts the selected SDK's setting flags. These flags have no SDK prefix and update settings for the selected scope.

For example:

```console
dagger module client add go database
dagger module client add go github.com/acme/payments
```

### `dagger module client rm`

This command removes one client from the current scope.

The engine calls the scope generator with the remaining scope state. The SDK module removes obsolete generated files that it owns.

The command reports an error when the target is not unique in the current scope.

### `dagger module client list`

This command prints these columns:

```text
SCOPE  SDK  TARGET
```

Without `--all`, the command lists clients for the current scope.

With `--all`, the command lists clients for all scopes.

The `--sdk` flag filters the result by SDK name.

The command sorts rows by scope, SDK, and target.

### `dagger sdk list`

This command lists the SDKs recorded in the current `dagger.toml`.

The command prints these columns:

```text
SDK  SOURCE
```

`SOURCE` is the configured provider module source, including its pin when present.

The command sorts rows by SDK name.

### `dagger sdk scope list`

This command lists all recorded SDK scopes.

The command prints these columns:

```text
NAME  PATH  SDK  IS-MODULE
```

`--name` and `--sdk` select exact values. `--is-module` selects either module scopes or client-only scopes. The command sorts rows by path and SDK.

### `dagger sdk scope` fields

These commands read or edit one scope field:

```console
dagger sdk scope [--path=PATH] is-module [-u] [BOOL]
dagger sdk scope [--path=PATH] name [-u] [NAME]
dagger sdk scope [--path=PATH] sdk [-u] [SDK]
```

With no value, each command prints the current value. One value sets the field. `--unset` or `-u` removes the field.

Without `--path`, the command selects the most specific recorded scope that contains `Workspace.cwd`. With `--path`, it selects the scope at that exact path. A relative path starts at `Workspace.cwd`. An absolute path starts at the workspace root.

Setting `sdk` moves the complete scope record to the selected SDK. Unsetting `sdk` removes the complete scope record. Unsetting another field removes the scope record if no scope data remains.

These field commands edit `dagger.toml` only. They do not run generation. A later generation uses the new scope data.

SDK scope commands do not use an environment overlay. SDKs and scopes belong to the base workspace config.

### Removed commands

Remove these commands:

- The old `dagger sdk` lifecycle subcommands. The new group contains only `list` and `scope`.
- `dagger module sdk`.
- `dagger module deps` and all subcommands.
- `dagger module engine` and all subcommands.
- `dagger api client` and all subcommands.

## Workspace configuration

The workspace config keeps SDK data under the top-level `sdks` key.

```toml
[modules.go-sdk]
source = "dagger.io/go-sdk"

[modules.go-sdk.settings]
legacy-module-compat = false

[modules.typescript-sdk]
source = "dagger.io/typescript-sdk"

[modules.typescript-sdk.settings]
runtime = "node"

[sdks.go]
module = "go-sdk"

[sdks.go.scopes.".dagger/modules/payments"]
is-module = true
name = "payments"
clients = [
  "database",
  "github.com/acme/cache",
]

[sdks.go.scopes.".dagger/modules/payments".settings]
legacy-module-compat = true

[sdks.typescript]
module = "typescript-sdk"

[sdks.typescript.scopes."apps/web"]
clients = [
  "github.com/acme/payments",
]

[sdks.typescript.scopes."apps/web".settings]
runtime = "bun"
```

### SDK entry

`sdks.<SDK>.module` must name one installed module.

One installed module can provide only one SDK name in a workspace.

### Scope entry

The map key is the persisted scope path.

The path follows normal workspace path rules. A relative path starts at the directory that contains `dagger.toml`.

The engine normalizes scope paths before comparison.

`is-module = true` marks the scope as a module source.

An omitted `is-module` value is false.

`name` stores the scope name. It is required when `is-module = true`. The engine passes this name to every generation call for the scope.

One scope can contain a module, clients, or both.

The engine removes an empty scope entry.

### Client entry

Each string stores one user-facing target reference. The reference can be a local path, a module address, or an installed name.

One scope cannot contain the same target twice.

### Scope settings

Scope settings are constructor-setting overrides for the SDK provider.

The engine uses this precedence order:

```text
provider constructor defaults
→ effective [modules.<PROVIDER>.settings]
→ [sdks.<SDK>.scopes.<SCOPE>.settings]
```

The effective provider settings include the selected workspace environment and user settings.

The engine persists only explicit scope overrides.

The scope settings apply to the module and all clients in that scope.

This behavior can change existing generated files. The config and file changes appear in the same workspace diff.

Do not add per-client settings in this work.

## SDK-module interface

The engine must use this new interface for an installed SDK provider.

```graphql
interface Sdk {
  """
  Find the SDK client root that contains Workspace.cwd.

  Return a workspace-root-relative parent path of Workspace.cwd.
  Return "." for the workspace root.
  Return null when this SDK does not find a usable scope.
  """
  findClientRoot(
    """The input workspace."""
    ws: Workspace!
  ): String

  """
  Generate all module and client files for one SDK scope.

  Workspace.cwd is the persisted scope.
  clients is the complete desired client set.
  Return the modified workspace.
  """
  generateScope(
    """The input workspace."""
    ws: Workspace!

    """Whether this scope contains a module."""
    isModule: Boolean!

    """The persisted scope name."""
    name: String!

    """The engine-resolved client target module sources."""
    clients: [ModuleSource!]!
  ): Workspace!
}
```

An SDK module can also implement this optional function:

```graphql
"""
Return a custom default source path for a new module.

Return a workspace-root-relative path.
Return "" to use the engine default.
"""
defaultModulePath(
  """The command invocation workspace."""
  ws: Workspace!

  """The resolved module name."""
  name: String!
): String!
```

The engine must validate the complete names, arguments, and result types of required functions. When `defaultModulePath` exists, the engine must also validate its complete shape. An SDK module that does not implement the optional function still implements the interface.

The engine must not detect this interface from legacy runtime functions.

### `findClientRoot`

The engine calls `findClientRoot` with the command invocation Workspace.

The method can inspect files such as `go.mod`, `package.json`, or `pyproject.toml`.

The method returns null when it does not find a client root.

The engine validates the returned path. The path must contain the input `Workspace.cwd`.

The engine calls `findClientRoot` on a provider instance with global settings only. Scope settings are not available until the scope is known.

The method must not modify the workspace.

### Current-scope lookup

The lookup always applies to one selected SDK:

1. Find the deepest recorded scope for that SDK that contains `Workspace.cwd`.
2. Call that SDK's `findClientRoot` with the command invocation Workspace.
3. Return the deeper non-null path.

The engine uses this lookup in these cases:

| Command | Lookup | Use of returned path |
| --- | --- | --- |
| `module init` | None | Not applicable |
| `module client add SDK` | The selected SDK | Persist the client scope |
| `module client scope --sdk=SDK` | The selected SDK | Print the selected scope |
| `dagger generate` | None | Use persisted scopes |

The engine does not use current-scope lookup to initialize a module or regenerate a known scope.

### `defaultModulePath`

This function customizes only the default path for `module init`.

The engine calls it after it resolves the module name and selects the SDK provider. The engine calls it on a provider instance that includes global settings and explicit SDK-setting overrides from the init command.

The engine does not call the function when `--path` is set. An explicit path always wins.

The engine validates a nonempty result as a workspace-root-relative destination path. The result does not have to contain the command invocation CWD. It must not escape the Workspace.

An absent function or an empty result selects the engine default path.

The engine persists the selected path before generation. It never calls the function again for that module. An SDK update cannot move an existing module.

### `generateScope`

The engine persists the scope before it calls this method.

The engine creates a derived Workspace with `Workspace.cwd` set to the persisted scope.

The engine makes the scope directory available before the call. A new module scope can therefore inspect its current directory before it creates the first file.

The engine passes the complete scope state. `name` identifies the scope. `isModule` reports whether the scope contains a module. `clients` contains zero or more resolved `ModuleSource` values.

The module and client concepts remain separate. The interface does not require separate generation calls for them.

The SDK module can call `client.clientSchemaIntrospectionJSON` when code generation needs a target schema.

The persisted scope sets `Workspace.cwd`. It is not a write boundary. The method can update files anywhere in the Workspace. For example, a Go SDK can update a `go.mod` or `go.sum` above the persisted scope.

The SDK module must use `name`. It must not infer the scope name from `Workspace.cwd`.

When `isModule` is true, the result must contain a valid `dagger-module.toml` in `Workspace.cwd`. The engine does not create this file before the call.

The SDK module uses the existence of `dagger-module.toml` as the lifecycle marker:

- If the file does not exist, initialize the module and apply templates.
- If the file exists, regenerate the module and preserve existing state.

The SDK module selects the runtime and writes it to `dagger-module.toml`.

The SDK module can change the runtime when effective scope settings change.

During the code-facing transition, an SDK module can generate its legacy module bundle and its legacy standalone clients. A future version can generate separate module and client packages without a change to this interface.

The engine must parse and validate the output config.

The engine exposes one module manifest builder. An SDK constructs a manifest with structured values. The engine owns the file names, default values, schemas, and serialization.

```graphql
extend type Query {
  moduleManifest(
    loadTOML: FileID
    loadJSON: FileID
  ): ModuleManifest!
}

type ModuleManifest {
  withName(name: String!): ModuleManifest!
  withLegacyRuntimeDependency(source: String!, name: String, pin: String): ModuleManifest!
  withoutLegacyRuntimeDependency(name: String!): ModuleManifest!
  withoutLegacyRuntimeDependencies: ModuleManifest!

  withDangEntrypoint(source: String!): ModuleManifest!
  withModuleEntrypoint(source: String!): ModuleManifest!

  withLegacyGoRuntime(moduleSource: String, engineVersion: String): ModuleManifest!
  withLegacyDangRuntime(moduleSource: String, engineVersion: String): ModuleManifest!
  withLegacyPythonRuntime(moduleSource: String, engineVersion: String): ModuleManifest!
  withLegacyTypescriptRuntime(moduleSource: String, engineVersion: String): ModuleManifest!
  withLegacyPHPRuntime(moduleSource: String, engineVersion: String): ModuleManifest!
  withLegacyElixirRuntime(moduleSource: String, engineVersion: String): ModuleManifest!
  withLegacyJavaRuntime(moduleSource: String, engineVersion: String): ModuleManifest!
  withLegacyInclude(path: String!): ModuleManifest!
  withoutLegacyFields: ModuleManifest!

  validate(targetEngineVersion: String): Void!
  tomlFile: File!
  legacyJSONFile: File!
  directory: Directory!
}

extend type Workspace {
  withFile(
    path: String!
    source: File!
    permissions: Int
  ): Workspace!
}
```

`withDangEntrypoint` sets an embedded Dang entrypoint. `withModuleEntrypoint` sets a module entrypoint. If both functions are called, the last call replaces the earlier entrypoint.

The typed legacy runtime functions cover all module runtimes built into the engine: Go, Dang, Python, TypeScript, PHP, Elixir, and Java. Rust is a client SDK, not a module runtime. Each function sets the legacy runtime, module source, and engine version as one unit. A null module source omits `source`; the legacy loader then uses the manifest directory. A null engine version uses the running engine version. `withLegacyRuntimeDependency` adds a module to the schema used by the legacy runtime. `withLegacyInclude` is additive and keeps call order. `withoutLegacyFields` removes the runtime, module source, engine version, include paths, and runtime dependencies.

`tomlFile` returns `dagger-module.toml`. It contains the entrypoint and any legacy fallback fields. `legacyJSONFile` returns `dagger.json`. It contains only the legacy fields and returns an error if no legacy runtime is set.

`directory` always returns the applicable manifest file overlay. It contains `dagger-module.toml`. It also contains `dagger.json` when a legacy runtime is set.

The TOML schema has no explicit manifest version. An entrypoint selects the new loading path. A new engine uses the entrypoint and ignores the legacy fields. An engine that does not support entrypoints ignores the entrypoint and uses the legacy runtime. An invalid entrypoint is an error. The engine must not fall back to the legacy runtime after an entrypoint error.

The builder can generate entrypoint manifests before the engine supports loading them. Entrypoint loading is a separate change.

For example, a Dang SDK can generate the current manifest and its legacy fallback without TOML or JSON logic:

```dang
ws.withDirectory(
  ".",
  moduleManifest
    .withName(name: name)
    .withDangEntrypoint(source: "main.dang")
    .withLegacyDangRuntime
    .directory,
)
```

The SDK can leave an existing manifest unchanged during normal regeneration. Later manifest read and edit helpers can extend the same type without changing the SDK-module interface.

## SDK settings and CLI flags

Provider constructor arguments define SDK settings.

The engine must use the normal module-setting rules and supported setting types.

The CLI must flatten these settings into flags. The flag form follows how the command selects its SDK.

`dagger module init` names its SDK positionally. Only that SDK contributes flags, so the flags carry no prefix:

```text
dagger module init go --legacy-module-compat=false
dagger module init typescript --runtime=bun
```

`dagger module client add` selects its SDK as a subcommand. Only that SDK contributes flags, so the flags carry no prefix:

```text
dagger module client add go database --legacy-module-compat=false
dagger module client add typescript database --runtime=bun
```

`dagger module init --help` and `dagger module client add --help` must list one subcommand for each installed SDK. Each SDK subcommand's help must list that SDK's settings.

Help must work for local and remote workspaces selected with `-W`.

The CLI must reject a setting flag that does not belong to the selected SDK module.

The engine must persist explicit setting flags before generation. The engine must construct the provider with the effective scope settings.

## Synthetic generators

An SDK provider does not need to expose `@generate` functions.

The engine must expose one synthetic generator for each installed SDK provider:

```text
<PROVIDER>:generate
```

For example:

```console
$ dagger generate -l
go-sdk:generate          # Generate SDK-managed scopes
typescript-sdk:generate  # Generate SDK-managed scopes
```

The engine must ignore `@generate` functions from a module when that module runs in the SDK-provider role.

The initial generation call is a convenience. The call uses the same implementation as the synthetic generator.

## Scope selection and generation context

The command invocation CWD selects which persisted scopes to generate.

The persisted scope sets the CWD for each SDK call.

These two paths can be different.

Use this flow:

```text
command Workspace.cwd
→ select applicable persisted scopes
→ set a derived Workspace.cwd to one persisted scope
→ call the SDK module
```

Initial generation and later generation therefore use the same generation CWD.

## Generation graph

The engine must build a dependency graph of SDK scopes.

Each generated scope has one node.

Each local client target creates an edge to its module scope.

A scope depends on each local module scope that its clients target.

The engine calls `generateScope` once for each node. The call receives the complete module and client state for that scope.

Each call receives the Workspace from its prior dependency.

The engine can run independent branches in parallel.

The engine must fold branch results in deterministic config order.

The engine must detect and report local generation cycles.

This graph replaces `ModuleSource.generateLocalDependencies` and its staged-workspace state. The engine must remove that beta API and implementation. The SDK-managed client graph is the only local generation orchestrator.

## Lifecycle

The design separates state changes from file generation.

### State-changing commands

The engine owns these commands:

- `module install`
- `module uninstall`
- `module update`
- `module settings`
- `module init`
- `module client add`
- `module client update`
- `module client rm`
- `workspace update`

These commands change workspace intent, lock state, or both.

### Idempotent generation

The SDK module owns the `generateScope` operation.

For the same Workspace, scope state, provider version, and effective settings, the operation must return the same result.

The engine can call an operation during a state-changing command. The engine can also call the same operation during `dagger generate`.

### Module initialization flow

The engine must use one atomic workspace change:

1. Resolve an explicit `--path`, if present.
2. Resolve the module name.
3. Select the SDK provider named by the required SDK argument.
4. Decode explicit SDK-setting overrides.
5. If `--path` is absent, call optional `defaultModulePath` on the selected provider.
6. Use a nonempty SDK result. Otherwise, use the engine default path.
7. Validate and stage the scope path, resolved name, and explicit scope settings in `dagger.toml`. When the name and path are both inferred, install the module as the workspace entrypoint.
8. Construct the provider with effective scope settings.
9. Set a derived Workspace CWD to the module scope.
10. Run the scope generator with the complete scope state.
11. Validate the result and `dagger-module.toml`.
12. Export the complete workspace change.

The SDK module uses the presence of `dagger-module.toml` to select initialization or regeneration behavior.

### Client-add flow

The engine must use one atomic workspace change:

1. Resolve the target reference and lock pin.
2. Select the SDK provider.
3. Resolve the current scope for the selected SDK.
4. Persist that scope.
5. Stage the client entry in `dagger.toml`.
6. Stage explicit scope settings.
7. Construct the provider with effective scope settings.
8. Set a derived Workspace CWD to the persisted scope.
9. Run the scope generator with the complete scope state.
10. Validate and export the complete workspace change.

### Generate flow

`dagger generate` must use this flow:

1. Read all applicable persisted scopes.
2. Build the generation graph.
3. Construct one provider instance for each effective SDK and scope settings pair.
4. Set each generation Workspace CWD to its persisted scope.
5. Run scope generation in graph order.
6. Validate each SDK result.
7. Export the combined workspace change.

The engine does not call `findClientRoot` or `defaultModulePath` during this flow.

### Client removal flow

`dagger module client rm` removes the client from workspace state. The engine then calls `generateScope` with the remaining state.

The SDK module owns its generated files. It must remove obsolete files that it can identify.

## Responsibility split

| Responsibility | Owner |
| --- | --- |
| CLI command grammar | CLI |
| Dynamic help and SDK setting flags | CLI |
| SDK provider selection | Engine |
| Scope records | Engine |
| Client records | Engine |
| Lockfile pins | Engine |
| Settings persistence and merge | Engine |
| Target reference resolution | Engine |
| Synthetic generator discovery | Engine |
| Generation graph and order | Engine |
| Atomic workspace export | Engine |
| Scope detection | SDK module |
| Optional default module path | SDK module |
| Project layout | SDK module |
| Module templates | SDK module |
| Module runtime selection | SDK module |
| `dagger-module.toml` creation | SDK module |
| Complete scope generation | SDK module |

The engine must reject an SDK result that modifies `dagger.toml`.

The engine must not reject an SDK result only because it modifies a file outside the persisted scope. The Workspace is the output boundary.

The engine must reject an SDK result that changes the generation CWD.

When a scope contains a module, the engine must validate the generated `dagger-module.toml` at the persisted module path.

## Runtime separation

Three concepts exist during this change.

1. The legacy engine SDK is a runtime and code generator.
2. The beta SDK module is the interface that this design replaces.
3. The new SDK module implements the interface in this document.

The engine must not combine these concepts in one interface.

The new SDK-module loader must not use the legacy `core.SDK` capability bucket.

The runtime loader continues to read runtime data from `dagger-module.toml`.

The new SDK-module loader reads provider data from `dagger.toml`.

The two loaders can share low-level module-loading code. They must not share capability detection or lifecycle state.

Remove these beta interfaces without a compatibility adapter:

- SDK-owned `@generate` discovery
- `currentModule.asSDK`, when no remaining caller needs it
- `ModuleSource.generateLocalDependencies`
- `Workspace.__withGeneratedLocalDependencies`
- staged local-dependency generation state on `Workspace`

`initModule`, `initClient` and `targetRuntime` stay. They belong to the legacy runtime interface, which this work does not change. See non-goal 2.

Gate all new public GraphQL fields and types with the v1 schema view.

## Error rules

The engine must report these errors before export:

- The SDK provider is not installed.
- The provider does not implement the complete SDK-module interface.
- Scope selection is ambiguous.
- `findClientRoot` returns an invalid path.
- `defaultModulePath` returns an invalid path.
- A target reference cannot resolve.
- A local generation graph has a cycle.
- A provider returns an invalid Workspace.
- A provider changes `dagger.toml`.
- A provider changes the generation CWD.
- Module generation does not produce a valid `dagger-module.toml`.
- A setting flag does not belong to the selected SDK module.

All state-changing commands must be atomic. A failed SDK call must leave the host workspace unchanged.

## Implementation status

The branch implements the locked design, including update-time client generation and the unified module-manifest builder. Generated SDK clients and schema fixtures include the current schema.

The [Outstanding work](#outstanding-work) section is the source of truth for incomplete work.

## Acceptance examples

### Initialize a Go module

```console
dagger module install dagger.io/go-sdk
dagger module init go
git diff -- dagger.toml .
```

The diff must include one valid generated module. The persisted module path must be the nonempty `defaultModulePath` result, or `.dagger/modules/<inferred-name>` when the optional function is absent or returns an empty string.

### Initialize a TypeScript module with a scope setting

```console
dagger module install dagger.io/typescript-sdk
dagger module init typescript --runtime=bun
```

The scope settings must contain `runtime = "bun"`. The generated module config must select the Bun runtime.

### Add and regenerate a client

```console
cd services/payments/cmd/server
dagger module client add go database
cd ../../..
dagger generate
```

Both commands must generate the client from the same persisted scope.

### Generate a local dependency graph

If module A has a client for local module B, the engine must generate B before the client in A.

The engine must report a clear error for a local cycle.

## Handoff result

The completed implementation must include:

- The command tree in this document.
- The scope-based workspace schema.
- The new SDK-module interface.
- One synthetic scope generator for each SDK provider.
- Graph-based local generation.
- An in-tree demo Go SDK module.
- Focused unit and integration tests.
- A successful engine-playground demonstration.
