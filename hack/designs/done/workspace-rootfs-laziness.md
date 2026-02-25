# Workspace Rootfs Laziness

## Problem

For local workspaces, `buildCoreWorkspace()` creates `Rootfs = host.directory(gitRoot)` — a single reference to the entire git root with no include/exclude filters. All `workspace.directory()` and `workspace.file()` calls navigate subdirectories of this one reference.

When buildkit resolves any part of this reference, it syncs the **entire git root** from the host. Include/exclude patterns passed to `ws.Directory("src", include: ["*.go"])` are applied *after* the full tree sync, not during. This negates the purpose of dynamic, lazy workspace access.

## Existing pattern

The `+defaultPath` mechanism already solves this. `ModuleSource.loadContextFromSource()` (`core/modulesource.go:596`) creates a **per-call** `host.directory(specificPath, include, exclude)` through the host client's session. Only the requested subset is synced. Filters are applied during the filesync transfer, not after.

## Fix

One shared helper abstracts the local/remote distinction. All resolvers (`directory()`, `file()`, future `glob()`/`search()`) call through it. The branching happens once.

### Add `resolveRootfs` helper

**File: `core/schema/workspace.go`**

```go
// resolveRootfs returns a lazy directory reference for a resolved workspace path.
// Local: per-call host.directory(absPath, include, exclude) via workspace client session.
// Remote: navigates the pre-fetched rootfs.
func (s *workspaceSchema) resolveRootfs(
    ctx context.Context, ws *core.Workspace, resolvedPath string, filter core.CopyFilter,
) (dagql.ObjectResult[*core.Directory], error)
```

- **Local** (`ws.HostPath() != ""`): switch to workspace client context (existing `withWorkspaceClientContext`), call `host.directory(absPath, include, exclude)`. Pattern: `loadContextFromSource` local case (`core/modulesource.go:611-658`).
- **Remote** (`ws.HostPath() == ""`): navigate `ws.rootfs.directory(resolvedPath)`, apply filters via `withDirectory` if needed. Existing code moves here unchanged.

### Simplify resolvers to use the helper

`directory()` becomes: resolve path → `resolveRootfs(ctx, ws, path, filter)` → done.

`file()` becomes: resolve path → `resolveRootfs(ctx, ws, parentDir, noFilter)` → `.file(basename)`.

### Demote `Rootfs` to internal

`Rootfs` is currently `field:"true"` (exposed in GraphQL). It's only needed internally by the remote code path in `resolveContextDir`. Make it unexported.

```go
type Workspace struct {
    rootfs   dagql.ObjectResult[*Directory] // internal, remote only
    hostPath string                          // internal, local only
    // ... public fields unchanged ...
}
```

### Stop creating eager Rootfs for local

**File: `engine/server/session.go:buildCoreWorkspace()`**

For local: don't call `host.directory(gitRoot)`. Just store `hostPath` and `ClientID`.

For remote: store cloned git tree in `rootfs` (unchanged).

## Content-addressed caching

No change needed. Caching in `modfunc.go` hashes *returned content*, not Rootfs. Per-call `host.directory()` references are content-hashed via `DagOpDirectoryWrapper` + `WithHashContentDir`.

## Tasks

- [x] Add `resolveRootfs` helper in `core/schema/workspace.go` — single local/remote branching point
- [x] Rewrite `directory()` and `file()` resolvers to use the helper
- [x] Demote `Rootfs` to unexported `rootfs` in `core/workspace.go`, add accessor for remote path
- [x] Stop creating eager `host.directory(gitRoot)` for local in `engine/server/session.go:buildCoreWorkspace()`
- [x] Update any remaining `Rootfs` references across codebase
- [ ] Type-check: `go build ./cmd/dagger/...` and `GOOS=linux GOARCH=amd64 go build ./engine/server/...`
- [ ] Manual e2e: verify lazy sync (local) and remote workspaces still work
