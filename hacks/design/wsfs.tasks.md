# WSFS Implementation Tasks

- [x] Stage 1: API and mount model scaffolding
  - Add `Container.withMountedWorkspace` schema field and container method.
  - Add `WorkspaceMountSource` to container mount union.
  - Add explicit runtime-not-implemented errors where workspace mounts would execute today.
- [x] Stage 2: Conservative caching for workspace-mounted execs (v0)
  - Force `withExec` cache key to `CachePerCall` whenever the container has any workspace mount.
- [ ] Stage 3: WSFS runtime bootstrap in `withExec`
  - [x] Add explicit WSFS runtime setup/cleanup hook in `withExec`.
  - [ ] Start/stop WSFS runtime for workspace mounts around container execution.
  - [ ] Persist per-mount writable upper-layer state across container lineage.
- [ ] Stage 4: Lazy operation mapping
  - Implement `read`, `readdir`, and `stat` mapping to workspace APIs with shallow directory listing.
- [ ] Stage 5: Validation and hardening
  - [x] Add unit tests for workspace mount detection and runtime-hook behavior.
  - [ ] Add integration tests for mount semantics, laziness, and caching behavior.
