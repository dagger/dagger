# Disable Workspace Inheritance From Runtime Dependencies

author: dawg
created: 2026-05-26
status: implemented on branch
base branch: workspace
tracking branch: codex/workspace-disable-workspace-inheritance

## Purpose

Workspace context must not leak from a user invocation into modules that are
called by another module at runtime.

Today a user can call module A from a selected workspace, and module A can call
dependency module B without explicitly passing any workspace value. B still sees
the user's selected workspace. That is a sandboxing problem: B receives authority
over the caller's workspace only because it is in A's dependency graph.

The intended behavior is:

1. A direct user/client call to module A may receive the caller's contextual
   workspace.
2. A runtime call from module A to dependency module B must not receive that
   workspace implicitly.
3. If B needs a `Workspace`, A must pass a `Workspace` argument explicitly.
4. The explicit value is usually A's own contextual workspace today, but the
   design must not assume that forever. Later A may pass a synthetic workspace
   once synthetic workspaces exist.

## Definitions

`Workspace` means the core GraphQL object defined in `core/workspace.go`.

`Contextual workspace` means the selected workspace currently available through
`currentWorkspace` and through automatic injection of omitted function arguments
whose type is core `Workspace`.

`Direct client` means a non-module client such as the CLI, SDK user process, or
other host-side client that created or joined the session.

`Module runtime client` means the privileged nested client used to execute a
module function. In the server this client can be identified by module context
(`client.mod`) or by active function-call context (`client.fnCall`), depending
on which runtime path initialized the client. The core-side equivalent is
`CurrentModule(ctx)` or `CurrentFunctionCall(ctx)`.

`Runtime dependency call` means module A's runtime client issues a GraphQL call
to module B, where B is available through A's `dagger.json` dependencies and
A's generated SDK.

## Current Behavior

There are two separate inheritance paths. Both must be changed.

### 1. Omitted Workspace Arguments Are Injected At Call Time

`core/modfunc.go` handles dynamic inputs in `ModuleFunction.DynamicInputsForCall`.
For every omitted argument, it checks whether the argument is a `Workspace`:

```go
if argMetadata.IsWorkspace() {
    workspaceArgs = append(workspaceArgs, argMetadata)
    continue
}
```

Those arguments are later resolved by `ModuleFunction.loadWorkspaceArg`, which
selects `currentWorkspace(skipMigrationCheck: true)` from the current GraphQL
client.

This happens before the callee runtime starts. Therefore, when module A calls
module B and omits B's `Workspace` argument, the injection runs in A's runtime
client, not in B's runtime client. If A inherited the user's workspace, B gets
that workspace as an explicit input even though A did not provide one in source.

Blocking only B's runtime client from inheriting workspace is not sufficient.
The call-time injection in A's client must also be blocked.

### 2. Module Runtime Clients Inherit Parent Workspace Bindings

`engine/server/session_workspaces.go` chooses a workspace binding mode:

```go
func workspaceBindingMode(client *daggerClient) (workspaceBindingModeType, string) {
    if workspaceRef, ok := workspaceRefFromClientMetadata(client.clientMetadata); ok {
        return workspaceBindingDeclared, workspaceRef
    }
    if client.pendingWorkspaceLoad {
        return workspaceBindingDetectHost, ""
    }
    return workspaceBindingInherit, ""
}
```

Module runtime clients have module context, so `initializeDaggerClient` does not
set `pendingWorkspaceLoad`. They usually have no explicit `Workspace` metadata.
That makes them fall through to `workspaceBindingInherit`.

`inheritWorkspaceBinding` then walks parent clients and copies the first parent
workspace it finds:

```go
for i := len(client.parents) - 1; i >= 0; i-- {
    parent := client.parents[i]
    if err := srv.ensureWorkspaceLoaded(ctx, parent); err != nil {
        return err
    }
    if parent.workspace != nil {
        client.workspace = parent.workspace
        return nil
    }
}
```

This is correct for the first module runtime client in a direct user call:

```text
user client -> module A runtime
```

Module A should see the user's selected workspace.

It is wrong for a dependency runtime client:

```text
user client -> module A runtime -> module B runtime
```

B must not inherit A's inherited workspace.

## Required Semantics

Keep these cases distinct:

| Scenario | Result |
|---|---|
| Direct client calls A, A has omitted `Workspace` arg | Inject direct client's selected workspace |
| Direct client calls A, A calls `dag.CurrentWorkspace()` | Return direct client's selected workspace |
| A calls B and omits B's `Workspace` arg | Error; A must pass a value explicitly |
| A passes `Workspace` arg to B explicitly | B receives exactly that value |
| A calls B, and B calls `dag.CurrentWorkspace()` | Error; no inherited current workspace |
| A calls B with ordinary `+defaultPath` `Directory` or `File` args | Unchanged; these resolve from module source/default path context |
| Workspace env overlay (`--env`) selected by direct client | Available to A, not implicitly inherited by B |

The error text does not need to be identical to today's
`ErrNoCurrentWorkspace` text, but it should make the fix obvious. Prefer
including "pass a Workspace explicitly" in the omitted-argument error.

## Implementation Plan

### Step 1: Gate Automatic Workspace Argument Injection

Change `ModuleFunction.loadWorkspaceArg` in `core/modfunc.go` so it refuses to
inject a workspace when the current GraphQL client is a module runtime client.

Use `CurrentQuery(ctx).CurrentModule(ctx)` and
`CurrentQuery(ctx).CurrentFunctionCall(ctx)` as the local abstractions; do not
reach into `engine/server` from `core`.

Suggested shape:

```go
func (fn *ModuleFunction) loadWorkspaceArg(
    ctx context.Context,
    dag *dagql.Server,
) (dagql.IDType, error) {
    query, err := CurrentQuery(ctx)
    if err != nil {
        return nil, fmt.Errorf("get current query: %w", err)
    }
    if _, err := query.CurrentModule(ctx); err == nil {
        return nil, fmt.Errorf(
            "%w: workspace arguments are not inherited by module runtime calls; pass a Workspace explicitly",
            ErrNoCurrentWorkspace,
        )
    } else if !errors.Is(err, ErrNoCurrentModule) {
        return nil, fmt.Errorf("get current module: %w", err)
    }
    if fnCall, err := query.CurrentFunctionCall(ctx); err == nil && fnCall != nil {
        return nil, fmt.Errorf(
            "%w: workspace arguments are not inherited by module runtime calls; pass a Workspace explicitly",
            ErrNoCurrentWorkspace,
        )
    } else if err != nil && !errors.Is(err, ErrNoCurrentModule) {
        return nil, fmt.Errorf("get current function call: %w", err)
    }

    // existing currentWorkspace selection follows here
}
```

Why this works:

Direct client -> A runs dynamic input injection in the direct client's context,
so `CurrentModule` returns `ErrNoCurrentModule` and injection is allowed.

A -> B runs dynamic input injection in A's module runtime client, so
`CurrentModule` or `CurrentFunctionCall` succeeds and injection is denied. A can
still pass an explicit `Workspace` ID; explicit args are skipped before
`loadWorkspaceArg` is reached.

Do not remove `Workspace` from the generated schema and do not make callers pass
the argument when they are direct users. This is a runtime authorization rule,
not a schema-shape change.

### Step 2: Stop Workspace Binding At Module Parent Boundaries

Change `inheritWorkspaceBinding` in `engine/server/session_workspaces.go` so a
module runtime client may inherit only from a non-module parent. It must not
search past another module runtime client. Non-module nested clients keep the
previous inheritance behavior; the sandbox boundary is specifically module
runtime -> dependency module runtime.

Suggested helper:

```go
func isModuleRuntimeClient(client *daggerClient) bool {
    return client != nil && (client.mod.Self() != nil || client.fnCall != nil)
}
```

Suggested change:

```go
for i := len(client.parents) - 1; i >= 0; i-- {
    parent := client.parents[i]
    if isModuleRuntimeClient(client) && isModuleRuntimeClient(parent) {
        return nil
    }

    if err := srv.ensureWorkspaceLoaded(ctx, parent); err != nil {
        return err
    }

    parent.workspaceMu.Lock()
    parentWorkspace := parent.workspace
    parent.workspaceMu.Unlock()
    if parentWorkspace == nil {
        continue
    }

    client.workspaceMu.Lock()
    if client.workspace == nil {
        client.workspace = parentWorkspace
    }
    client.workspaceMu.Unlock()
    return nil
}
```

Parent order matters: `initializeDaggerClient` clones the parent's existing
parents and then appends the immediate parent. Iterating backward sees the
immediate parent first.

This keeps:

```text
user client -> module A runtime
```

because A's immediate parent is non-module.

It blocks:

```text
user client -> module A runtime -> module B runtime
```

because B's immediate parent is module A's runtime client.

Keep this predicate aligned with Step 1. Do not use `client.fnCall` as an
additional server-only marker unless the call-time injection gate also checks
`CurrentFunctionCall`; otherwise the two gates will disagree about what counts
as module runtime context.

This also matches the skipped TODO in
`core/integration/workspace_selection_test.go`: command-scoped workspace
inheritance for arbitrary nested commands should be explicit in the future, not
ambient inheritance from the module function that created the exec.

### Step 3: Preserve Explicit Workspace Passing

No special transport should be added for explicit passing.

An explicit `Workspace` argument should continue to work through existing object
ID serialization. `core.Workspace` persists `ClientID`, `HostPath`, `Rootfs`,
`ConfigFile`, `LockFile`, and compat provenance in `core/workspace.go`. Workspace
filesystem methods already use the workspace owner client context through
`core/schema/workspace.go` helpers.

The important part is that explicit object passing and ambient inheritance must
remain different:

```go
// Allowed: A chooses what authority B receives.
func (m *A) CallB(ctx context.Context, workspace *dagger.Workspace) (string, error) {
    return dag.B().NeedsWorkspace(ctx, workspace)
}

// Not allowed: B silently receives A's current workspace.
func (m *A) CallB(ctx context.Context) (string, error) {
    return dag.B().NeedsWorkspace(ctx)
}
```

Adjust the exact generated SDK syntax as needed; the semantic point is that
only the explicit call supplies B's workspace argument.

## Regression Tests

This branch includes focused unit coverage for both required gates.

Implemented unit coverage:

1. `core/modfunc_test.go` verifies omitted `Workspace` argument injection is
   rejected when the caller context has a current module.
2. `core/modfunc_test.go` also verifies omitted `Workspace` argument injection
   is rejected when the caller context has an active function call.
3. `engine/server/session_test.go` verifies a dependency module runtime does
   not inherit through another module runtime parent.
4. `engine/server/session_test.go` verifies the same inheritance stop for
   function-call-only runtime clients.
5. `engine/server/session_test.go` also verifies non-module nested clients still
   inherit through a module parent, preserving the narrower existing behavior.

Implemented integration coverage:

```text
core/integration/module_dependency_runtime_test.go
core/integration/testdata/modules/go/runtime-workspace-isolation/
  dagger.json            # module A, depends on ./dep
  main.go
  dep/
    dagger.json          # module B
    main.go
```

Module A exposes:

```go
func (m *Test) ExplicitWorkspaceArg(ctx context.Context, workspace *dagger.Workspace) (string, error)
func (m *Test) ImplicitWorkspaceArg(ctx context.Context) (string, error)
func (m *Test) CurrentWorkspaceFromDep(ctx context.Context) (string, error)
```

Module B exposes:

```go
func (m *Dep) ReadWorkspaceArg(ctx context.Context, workspace *dagger.Workspace) (string, error)
func (m *Dep) ReadCurrentWorkspace(ctx context.Context) (string, error)
```

Assertions:

1. A direct call to A with an omitted `Workspace` argument succeeds, proving A
   receives the contextual workspace.
2. A explicitly passing that `Workspace` to B succeeds, proving explicit
   delegation still works.
3. A calling B while omitting B's `Workspace` argument fails and mentions
   explicit passing.
4. B calling `dag.CurrentWorkspace()` fails with no current workspace.

The server unit test beside `TestEnsureWorkspaceLoadedInheritsParentWorkspace`
uses:

```go
func TestEnsureWorkspaceLoadedDoesNotInheritBetweenModuleRuntimes(t *testing.T)
```

Construct:

```text
root parent: workspace != nil
module parent: parents=[root], workspace same as root, mod != nil
child module runtime: parents=[root, module parent]
```

Call `srv.ensureWorkspaceLoaded(context.Background(), child)` and assert
`child.workspace == nil`.

Cover both `client.mod` and `client.fnCall`. The integration fixture exercises
the real generated-SDK path, while the unit tests keep the predicate explicit.

## Files To Touch

Primary implementation:

- `core/modfunc.go`
- `engine/server/session_workspaces.go`

Primary tests:

- `engine/server/session_test.go`
- `core/modfunc_test.go`
- `core/integration/module_dependency_runtime_test.go`
- `core/integration/testdata/modules/go/runtime-workspace-isolation/...`

No SDK codegen changes are expected unless fixture generation reveals that the
test source needs normal generated-client updates.

## Risks And Checks

The main risk is fixing only one path. If only `inheritWorkspaceBinding` changes,
B can still receive an omitted `Workspace` argument because injection happens in
A's client before B's runtime starts. If only `loadWorkspaceArg` changes, B can
still call `dag.CurrentWorkspace()` inside its own runtime and inherit A's
workspace. Both gates are required.

Run at least:

```sh
go test ./core -run 'TestModuleFunction'
go test ./engine/server -run 'TestEnsureWorkspaceLoaded'
dagger --progress=plain call engine-dev test --pkg="./core/integration" --run="^TestModule/TestRuntimeDependencyDoesNotInheritWorkspace$"
```

## Compatibility Notes

This intentionally breaks ambient workspace access for runtime dependency
modules. That is the requested sandboxing boundary.

Direct user calls keep the ergonomic behavior: a top-level module can receive a
selected workspace without spelling it out in the CLI call.

Dependency modules keep full functionality when the caller passes a `Workspace`
object explicitly. This makes authority visible in module A's source code and
preserves a future path for A to pass a synthetic workspace instead of the user's
workspace.
