# Workspace API

This spec describes the desired behavior of the Workspace API.

## Scope

- In scope: `currentWorkspace`, `Workspace`, auto-injected `Workspace` function args, path/safety semantics, and caching semantics for workspace-backed calls.
- Out of scope: migration/lock/update workflows outside this API surface.

## API Surface

- Query:
  - `currentWorkspace(skipMigrationCheck: Boolean = false): Workspace!` (experimental)
- `Workspace`:
  - `id: WorkspaceID!`
  - `root: String!` (absolute workspace root locator; local absolute path or canonical remote git locator)
  - `clientId: String!`
  - `directory(path: String!, include: [String!] = [], exclude: [String!] = [], gitignore: Boolean = false): Directory!`
  - `file(path: String!): File!`
  - `findUp(name: String!, from: String!): String`

## Semantics

- Workspace context:
  - Start from session current working directory (`.`).
  - Resolve to absolute path.
  - Walk up to nearest ancestor containing `.git`; if none, use the starting directory.
  - Workspace context can be local host filesystem or remote git repository context.
- Workspace access boundary:
  - If a git repository is detected, the boundary is the repository root.
  - If no repository is detected, the boundary is the starting directory (caller CWD).
- Workspace identity:
  - `Workspace` stores both `root` and `clientId`.
  - `Workspace.root` returns the absolute workspace root locator.
  - `Workspace.root` is the discovered workspace root, not the caller CWD (unless caller CWD is itself the discovered root).
  - Local example: `/Users/idlsoft/myapp/foo/bar/my/workspace`.
  - Remote git example: `https://github.com/idlsoft/myapp#:foo/bar/my/workspace`.
  - Host filesystem operations are executed under the owning client (`clientId`), including when invoked from module runtime contexts.
- Paths:
  - No filesystem sandbox layer is applied.
  - Paths resolve directly in the workspace's underlying context (host filesystem for local workspaces, repository tree for remote git workspaces).
  - All workspace path arguments (`directory.path`, `file.path`, `findUp.from`) must be absolute.
  - Relative path arguments are rejected.
  - By default, path resolution outside the workspace access boundary fails.
- `findUp`:
  - Searches upward from `from`.
  - Stops at workspace access boundary.
  - Returns an absolute path or `null`.

### Path Contract

| Field | Type | Contract | Valid examples | Invalid examples |
|---|---|---|---|---|
| `Workspace.root` | return value | Always absolute workspace locator | `/Users/alice/repo`, `https://github.com/acme/repo#:sub/dir` | `repo`, `./repo` |
| `Workspace.directory(path)` | argument | Must be absolute in workspace context | `/work`, `/work/sub` | `.`, `./sub`, `../x`, `sub` |
| `Workspace.file(path)` | argument | Must be absolute in workspace context | `/work/main.go`, `/work/sub/a.txt` | `.`, `./a.txt`, `../a.txt`, `a.txt` |
| `Workspace.findUp(from)` | argument | Must be absolute in workspace context | `/work`, `/work/sub` | `.`, `./sub`, `../x`, `sub` |
| `Workspace.findUp` | return value | Absolute path when found, else `null` | `/work/dagger.json`, `null` | `dagger.json`, `./dagger.json` |

### Failure Behavior

- Relative workspace path arguments fail with an error (e.g. `path "." must be absolute`).
- Absolute paths outside the workspace access boundary fail with an error (e.g. `outside workspace access boundary`).
- `findUp` returns `null` when no match is found before the access boundary.

### Conformance Check

Use this command in a branch-built playground. It must fail on relative path input:

```sh
dagger -m ./toolchains/engine-dev call playground \
  with-exec \
  --args=dagger --args=-M --args=-c \
  --args='.core | current-workspace | directory . --exclude=\*/* | entries' \
  combined-output
```

Expected error contains:

```text
path "." must be absolute
```

## Workspace Function Arguments

- Any function argument typed `Workspace` is made optional and auto-injected when not explicitly set.
- Workspace arguments are not exposed as CLI flags for module calls.
- `Workspace` cannot be stored as a module object field; it is function-arg-only.

## Caching

- Workspace-aware calls are content-addressed with workspace-derived content digests.
- Unchanged relevant workspace content can hit cache.
- Changes to relevant referenced content invalidate cache.

## Connect Params and Client Metadata

This section documents connect parameters that become engine `ClientMetadata`.

### Mapped from connect params to client metadata

| Connect param | ClientMetadata field |
|---|---|
| `ID` | `ClientID` |
| `Version` | `ClientVersion` |
| `SessionID` | `SessionID` |
| `SecretToken` | `ClientSecretToken` |
| `Interactive` | `Interactive` |
| `InteractiveCommand` | `InteractiveCommand` |
| `AllowedLLMModules` | `AllowedLLMModules` |
| `EagerRuntime` | `EagerRuntime` |
| `CloudAuth` | `CloudAuth` |
| `EnableCloudScaleOut` | `EnableCloudScaleOut` |

Notes:
- There is no `extraModules` field; the module allowlist field is `AllowedLLMModules`.
- `workdir` is a connect/provisioning input (`--workdir`) that affects workspace detection start path, but it is not a `ClientMetadata` field.

### Present in client metadata but derived (not direct connect params)

- `ClientHostname` (local hostname)
- `ClientStableID` (host stable ID)
- `Labels` (telemetry/VCS labels)
- `CloudOrg` (current org resolution)
- `DoNotTrack` (analytics setting)
- `SSHAuthSocketPath` (`SSH_AUTH_SOCK`)
- `UpstreamCacheImportConfig` / `UpstreamCacheExportConfig` (cache env config)
- `CloudScaleOutEngineID` (connector engine identity)
