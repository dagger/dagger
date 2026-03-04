# Workspace Binding and Access Control

## Status: Draft

Builds on:
- [Part 1: Workspaces and Modules](https://gist.github.com/shykes/e4778dc5ec17c9a8bbd3120f5c21ce73)
- [Part 2: Workspace API](https://gist.github.com/shykes/86c05de3921675944087cb0849e1a3be)
- [Lockfile: Lookup Resolution](./lockfile.md)

## Summary

This document defines how workspace behavior applies across nested clients.

A client has:

1. A **workspace binding**: which workspace it is operating in.
2. A set of **workspace grants**: what it is allowed to do through the workspace API.

Workspace binding and lockfile are always coupled: a client's `currentWorkspace` and lookup lock state (`.dagger/lock`) come from the same bound workspace.

Default binding is intentionally asymmetric:

1. Module clients inherit workspace binding.
2. Non-module clients bind a fresh workspace from their own `.`.

Lockfile behavior is engine-internal and is **not** governed by workspace grants.

Grants in this document apply to **ambient workspace access** only (auto-injected workspace and `dag.CurrentWorkspace()` style access). Explicitly passed `Workspace` arguments are treated as deliberate delegation from the caller.

## Binding and Access Diagram

```text
                         (connect)
                             |
                             v
                    +-------------------+
                    | New client C      |
                    +-------------------+
                             |
             +---------------+----------------+
             |                                |
   no explicit workspace                 explicit workspace
      declaration                             declaration
             |                                |
             v                                v
 +---------------------------+     +---------------------------+
 | module client?            |     | bind(C) = declared bind   |
 +---------------------------+     +---------------------------+
      | yes         | no
      v             v
 +----------------+  +---------------------------+
 | bind(C)=parent |  | bind(C)=detect from "."   |
 +----------------+  +---------------------------+
             |                                |
             +---------------+----------------+
                             |
                             v
                    +-------------------+
                    | Runtime calls     |
                    +-------------------+
                             |
    +---------+----------------------+----------------------+
    |                                |                      |
    v                                v                      v
ambient workspace access     explicit Workspace arg      lookup call
(injection/currentWorkspace) (passed by caller)     (modules/git/http/container)
    |                                |                      |
    v                                v                      v
check grant: read_files?      use delegated authority  use lock mode + bind(C)
    |                                |                      |
+---+---+                            |                      |
|       |                            |                      |
allow  deny                          |               read/write bound lockfile
                                     |                     (.dagger/lock for bind(C))
```

## Definitions

| Term | Meaning |
| --- | --- |
| Workspace binding | The canonical workspace context attached to a client (root location, detected workspace metadata, and lockfile location). |
| Workspace grants | Capabilities that authorize workspace API operations for a client. |
| Bound lockfile | The `.dagger/lock` associated with the currently bound workspace. |
| Ambient workspace | Workspace resolved from the client's own binding (for example `currentWorkspace` and ambient injection). |
| Explicit workspace argument | A `Workspace` value passed by a caller as a normal function argument. |
| Module client | A client executing module runtime/module function context (for example, module runtime or dependency module runtime). |

## Problem

1. Workspace behavior for nested clients is not formally specified.
2. "Current workspace" and lockfile scope can diverge in practice, causing confusing lookup behavior.
3. Non-module nested clients (for example, nested CLI in a playground) can accidentally inherit a parent workspace that does not match their own filesystem.
4. Access control for workspace API use by nested/dependency module clients is implicit and inconsistent.

## Goals

1. Define a single binding model across main and nested clients.
2. Keep lockfile resolution deterministic and coupled to the bound workspace.
3. Allow capability-based restriction of workspace API access.
4. Keep v1 simple with one grant: `read_files`.

## Non-Goals (V1)

1. Fine-grained grants beyond `read_files`.
2. Lockfile-specific grants.
3. Full policy language for per-function permission derivation.

## Model

### 1. Workspace Binding

Connect parameter:

- `workspace` (optional string): explicitly declare workspace binding for the connecting client.
- Unset means "use default binding rules"; it is semantically different from a set value.

Binding rules:

1. If `workspace` is set: bind to that declared workspace.
2. Else if the client is a module client: inherit nearest ancestor workspace binding.
3. Else: bind from workspace detection in the connecting client's own `.`.

Notes:

- Top-level clients are non-module clients, so rule 3 applies when `workspace` is unset.
- Nested non-module clients also use rule 3 by default.
- Engine infers local-vs-remote workspace resolution from `workspace` when set.

### 2. Binding/Lock Coupling

For any client:

- `currentWorkspace` resolves from the client's workspace binding.
- Lookup lock resolution reads/writes the bound lockfile of that same workspace.

There is no mode where current workspace and current lockfile come from different bindings.

### 3. Workspace Grants

V1 defines a single grant:

- `read_files`

Grant semantics:

- `read_files` permits read-only workspace filesystem access (e.g. workspace file/directory/find-up style operations).
- Without `read_files`, these operations must fail with permission denied.

Grant scope rule (normative):

- Workspace grants are enforced for **ambient workspace access**.
- Explicitly passed `Workspace` arguments are not reduced by the callee's ambient grants.
- A caller that passes a `Workspace` argument is explicitly delegating that workspace authority to the callee.

### 4. Lockfile Is Not Grant-Gated

Lockfile read/write for lookup resolution (`modules.resolve`, `container.from`, future lookup ops) is engine-internal behavior.

- It follows lock mode and workspace binding.
- It is not enabled/disabled by workspace grants.
- No lockfile-specific grant exists in V1.

## Nested Client Behavior

### Baseline

Only module clients inherit workspace binding by default.

Nested non-module clients bind from their own `.` by default.

### Module Clients

Workspace object injection may occur as normal. Actual workspace operations are authorized by grants.

Recommended V1 default grant policy:

1. Top-level module client directly called by the workspace owner: `read_files` granted.
2. Dependency module runtime clients (transitive module calls): no workspace grants by default.

This preserves explicit delegation while preventing ambient workspace access in dependency runtimes.

Explicit delegation exception:

- If a caller explicitly passes a `Workspace` argument to a dependency/runtime function, that explicit argument is allowed according to the delegated workspace value, even when ambient grants are empty.

### Nested `withExec` / `asService` Clients

Nested non-module clients do not inherit binding by default.

They bind from their own `.` (for example, a nested CLI started inside a playground/container filesystem sees that filesystem as its default workspace).

If they need a non-default workspace, they must explicitly set `workspace` when connecting.

Recommended V1 default grant policy:

1. Ambient workspace access is disabled by default.
2. `withExec` may explicitly opt in to grant `read_files`.
3. `asService` may explicitly opt in to grant `read_files`.

## Example Scenarios

1. Host CLI calls module function:
- Module runtime client inherits host workspace binding.
- Direct module call gets `read_files` and can use workspace reads.

2. Module A calls dependency module B:
- B inherits same workspace binding.
- B receives no workspace grants by default, so workspace API reads fail.
- Lookup locking still uses the bound lockfile internally.

3. User opens terminal inside playground and runs another Dagger client:
- If nested client does not set `workspace`, it binds from its own `.` inside playground/container.
- If nested client sets `workspace`, it binds to that declared workspace; both current workspace and lockfile switch to that new binding.

## API/Engine Implications

### Client Connect Metadata

This design adds exactly one new optional connect parameter:

1. `workspace`: explicitly declare a workspace binding for the connecting client. If unset, default binding rules apply.

All other client connect parameters remain defined elsewhere and are unchanged by this design.

Workspace grants are not supplied by connecting clients.

Grant assignment is determined by the API that spawns the client (module loading/runtime policy, `withExec`, `asService`).

### Server State

Each client state tracks:

1. Effective workspace binding.
2. Effective workspace grants.

### Enforcement

Workspace API resolvers enforce grants at operation time for ambient workspace access paths.

Explicitly passed `Workspace` arguments use delegated authority from the passed value, not the callee's ambient grant set.

Lookup lock resolution uses effective workspace binding regardless of grants.

## Compatibility

This model formalizes behavior without requiring module authors to change function signatures.

- Automatic workspace injection can remain.
- Capability checks become explicit and consistent.
- Existing lock modes (`strict|auto|update`) remain unchanged; only binding source becomes explicit.

## Future Extensions

Future grants can be added without changing binding semantics, e.g.:

1. `write_files`
2. `read_env`
3. `write_config`

The coupling rule remains: workspace and lockfile are always from the same binding.
