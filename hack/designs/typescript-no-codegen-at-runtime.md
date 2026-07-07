# TypeScript SDK — no codegen at runtime

Status: draft / design
Author: Tom Chauveau
Base branch: `workspace-typescrip-no-codegen-at-runtime` (on top of PR #13381)

## Implementation status (2026-07-10)

Self calls are **deferred**: the `dag.Schema().Merge()` schematool is a dev/v1
engine feature not present in the released engine available locally, so the
Codegen merge (§4.3) is not wired and the generated bindings stay deps-only (no
module self types). What is implemented on the branch now:

- **Done:** the introspection emitter (§4.2, `introspection_json.ts`) +
  `EMIT_INTROSPECTION_JSON_FILE` sink + unit test — tested, inert, staged for
  when self calls are enabled.
- **Done:** `ModuleTypes` removed from the TS SDK (method, `AsEntrypoint`, and
  the dispatcher case in `dagger.gen.go`), so type discovery flows through the
  runtime `getModDef` → the committed static entrypoint's `register()`, exactly
  like the Go SDK. `dagger.gen.go` was hand-edited (dispatcher case only) rather
  than regenerated, because a regen against the released engine would replace
  the dev-engine client; a proper dev-engine regen is still pending.
- **Done:** the no-codegen `SetupContainer` path (§4.4) for node/deno/bun +
  `requireGeneratedFiles`, plus `ModuleRuntime`'s `introspectionJSON` marked
  `+optional`. Inert until the engine omits the argument (§4.5, colleague's PR).
- **Deferred (needs dev engine):** the Codegen self-type merge (§4.3) and the
  `RuntimeCodegenDisabled`/`SelfCallsEnabled` engine wiring (§4.6).

## 1. Goal

Bring the TypeScript SDK to feature parity with the Go SDK work landed on this
branch (commits `1fd00de3`, `0a9f2a8b`, `d4b21218`):

- A module can **opt out of runtime codegen** by setting
  `codegen.automaticGitignore = false` in `dagger.json`. It then commits its
  generated files (`sdk/`, `__dagger.entrypoint.ts`, `package.json`,
  `tsconfig.json`/`deno.json`, lockfile) and the runtime **trusts them as-is**:
  no codegen pass, no entrypoint generation, no config rewriting at
  `dagger call` / `dagger functions` time. We simply build a container, mount
  the committed sources, install dependencies, and run the committed entrypoint.
- Codegen still runs at `dagger init` / `dagger develop` / `dagger generate`
  time, and it must now produce **complete** client bindings — including the
  module's own types so **self-calls** work without a runtime regeneration.
  All four self-call shapes the Go work covers (scalar fields, enum args,
  secondary-object namespacing, object-arg-by-ID via `@expectedType`) are in
  scope from the first cut.
- Remove the `ModuleTypes` SDK function from the TypeScript SDK, mirroring the
  Go SDK dropping `moduleTypes`. Type discovery moves to the runtime's
  `getModDef` path (the static entrypoint's `register()`), exactly like Go.

The net effect: for an opted-out module, `dagger call` starts a plain container
that runs `tsx __dagger.entrypoint.ts` (or `deno run` / `bun`) against the
committed tree, with zero codegen containers in the critical path.

## 2. How Go does it today (reference)

This is the model we mirror. Three moving parts:

1. **Drop `moduleTypes`** — `core/sdk/go_sdk.go`:
   - `AsModuleTypes()` now returns `nil, false`.
   - `AlwaysEnablesSelfCalls()` returns `true`.
   - The engine (`core/schema/modulesource.go` `runModuleDefInSDK`) sees
     `AsModuleTypes()==false` and falls through to `moduleDefViaRuntime`: it
     loads the runtime and calls the special empty-object/empty-function
     `getModDef` to obtain the module definition.

2. **Single-pass `generate-module` that merges the module's own types**
   (`cmd/codegen/generator/go/generate_module.go` L120-172):
   - Emit the module's own types as introspection JSON via
     `templates.NewModuleIntrospectionEmitter(...).ModuleIntrospectionJSON(moduleName)`
     (`cmd/codegen/generator/go/templates/introspect_emit.go`).
   - Merge them into the deps introspection JSON with the engine schema tool:

     ```go
     merged, err := g.Config.Dag.
         Schema(dagger.JSON(depsJSON)).
         Merge(dagger.JSON(moduleTypesJSON), moduleName).
         Contents(ctx)
     ```

   - Generate the client bindings from the **merged** schema, so the bindings
     contain the module's own types (self-calls).
   - `generate-module` therefore now needs an engine connection for Go
     (`cmd/codegen/generate_module.go`: `getGlobalConfig(ctx, lang == Go)`).

3. **Opt-out gate in the runtime** (`core/sdk/go_sdk.go` `Runtime` +
   `useRuntimeCodegen` + `baseWithoutCodegen` + `requireGeneratedFiles`):
   - `useRuntimeCodegen(src)` returns `true` (regenerate) unless
     `codegen.automaticGitignore == false` **and** the module's pinned
     `engineVersion` is at least as new as the running engine
     (`!engine.CheckVersionCompatibility(src.EngineVersion, engine.Version)`).
     Version skew ⇒ fall back to runtime codegen (so the repo's own dev modules,
     pinned to the last stable release but run against an in-dev engine, keep
     working).
   - On the no-codegen path it verifies the committed files exist
     (`dagger.gen.go`, `internal/dagger/dagger.gen.go`) and errors with a
     "run `dagger generate`" message if not, then mounts the context dir as-is
     and runs `go build` against it.

The critical helper to port is **`introspect_emit.go`**: it converts the parsed
module types into introspection JSON that matches, byte-for-byte in semantics,
what the engine would have built (namespacing, nullability, `id` Node field,
`@expectedType` on object/interface args, enum member SCREAMING_SNAKE, Query
constructor field, and the **legacy per-type `<T>ID` scalars** for pre-v0.21
schema views). The TypeScript emitter must reproduce the same shape.

## 3. TypeScript SDK today (as-is)

The TypeScript SDK runtime is itself a Dagger module written in **Go**, living
under `sdk/typescript/runtime/`. It shells out to two things:

- `cmd/codegen` (the `/codegen` binary) for client-binding and entrypoint
  generation — same binary as Go.
- `ts-introspector` (the `/bin/ts-introspector` binary, TS source under
  `sdk/typescript/src/module/introspector/` + `entrypoint/`) which parses the
  user's TS source with ts-morph into a `DaggerModule`.

### 3.1 SDK functions (`sdk/typescript/runtime/main.go`)

- `Codegen(modSource, introspectionJSON) -> GeneratedCode`
  1. `analyzeModuleConfig` → detect runtime (node/deno/bun), package manager,
     paths, base image, sdk-lib origin.
  2. per-runtime `GenerateDir(ctx)` → produces the codegen overlay:
     `package.json`, `tsconfig.json`/`deno.json`, lockfile, `sdk/` (bundled lib
     - `client.gen.ts` + per-dep `<dep>.gen.ts`), and the wrapped source dir.
     The bindings are generated **from `introspectionJSON`** (engine deps only)
     via `LibGenerator.GenerateBundleLibrary` → `GenerateBindings` →
     `codegen generate-module --introspection-json-path /schema.json`.
  3. add default `src/index.ts` template if the module has no sources yet.
  4. `NewIntrospector(...).EmitEntrypoint(...)` → runs `ts-introspector` in
     `EMIT_TYPEDEF_JSON_FILE` mode to write `typedef.json`, then
     `codegen generate-entrypoint --typedef-json-path typedef.json` to render
     `__dagger.entrypoint.ts`. That file is added to the overlay.
  5. returns `GeneratedCode` with `WithVCSGeneratedPaths([sdk/**, __dagger.entrypoint.ts])`
     and `WithVCSIgnoredPaths([...])`.

- `ModuleTypes(modSource, introspectionJSON, outputFilePath) -> Container`
  **(to be removed)**
  1. `LibGenerator.GenerateBindings(introspectionJSON, Bundle, ...)` → bindings.
  2. `NewIntrospector(...).AsEntrypoint(outputFilePath, name, src, clientBindings)`
     → returns a container whose entrypoint runs `ts-introspector` with
     `TYPEDEF_OUTPUT_FILE` set; when executed, it registers the module via
     `Register` and writes the module ID. This is the engine's `moduleTypes`
     path.

- `ModuleRuntime(modSource, introspectionJSON) -> Container`
  1. `analyzeModuleConfig`.
  2. per-runtime `SetupContainer(ctx)`.
  (`introspectionJSON` becomes **`+optional`** — see §4.4.)

- `GenerateClient(...)`, `RequiredClientGenerationFiles()` — standalone client
  generation; **out of scope** here (no runtime execution), but the merge work
  in §4.3 may be reused.

### 3.2 `SetupContainer` (per runtime — node/deno/bun)

Example `NodeRuntime.SetupContainer` (`runtime_node.go` L46-138), the others
mirror it:

- Build the SDK library lazily (`GenerateBundleLibrary(introspectionJSON, ...)`).
- In an errgroup:
  - `CreateOrUpdateTSConfigForModule` → `tsconfig.json`.
  - sync the SDK library (`sdk/`).
  - `NewIntrospector(...).EmitEntrypoint(...)` → regenerate `__dagger.entrypoint.ts`.
  - `withPackageJSON` (rewrite package.json) + package-manager setup +
    `withInstalledDependencies`.
- Assemble the final container: mount `sdk/`, mount `node_modules/@dagger.io/dagger`
  → the generated lib, mount `tsconfig.json`, overlay the wrapped source,
  mount the generated entrypoint, set entrypoint
  `tsx --tsconfig ... __dagger.entrypoint.ts`.

So **today `SetupContainer` regenerates bindings + entrypoint + config on every
`dagger call`**. That is exactly what we want to skip when opted out.

### 3.3 `ts-introspector` outputs (`introspection_entrypoint.ts`)

`scan(files, moduleName, ...)` → `DaggerModule`. Three sinks:

- `DRY_RUN` → prints the module.
- `EMIT_TYPEDEF_JSON_FILE` → `serializeModule()` (`typedef_json.ts`) → stable
  typedef JSON (carries `kind`, `isExported`, **`location`** (source file),
  `constructor`, `methods`, `properties`, arg flags…). Consumed by
  `generate-entrypoint`.
- `TYPEDEF_OUTPUT_FILE` (default) → `connection(() => new Register(result).run())`
  → registers typedefs via the GraphQL API and writes the module ID. This is
  the `moduleTypes` mechanism.

`Register` (`register.ts`) is the canonical map from `DaggerModule` →
`dag.typeDef().withObject(...).withFunction(...)...` — i.e. the source of truth
for how each TS construct becomes an engine TypeDef. **The new introspection
emitter must mirror `Register` the way `introspect_emit.go` mirrors Go's
`TypeDef`.**

## 4. Proposed changes

### 4.1 Remove `ModuleTypes` from the TypeScript SDK

- Delete `TypescriptSdk.ModuleTypes` (`main.go` L69-91) and
  `Introspector.AsEntrypoint` (`introspector.go` L32-59) if unused afterwards.
- Regenerate the TS SDK module's own `dagger.gen.go` so the `ModuleTypes`
  function disappears from its object definition. The engine then sees
  `AsModuleTypes()==false` for the TS SDK and takes the `moduleDefViaRuntime`
  path (`core/schema/modulesource.go`), i.e. it loads the runtime and calls
  `getModDef`.
- **Verified safe:** the runtime already serves `getModDef`. The generated
  static `__dagger.entrypoint.ts` dispatches the empty-`parentName` call to
  `register()` — the dynamic entrypoint does the same
  (`sdk/typescript/src/module/entrypoint/entrypoint.ts:24`:
  `if (parentName === "") result = await new Register(scanResult).run()`), and
  the static template renders an equivalent `register()` block. So type
  discovery keeps working with `ModuleTypes` gone.
- No further engine change is required: the `runModuleDefInSDK` fallback landed
  on this branch already handles a capability-less SDK, and the
  `ErrStaleSDKCapability` fallback covers sources persisted before the drop.

### 4.2 New TypeScript introspection emitter (`introspect_emit.go` equivalent)

Add a third serializer to the introspector, **in TypeScript, in the same
`ts-introspector` execution that already writes the typedef JSON** (decision
Q4). One `scan()` of the user's source produces the in-memory `DaggerModule`;
we serialize it two ways from that single scan:

- `serializeModule()` → typedef JSON (existing) → drives `generate-entrypoint`.
- new `serializeIntrospection()` → **introspection JSON** of the module's own
  types → drives the merge for client bindings.

Add `sdk/typescript/src/module/introspector/introspection_json.ts` alongside
`typedef_json.ts`, and a new env sink `EMIT_INTROSPECTION_JSON_FILE` in
`introspection_entrypoint.ts` that writes it when set (independently of, and
composable with, `EMIT_TYPEDEF_JSON_FILE`). It walks the `DaggerModule` and
produces the introspection `Response`, reusing the exact type-mapping rules
already encoded in `Register` (§3.3) — `Register` is the source of truth the way
Go's `TypeDef` is for `introspect_emit.go`.

**The consumer side already exists — this is the one big de-risk.** The TS
binding generator already understands the shape Go's emitter produces: it reads
`@expectedType` for object/interface-by-ID args and renders the legacy per-type
`<T>ID` names (`cmd/codegen/generator/typescript/templates/functions.go`
L193-284, via `legacyTypeScriptSDKCompat()` / `legacyIDName()` /
`Directives.ExpectedType()`). So **no TS `generate-module` template changes are
needed** for any of the four self-call shapes; the emitter simply has to emit
the same introspection JSON as Go, and feeding the merged schema to
`generate-module` yields self-call bindings.

The emitter must reproduce **all** of the following from `introspect_emit.go`
(this is the "keep compat with legacy iface" requirement):

1. **Objects → Object types**, methods → fields, exposed properties → fields.
   Skip any `id`-named member (engine-reserved) and append the synthetic
   **Node `id` field** (`introspectNodeIDField`) to every object and interface.
2. **Interfaces → Interface types** (methods → fields + Node `id`).
3. **Enums → Enum types**, member names via the engine's
   `gqlEnumMemberName` rule (already-conventional names kept, else
   SCREAMING_SNAKE). Validated by the Go test `can self-call with enum arguments`.
4. **Nullability** mirroring `Register`/TypeDef:
   - non-optional scalar/object/enum/list → `NON_NULL`.
   - optional scalar (`isOptional`/nullable) → strip `NON_NULL`.
   - object/interface refs → `NON_NULL` (a pointer only changes the TS type).
   - void/unknown → nullable `Void` scalar.
5. **Namespacing** of module-local type names exactly as the engine's
   `namespaceObject` (`TestBox` for type `Box` in module `test`), via a TS port
   of `namespaceTypeName`. Validated by `can self-call through a secondary
   object type`.
6. **Object/interface arguments passed by ID**: emit an `ID` scalar TypeRef and
   an `@expectedType(name: "<TypeName>")` directive, mirroring
   `introspectArgTypeRef`. Validated by `can self-call with an object argument`.
7. **Arg defaults / deprecation / descriptions**, honoring `+optional` semantics
   (only true `optional`, not every default/variadic).
8. **Query type** carrying the module's constructor field
   (`strcase.ToLowerCamel(moduleName)` → main object, with constructor args).
9. **Skip a module type whose namespaced name already exists** in the deps
   schema (engine would reject/resolve to the existing type).
10. **Legacy per-type `<T>ID` scalars** — the equivalent of
    `legacyGoSDKCompat()`: for pre-cutover schema views the client templates
    render `id` fields as a `<T>ID` alias, so every object/interface must also
    contribute a `<T>ID` scalar type. Note this now requires
    `core/schematool.go` to treat scalars as module-defined types — **already
    done on this branch** (`isModuleDefinedType` includes `TypeKindScalar`).

The "legacy iface" phrasing maps to the memory note
`ts-deps-own-files-e2e-findings` (per-type ID imports + extendable-return
augmentations). Whatever the TS client templates expect for a module's own `id`
field must be satisfied by these emitted scalars, or the generated
`client.gen.ts` won't type-check.

### 4.3 `Codegen`: merge module types, feed bindings + entrypoint

Rework `TypescriptSdk.Codegen` (and the per-runtime `GenerateDir`) so the
module's own types are merged into the schema **before** generating bindings,
mirroring Go's single-pass `generate-module`:

1. Run the introspector once over the user's source to obtain the module's own
   **introspection JSON** (§4.2) *and* the **typedef JSON** (for the entrypoint).
   A single `ts-introspector` exec can write both files.
2. Merge with the engine's introspection JSON using the schema tool from Go
   (the TS runtime is Go, so it calls `dag.Schema(...).Merge(...)` directly —
   the same call as `generate_module.go` L158-161):

   ```go
   merged, err := dag.
       Schema(dagger.JSON(depsJSON)).
       Merge(dagger.JSON(moduleIntrospectionJSON), cfg.name).
       Contents(ctx)
   ```

   `depsJSON` is the contents of the `introspectionJSON *dagger.File` argument.
   Guard with the same "dependency installed under the module's own name"
   rejection as Go (`generate_module.go` L136-141) — merge idempotency keys on
   `@sourceMap.module == moduleName`.
   The merge is performed **unconditionally** (decision Q5): TS self-calls are
   always-on, consistent with Go and Dang, so the committed bindings always
   carry the module's own types. See §4.6 on why this needs no engine flag.
3. Feed the **merged** introspection JSON to the **client-bindings generator**
   (`generate-module`), so `client.gen.ts` (+ per-dep files) now include the
   module's own types → self-calls work from committed bindings.
4. Feed the module's **typedef JSON** (module's own types, from the same scan)
   to the **entrypoint generator** (`generate-entrypoint`) — decision Q1. The
   entrypoint needs source-file `location`s to import the user's classes, which
   only the typedef JSON carries, so the entrypoint generator keeps its current
   input.

Concretely this means `LibGenerator.GenerateBindings` /
`GenerateBundleLibrary` should accept the **merged** introspection file instead
of the raw `introspectionJSON`. The merge is computed once in `Codegen` and the
resulting `*dagger.File` threaded into the lib generator.

`Codegen` always runs at `dagger develop` / `dagger generate` time and always
receives a non-null introspection JSON; the null-vs-non-null runtime gate of
§4.4 does **not** apply to `Codegen`.

Because the merge needs `dag.Schema().Merge()`, the **TS SDK runtime module must
be regenerated** against a v1 engine that ships the schema tool — the current
`sdk/typescript/runtime/internal/dagger/dagger.gen.go` has no `Schema`/`Merge`
(it only exposes `ModuleSource.IntrospectionSchemaJSON`). Mirror the Go SDK's
`chore: regenerate with v1 engine` step. The codegen container that runs the
merge must set `ExperimentalPrivilegedNesting: true` (it dials the engine),
like Go's `baseWithCodegen` addition.

### 4.4 `ModuleRuntime` / `SetupContainer`: the no-codegen path

**The runtime path is selected purely by whether `introspectionJSON` is
passed** (decision, 2026-07-09). The engine owns the opt-out gate and signals it
to the module SDK by omitting the argument:

- `introspectionJSON == nil` → **no codegen at runtime** (trust committed files).
- `introspectionJSON != nil` → **codegen at runtime** (today's behavior).

So `TypescriptSdk.ModuleRuntime` branches on `introspectionJSON == nil` and calls
either the new no-codegen `SetupContainer` path or the existing one. The TS SDK
module does **not** read `codegen.automaticGitignore` or the engine version
itself — the engine decides (§4.5). The `introspectionJSON` argument of
`ModuleRuntime` must be declared **`+optional`** so the engine can omit it.

Add an opt-out branch to each runtime's `SetupContainer`
(`runtime_node.go`, `runtime_deno.go`, `runtime_bun.go`):

- **If `introspectionJSON == nil`** (no codegen): build a container that
  1. `From(cfg.image)` with the runtime prelude (ca-certs / tsx symlink for
     node, deno/bun cache mounts as today),
  2. mounts the committed module source tree **as-is** (including committed
     `sdk/`, `__dagger.entrypoint.ts`, `package.json`/`deno.json`,
     `tsconfig.json`, lockfile),
  3. makes `@dagger.io/dagger` resolvable by mounting the committed `sdk/` at
     `node_modules/@dagger.io/dagger` (node/bun) — Deno resolves via committed
     `deno.json`,
  4. installs dependencies from the committed lockfile (reuse the existing
     package-manager install path, but **do not** rewrite `package.json` /
     `tsconfig.json` / `deno.json`, and **do not** generate a lockfile — the
     committed one is authoritative),
  5. sets the entrypoint to the committed `__dagger.entrypoint.ts`
     (`tsx ... __dagger.entrypoint.ts` / `deno run ... __dagger.entrypoint.ts` /
     bun equivalent).
  No `NewLibGenerator`, no `EmitEntrypoint`, no `CreateOrUpdate*` calls.

- **Otherwise** (`introspectionJSON != nil`): keep the current behavior verbatim
  (regenerate lib + entrypoint + config, then assemble).

- **Verify committed files exist** on the no-codegen path (mirror Go's
  `requireGeneratedFiles`): check for the committed `sdk/client.gen.ts` and
  `__dagger.entrypoint.ts` (and package.json/tsconfig or deno.json). If missing,
  fail with an actionable "run `dagger generate` and commit the generated files"
  error, rather than a confusing runtime `tsx`/module-resolution failure.

### 4.5 Deciding whether to run runtime codegen (engine-side gate)

The gate lives **in the engine**, so the decision has one implementation and the
TS SDK module stays dumb (it only checks `introspectionJSON == nil`).

For module-based SDKs the engine calls the SDK's `moduleRuntime` from
`core/sdk/module_runtime.go` (`runtimeModule.Runtime`), which today
unconditionally computes `deps.SchemaIntrospectionJSONFileForModule(ctx)` and
passes it as the `introspectionJson` argument (L36-61). Change it to:

1. Compute the opt-out decision with the existing helper
   `useRuntimeCodegen(source)` (`core/sdk/go_sdk.go`, package `sdk`, so directly
   reusable): run codegen unless `codegen.automaticGitignore == false` **and**
   the module's pinned `engineVersion` is at least as new as the running engine
   (`!engine.CheckVersionCompatibility(src.EngineVersion, engine.Version)`) —
   identical rule to Go, incl. the version-skew fallback. It reads
   `src.Self().CodegenConfig` directly from the core struct.
2. If codegen is enabled → compute the schema JSON file and pass
   `introspectionJson` as today.
3. If codegen is disabled → **omit** `introspectionJson` (skip the
   `SchemaIntrospectionJSONFileForModule` computation entirely — a nice perf
   win) so the SDK receives `null`.

Because `useRuntimeCodegen` and `engine.Version`/`CheckVersionCompatibility` all
live engine-side, the running engine version is available here — no need to
surface it to the module.

**No opt-in capability marker** (decision). The engine applies the gate for
module SDKs uniformly; the SDK's own `introspectionJSON` argument being
**`+optional`** *is* the implicit signal that it supports the no-codegen path.
An SDK that keeps the argument required simply never gets it omitted (a required
arg with no value would be a dagql error), so `automaticGitignore=false` is a
no-op for SDKs that haven't adopted the pattern — safe, and rolled out per SDK.
TS adopts it here; Go has its own engine-builtin gate in `go_sdk.go` and is
unaffected by this module path.

**`ModuleSource.codegenConfig` GraphQL field: removed** (decision). It was only
needed to read `automaticGitignore` inside the module; the engine now reads
`src.Self().CodegenConfig` directly, so revert that field (both
`core/modulesource.go` `field:"true"` tag and the
`dagql.Fields[*modules.ModuleCodegenConfig]{}.Install` in
`core/schema/modulesource.go`).

### 4.6 Self-calls enabled when there is no runtime codegen

Decision (2026-07-09): **self calls are considered enabled whenever the module
is in no-codegen mode** — i.e. whenever the engine would omit `introspectionJSON`
(§4.4/§4.5). This ties self-calls to the exact same signal as the runtime path,
with **no per-SDK `AlwaysEnablesSelfCalls()` marker**.

Two parts:

1. **Codegen always merges** the module's own types into the bindings (§4.3),
   so a no-codegen module always ships complete self-call bindings.
2. **The engine treats self-calls as enabled for that module.** Implement one
   shared predicate on `core.ModuleSource` and route both call sites through it:
   - Add `RuntimeCodegenDisabled()` on `*core.ModuleSource` (core already imports
     `engine`): returns true when
     `CodegenConfig.AutomaticGitignore == false` **and** the committed files are
     engine-compatible (`engine.CheckVersionCompatibility(src.EngineVersion,
     engine.Version)`) — the negation of Go's `useRuntimeCodegen`.
   - `SelfCallsEnabled()` (`core/modulesource.go:168`) gains a branch:
     `if src.RuntimeCodegenDisabled() { return true }` (in addition to the
     existing `SELF_CALLS` flag and `selfCallsAlwaysEnabler` paths).
   - `useRuntimeCodegen(src)` (`core/sdk/go_sdk.go`) becomes
     `!src.Self().RuntimeCodegenDisabled()`, so the runtime gate and self-calls
     enablement can never disagree.

This makes the shadow-tolerance check at `core/module.go:2226` pass for opted-out
modules (a self type may share a name with a dependency type) without any
`SELF_CALLS` flag or SDK marker — the previously-flagged edge case is now
resolved by construction.

Why no capability is needed for the *runtime resolution* itself, verified on this
branch: Go dropped `ModuleTypes` (so `typeDefsEnabled == false` in
`runModuleDefInSDK`), which means `mod.IncludeSelfInDeps` — set only under
`if typeDefsEnabled && isSelfCallsEnabled(src)` at
`core/schema/modulesource.go:3027` — is **not** set for Go, yet the self-calls
fixture (`Print`/`PrintDefault` call `dag.Test().ContainerEcho()`, module calling
itself) passes end to end. A runtime self-call resolves because the executing
module is already loaded in the nested session its `dag` talks to, plus the
merged bindings supply the types.

Note: dropping `ModuleTypes` (§4.1) means the engine's `runCodegen` self-append
(`core/schema/modulesource.go` L2483) no longer fires for TS
(`AsModuleTypes()==false`), so `Codegen` receives **deps-only** introspection
and the in-`Codegen` merge is now the only thing contributing self types to the
bindings — exactly the Go arrangement.

## 5. End-to-end flows after the change

### 5.1 `dagger develop` / `dagger generate` (codegen time) — always regenerates

1. Engine computes deps introspection JSON, calls `TypescriptSdk.Codegen`
   (always with a non-null introspection JSON).
2. `Codegen` runs the introspector → module introspection JSON + typedef JSON.
3. Merge module introspection into deps → merged.
4. `generate-module` on merged → `sdk/` bindings with self types.
5. `generate-entrypoint` on typedef JSON → `__dagger.entrypoint.ts`.
6. `GeneratedCode` overlay written to the user's tree. With
   `automaticGitignore=false` the engine does **not** write the `.gitignore`
   (`core/schema/modulesource.go` L2576-2577), so `sdk/` +
   `__dagger.entrypoint.ts` get committed.

### 5.2 `dagger call` — opted out (`automaticGitignore=false`, no skew)

1. Engine's `runtimeModule.Runtime` computes `useRuntimeCodegen(source) == false`
   → **omits** `introspectionJson`, calls `TypescriptSdk.ModuleRuntime` with
   `introspectionJSON = null`.
2. `ModuleRuntime` sees `introspectionJSON == nil` → no-codegen `SetupContainer`.
3. Verify committed files present.
4. Container: mount committed tree, mount committed `sdk/` as
   `@dagger.io/dagger`, install deps from lockfile, entrypoint = committed
   `__dagger.entrypoint.ts`. No codegen containers.
5. Engine invokes the module (getModDef via the committed entrypoint's
   `register()`, then the requested function via `invoke()`).

### 5.3 `dagger call` — default (`automaticGitignore` unset/true) or version skew

Engine's gate returns `useRuntimeCodegen == true` → passes `introspectionJson`
as today → `ModuleRuntime` takes the existing `SetupContainer` path (regenerate
lib + entrypoint + config, then run). Unchanged from today.

## 6. Files to change (checklist)

TypeScript SDK runtime (Go, `sdk/typescript/runtime/`):

- `main.go` — remove `ModuleTypes`; mark `ModuleRuntime`'s `introspectionJSON`
  `+optional` and branch on `nil`; rework `Codegen` to merge (§4.3); thread
  merged introspection file into lib generation.
- `introspector.go` — remove `AsEntrypoint`; add a mode to emit introspection
  JSON (or a new `EmitModuleIntrospection`); keep `EmitEntrypoint`.
- `lib_generator.go` — accept the merged introspection file for `generate-module`.
- `runtime_node.go`, `runtime_deno.go`, `runtime_bun.go` — add the no-codegen
  branch to `SetupContainer` (selected by `introspectionJSON == nil`);
  `requireGeneratedFiles`-style check.
- `internal/dagger/dagger.gen.go` — regenerate against a v1 engine (adds
  `dag.Schema().Merge()`), and drop `ModuleTypes` from the SDK's own definition.
- `config.go` — no change needed for the runtime gate (the engine decides). Only
  touch it if the no-codegen `SetupContainer` needs extra config fields.

TypeScript introspector (TS, `sdk/typescript/src/module/`):

- `introspector/introspection_json.ts` (new) — `DaggerModule` → introspection
  JSON mirroring `introspect_emit.go`, incl. legacy `<T>ID` scalars.
- `entrypoint/introspection_entrypoint.ts` — add the
  `EMIT_INTROSPECTION_JSON_FILE` sink (written in the same run as the typedef
  JSON).
- No `generate-module` template changes needed — `@expectedType` + legacy IDs
  are already handled (§4.2).

Engine:

- `core/modulesource.go` — add `RuntimeCodegenDisabled()` on `*ModuleSource`;
  make `SelfCallsEnabled()` return true when it holds (§4.6). Revert the
  in-progress `ModuleSource.codegenConfig` GraphQL field (§4.5).
- `core/sdk/go_sdk.go` — refactor `useRuntimeCodegen` to
  `!src.Self().RuntimeCodegenDisabled()` (single source of truth) (§4.6).
- `core/sdk/module_runtime.go` — compute `useRuntimeCodegen(source)` and omit
  `introspectionJson` (skip `SchemaIntrospectionJSONFileForModule`) when codegen
  is disabled (§4.5). No capability check.

Already done on this branch (no further change needed):

- `core/schematool.go` `isModuleDefinedType` includes `TypeKindScalar`.
- `runModuleDefInSDK` / `moduleDefViaRuntime` fallback for capability-less SDKs.
- `ErrStaleSDKCapability` handling for persisted sources.

Docs / changie entry as usual.

## 7. Testing

- Port the Go `RuntimeCodegenSuite` idea to TS:
  - `automaticGitignore=false` + pinned engine + **missing** committed files ⇒
    actionable error mentioning `dagger generate`.
  - `automaticGitignore=false` + **older** pinned engine ⇒ falls back to runtime
    codegen and succeeds (version skew, i.e. `introspectionJSON != nil`).
  - `automaticGitignore=false` + committed files present ⇒ `dagger call` runs
    with no codegen container in the trace (`introspectionJSON == nil`).
- Extend the self-calls suite (`TestSelfCalls`) to run for `sdk == "typescript"`:
  scalar field exposure, enum args (SCREAMING_SNAKE), secondary object type
  namespacing (`TestBox`), object arg by ID (`@expectedType`), self-calls as a
  dependency and transitively.
- Golden introspection JSON test for the new emitter mirroring
  `introspect_emit_test.go`.

## 8. Resolved decisions

- **Runtime gate = null `introspectionJSON`** (2026-07-09). The engine owns the
  opt-out decision and signals it by passing / omitting `introspectionJSON` to
  `ModuleRuntime`. The TS SDK module just branches on `nil`. **No opt-in
  capability marker** — the `+optional` argument is the implicit signal.
  (§4.4, §4.5.)
- **First cut supports all four self-call shapes** directly (scalar fields, enum
  args, secondary-object namespacing, object-arg-by-ID). Confirmed to need no TS
  `generate-module` template changes — `@expectedType`/legacy IDs already
  handled. (§4.2.)
- **Q1 — entrypoint generator input.** Send the module **typedef JSON** to
  `generate-entrypoint` and the **merged introspection JSON** to the client
  bindings generator. (§4.3.)
- **Q2/Q3 — reading `automaticGitignore`.** Read engine-side from
  `src.Self().CodegenConfig`; **revert** the `ModuleSource.codegenConfig`
  GraphQL field — not needed. (§4.5.)
- **Q4 — introspection emitter location.** Emit in TypeScript, in the same
  `ts-introspector` execution that writes the typedef JSON. (§4.2.)
- **Q5 — always-merge.** Merge is unconditional. **Self-calls are enabled
  whenever there is no runtime codegen** (introspectionJSON omitted): a shared
  `RuntimeCodegenDisabled()` predicate drives both the runtime gate and
  `SelfCallsEnabled()`. No per-SDK `AlwaysEnablesSelfCalls()` marker. (§4.6.)
- **Q6 — committed config files.** No runtime validation of
  `package.json`/`tsconfig.json`/`deno.json` beyond the generated-files check:
  `dagger generate` reruns `Codegen`, which updates them when needed.

## 9. Residual items to confirm during implementation

- **`introspectionJSON` optionality.** Confirm marking `ModuleRuntime`'s
  `introspectionJSON` `+optional` lets the engine omit it cleanly (null
  `*dagger.File` on the SDK side), and that no existing caller relies on it being
  required.
- **Deno / Bun no-codegen specifics.** Confirm how the committed `sdk/` is
  referenced for `@dagger.io/dagger` resolution in the committed `deno.json` /
  bun setup on the no-codegen path (node/bun mount `node_modules`; Deno uses
  imports).
