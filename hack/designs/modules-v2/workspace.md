# Workspace

## Status: API + plumbing shipped; configuration in progress

## Summary

Collapses three shipped/in-progress components:
- **Workspace API** (done) — `dagger.Workspace` type, `workspace.directory()` /
  `workspace.file()`, workspace-aware module constructors.
- **Workspace plumbing** (done) — engine-side workspace binding, module loading
  moved from CLI to engine.
- **Workspace configuration** (in progress) — `.dagger/config.toml` with module
  entries, ignore patterns, blueprint references.

FIXME: Workspace API and configuration details to be written up. The binding
and access control model below is the most detailed section.

## Core Workspace Types

### `WorkspacePath`

`WorkspacePath` is the canonical path type for paths inside a bound workspace.

Canonical form:

- workspace-root-relative
- `/` separators
- no leading `./`
- no `..` segments
- no trailing `/`, except `/` itself for workspace root

Examples:

- `docs`
- `docs/intro.md`
- `/`

CLI input may use local path syntax such as `./docs`. The CLI resolves that
path relative to the client's current working directory, then converts it to
canonical workspace-root-relative `WorkspacePath`.

## Workspace Binding and Access Control

A client has:

1. A **workspace binding**: which workspace it is operating in.
2. A set of **workspace grants**: what it is allowed to do through the workspace API.

Workspace binding and lockfile are always coupled: a client's `currentWorkspace`
and lookup lock state (`.dagger/lock`) come from the same bound workspace.

Default binding is intentionally asymmetric:

1. Module clients inherit workspace binding.
2. Non-module clients bind a fresh workspace from their own `.`.

Lockfile behavior is engine-internal and is **not** governed by workspace grants.

Grants apply to **ambient workspace access** only (auto-injected workspace and
`dag.CurrentWorkspace()` style access). Explicitly passed `Workspace` arguments
are treated as deliberate delegation from the caller.

### Binding Rules

Connect parameter:

- `workspace` (optional string): explicitly declare workspace binding for the
  connecting client.
- Unset means "use default binding rules"; it is semantically different from a
  set value.

Rules:

1. If `workspace` is set: bind to that declared workspace.
2. Else if the client is a module client: inherit nearest ancestor workspace
   binding.
3. Else: bind from workspace detection in the connecting client's own `.`.

Notes:

- Top-level clients are non-module clients, so rule 3 applies when `workspace`
  is unset.
- Nested non-module clients also use rule 3 by default.
- Engine infers local-vs-remote workspace resolution from `workspace` when set.

### CLI Workspace Target UX

Top-level CLI commands that support explicit workspace selection should use an
optional positional workspace target, not a dedicated workspace flag:

- `dagger <cmd> [workspace --] [command args...]`

Rules:

1. If `workspace --` prefix is present, CLI sends `workspace` connect metadata.
2. If prefix is absent, CLI leaves `workspace` unset and default binding rules
   apply.
3. `--mod/-m` stays orthogonal: it controls extra module loading, not workspace
   binding.
4. Commands MAY infer explicit workspace when the first positional token is
   unambiguously workspace-like (URL/path-like refs). This should not shadow
   normal function/check names.

### Binding/Lock Coupling

For any client:

- `currentWorkspace` resolves from the client's workspace binding.
- Lookup lock resolution reads/writes the bound lockfile of that same workspace.

There is no mode where current workspace and current lockfile come from
different bindings.

### Workspace Grants

V1 defines a single grant:

- `read_files`

Grant semantics:

- `read_files` permits read-only workspace filesystem access.
- Without `read_files`, these operations fail with permission denied.

Grant scope rule:

- Workspace grants are enforced for **ambient workspace access**.
- Explicitly passed `Workspace` arguments are not reduced by the callee's
  ambient grants.
- A caller that passes a `Workspace` argument is explicitly delegating that
  workspace authority to the callee.

### Lockfile Is Not Grant-Gated

Lockfile read/write for lookup resolution is engine-internal behavior.

- It follows lock mode and workspace binding.
- It is not enabled/disabled by workspace grants.
- No lockfile-specific grant exists in V1.

### Nested Client Behavior

#### Module Clients

Workspace object injection may occur as normal. Actual workspace operations
are authorized by grants.

Recommended V1 default grant policy:

1. Top-level module client directly called by the workspace owner: `read_files`
   granted.
2. Dependency module runtime clients (transitive module calls): no workspace
   grants by default.

Explicit delegation exception:

- If a caller explicitly passes a `Workspace` argument to a
  dependency/runtime function, that explicit argument is allowed according to
  the delegated workspace value, even when ambient grants are empty.

#### Nested `withExec` / `asService` Clients

Nested non-module clients do not inherit binding by default. They bind from
their own `.`.

Recommended V1 default grant policy:

1. Ambient workspace access is disabled by default.
2. `withExec` may explicitly opt in to grant `read_files`.
3. `asService` may explicitly opt in to grant `read_files`.

### Future Extensions

Future grants can be added without changing binding semantics:

1. `write_files`
2. `read_env`
3. `write_config`

The coupling rule remains: workspace and lockfile are always from the same
binding.
