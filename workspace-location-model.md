# Workspace Location Model

Status: implementation handoff

## Summary

The workspace model is changing so the workspace is bound to a filesystem
boundary, not to `.dagger/config.toml`.

A Dagger client loads a workspace from a **workspace location**. Dagger derives:

- **workspace root**: the filesystem boundary, usually the git root
- **workspace cwd**: the selected location relative to the workspace root
- **workspace config directory**: the selected `.dagger/` directory for this session

`.dagger/config.toml` no longer marks the workspace root. It is configuration
for a workspace location.

Workspace config is **not layered**. One Dagger session uses one selected
workspace config directory.

## Goals

1. Make filesystem semantics simple:

   ```text
   / = workspace root
   . = workspace cwd
   ```

2. Make `-W/--workspace` work consistently for local and remote workspaces by
   treating it as a workspace location selector.

3. Avoid accidental subdirectory config writes while still supporting teams that
   own only a subdirectory of a repo.

4. Keep one Dagger client session tied to one workspace config.

5. Make external CI and Dagger Cloud Checks able to enumerate workspace config
   directories and run one independent check session per config.

## Non-Goals

- No config layering or cascading.
- No in-session aggregation of sibling workspace config directories.
- No automatic CLI discovery of all configs under a repo for one `dagger check`.
- No special interpretation of `.dagger/config.toml` as a workspace boundary.

## Terminology

| Term | Meaning |
| --- | --- |
| Workspace root | Filesystem boundary used for sandbox `/`, usually the git root. |
| Workspace location | The selected start location for loading a workspace. Comes from process cwd, `--workdir`, or `-W/--workspace`. |
| Workspace cwd | Workspace location relative to the workspace root. Sandbox `.` resolves here. |
| Workspace config directory | The physical `.dagger/` directory selected for this session. Contains `config.toml` and `lock`. |
| Selected workspace config | The single `.dagger/config.toml` selected for this session. |

Avoid these terms in user-facing text:

- workspace directory
- initialized workspace
- workspace config stack
- config layer

## Workspace Discovery

Workspace discovery returns the workspace boundary.

For local workspaces:

1. Start from the workspace location.
2. Find the git root.
3. If there is no git root, use the workspace location as the workspace root.
4. Set workspace cwd to the relative path from workspace root to workspace location.

For remote workspaces:

1. `-W` selects a remote workspace location, for example a git ref plus subpath.
2. The workspace root is the remote repo root.
3. The workspace cwd is the selected subpath inside that repo.

`.dagger/config.toml` is ignored for workspace root discovery.

## Config Selection

Workspace config is not layered.

Read selection:

1. Start at workspace cwd.
2. Walk upward toward workspace root.
3. Select the first `.dagger/config.toml` found.
4. Stop. Parent configs are not merged.

Examples:

```text
repo/.dagger/config.toml
repo/apps/web/.dagger/config.toml
```

From `repo/apps/web`, the selected config is:

```text
repo/apps/web/.dagger/config.toml
```

The root config is ignored for that session.

From `repo`, the selected config is:

```text
repo/.dagger/config.toml
```

If no config is found upward from workspace cwd, the workspace has no selected
config. Read-only config commands should report that no workspace config was
found.

## Config Write Target

All commands that write workspace config use the same target selection rule.

Default write target:

1. If a workspace config directory is selected by read selection, write there.
2. If none is selected, create and write to `.dagger/` at workspace root.

`--here` override:

```text
--here
Create or update the workspace config directory at the selected workspace cwd.
```

`--here` is idempotent. It always targets the workspace cwd, even if a parent
workspace config directory already exists.

Examples:

```text
repo/
  apps/web/
```

From `repo/apps/web`, with no existing `.dagger/config.toml`:

```bash
dagger install github.com/acme/toolchain
```

writes:

```text
repo/.dagger/config.toml
```

From the same location:

```bash
dagger install --here github.com/acme/toolchain
```

writes:

```text
repo/apps/web/.dagger/config.toml
```

From `repo/apps/web`, if `repo/.dagger/config.toml` already exists:

```bash
dagger install github.com/acme/toolchain
```

writes:

```text
repo/.dagger/config.toml
```

But:

```bash
dagger install --here github.com/acme/toolchain
```

writes:

```text
repo/apps/web/.dagger/config.toml
```

Output for config-writing commands must print the path written. When a config
directory is created, output must say where it was created.

## Lockfile Selection

`.dagger/lock` follows exactly the same selected workspace config directory as
`.dagger/config.toml`.

Reads:

```text
read selected .dagger/lock
```

Writes:

```text
write selected .dagger/lock
```

There is no lockfile merge and no root-only lockfile.

This preserves subdirectory ownership. If a team owns:

```text
repo/apps/web/.dagger/config.toml
```

it also owns:

```text
repo/apps/web/.dagger/lock
```

## Path Resolution

Use the "when in Rome" rule: a path is resolved in the coordinate system where
it is written.

### Config Paths

Paths written in `.dagger/config.toml` resolve relative to the directory
containing that config file, namely the selected `.dagger/` directory.

Example:

```toml
[modules.go]
source = "../toolchains/go"
```

In:

```text
repo/apps/web/.dagger/config.toml
```

the source resolves from:

```text
repo/apps/web/.dagger/
```

### CLI Argument Paths

CLI path arguments use CLI caller rules:

- absolute paths are host absolute paths
- relative paths resolve from the selected workspace location

This matches the `git -C` style of behavior: selecting another location changes
the base for relative CLI paths.

Examples:

```bash
dagger -W apps/web call build --source=.
```

`--source=.` resolves to:

```text
repo/apps/web
```

```bash
dagger -W apps/web call build --source=/usr/local/src
```

`--source=/usr/local/src` remains a host absolute path.

The same rule applies when `-W` selects a remote workspace location. To refer to
a path inside the selected workspace location, use a relative path.

### Sandbox and Workspace API Paths

Workspace API paths use sandbox coordinates:

- `/` resolves to workspace root
- `.` resolves to workspace cwd
- relative paths resolve from workspace cwd
- paths must not escape the workspace root boundary

Examples:

```graphql
currentWorkspace {
  directory(path: ".")
  directory(path: "/")
  file(path: "package.json")
}
```

With workspace root `repo` and workspace cwd `apps/web`:

```text
directory(".")          -> repo/apps/web
directory("/")          -> repo
file("package.json")    -> repo/apps/web/package.json
```

## CLI Behavior

### `-W/--workspace`

`-W/--workspace` selects the workspace location to load from.

Help text:

```text
Select the workspace location to load from (local path or git ref)
```

This does not select a `.dagger/` directory. It selects the location from which
Dagger derives workspace root, workspace cwd, and selected workspace config.

### `--workdir`

`--workdir` remains a local process cwd selector. For local workspaces, changing
`--workdir` can have the same workspace-location effect as `-W`.

`-W` is the preferred user-facing workspace selector because it also supports
remote workspace locations.

### `--here`

Add `--here` to commands that write workspace config or lock state.

Initial commands expected to use the shared write-target rule:

- `dagger install`
- `dagger config <key> <value>`
- `dagger settings <module> <key> <value>`
- `dagger env create`
- `dagger env rm`
- `dagger module init` when it auto-installs a workspace module
- workspace lock/update commands

Read-only commands should not create config directories.

### `dagger init`

Top-level `dagger init` should stop meaning "initialize a workspace".

Recommended behavior:

- hide `dagger init`
- make it a deprecated alias to `dagger module init`
- print a short deprecation notice:

  ```text
  `dagger init` is deprecated. Use `dagger module init` instead.
  ```

`dagger workspace init` should also be reviewed because the new model has no
"initialize workspace" operation. Prefer implicit config creation through
config-writing commands.

### Workspace Introspection

Add precise subcommands:

```bash
dagger workspace root
dagger workspace cwd
dagger workspace config-file
```

Expected output:

- `workspace root`: host path or remote address for the workspace root
- `workspace cwd`: path relative to workspace root, `.` for root
- `workspace config-file`: selected `.dagger/config.toml`, or a clear "none"
  result if no config is selected

`dagger workspace info` may remain as a summary, but the precise commands should
be the primary interface for scripts and debugging.

## GraphQL API

Cut cleanly to the new model. Do not keep fields that encode the old distinction
between workspace boundary, workspace path, and workspace cwd.

Proposed `Workspace` shape:

```graphql
type Workspace {
  """Canonical address of the selected workspace location."""
  address: String!

  """Current location within the workspace root. "." means the root."""
  cwd: String!

  """Selected workspace config directory, relative to the workspace root."""
  configDirectory: String

  """Selected workspace config file, relative to the workspace root."""
  configFile: String

  """Whether a workspace config file was selected."""
  hasConfig: Boolean!

  """The client ID that owns this workspace's host filesystem."""
  clientID: String!

  """Return a Directory from the workspace sandbox."""
  directory(path: String!): Directory!

  """Return a File from the workspace sandbox."""
  file(path: String!): File!

  """Walk upward from a workspace path, stopping at the workspace root."""
  findUp(name: String!, from: String = "."): String
}
```

Remove:

- `Workspace.path`
- `Workspace.initialized`
- `Workspace.configPath`
- `Workspace.cwd()`
- `WorkspaceCwd`

Keep existing workspace operations unless the new model requires a behavior
change:

- `install`
- `moduleInit`
- `configRead`
- `configWrite`
- `envList`
- `envCreate`
- `envRemove`
- `moduleList`
- `checks`
- `generators`
- `services`
- `refreshModules`
- `update`
- `migrate`

The filesystem behavior changes are limited to `Workspace.directory`,
`Workspace.file`, and `Workspace.findUp`:

```text
/             = workspace root
.             = workspace cwd
relative path = workspace cwd
```

No broader API reshaping should be included just because the API is being
touched.

## CI and Dagger Cloud Checks

One Dagger client session uses one selected workspace config.

The CLI and engine do not aggregate multiple workspace configs in one session.

For external CI:

- a CI job can enumerate `.dagger/config.toml` files
- it can run one Dagger session per config directory
- those sessions can run in parallel

For Dagger Cloud Checks:

- scan the repo for workspace config directories
- run one `dagger check` session for each selected workspace location
- run those sessions in parallel

This keeps the engine model simple and gives managed CI a clean way to support
multi-team repos.

## Implementation Plan

### 1. Split Workspace Root Discovery From Config Selection

Update workspace detection so it returns only:

- workspace root
- workspace cwd

It must not use `.dagger/config.toml` to decide the root.

Affected area:

- `core/workspace/detect.go`
- `engine/server/session_workspaces.go`

### 2. Add Shared Config Target Selection

Introduce one helper for selecting workspace config targets.

It should support:

- read selection: nearest `.dagger/config.toml` upward from workspace cwd
- write selection: selected config, root fallback, `--here` override
- local host paths for writes
- remote/rootfs reads for remote workspaces

All config-writing commands must use this helper.

### 3. Move Lock Handling To The Selected Config Directory

Update lock path helpers so they use the selected workspace config directory,
not unconditional workspace root.

Affected areas:

- engine workspace lock state
- schema lockfile helpers
- workspace update/refresh commands

### 4. Update Workspace API Path Resolution

`Workspace.directory`, `Workspace.file`, and `Workspace.findUp` should resolve:

- relative paths from workspace cwd
- absolute paths from workspace root

Remove `Workspace.cwd()` and `WorkspaceCwd`. Expose `Workspace.cwd` as a string
field instead.

### 5. Update Module Loading

Module loading must use the selected workspace config only.

When resolving local module sources from config:

- resolve relative to the selected `.dagger/` directory
- do not merge parent configs

Legacy `dagger.json` compatibility should continue to apply only when no
workspace config is selected.

### 6. Update CLI Commands

Add `--here` to config-writing commands and route all writes through the shared
target selection helper.

Update command output to state:

- config file written
- lockfile written when relevant
- config directory created when relevant

Hide/deprecate top-level `dagger init` as described above.

### 7. Update Docs and Generated CLI Reference

Replace old terminology:

- workspace path
- workspace directory
- initialized workspace
- config stack/layering

With:

- workspace root
- workspace location
- workspace cwd
- workspace config directory
- selected workspace config

## Test Plan

### Workspace Discovery

- A nested `.dagger/config.toml` does not change workspace root.
- In a git repo, workspace root is the git root.
- Workspace cwd is relative to the selected workspace location.
- Without git, workspace root is the selected location.
- Remote `-W` with a subpath sets root to repo root and cwd to the subpath.

### Config Selection

- From a subdir with its own config, select the subdir config.
- From the repo root, select the root config.
- If both root and subdir configs exist, do not merge them.
- If no config exists upward from cwd, reads report no selected config.

### Config Writes

- From a subdir with no config anywhere, write creates root `.dagger/config.toml`.
- From a subdir with root config, write updates root config.
- From a subdir with subdir config, write updates subdir config.
- `--here` creates or updates cwd `.dagger/config.toml` even when parent config exists.
- All config-writing commands share the same behavior.

### Lock Writes

- Lock writes land beside the selected config.
- `--here` moves lock writes to cwd `.dagger/lock`.
- No root lock is created for a subdir-owned config unless root is the selected target.

### Path Resolution

- Workspace API `directory("/")` resolves to workspace root.
- Workspace API `directory(".")` resolves to workspace cwd.
- Workspace API relative paths resolve from workspace cwd.
- Workspace API paths cannot escape workspace root.
- Config local sources resolve relative to selected `.dagger/`.
- CLI relative path args resolve from selected workspace location.
- CLI absolute path args remain host absolute paths.

### CLI and UX

- `-W` help says "workspace location".
- Config-writing output prints the exact config file path.
- First config creation output prints the created config directory.
- `dagger workspace root`, `cwd`, and `config-file` return script-friendly output.
- Top-level `dagger init` is hidden and prints the deprecation notice.

### GraphQL API

- `Workspace.cwd` returns the workspace cwd string.
- `Workspace.configDirectory` returns the selected config directory or null.
- `Workspace.configFile` returns the selected config file or null.
- `Workspace.hasConfig` is true only when a config file is selected.
- Removed fields are absent from generated SDKs.
- `Workspace.directory(".")` resolves to workspace cwd.
- `Workspace.directory("/")` resolves to workspace root.
- `Workspace.findUp` defaults to searching from workspace cwd.

## Open Implementation Notes

- Decide whether `dagger workspace config-file` should print nothing, `none`, or
  return a non-zero exit code when no config is selected.
- Decide whether `dagger workspace init` is hidden, deprecated, or removed.
