# Workspace Connect Rush-Merge Plan

## Status: Implementation Plan

This note captures the smallest mergeable slice needed to unblock check
enumeration via explicit workspace loading, with a phased rollout.

## Context

- `dagger check -l` works because it uses `currentWorkspace().checks()`.
- Dagger Cloud currently loads checks through `moduleSource(ref).checks.list()`.
- That path now only sees the direct module checks, not the workspace/toolchain
  aggregate.
- On the `workspace` branch, toolchains and blueprints are moving out of
  generic `ModuleSource` loading and into explicit workspace loading.

## Decision

Do not restore legacy multi-module behavior to `ModuleSource.checks()`.

Instead:

1. Expose explicit workspace selection on hidden `dagger session` only.
2. In phase 1, expose a matching connect option in the Go SDK only.
3. In phase 2, expand the same connect option shape to other SDKs.
4. Have callers bind a session to a workspace ref, then enumerate checks
   through `currentWorkspace().checks().list()`.

This keeps the rushed merge aligned with the workspace architecture instead of
reviving deprecated compat behavior.

## Non-Goals

- Do not make `--workspace` a public global CLI flag in this slice.
- Do not change the `dagger session` stdout handshake format.
- Do not reintroduce workspace aggregation semantics into
  `moduleSource(ref).checks()`.

## Phase 1 Scope

### 1. `dagger session`

Add hidden `-W` / `--workspace` to `dagger session` and pass it through to
`client.Params.Workspace`.

Important:

- this is session-only, not a broader CLI feature
- the value must be treated as an opaque string
- do not canonicalize it in the CLI, since it may be a git ref

### 2. Go SDK Connect Surface

Add explicit workspace selection to auto-provisioned Go SDK sessions.

Behavior:

- if a workspace is set, append `--workspace <ref>` to the spawned
  `dagger session` command
- if attaching to an existing session from environment variables, do not try
  to reinterpret or inject workspace selection
- reject conflicting explicit workspace configuration when reusing an existing
  session rather than silently ignoring it

### 3. Go Generated Clients

Generated clients should inherit the new connect option where that machinery
already exists.

- Go generated clients should expose the new option through the existing
  `dagger.ClientOpt` aliasing path

## Phase 2 Scope

Expand the same explicit workspace selection behavior to the other SDKs:

- TypeScript
- Python
- Rust
- Java
- PHP

Phase 2 should preserve the same core rules as phase 1:

- pass the workspace value through as an opaque string
- only append `--workspace <ref>` when spawning `dagger session`
- do not try to retrofit workspace selection onto reused existing sessions

TypeScript generated clients should inherit the option through shared
`ConnectOpts` as part of that phase.

## Implementation Steps

1. Add `sessionWorkspace` flag state in `cmd/dagger/session.go`.
2. Thread `sessionWorkspace` into `client.Params.Workspace`.
3. Add Go `WithWorkspace(ref string)` and plumb it into
   `sdk/go/engineconn/session.go`.
4. Expose the Go generated-client alias if needed.
5. In phase 2, add `Workspace` to TypeScript connect options and session argv
   building.
6. In phase 2, add `workspace` to Python provisioning config and session argv
   building.
7. In phase 2, add workspace config to Rust and forward it in CLI session
   startup.
8. In phase 2, add Java connect surface and plumb it into `CLIRunner`.
9. In phase 2, add PHP connect surface and plumb it into
   `ProcessSessionConnection`.
10. In phase 2, update TypeScript generated-client declarations if needed.

## Testing

Minimum coverage:

1. `dagger session` forwards `--workspace` into engine client params.
2. Go SDK appends `--workspace` when `WithWorkspace(...)` is used.
3. Existing-session reuse rejects incompatible explicit workspace settings.

Phase 2 coverage:

1. At least one non-Go SDK test verifies argv construction with workspace set.
2. TypeScript generated-client declarations are updated if the shared connect
   type changes.

Nice to have:

- a small integration test that proves a session bound to an explicit workspace
  can enumerate `currentWorkspace().checks()`

## Rationale For Phasing

Phase 1 is sufficient for Dagger Cloud, since Cloud uses the Go SDK and reaches
`dagger session` through SDK provisioning rather than calling it directly.

Phase 2 is parity work for the remaining SDKs once the Go path is merged and
validated.
