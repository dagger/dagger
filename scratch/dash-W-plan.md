# dash-W Implementation Plan

## Goal

Add a hidden but functional global `-W, --workspace` flag on `dagger` so CLI commands can select an explicit workspace without changing the existing engine workspace model.

## Scope

- Keep the change as CLI plumbing: parse a global workspace ref and forward it through `client.Params.Workspace`.
- Preserve existing `dagger session --workspace` behavior while avoiding duplicated forwarding logic.
- Add focused tests for flag registration, param forwarding, and one integration contract for local workspace selection.
- Do not lift workspace-branch integration scaffolds that depend on native workspace config/env/lock refactors.

## Implementation

1. Add a package-level `workspaceRef` in `cmd/dagger`.
2. Register hidden global `--workspace, -W` in `installGlobalFlags`.
3. Add a small helper that applies the global workspace ref to `client.Params` only when the caller has not already set `Params.Workspace`.
4. Call that helper from `withEngine`.
5. Reuse the helper in session param construction so `dagger session --workspace` and global `dagger -W ... session` converge on the same path.
6. Add unit coverage in `cmd/dagger` for hidden global flag registration and precedence.
7. Add focused integration coverage that proves `-W <local-dir>` selects a different workspace than the command cwd.

## Verification

- `go test ./cmd/dagger`
- Targeted integration test for the new `-W` behavior.
- Review `git diff` for duplicated code, accidental public help exposure, and unnecessary workspace-branch coupling.
