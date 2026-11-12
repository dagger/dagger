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
- `DangVersion`, its Dang language version, e.g. `"1.0"`, `"2.1"` or `"2.4"`.

Both come from the `dang.toml` governing the module's source, which the engine
reads itself (the runtime presets the Dagger import, so Dang skips the file's
`[imports]`). See `v2/version.go`:

- `[imports.Dagger] dagger = "vX.Y.Z"` is the engine version. It wins over the
  `engineVersion` of `dagger.json`; the engine warns when the two disagree.
  `dagger = true` keeps the `engineVersion`.
- `dang = "X.Y"` is the Dang version. Without one, it is pinned by the
  module's engine version (`DefaultModuleDangVersion`), to the Dang version
  engines of that version evaluated it as:

  | engine version                                               | Dang |
  | ------------------------------------------------------------ | ---- |
  | before `engine.MinimumDangV2ModuleVersion` (v0.21.5)         | 1.0  |
  | before `Dang24ModuleVersion` (v1.0.0-beta.28)                | 2.1  |
  | `Dang24ModuleVersion` and later                              | 2.4  |

  Dang 1.0 (`.{ }` is selection) is the language of the Dang v1 line these
  modules were written for. A manifest v2 entrypoint that declares no `dang`
  version is Dang 2.1 (`DefaultEntrypointDangVersion`), whatever its engine
  version.

Which `dang.toml`: the nearest one walking up from the source directory to the
module directory, never above it (`entrypoint.FindConfigFileInContext`), so the
result does not depend on how the module was loaded. Dang's own discovery walks
further, up to a `.git`; when it would reach a `dang.toml` above the module
directory, the source runs from an isolated copy (`dangSourceDir`), so the
engine's versions decide. For a manifest v2 module, the one governing its
entrypoint, with its own defaults (`ResolveEntrypointVersions`).

A config write (`dagger develop`, `install`, ...) keeps the manifest's own
`engineVersion` (`ModuleSource.ConfigEngineVersion`), not the one from
`dang.toml`, unless `withEngineVersion` explicitly overrides it. That override
survives source and SDK mutations within the API chain; a fresh load reads
`dang.toml` again.

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

Bump `github.com/vito/dang/v2`. Dang reads source whose version nothing
declares as its current version, so the engine pins every module that declares
none (see the table above): a module keeps the version it was written in when
Dang moves on. When the new version changes the meaning of existing source,
add a cut to `DefaultModuleDangVersion` at the first engine version that embeds
it, so modules written for that engine on default to it. The resolved version
keys the module source digest, so cached results built under one version never
answer for another.

A pin covers only what Dang gates on its language version. Changes Dang makes
to every version reach every module: from vito/dang v2.4.1, bare `JSON` in a
type is Dang's builtin JSON-text scalar, not Dagger's `JSON` scalar, which is
`Dagger.JSON` (convert between them with a cast). The module entrypoint
contract keeps bare `JSON`, as released engines declare it. Values display as
Dang literals (a Dagger object as its lazy query, not its ID) in `toString`,
interpolation and `print`.

A new Dang major that breaks the v2 line's compatibility would need its own
package again; prefer a language version gate upstream.
