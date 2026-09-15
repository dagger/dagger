# Client Codegen Engine Cleanup

## Status

Pending. Deferred cleanup, tracked here so it isn't lost.

## Summary

Phase 1 of the client-codegen workflow adds two additive, `v1.0.0-0`-gated
engine primitives that let a `.dang` SDK module generate a TypeScript client
itself from plain data (schema JSON + dependency list) instead of the engine
driving generation through the SDK runtime:

- `ModuleSource.clientSchemaIntrospectionJSON: File!` — the client-facing schema
  (`SchemaIntrospectionJSONFileForClient`: no hidden types; **core plus the bound
  module only**, installed namespaced on `Query` — reached via `dag.<moduleName>`,
  never promoted to the Query root). The bound module's own dependencies are
  **deliberately excluded**: a client is generated for one module, not its whole
  dependency graph. Registered in `core/schema/modulesource.go`; the core-only
  base build + namespaced module install is the shared helper
  `clientSchemaIntrospectionJSONFile` (it starts from `loadDefaultSchemaBuilder`, a
  core-only `SchemaBuilder`, then installs just the bound module), reused by
  `runClientGenerator` — so both the new primitive and the old engine-driven path
  produce deps-free clients.
- `CurrentModuleAsSDKClient.moduleSource: ModuleSource!` — resolves the bound
  module from the stored `{module, pin}` via `resolveClientTargetModule` (the
  same path `clientGenerate` uses). Registered in `core/schema/module.go`,
  resolver in `core/schema/module_as_sdk.go`.

Both are purely additive. The **old engine-driven client path was intentionally
left in place** so nothing regresses while the companion `dagger/dagger`
proposal (`hack/designs/client-codegen-workflow.md`) and the split-out
`.dang` SDK module (`dagger/typescript-sdk` `design/client-gen.md`) land the
consuming side. Once the Dang SDK dispatches `generateAllClient` as a
discoverable `@generate` rollup generator, the code below becomes dead and
should be removed in a follow-up.

## What to remove once `generateAllClient` ships

Coordinate with the `.dang` SDK side landing first (it must dispatch
`generateAllClient` before these are deleted).

### Engine (this repo)

- **`workspaceSchema.clientGenerate`** and its per-client loop
  (`core/schema/workspace_client.go`): "Regenerate all generated API clients
  registered in workspace config". Replaced by the SDK-side `generateAllClient`
  rollup. Removing this also retires `workspaceClientInitGeneratedDiff`.
- **`moduleSourceSchema.runClientGenerator`** special-case
  (`core/schema/modulesource.go`) once no engine caller drives client
  generation through the SDK runtime. Note: keep
  `clientSchemaIntrospectionJSONFile` (extracted from it) — that is the new
  primitive's backing helper and is still used.
- **`AsClientInitializer` / `InitClient`** path
  (`core/schema/workspace_client.go` `clientInit` → `loadedSDK.AsClientInitializer()`
  → `InitClient`, and the corresponding SDK interface). Per companion doc Q7,
  `clientInit` becomes a pure config write (empty Changeset); the SDK emits no
  init-time files for a client. Remove the initializer selection and interface.
- The Dang SDK's `GenerateClient` stub in `core/sdk/dang_sdk.go` (currently
  returns "dang SDK does not have a client to generate") — superseded by the
  `@generate` rollup dispatch.

### `dagger/dagger` runtime (separate PR, per companion doc §6.2)

- `sdk/typescript/runtime/GenerateClient` and its client fan-out.
- `Local` lib origin end-to-end (`GenerateLocalLibrary`, `StaticLocalLib`, the
  `Local` constant, `detectSDKLibOrigin` branches).
- Client bundling in the runtime (`GenerateBundleLibrary` client arm,
  `StaticBundleClientIndexTS`, `coexistWithModule`).
- `analyzeClientConfig` (near-duplicate of `analyzeModuleConfig`).
- `CreateOrUpdate*ForClient` runtime wrappers (logic moved into the `.dang`
  side's `helpers/config-updator`).

## Notes

- The two new fields are gated `AfterVersion("v1.0.0-0")`, so they only appear
  in the CLI-1.0+ schema view, matching the gating on `CurrentModule.asSDK`.
- `clientSchemaIntrospectionJSON` must not be confused with the existing
  module-facing `introspectionSchemaJSON` (`SchemaIntrospectionJSONFileForModule`),
  which hides `TypesToIgnoreForModuleIntrospection` + `TypesHiddenFromModuleSDKs`
  and does not install the bound module at all. Feeding the module-facing schema
  to client codegen produces an incomplete/incorrect client.
- A client never promotes a module's functions to the Query root: the bound
  module is installed namespaced (`InstallOpts{}`, not `Entrypoint: true`) and
  reached via `dag.<moduleName>`. It is the only user module in the client schema
  — dependencies are excluded (see the summary), so a client has no `dag.<dep>()`
  bindings.
- Runtime serving is the companion half: a generated client must serve its bound
  module into the session at connect time for `dag.<moduleName>()` to resolve.
  Because deps are excluded here, only the bound module needs serving (never its
  deps). See `hack/designs/generated-client-module-loading.md`.
