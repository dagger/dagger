# Synthetic Workspaces

status: draft spec
created: 2026-06-05

## Why Care

`Workspace` is the source boundary for project operations. If a workspace value
falls back to the caller host, loses its git source, or exposes backend details
in the API, then module arguments, cache keys, git state, and future functional
writes become incorrect.

This design fixes that with one private source model. Callers see one
`Workspace` API. The engine keeps the source kind internally.

## Decision

Use one private, composable workspace source:

```text
workspaceSource =
  clientLocal(hostPath, clientID)
  gitRef(ref)
  directory(dir)
  overlay(base workspaceSource, changes Changeset)
```

`workspaceSource` is not public API. Do not expose it as a GraphQL field, enum,
interface, union, or discriminator.

Public constructors:

```graphql
Directory.asWorkspace(cwd: String): Workspace!
GitRef.asWorkspace(cwd: String): Workspace!
```

`GitRepository` resolves refs. If it ever grows `asWorkspace`, define it as an
explicit shorthand for resolving a `GitRef` first.

Future functional writes return overlays:

```graphql
extend type Workspace {
  withNewFile(path: String!, contents: String): Workspace!
  withNewDirectory(path: String!, source: Directory!): Workspace!
  withChanges(changes: Changeset!): Workspace!
  changes(other: Workspace!): Changeset!
}
```

## Source Rules

- Local current workspace: `clientLocal` plus selected config and loaded modules.
- Remote current workspace (`dagger -W github.com/acme/project@main`): `gitRef`
  plus selected config and loaded modules.
- `Directory.asWorkspace`: `directory`; no host fallback, no borrowed modules.
- `GitRef.asWorkspace`: `gitRef`; no `.git` materialization requirement.
- Functional writes: `overlay(base, changes)`; no side effects.

Paths resolve inside the workspace boundary:

- Relative paths start at `cwd`.
- Absolute paths start at `/`.
- Paths must not escape the source boundary.
- `findUp(name:)` accepts only a basename.

Filesystem reads use the effective source. `gitignore`, `include`, and
`exclude` apply to that source, not to the caller host.

## Git Rules

- `gitRef`: `workspace.git()` reports the selected ref/commit and is clean.
- `overlay(gitRef, changes)`: same ref/commit, dirty state from the overlay.
- `clientLocal`: inspect the local checkout if the source is in git.
- `directory`: filesystem-only by default. It is not a repository handle.

Do not implement `workspace.git()` by looking for `.git` in a temporary
materialization of a `gitRef`.

Repository-wide operations, like listing branches, belong on `GitRepository`.
They do not belong on `Workspace.git()` unless they have portable semantics for
every source.

## Implementation Steps

1. Add the private `workspaceSource` model and route workspace methods through
   it. Stop using address prefixes, `Rootfs != nil`, or `HostPath == ""` as the
   behavior switch.
2. Convert existing current workspace loading:
   - local current workspace -> `clientLocal`
   - remote current workspace -> `gitRef`
3. Add `Directory.asWorkspace` -> `directory`.
4. Add `GitRef.asWorkspace` -> `gitRef`.
5. Implement filesystem reads and filters against the effective source.
6. Implement `workspace.git()` from the source rules above.
7. Keep loaded modules as workspace/session state, not source identity.
8. Add overlay-backed functional writes.
9. Include source identity in persistence and cache keys.

## Local Mutations

Side-effecting workspace mutations are local-current-workspace only:

- `init`
- `install`
- `uninstall`
- `moduleInit`
- `configWrite`
- `envCreate`
- `envRemove`
- `update`
- `migrate`

They must reject remote current workspaces and value workspaces. They must not
write to temporary materializations or the caller host by accident.

## Tests

Required tests:

- backend/source details are not visible in the API schema
- `Directory.asWorkspace` reads only the supplied directory
- `GitRef.asWorkspace` reads the selected ref and reports clean git state
- remote `-W` still loads config/modules from the selected git ref
- `workspace.git()` for `gitRef` does not depend on `.git`
- `gitignore` filtering works for non-host sources
- value workspaces do not borrow host-loaded modules/checks/services
- local-only mutations reject non-local sources
- functional writes return overlays and do not mutate the base
- `overlay(gitRef, changes)` reports the overlay as uncommitted state
- source identity survives persistence and cache keys
- `findUp(name:)` rejects non-basename input
