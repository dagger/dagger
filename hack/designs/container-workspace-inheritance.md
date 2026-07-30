# Container Workspace Inheritance

## Status

Proposed for [#13054](https://github.com/dagger/dagger/issues/13054).

## Problem

A Dagger client started inside a container detects its workspace from the
container filesystem. That is usually correct, but it breaks commands that are
meant to operate on the caller's workspace.

For example, a Go test executed in a container may connect to Dagger and call
`CurrentWorkspace`. Today that resolves from the test container's working
directory, even when the outer client selected a different workspace.

`experimentalPrivilegedNesting` grants the command access to the Dagger API, but
does not say which workspace the nested client should use. Making every nested
client inherit its parent implicitly is also incorrect: module runtimes must not
gain ambient access to their callers' workspaces.

A workspace binding is also more than a `Workspace` object. A client that loads
workspace modules has a selected environment, pending module configuration, and
a served module schema. Passing only the object can make `currentWorkspace`
correct while `dagger call` still exposes the wrong modules or constructor
defaults.

## Solution

Let a container command explicitly inherit a `Workspace` as the default for
nested Dagger clients:

```go
testCtr.WithExec(
    []string{"go", "test", "./..."},
    dagger.ContainerWithExecOpts{
        InheritWorkspace: dag.CurrentWorkspace(),
    },
)
```

`inheritWorkspace` is both a workspace binding and a grant of nested Dagger
access. It is execution-scoped: it applies only to the command or service that
receives the argument and is never ambient container state.

When the argument is the caller's bound workspace, its effective environment
and workspace module state are inherited with it.

## API

The schema uses the v1 global `ID` scalar with an expected type:

```graphql
extend type Container {
  withExec(
    # existing arguments omitted

    """
    Grant the command access to the Dagger API and use this workspace by
    default for nested Dagger clients.

    Only grant this access to trusted commands.
    """
    inheritWorkspace: ID @expectedType(name: "Workspace")
  ): Container!

  asService(
    # existing arguments omitted

    """
    Grant the service command access to the Dagger API and use this workspace
    by default for nested Dagger clients.

    Only grant this access to trusted commands.
    """
    inheritWorkspace: ID @expectedType(name: "Workspace")
  ): Service!

  up(
    # existing arguments omitted

    """
    Grant the service command access to the Dagger API and use this workspace
    by default for nested Dagger clients.

    Only grant this access to trusted commands.
    """
    inheritWorkspace: ID @expectedType(name: "Workspace")
  ): Void

  terminal(
    # existing arguments omitted

    """
    Grant the terminal command access to the Dagger API and use this workspace
    by default for nested Dagger clients.

    Only grant this access to trusted commands.
    """
    inheritWorkspace: ID @expectedType(name: "Workspace")
  ): Container!
}

extend type Directory {
  terminal(
    # existing arguments omitted

    """
    Grant the terminal command access to the Dagger API and use this workspace
    by default for nested Dagger clients.

    Only grant this access to trusted commands.
    """
    inheritWorkspace: ID @expectedType(name: "Workspace")
  ): Directory!
}
```

The arguments are gated after `v1.0.0-0`, matching the `Workspace` API. They do
not appear in older schema views.

### Convenience surfaces

`Container.withExec` and `Container.asService` are the fundamental execution
boundaries. The remaining fields include `inheritWorkspace` only when they
define a new command.

`Directory.terminal` includes the option because it creates a temporary
container and defines its terminal command. There is no intermediate service on
which the caller can select the binding, so omitting the option would prevent
the convenience API from supporting workspace-aware interactive inspection.

`Container.up` also includes the option. `Container.up` is shorthand for
constructing a service with `Container.asService` and then calling `Service.up`;
it intentionally accepts the same command-definition options as
`Container.asService`. Forwarding `inheritWorkspace` preserves that established
equivalence. The binding is captured while constructing the service and has the
same lifetime and meaning as a binding passed directly to
`Container.asService`.

`Service.up` and `Service.terminal` do not include the option. They operate on
an already-defined service, whose inherited workspace was selected when its
command was defined. Adding the option at startup or attachment time would give
it a different lifetime and meaning.

## Workspace Selection

A nested client selects its workspace in this order:

| Priority | Source |
| --- | --- |
| 1 | Workspace explicitly declared by the nested client (`-W`, `--workspace`, or SDK connection options) |
| 2 | `inheritWorkspace` on the command that started the client |
| 3 | Detection from the nested client's own container filesystem |
| 4 | Ordinary parent-client inheritance |

An explicit nested workspace selection is complete: its workspace and
environment are loaded normally and no inherited module state is reused.

If `inheritWorkspace` is absent, behavior is unchanged.

## Binding Contract

Any `Workspace` value can be inherited for `currentWorkspace` and workspace
APIs. This includes caller-local, remote, synthetic, and overlaid workspaces.

Workspace module inheritance is narrower. A nested client can load modules from
an inherited workspace when that value identifies a live ancestor workspace
binding. This is the normal case:

```go
InheritWorkspace: dag.CurrentWorkspace()
```

The binding captures:

- the retained `Workspace` result;
- an opaque binding identity shared with the matching ancestor; and
- the ancestor's selected workspace environment, if any.

With no explicit nested environment, the nested client uses the captured
environment and module state. A nested `--env` may name that same environment.
Selecting a different environment requires an explicit workspace selection,
for example `dagger -W /workspace --env prod ...`; that higher-priority binding
uses the normal workspace loader.

If an inherited value is not a live ancestor binding, the nested client still
uses it for `currentWorkspace` and other core workspace APIs, but receives no
workspace module schema from it. This distinction matters because CLI clients
request workspace modules even for core-only operations such as
`dagger query`. The engine does not silently detect modules from the container,
reuse a different ancestor's schema, or attempt to reconstruct module
configuration from an arbitrary workspace value.

Generic module loading directly from an arbitrary `Workspace` object would
require a separate workspace-source loader covering caller routing, remote
roots, synthetic directories, overlays, lock state, compatibility workspaces,
and environment application. That is not part of this change.

## Nested Access

`inheritWorkspace` enables the same nested Dagger connection that
`experimentalPrivilegedNesting` enables. Supplying either argument is
sufficient; supplying both is valid and creates one nested connection.

This is intentionally stronger than mounting workspace files into the
container. The nested client receives Dagger API access and can use the
inherited `Workspace` according to its existing capabilities, including
owner-routed host filesystem operations for a caller-local workspace.

The inherited binding is trusted engine metadata. It is never read from the
nested client's HTTP metadata, so a command without the public argument cannot
forge inheritance. The nested client's own declared workspace and environment
are still accepted from its request metadata and handled according to the
precedence above.

## Execution Lifetime

At the schema boundary, the public ID is resolved to an internal inherited
binding containing a `dagql.ObjectResult[*core.Workspace]`. The object result,
not just its encoded handle, is retained as a dependency of the lazy execution:

- `Container.withExec` retains the binding in `ContainerExecState`.
- `Container.asService` and `Container.up` retain it in `Service`.
- Converting a container whose most recent command inherited a workspace to a
  service preserves that binding, unless `asService` selects a different one.
- Terminal commands pass it to their temporary service.
- The dependency graphs persist the workspace by result ID and reattach it when
  loaded. The binding identity and selected environment are persisted alongside
  that reference.

Core passes the retained workspace result through the same typed in-process
executor handoff used for module and environment context. The executor keeps it
on the running exec state and passes it to the nested session handler, which
type-checks it as `dagql.ObjectResult[*core.Workspace]` and reattaches it to the
nested client's dagql server.

The captured binding identity and environment travel separately in
engine-internal `ClientMetadata` fields marked `json:"-"`. They are distinct
from the public `Workspace` and `WorkspaceEnv` request fields. Binding selection
consults them only when the typed inherited workspace wins, so an explicit
nested workspace never accidentally receives the inherited environment.
Nested request headers cannot set them.

No encoded workspace handle is serialized or forwarded. After lazy container
or service state is restored, the reattached workspace dependency itself
crosses the executor boundary.

A terminal opened synchronously for a failed exec reuses that exec's runtime
metadata while its workspace dependency is still leased. This preserves the
binding for the debugging command without making it container state.

## Workspace Modules

Each loaded workspace binding has a stable opaque identity for the life of the
session. Ordinary inheritance and explicit command inheritance carry that
identity; persisted inherited bindings retain it for matching after
reattachment. The capture step uses exact object identity to recognize a
Workspace value produced by a live ancestor; all matching after capture uses
the opaque binding identity, never a Go pointer.

When a nested client requests workspace modules, the engine finds the nearest
ancestor with the same binding identity and selected environment. The ancestor
gathers its workspace module configuration even if that client did not request
a workspace schema for itself, then loads the subset demanded by the nested
request. The engine clones the ancestor's workspace-specific served schema and
entrypoint state, re-rooted on the nested client's `Query`.

Loading completes against the ancestor because caller-local pending module refs
belong to that client's filesystem session. The nested client receives the
resolved workspace schema, not host paths that would resolve against its
container.

Only workspace-origin module state is inherited. Parent extra modules (`-m`),
modules served explicitly through the API, and module-runtime context are not
copied or loaded as a prerequisite. The nested client's own extra modules load
first and keep their normal precedence over an ambient workspace entrypoint.

This schema reuse runs only for an explicit inherited binding and therefore
does not weaken the ordinary module-runtime inheritance boundary.

## Execution Paths

The binding is accepted by:

- normal `Container.withExec`;
- direct `Container.asService`;
- `Container.up`;
- `Container.terminal`;
- `Directory.terminal`; and
- a terminal opened for the failed `withExec` that carried the binding.

No path stores inheritance as ambient `Container` configuration. The binding
does follow the command when a container produced by
`withExec(inheritWorkspace: ...)` is converted to a service with `asService`,
as required for the conventional `withExec(...).asService()` service pattern.
An explicit `inheritWorkspace` on `asService` wins.

The implementation must use distinct schema argument structs, or explicitly
gated arguments, so shared core structs cannot expose the argument on
`Service.terminal` or on old API views by accident.

## Caching

The public workspace ID is part of the dagql call and must distinguish
executions using different inherited bindings. The runtime handle is not part
of serialized execution metadata.

Otherwise, existing container caching semantics are unchanged. A host-backed
workspace resolves live content when the command first executes, but the
resulting `Container` is still an immutable, cacheable snapshot. Reusing the
same workspace and container graph may reuse that snapshot. If Dagger later
needs watch-like reruns, that should be an explicit cache/refresh control rather
than a second, surprising meaning for `inheritWorkspace`.

## Compatibility

The arguments are optional. Their absence preserves current workspace
detection, parent inheritance, module-runtime boundaries, module loading, and
privileged nesting behavior.

Older SDKs continue to call the existing fields without the argument. Older
module schema views do not see it. Current SDK generation exposes an
SDK-appropriate workspace object or ID option on the five command methods.

## Behavioral guarantees

The implementation guarantees:

1. Declared workspace beats inherited workspace; inherited workspace beats
   container detection and ordinary parent inheritance.
2. A nested client cannot inject or replace the trusted inherited binding
   through request metadata.
3. `inheritWorkspace` alone enables nested Dagger access, and combining it with
   `experimentalPrivilegedNesting` is redundant.
4. `CurrentWorkspace` resolves a caller-provided local, remote, synthetic, or
   overlaid workspace rather than the container filesystem.
5. A matching ancestor's workspace modules, entrypoint, and selected
   environment are available to a nested `dagger call`.
6. Parent extra modules and module-runtime schema are not inherited.
7. A different explicit nested workspace wins. A mismatched nested environment
   fails clearly. An unmatched inherited value remains available to core
   workspace APIs without exposing unrelated workspace modules.
8. Direct service, `up`, container terminal, directory terminal, and exec-error
   terminal paths preserve the binding.
9. Lazy container and service persistence retain the workspace dependency and
   derive a valid handle after restoration.
10. Different inherited bindings do not share execution results; otherwise
    existing `withExec` caching behavior is unchanged.
11. Omitting the argument leaves existing behavior unchanged.
12. Introspection proves the argument is present only on the five v1 command
    fields, absent from older views, and absent from `Service.terminal`.
