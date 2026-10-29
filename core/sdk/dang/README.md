# Dang SDK version support

The engine embeds one copy of the Dang runtime per supported Dang major
version and routes each module to the version matching the Dang language
version its source is written in, so existing modules keep the language
semantics they were written against.

## Versions

A Dang module has two versions, both on its `ModuleSource`, read by the engine
from the `dang.toml` governing its source (the nearest one walking up from the
source directory to the module directory, never above it; see
`v2/version.go`):

- `EngineVersion`: `[imports.Dagger] dagger = "vX.Y.Z"`, which wins over the
  `engineVersion` of `dagger.json` (the engine warns when they differ; a
  config write keeps the manifest's own, `ConfigEngineVersion`). Without one,
  the `engineVersion`.
- `DangVersion`: `dang = "X.Y"`. Without one, Dang 1.0 for an engine version
  before `engine.MinimumDangV2ModuleVersion` (v0.21.5), and Dang's own default
  otherwise.

Dang's own discovery walks further up, to a `.git`; when it would reach a
`dang.toml` above the module directory, the source runs from an isolated copy
(`dangSourceDir`), so the engine's versions decide.

## Layout

- `../dang_sdk.go` (package `sdk`) — the dispatcher. `dangSDK` owns all
  version-agnostic `core.SDK` behavior and picks a `dangImpl` per call via
  `dangImplFor`, by the module's `DangVersion` (Dang 1.0 routes to v1).
- `shared/` (package `dangshared`) — version-agnostic plumbing (nested-client
  proxy server, client metadata, error conversion). Must never import
  `github.com/vito/dang` of any major.
- `v2/` (package `dangv2`) — the **living** implementation, importing
  `github.com/vito/dang/v2`. All feature work happens here. New Dagger
  features always require a new `engineVersion`, which always routes to the
  newest major — so frozen majors never need feature work.
- `v1/` (package `dangv1`) — **frozen** snapshot for Dang 1.0 modules (by
  default, those with `engineVersion < v0.21.5`; Dang v1: `.{ }` is selection). Byte-identical to
  the living package at snapshot time, modulo package clause, doc comments,
  and dang import paths.

## Maintenance policy

- **Features**: living package only.
- **Bugfixes in the engine-side glue** (type conversion, call dispatch): fix
  the living package; backport to frozen packages only if the bug affects old
  modules.
- **Bugfixes in Dang itself** for old modules: land on the corresponding
  maintenance branch upstream (e.g. `release/v1` in vito/dang), tag a patch
  (e.g. v1.0.x), bump go.mod.
- **Engine-internal refactors** (`core`, `dagql`, `engine` API churn) will
  break frozen packages at compile time; apply the same mechanical fix to all
  copies.

## Adding support for a new Dang major (vN+1)

1. Upstream: tag `vN+1.0.0` on a module path with the new major suffix; keep
   a maintenance branch for the now-frozen prior major. On that maintenance
   branch, rename the tree-sitter grammar's exported C symbols with a `_vN`
   suffix (see `pkg/dang/danglang/danglang.go` on `release/v1`): C symbols
   share one global namespace, so two majors with identical symbol names
   fail to link into the same binary. The canonical `tree_sitter_dang` names
   always stay with the living major so editor integrations keep working.
2. Copy the living package to a dir for the new major (e.g. `cp -r v2 v3`),
   then in `v3/` rewrite the dang import paths to the new major and the
   package clause to `dangv3`. `v2/` is now frozen — update its package doc to
   say so (see `v1/sdk.go` for the wording).
3. Add `MinimumDangV<N+1>ModuleVersion = "<next unreleased dagger version>"`
   to `engine/version.go`.
4. Extend the newest-first ladder in `dangImplFor` (`../dang_sdk.go`) with one
   new case.
5. `go get github.com/vito/dang/v<N+1>@v<N+1>.0.0`.
6. Tests: bump the dang testdata module pins under
   `core/integration/testdata/modules/dang/` to the new gate version so the
   main suite exercises the living major, and add a pinned regression module
   for any syntax whose meaning changed (see `legacy-selection/`).

## Caveats

- In-repo dang modules (`.dagger/modules/*`, `cmd/codegen`, `modules/markdownlint`)
  are pinned to the *released* engine version (the released CLI rejects
  configs newer than itself), so they route to the previous major until the
  next dagger release ships. Keep them syntax-neutral across the boundary, or
  bump them right after the release.
- An empty/missing `engineVersion` in a `ModuleSource` normalizes to the
  current engine version (→ newest major); pre-semver configs normalize to
  v0.11.9 (→ oldest major).
