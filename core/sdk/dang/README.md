# Dang SDK version support

The engine embeds one Dang library, the `github.com/vito/dang/v2` line, and
evaluates every Dang module with it. A module's source is parsed as the Dang
language version it is written in: Dang keeps every language version since 1.0
in one interpreter, gating only the syntax that changed (see `dang.Version` in
`pkg/dang/version.go` upstream), so existing modules keep the semantics they
were written against. Dang 1.0 modules, once evaluated with the Dang v1 line
(vito/dang v1.0.1), run in Dang 1.0 mode (vito/dang v2.3.0 or later: earlier
releases reserved `rescue` and let the builtin `loop` win over a module's own).

## Versions

A Dang module has two versions, both on its `ModuleSource`:

- `EngineVersion`, its Dagger version: API view, naming rules, default
  function caching.
- `DangVersion`, its Dang language version, e.g. `"1.0"` or `"2.1"`.

Both come from the `dang.toml` governing the module's source, which the engine
reads itself (the runtime presets the Dagger import, so Dang skips the file's
`[imports]`). See `v2/version.go`:

- `[imports.Dagger] dagger = "vX.Y.Z"` is the engine version. It wins over the
  `engineVersion` of `dagger.json`; the engine warns when the two disagree.
  `dagger = true` keeps the `engineVersion`.
- `dang = "X.Y"` is the Dang version. Without one, a module whose engine
  version is before `engine.MinimumDangV2ModuleVersion` (v0.21.5) is Dang 1.0
  (`.{ }` is selection), the language of the Dang v1 line these modules were
  written for; a later one gets Dang's own default.

Which `dang.toml`: the nearest one walking up from the source directory to the
module directory, never above it (`entrypoint.FindConfigFileInContext`), so the
result does not depend on how the module was loaded. Dang's own discovery walks
further, up to a `.git`; when it would reach a `dang.toml` above the module
directory, the source runs from an isolated copy (`dangSourceDir`), so the
engine's versions decide. For a manifest v2 module, the one governing its
entrypoint, with its own defaults (`ResolveEntrypointVersions`).

A config write (`dagger develop`, `install`, ...) keeps the manifest's own
`engineVersion` (`ModuleSource.ConfigEngineVersion`), not the one from
`dang.toml`.

A Dang 1.0 module builds its types with full inference (`dang.RunDir`) unless
it enables self calls explicitly, as the SDK of the Dang v1 line did; later
modules only declare them (`dang.DeclareDir`).

## Layout

- `../dang_sdk.go` (package `sdk`) — `dangSDK`, the `core.SDK` behavior, which
  scopes module sources before delegating to `dangv2.Impl`.
- `shared/` (package `dangshared`) — plumbing that does not depend on the Dang
  library (nested-client proxy server, client metadata, error conversion).
  Must never import `github.com/vito/dang` of any major.
- `v2/` (package `dangv2`) — the implementation, importing
  `github.com/vito/dang/v2`.

## A new Dang language version

Bump `github.com/vito/dang/v2`. Modules that declare no `dang` version and
have no `dang.toml` follow Dang's current version; ones with a `dang.toml` that
declares none get Dang's pinned `DefaultVersion`. Modules from before Dang 2
stay on 1.0. The resolved version keys the module source digest, so cached
results built under one version never answer for another.

A new Dang major that breaks the v2 line's compatibility would need its own
package again; prefer a language version gate upstream.
