# Workspace API

This spec describes the current merged behavior of the Workspace API.

## Scope

- In scope: `currentWorkspace`, `Workspace`, auto-injected `Workspace` function args, path/safety semantics, and caching semantics for workspace-backed calls.
- Out of scope: migration/lock/update workflows outside this API surface.

## API Surface

- Query:
  - `currentWorkspace(skipMigrationCheck: Boolean = false): Workspace!` (experimental)
- `Workspace`:
  - `id: WorkspaceID!`
  - `root: String!`
  - `clientId: String!`
  - `directory(path: String!, include: [String!] = [], exclude: [String!] = [], gitignore: Boolean = false): Directory!`
  - `file(path: String!): File!`
  - `findUp(name: String!, from: String = "."): String`

## Semantics

- Workspace detection:
  - Start from session current working directory (`.`).
  - Resolve to absolute path.
  - Walk up to nearest ancestor containing `.git`; if none, use the starting directory.
- Workspace identity:
  - `Workspace` stores both `root` and `clientId`.
  - Host filesystem operations are executed under the owning client (`clientId`), including when invoked from module runtime contexts.
- Paths:
  - All paths are sandboxed to `root`.
  - Traversal outside root via `..` is rejected.
  - Absolute paths are treated as root-relative (not host-root absolute).
- `findUp`:
  - Searches upward from `from`.
  - Stops at workspace root.
  - Returns root-relative path or `null`.

## Workspace Function Arguments

- Any function argument typed `Workspace` is made optional and auto-injected when not explicitly set.
- Workspace arguments are not exposed as CLI flags for module calls.
- `Workspace` cannot be stored as a module object field; it is function-arg-only.

## Caching

- Workspace-aware calls are content-addressed with workspace-derived content digests.
- Unchanged relevant workspace content can hit cache.
- Changes to relevant referenced content invalidate cache.
- `skipMigrationCheck` is accepted by `currentWorkspace`; current merged implementation does not apply behavior changes from this flag.

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
- There is no `extraModules` field in current merged code; the shipped module-allowlist field is `AllowedLLMModules`.
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
