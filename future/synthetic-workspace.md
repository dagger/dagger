# Synthetic Workspaces

author: codex
created: 2026-06-05
status: draft spec

## Summary

A synthetic workspace is a `Workspace` whose source of truth is an explicit
Dagger object instead of the caller's host filesystem.

The core contract is simple: a workspace backed by a `Directory` uses that
directory, and a workspace backed by a `GitRepository` uses that git repository.
No method may silently substitute the current host workspace, infer behavior
from address strings, or depend on incidental filesystem artifacts when the
backend already models the state.

## Caller Contract

Callers use synthetic workspaces when they want to pass project source as a
`Workspace` value without first checking it out on the host.

```graphql
directory.asWorkspace(cwd: "/app")
git(url: "https://github.com/acme/project").asWorkspace(cwd: "/app")
```

The returned workspace behaves as if the supplied source object is the workspace
boundary:

- Relative paths resolve from `cwd`.
- Absolute paths resolve from the workspace boundary.
- Filesystem reads use the supplied source content.
- Git reads use the supplied git source when the backend is a `GitRepository`.
- Local host state is used only by the local workspace backend.

## Backend Model

`Workspace` must have an explicit backend kind. Backend behavior must not be
derived from the workspace address, a URL scheme, or whether a root filesystem
field happens to be populated.

`currentWorkspace` is not itself a backend kind. It is a session binding that
can point at a local or remote workspace, and it may carry session state such as
the selected config, lock binding, and loaded module graph.

Required backend kinds:

- Local workspace backend: selected when the session workspace resolves to a
  local path; has a host path and owning client ID; supports host filesystem
  reads and local mutations.
- Remote workspace backend: selected when the session workspace resolves to a
  remote git workspace such as `dagger -W github.com/acme/project`; has a git
  source and source-backed filesystem view; supports read-only workspace
  operations and module loading for the current session, but has no host path.
- Directory backend: selected by `Directory.asWorkspace`; has a `Directory`
  source and no host path; supports source-backed filesystem reads.
- Git backend: selected by `GitRepository.asWorkspace`; has a `GitRepository`
  source and no host path; supports source-backed filesystem reads and git
  state.

The address is an identity/display value. It can participate in cache keys, but
it is not the type system for workspace behavior.

Persisted workspaces must persist the backend kind and the backend object
reference. Rehydrating a workspace must not turn a git-backed workspace into a
plain directory-backed workspace by losing the `GitRepository` backend.

## Construction

Current workspaces are constructed by session workspace selection. A local
selection uses the local workspace backend. A remote `-W` selection parses the
remote ref, resolves the selected tree, detects config inside that tree, and
loads modules for the session from that source.

`Directory.asWorkspace(cwd:)` creates a workspace whose boundary is the supplied
directory root. It does not inspect the host, does not create a git backend, and
does not attach a current-workspace module graph.

`GitRepository.asWorkspace(cwd:)` creates a workspace whose backend is the
supplied repository. It must preserve the repository identity and state:

- A local repository exposes the current worktree content, including
  uncommitted and untracked files represented by `GitRepository`.
- A remote repository exposes the selected remote state and is clean unless the
  backend itself represents changes.
- `workspace.git()` reports git state from the same `GitRepository` backend.

Constructing a git-backed workspace should be cheap. It should record the
backend and selected cwd; filesystem and git operations can fetch or materialize
the data they need. Creating the workspace must not eagerly download more of a
repository than is needed to establish the selected git state.

If a future constructor targets `GitRef`, its meaning should be explicit: the
workspace is backed by that ref. If construction stays on `GitRepository`, its
remote meaning is the repository's `HEAD`.

## Paths

`cwd` is a workspace path. It is exposed as an absolute workspace path: `/` at
the boundary, `/app` for a nested cwd. Internally, implementations may store it
however they want, but all public path behavior uses workspace paths.

For `file`, `directory`, and `findUp`:

- Relative paths resolve from `cwd`.
- Absolute paths resolve from the workspace boundary.
- Paths must be normalized and must not escape the workspace boundary.
- Implementations may validate existence lazily, but failures must refer to the
  workspace backend, not the caller host.

`findUp(name:)` searches for one path element while walking parent directories.
`name` must be a basename. Empty names, dot segments, slashes, and traversal are
invalid.

## Filesystem Reads

`workspace.file(path:)` and `workspace.directory(path:)` read from the backend:

- Local workspace backend reads from the owning client host path.
- Remote workspace backend reads from the selected remote source tree.
- Directory backend reads from the supplied `Directory`.
- Git backend reads from the `GitRepository` filesystem view.

`include`, `exclude`, and `gitignore` filtering apply to the backend content.
The gitignore root is the workspace boundary. Nested ignore files are interpreted
from the backend source. A source-backed workspace must not reject
`gitignore: true` merely because there is no host path.

For a local git backend, filesystem reads include the dirty worktree state
represented by the `GitRepository`. For a remote git backend, filesystem reads
come from the selected remote state.

## Git State

`workspace.git()` is a view of the workspace backend's git state:

- Git backend: return git state from the `GitRepository` backend. Do not require
  a materialized `.git` directory. Do not depend on `keepGitDir` or
  `discardGitDir` tree options.
- Directory backend: attempt to interpret the directory content as a git
  repository only if the content itself contains supported git metadata.
  Otherwise return "not in a git repository".
- Local workspace backend: inspect the selected host workspace as a local git
  repository.
- Remote workspace backend: report git state from the selected remote git source
  instead of requiring a `.git` directory in the materialized tree.

For git-backed workspaces:

- `workspace.git().head.commit` matches the source repository's `head.commit`.
- Local dirty state is preserved in `workspace.git().uncommitted`.
- Remote repositories report no uncommitted changes unless the backend models
  changes.
- Worktree support is determined by `GitRepository`, not by an extra
  `Workspace.git` check for a `.git` directory.

The presence or absence of `.git` in a materialized filesystem is an
implementation detail for git-backed workspaces.

## Config And Module State

Config selection walks from `cwd` up to the workspace boundary using backend
content. It never traverses outside the backend boundary. Read-only workspace
configuration APIs then read from the selected backend config. They must not
require host state.

Remote current workspaces are already full current workspaces: they select config
and gather/load modules from the remote source for the session. They should not
be grouped with bare `Directory.asWorkspace` values, which have source content
but no current-workspace module graph.

Examples:

- `configRead` reads the backend's config file, or the empty config if no config
  exists.
- `envList` lists environments from the backend config, or returns an empty list
  if no config exists.
- `moduleList` lists modules from the backend config, resolving module source
  paths relative to the config file's directory.

Generated checks, generators, services, and module settings require a loaded
module graph, not just source files. `asWorkspace` must not implicitly borrow
the caller's loaded modules. Without an explicit module graph for the synthetic
workspace, check/generator/service listing APIs return empty results
consistently across backend kinds. Module settings are unavailable until there
is a module graph for the synthetic workspace.

If synthetic workspaces later support loading modules from source, that loading
path must be explicit and must use the workspace backend as its source of truth.

## Mutations

Synthetic workspaces are read-only in this feature.

Local mutations are local-workspace-backend only:

- `init`
- `install`
- `uninstall`
- `moduleInit`
- `configWrite`
- `envCreate`
- `envRemove`
- `update`
- `migrate`

These methods must reject remote current workspaces and source-backed synthetic
workspaces with consistent local-only errors. They must not write to the caller
host, temporary materializations, or a git checkout created as an implementation
detail.

A future source mutation API should return a new `Directory`, `Changeset`, or
git-specific value instead of pretending that a source-backed `Workspace` is a
writable host workspace.

## Cache And Identity

Workspace IDs and cache keys must include the backend kind and the relevant
backend identity:

- Directory backend identity follows the `Directory` content identity.
- Git backend identity follows the `GitRepository` state used by filesystem and
  git operations, including local dirty state when present.
- Local workspace backend identity remains tied to the selected host workspace
  and owning client where host reads are involved.
- Remote workspace backend identity follows the selected remote git source,
  version, and workspace subpath.

Passing a synthetic workspace as a module argument must invalidate dependent
results when the source backend content or git state changes. It must not depend
on unrelated files from the caller's current workspace.

## Test Contract

The test suite should be readable as the feature contract.

Required coverage:

- `Directory.asWorkspace` uses the supplied directory for relative paths,
  absolute paths, `findUp`, and `gitignore` filtering.
- `GitRepository.asWorkspace` covers both local and remote repositories.
- Local git-backed workspaces expose dirty worktree content and dirty git state.
- Remote git-backed workspaces expose fetched source content and clean git
  state.
- `workspace.git()` on a git-backed workspace matches the source
  `GitRepository`, independent of `.git` materialization.
- Source-backed config reads read backend config content.
- Source-backed module/check/service listings do not borrow host-loaded state.
- Remote current workspaces selected with `-W` keep current-workspace behavior:
  config and modules come from the selected remote source, and local mutations
  reject.
- Local-only mutations reject source-backed workspaces.
- Backend classification is explicit and survives persistence.
- `findUp(name:)` rejects non-basename names.

Tests should assert caller-visible behavior, not incidental implementation
details such as address prefixes, rootfs field presence, or whether a temporary
checkout contains `.git`.
