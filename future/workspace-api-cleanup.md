# Future Workspace API Cleanup

author: codex
created: 2026-06-04
status: future task
related: https://github.com/dagger/dagger/pull/13217

The current `Workspace` API has grown into a mixed surface for several different
concepts:

- filesystem views over a workspace boundary
- local host workspace configuration and mutation
- loaded module discovery
- lockfile refresh and migration workflows
- generated check, generator, and service indexes

Synthetic workspaces make this split visible. PR #13217 intentionally exposes
source-backed construction on the source objects:
`Directory.asWorkspace(cwd:)` and `GitRepository.asWorkspace(cwd:)`. Those can
honestly support a rootfs-backed filesystem view, but cannot honestly support
local-host mutation, config loading, module installation, or workspace migration
without inventing new semantics.

The goal of this future cleanup is to make the API shape explicit enough that
synthetic workspaces do not rely on a long list of special-case rejections.

## PR #13217 reviewer notes

- There is no public root constructor like `Query.workspace(dir:)`.
- Public construction is on source objects:
  `Directory.asWorkspace(cwd:)` and `GitRepository.asWorkspace(cwd:)`.
- The source object is the workspace boundary. Use `dag.Directory()` when an
  empty synthetic workspace is needed.
- `cwd` is the current working directory inside the source root and defaults to
  `/`.
- Public `cwd` values are absolute workspace paths: `/` at the boundary,
  `/services/api` for a nested cwd. Internal storage may still use `.`.
- `Directory.asWorkspace` is filesystem-only unless the supplied directory
  already contains usable `.git` metadata.
- `GitRepository.asWorkspace` keeps git metadata so `workspace.git` can work.
- Filesystem APIs are supported: `file`, `directory`, and `findUp`.
- Listing APIs with no loaded workspace state return empty results.
- Local-host/config/mutation APIs reject with local-only errors for synthetic
  workspaces.

## Required decisions

- Define the supported contract for source-backed workspace construction.
  - Public construction remains on source objects:
    `Directory.asWorkspace(cwd:)` and `GitRepository.asWorkspace(cwd:)`.
  - Must support filesystem APIs: `file`, `directory`, `findUp`.
  - The source object is the workspace boundary. Use `dag.Directory()` when an
    empty synthetic workspace is needed.
  - `Directory.asWorkspace` is filesystem-only. `workspace.git` is only expected
    to work when the root contains usable `.git` metadata, which is the intended
    reason to use `GitRepository.asWorkspace`.
  - Listing APIs with no loaded workspace state should return empty results, not
    unsupported errors.
  - Mutating APIs must either move off the synthetic-capable surface or have a
    clearly named local-only receiver.

- Separate filesystem APIs from local workspace management APIs.
  - Filesystem view: `file`, `directory`, `findUp`.
  - Local config mutation: `init`, `configWrite`, `envCreate`, `envRemove`,
    `moduleInit`, `install`.
  - Local lock/migration workflows: `refreshModules`, `update`, `migrate`.

- Decide whether config reads are part of the synthetic contract.
  - Option A: synthetic workspaces do not expose config reads.
  - Option B: config reads parse config from the supplied directory rootfs.
  - Avoid the current middle ground where `configRead` exists on the same type
    but is only valid for host-loaded workspaces.

- Decide where module-derived discovery belongs.
  - `checks`, `generators`, `services`, `moduleList`, and `envList` can return
    empty for synthetic workspaces today.
  - If synthetic workspaces should discover modules later, that should be a
    first-class loading path, not an accidental extension of current-workspace
    context injection.

- Rename or split local-only operations so their constraints are visible in the
  API.
  - Examples: local workspace manager, workspace config editor, workspace
    migration planner, or similar explicit grouping.
  - Avoid a single `Workspace` object where half the methods only work for
    host-backed instances.

## Current synthetic behavior to preserve or resolve

Construction:

- `directory.asWorkspace(cwd:)`
- `gitRepository.asWorkspace(cwd:)`

Supported:

- `workspace.file(path:)`
- `workspace.directory(path:)`
- `workspace.findUp(name:, from:)`
- `workspace.git()` when created from a git repository or another rootfs that
  contains supported `.git` metadata.

Empty listings:

- `workspace.checks().list()`
- `workspace.generators().list()`
- `workspace.services().list()`
- `workspace.moduleList()`
- `workspace.envList()`

Rejected today and requiring cleanup:

- `workspace.directory(path:, gitignore: true)`
- `workspace.init(...)`
- `workspace.configRead(...)`
- `workspace.configWrite(...)`
- `workspace.envCreate(...)`
- `workspace.envRemove(...)`
- `workspace.moduleInit(...)`
- `workspace.install(...)`
- `workspace.refreshModules(...)`
- `workspace.update(...)`
- `workspace.migrate(...)`
- `workspaceModule.settings()`

## Merge bar

Before merging synthetic workspace support, the PR should either:

- shrink/split the API so unsupported methods are not exposed on synthetic
  workspaces, or
- document and test a deliberate compatibility contract where filesystem methods
  work, listing methods return empty, and local-host/config/mutation methods
  reject with consistent local-only errors.

The final shape should make it obvious to SDK users which operations are valid
for a synthetic workspace without requiring them to learn the implementation
distinction between host-backed and rootfs-backed workspaces.
