# Synthetic Workspaces

status: draft spec
created: 2026-06-05

## Summary

A workspace is a project-shaped view over a private source backend. The backend
may be the caller's local filesystem, a resolved git ref, a Dagger `Directory`,
or another workspace plus changes.

The backend source is an implementation detail. It must not be exposed in the
API schema as a field, enum, interface, or union. Callers interact with a
`Workspace` through workspace operations, not by switching on its backend.

The design goal is simple: every workspace operation reads from the workspace's
own source of truth. It must never silently fall back to the caller's current
host workspace, infer behavior from address strings, or depend on incidental
materialization details such as whether a temporary directory contains `.git`.

## Public Contract

The public API should describe behavior, not storage.

Initial constructors:

```graphql
Directory.asWorkspace(cwd: String): Workspace!
GitRef.asWorkspace(cwd: String): Workspace!
```

`GitRepository` is the object used to resolve refs. A convenience constructor on
`GitRepository` can exist later only if its semantics are explicit, such as
"resolve `head` now, then create a `GitRef` workspace".

Future functional update APIs return new workspaces:

```graphql
extend type Workspace {
  withNewFile(path: String!, contents: String): Workspace!
  withNewDirectory(path: String!, source: Directory!): Workspace!
  withChanges(changes: Changeset!): Workspace!
  changes(other: Workspace!): Changeset!
}
```

These APIs do not mutate the backing source. They produce a new workspace value
whose effective contents are the previous workspace plus the requested changes.

## Private Internal Model

The implementation should have one backend dimension with composable variants:

```text
Workspace {
  source: workspaceSource // private; not in the API schema
  cwd: workspace path
  selectedConfig: optional
  moduleGraph: optional
}

workspaceSource =
  clientLocal(hostPath, clientID)
  gitRef(ref)
  directory(dir)
  overlay(base workspaceSource, changes Changeset)
```

This is an internal model, not an API shape.

`currentWorkspace` is not a separate backend kind. It is a way to construct a
workspace and attach session state:

- A local current workspace uses `clientLocal` plus selected config and loaded
  modules for the session.
- A remote current workspace uses `gitRef` plus selected config and loaded
  modules for the session.
- `Directory.asWorkspace` uses `directory`.
- `GitRef.asWorkspace` uses `gitRef`.
- Functional write methods use `overlay`.

The address of a workspace may be useful for display or identity, but it is not
the type system for behavior.

## Source Semantics

`clientLocal` represents a live path owned by a connected client. It can read
from the client filesystem and can support local side-effecting mutations when
the workspace is the current local workspace.

`gitRef` represents a resolved git ref. Its filesystem view is the tree at that
ref. Its git state is clean by definition: dirty is false and uncommitted changes
are empty.

`directory` represents a Dagger `Directory`. It is a filesystem source. It does
not imply git repository access just because the directory was produced from git
somewhere earlier.

`overlay` represents changes applied to another source. It does not mutate the
base. Its effective filesystem view is `base` plus `changes`.

## Construction

Local workspace selection constructs `clientLocal(hostPath, clientID)`.

Remote workspace selection, such as `dagger -W github.com/acme/project@main`,
should construct a `gitRef` source. The selected ref, workspace subpath, config,
and loaded modules are current-session state attached to the workspace. The
implementation may expose a `Directory` view of the ref for filesystem reads,
but must not flatten the workspace into a plain directory and lose the git ref as
the source of truth.

`Directory.asWorkspace` constructs a workspace from the supplied `Directory`.
It does not inspect the host, borrow the current workspace, or attach the
current workspace's module graph.

`GitRef.asWorkspace` constructs a workspace from the supplied ref. It preserves
the ref as the source of truth for filesystem and git operations. Constructing
the workspace should be cheap; filesystem reads can fetch or materialize the
tree lazily as needed.

## Paths

`cwd` is a workspace path. `/` is the source boundary; `/app` is a nested working
directory. Relative paths resolve from `cwd`. Absolute paths resolve from the
workspace boundary.

For `file`, `directory`, and `findUp`:

- Paths must be normalized.
- Paths must not escape the workspace boundary.
- Failures must refer to the workspace source, not the caller host.

`findUp(name:)` searches for one path element while walking parent directories.
`name` must be a basename. Empty names, dot segments, slashes, and traversal are
invalid.

## Filesystem Reads

`workspace.file(path:)` and `workspace.directory(path:)` read from the effective
source:

- `clientLocal` reads from the owning client's host path.
- `gitRef` reads from the selected git tree.
- `directory` reads from the supplied `Directory`.
- `overlay` reads from the base source with the overlay changes applied.

`include`, `exclude`, and `gitignore` filtering apply to the effective source
content. The gitignore root is the workspace boundary. Nested ignore files are
interpreted from the source being read. A non-local workspace must not reject
`gitignore: true` merely because there is no host path.

## Git State

`workspace.git()` reports git state for the workspace source when the source has
portable git semantics:

- `gitRef`: report the selected ref and commit; dirty is false.
- `overlay(gitRef, changes)`: report the same selected ref and commit; dirty is
  true when the overlay contains git-visible changes; uncommitted state is the
  overlay changes.
- `clientLocal`: inspect the selected local workspace as a local git checkout
  when it is one.
- `overlay(clientLocal, changes)`: preserve the base local git identity and add
  the overlay changes to the reported uncommitted state.
- `directory`: filesystem-only by default. It is not a repository handle.

`workspace.git()` must not require a materialized `.git` directory for `gitRef`
sources. The presence or absence of `.git` in a materialized filesystem is an
implementation detail.

Repository-wide operations, such as listing all branches, belong on
`GitRepository` or another explicit repository handle. They should not be added
to `Workspace.git()` unless their behavior is well-defined for every workspace
source.

## Config And Modules

Config selection walks from `cwd` up to the workspace boundary using source
content. It never traverses outside the source boundary.

Current workspaces can carry selected config and a loaded module graph because
session workspace selection performs that loading. This is true for both local
current workspaces and remote current workspaces.

Synthetic workspaces created from `Directory.asWorkspace` or `GitRef.asWorkspace`
do not implicitly borrow the caller's loaded modules. Source-backed config reads
use the workspace source, but APIs that require a loaded module graph must return
empty results or a clear "no loaded module graph" error until module loading from
that workspace is explicitly supported.

If synthetic workspaces later support module loading, that loading path must use
the workspace source as its source of truth and attach the resulting module graph
to the returned workspace value.

## Mutations

There are two mutation families.

Local side-effecting mutations are only valid for a current local
`clientLocal` workspace:

- `init`
- `install`
- `uninstall`
- `moduleInit`
- `configWrite`
- `envCreate`
- `envRemove`
- `update`
- `migrate`

These methods must reject remote current workspaces and value workspaces with
consistent local-only errors. They must not write to the caller host, temporary
materializations, or implementation-detail checkouts.

Functional update methods are valid for all workspaces. They return
`overlay(base, changes)` and have no side effects.

## Cache And Identity

Workspace cache keys and persisted IDs must include the private source identity:

- `clientLocal`: selected host path and owning client where host reads are
  involved.
- `gitRef`: repository identity, selected ref or commit, and workspace subpath.
- `directory`: directory content identity.
- `overlay`: base source identity plus changes identity.

Persisted workspaces must preserve the private source kind and source reference.
Rehydrating a `gitRef` workspace must not turn it into a plain `directory`
workspace by dropping git provenance.

Passing a workspace as an argument must invalidate dependent results when the
effective source changes. It must not depend on unrelated files from the
caller's current workspace.

## Test Contract

The test suite should read like the feature contract.

Required coverage:

- Backend source is not exposed in the API schema.
- `Directory.asWorkspace` reads from the supplied directory for relative paths,
  absolute paths, `findUp`, and `gitignore` filtering.
- `GitRef.asWorkspace` reads from the selected ref and reports clean git state.
- Remote current workspaces selected with `-W` use git ref source behavior while
  preserving current-workspace config and module loading.
- `workspace.git()` on a `gitRef` workspace matches the selected ref without
  depending on `.git` materialization.
- Functional writes return modified workspace values and do not mutate the base
  source.
- `overlay(gitRef, changes)` preserves the base commit/ref and reports the
  overlay as uncommitted state.
- Source-backed config reads use backend content.
- Source-backed module/check/service listings do not borrow host-loaded state.
- Local-only side-effecting mutations reject remote current workspaces and value
  workspaces.
- Source identity survives persistence and rehydration.
- `findUp(name:)` rejects non-basename names.

Tests should assert caller-visible behavior, not incidental implementation
details such as address prefixes, rootfs field presence, or whether a temporary
checkout contains `.git`.
