# Consolidating `moduleTypes` into `codegen`

## Status: Proposed (`no-codegen-at-runtime` branch)

This is the first phase of the broader "no codegen at runtime" goal. It removes
the `moduleTypes` SDK entrypoint and folds type discovery into the same
`codegen` call that already generates module bindings. Self-calls keep working
through generated code. The engine falls back to the single empty-function-name
path it had before `moduleTypes` was split out.

Later phases (not covered here) will attack the runtime codegen invocation
itself; this design only reshapes the codegen/typedef boundary.

## Goals

- Delete the `moduleTypes` SDK interface and every SDK-side implementation of
  it.
- Reinstate the engine's unified "call the runtime container with an empty
  function name" path as the only way the engine materializes a module's
  typedefs.
- Move the "append module-defined types to the introspection JSON" step from
  the engine into the SDK's own `codegen` call, so a single SDK invocation
  produces everything the module needs to build and run, including self-call
  bindings.
- Provide a shared schema-manipulation tool (new subcommands in `cmd/codegen`)
  so SDKs don't re-implement the merge in each language.

### Non-goals

- Removing codegen entirely from runtime containers — that is the headline
  long-term goal and will need its own design.
- Reworking the introspection JSON shape or the GraphQL schema semantics.
- Changing `ClientGenerator` / `generate-client`.

## Current state

Today the engine orchestrates two distinct SDK calls for a module with
self-calls enabled:

```
asModule (engine)
├── if SDK has moduleTypes AND self-calls enabled:
│     ModuleTypes(deps, source)  →  returns ModuleID
│     mod.Deps = mod.Deps.Append(mod)   # self added to deps
│
└── else:
      Runtime(deps, source)  →  container
      container.withExec([])  (empty function name)  →  ModuleID

codegen (separate engine call, later)
  Codegen(deps, source)  →  generated directory
    # deps here already include self when moduleTypes ran,
    # because asModule appended mod to Deps above
```

Key files:

- `core/sdk.go` — `ModuleTypes` interface (lines 352–385), `SDK` aggregate
  (lines 396–408).
- `core/sdk/module_typedefs.go` — `moduleTypes` dispatch for module-based SDKs.
- `core/sdk/go_sdk.go:227` — Go SDK's `ModuleTypes` implementation, runs
  `codegen generate-typedefs` in a container.
- `core/sdk/consts.go:32-39` — `sdkFunctions` list includes `"moduleTypes"`.
- `core/schema/modulesource.go:2519` — engine's self-append branch in
  `runGeneratedCodeDirectory`.
- `core/schema/modulesource.go:2909` — `runModuleDefInSDK`'s branch on
  `typeDefsEnabled`.
- `core/schema/modulesource.go:3097` — `initializeSDKModule`'s self-append.
- `sdk/python/runtime/main.go:195` — Python SDK's `ModuleTypesExp` (experimental).
- `sdk/python/runtime/template/runtime.py:8` — `--register` entrypoint.
- `cmd/codegen/generator/go/generate_typedefs.go` — Go SDK's typedef subcommand.
- `cmd/codegen/generator/go/generate_module.go` — Go SDK's bindings subcommand.

## Target state

Everything happens in the SDK's single `codegen` call:

```
asModule (engine)
  Runtime(deps, source)  →  container
  container.withExec([])   →  ModuleID
    # deps here do NOT include self; the generated code already baked
    # the module's own types into the entrypoint dispatcher

codegen (SDK does all three phases)
  1. receive introspection JSON (deps only; no self)
  2. analyze user source code        →  TypeDefs for module-defined types
  3. if self-calls:
        codegen merge-schema  →  new introspection JSON with self included
     else:
        keep introspection JSON as received
  4. generate bindings + entrypoint dispatcher against that JSON
  5. entrypoint dispatcher, on empty function name, returns the ModuleID
     reflecting the TypeDefs extracted in step 2
```

The invariant is: **introspection JSON arriving at the SDK codegen entry never
includes the module's own types.** The SDK adds them if self-calls is on; the
result never leaves the SDK runtime — the engine never sees the merged JSON.

## Component responsibilities

| Component | Responsibility |
|---|---|
| **Engine** (`core/schema/modulesource.go`, `core/sdk.go`) | No longer routes to `ModuleTypes`. Always calls `Runtime` + empty function name to get a module's typedefs. Passes only deps introspection to codegen. |
| **`cmd/codegen` schema subcommands** | New `inspect-schema` and `merge-schema` subcommands. Schema-agnostic JSON helpers, language-agnostic. |
| **Go SDK codegen** (`cmd/codegen/generator/go/…`) | New AST-based source analyzer (Phase 1, replacing `packages.Load`). Uses the schema-merge library in-process (Phase 2). Generates bindings + entrypoint dispatcher. |
| **Module-SDK codegen** (Python, TypeScript, …) | Each SDK's own codegen handles Phase 1 natively in its language. Subprocess-invokes `codegen merge-schema` for Phase 2. Runs its own bindings generator for Phase 3/4. |
| **SDK dev toolchains** (`toolchains/*-sdk-dev`) | Each SDK's dev toolchain compiles `cmd/codegen` and drops the binary into the SDK source tree, same pattern as TypeScript today (`toolchains/typescript-sdk-dev/ts-sdk.dang:230`). |

## New `cmd/codegen` subcommands

### `codegen inspect-schema`

Read-only queries against an introspection JSON file. Lets SDKs answer "is
this type from the schema or module-defined?" during Phase 1 without parsing
JSON themselves.

```
codegen inspect-schema --introspection-json-path schema.json <subcommand>
```

Initial subcommands:

- `list-types [--kind object|interface|enum|scalar]` — JSON array of type names.
- `has-type --name <T>` — exit 0/1, prints `true`/`false`.
- `describe-type --name <T>` — prints the type's JSON entry.

All output goes to stdout as JSON for easy shell piping.

### `codegen merge-schema`

Takes introspection JSON plus a TypeDefs JSON file (the module's own types),
produces a merged introspection JSON.

```
codegen merge-schema \
  --introspection-json-path schema.json \
  --module-types-path types.json \
  --output merged.json
```

Input `types.json` uses the existing engine `Module` / `TypeDef` JSON shape —
the same shape that `dag.Module().WithObject(...).ID()` already produces. No
new vocabulary to learn. If emitting that shape turns out painful for non-Go
SDKs, we move to a purpose-built minimal format as a follow-up.

Behavior:

- Parse the introspection JSON into `introspection.Schema`.
- Convert input TypeDefs into `introspection.Type` entries via a helper in
  `cmd/codegen/schematool/` (reusable both as a library and from the CLI).
- Append types to the schema, preserving directive metadata
  (`@sourceModuleName`, default-path, etc.).
- Write the merged JSON.

### Internal layout

New package `cmd/codegen/schematool/`:

- `schematool.go` — core merge + inspect logic. Usable as a library; the Go
  SDK's in-process codegen imports it directly.
- `cli.go` — Cobra wiring for the two new subcommands.

Existing `cmd/codegen/introspection/` already has the `Schema` and `Type`
types `schematool` builds on.

### Explicit non-responsibilities

- `cmd/codegen` does not run language-specific source analysis. Each SDK owns
  that.
- It does not generate bindings. Each SDK owns that.
- It knows nothing about runtime containers, paths, or module configuration.
  Pure JSON in, pure JSON out.

## Per-SDK codegen flow

### Go SDK (pilot)

The Go SDK's codegen binary *is* `cmd/codegen`, so Phase 1/2/3/4 run in-process
(no subprocess). `generate-module` is rewritten:

```
cmd/codegen/generator/go/generate_module.go  (rewritten)
  1. Load introspection JSON (deps only).
  2. Phase 1 — source analysis (NEW, AST-only, replacing packages.Load):
       parse all .go files in module source path with go/parser
       walk AST:
         - named structs            → ObjectTypeDef
         - named interfaces         → InterfaceTypeDef
         - typed constant groups    → EnumTypeDef
         - methods on module types  → Functions
       resolve type references against:
         - the parsed package
         - import paths:
             "dagger.io/dagger"  → look up in introspection JSON;
                                   reject if not found
             stdlib primitives  → known set
             anything else      → clean error naming the unsupported type
       emit []core.TypeDef using the engine TypeDef JSON shape.
  3. Phase 2 — schema merge (self-calls only):
       if self-calls on:
         schematool.Merge(introspection, typeDefs)  →  merged schema
       else:
         schema = introspection (unchanged)
  4. Phase 3 — bindings generation:
       reuse existing template code with the (possibly merged) schema.
       emit dagger.gen.go + internal/dagger/**
  5. Phase 4 — entrypoint dispatcher:
       the template already emits a main() that dispatches on parent-name.
       when parent-name is empty, return the ModuleID reflecting the
       Phase-1 TypeDefs — identical to what today's TypeDefs() template does,
       just fed from the AST scan instead of packages.Load output.
```

**Legacy path retention**: the `packages.Load`-based analyzer is kept in
isolated files (`*_legacy.go` with `//go:build legacy_typedefs` or equivalent)
and a `--legacy-typedefs` flag. Default is the new AST path. Delete in PR 2
once soak validates parity.

**Files deleted in the Go SDK pilot PR (final commit)**:

- `cmd/codegen/generate_typedef.go` — subcommand no longer needed.
- `cmd/codegen/generator/go/generate_typedefs.go` — merged into generate_module.
- `core/sdk/go_sdk.go`'s `ModuleTypes` method and `AsModuleTypes` override.

### Module-SDK template

Each module-SDK's codegen grows the same three-phase flow, language-native:

```
<sdk>/codegen  (language-native)
  1. Load introspection JSON (deps only).
  2. Phase 1 — source analysis (language-specific):
       - Python: walk the user package via importlib / AST
       - TypeScript: parse with the TS compiler API
       - PHP / Elixir / Java / Rust: each uses its current analyzer
       Output: module TypeDefs written to a temp file,
       using the engine TypeDef JSON shape.
  3. Phase 2 — schema merge (self-calls only):
       subprocess: /codegen merge-schema
                     --introspection-json-path <X>
                     --module-types-path <Y>
                     --output <Z>
  4. Phase 3 — bindings generation (language-specific):
       the SDK's own generator runs on the (possibly merged) JSON
       and writes the language's bindings + module scaffold.
  5. Phase 4 — entrypoint dispatcher:
       the language's runtime entrypoint, when called with empty parent
       name, returns the TypeDefs captured in Phase 1 (serialized and
       baked into generated code) — not re-derived at runtime.
```

Per-SDK dev-toolchain work: each `toolchains/<sdk>-sdk-dev/` module adds a
`binary` target building `./cmd/codegen` and bundling the result into the SDK
source tree at `/codegen`, mirroring the existing TypeScript pattern.

Per-SDK deletion (in that SDK's migration PR):

- `ModuleTypes` / `ModuleTypesExp` methods.
- Runtime `--register` entry (Python) or equivalent.

### Rollout interplay

During the pilot window (Go migrated, module-SDKs still on `moduleTypes`):

- Engine keeps the `AsModuleTypes()` branch in `runModuleDefInSDK`.
- Go SDK stops implementing `AsModuleTypes`. Engine falls through to the
  empty-function-name path automatically.
- Module-SDKs keep implementing `AsModuleTypes`. Engine keeps routing them the
  old way.
- Once the last module-SDK migrates, a final PR deletes the `ModuleTypes`
  interface, `AsModuleTypes`, and the engine branch.

## Engine-side changes

### `core/sdk.go`

- Delete `ModuleTypes` interface (current lines 352–385).
- Remove `AsModuleTypes() (ModuleTypes, bool)` from the `SDK` interface.
- Keep `Runtime`, `CodeGenerator`, `ClientGenerator` unchanged.

### `core/sdk/` package

- Delete `core/sdk/module_typedefs.go` entirely.
- Remove `"moduleTypes"` from `core/sdk/consts.go`'s `sdkFunctions`.
- Remove `AsModuleTypes` from all three SDK implementations
  (`core/sdk/go_sdk.go`, `core/sdk/module.go`, `core/sdk/dang_sdk.go`).
  These deletions are staged across rollout steps — see
  "Rollout-safe ordering" below.

### `core/schema/modulesource.go`

- `runGeneratedCodeDirectory` (around line 2519): remove the
  `if _, ok := srcInst.Self().SDKImpl.AsModuleTypes(); ok && isSelfCallsEnabled(srcInst)`
  branch that calls `asModule` and appends mod to deps. Deps passed into
  codegen are always the untouched dep set now.
- `runModuleDefInSDK` (around line 2909): delete the `typeDefsEnabled` branch.
  Always take the `runtimeImpl.Runtime(...)` + empty-function-name path.
- Around line 2996: delete
  `if typeDefsEnabled && isSelfCallsEnabled(...) { mod.Deps = mod.Deps.Append(mod) }`.
  Self is no longer appended at engine level.
- Around line 3097: same cleanup in `initializeSDKModule`.

### Semantics after the change

- **Generated bindings** carry self-call methods because Phase 2 ran at
  codegen time and baked self-types into bindings. `dag.MyModule().SomeFn()`
  in user code routes through the engine the same way deps calls do.
- **Engine-side `mod.Deps`** no longer includes `mod`. External callers
  introspecting the module's schema still see the module's types because the
  types are registered through the normal entrypoint-empty-name response —
  the same mechanism that registers any module today.
- Schema visibility for external callers is unchanged. Self-call support is
  now purely a codegen concern.

### Rollout-safe ordering

The engine changes land in **two** steps so nothing breaks mid-flight:

- **Step A** (lands with the Go pilot, module-SDKs unchanged): nothing in the
  engine is deleted. Go SDK just stops advertising `AsModuleTypes`; the
  existing `AsModuleTypes()` branch continues to serve module-SDKs.
- **Step B** (lands after the last module-SDK migrates): deletes the
  `ModuleTypes` interface, `AsModuleTypes`, the engine branches, and
  `core/sdk/module_typedefs.go`.

## Binary distribution

Mirrors the TypeScript SDK pattern:
`toolchains/typescript-sdk-dev/ts-sdk.dang:230`,
`sdk/typescript/runtime/lib_generator.go:34`.

- Each SDK dev toolchain gains a `binary` target that builds `./cmd/codegen`
  with `-ldflags "-s -w"` and exports the file.
- The SDK release pipeline places that file at `/codegen` inside the SDK
  source tree before publishing.
- The SDK's runtime module mounts it with `sdkSourceDir.File("/codegen")` and
  invokes subcommands.

**Multi-arch**: the Go SDK's release pipeline already produces multi-arch
binaries; the existing TypeScript toolchain handles this path for TS. Each
new SDK's release pipeline must be updated to cover the arches it ships.

Some SDKs currently don't carry any Go-built artifact in their source tree
(Python has its own Python codegen; Elixir, PHP have none). For those,
`/codegen` is a net-new shipped file — the SDK's release checklist,
`.gitignore`, and package manifest need an update. The migration PR for each
SDK is responsible for these updates.

## Rollout plan

Pilot-then-propagate, Go-first.

### PR 1 — `cmd/codegen` schema subcommands + Go SDK AST pilot

Single PR, organized as reviewable commits:

1. New `cmd/codegen/schematool/` package (merge + inspect), subcommands
   `inspect-schema` and `merge-schema`. Unit tests.
2. New `cmd/codegen/generator/go/astscan/` AST analyzer. Unit tests + golden
   files covering top-level structs, interfaces, enums, methods, import
   resolution, error cases.
3. Rewire `cmd/codegen/generator/go/generate_module.go` to use the AST
   analyzer + `schematool.Merge` in-process. Drop `generate-typedefs` from
   the default Go SDK flow. Old `packages.Load`-based path moved into
   clearly-isolated files, gated by `--legacy-typedefs` flag. Default is the
   new path.
4. `core/sdk/go_sdk.go`: `AsModuleTypes` is kept (the `SDK` interface still
   requires it during the pilot window) but returns `nil, false`. The
   `ModuleTypes` method body on `*goSDK` is deleted. Engine's
   empty-function-name fallback takes over for Go. No other engine changes
   yet — the `ModuleTypes` interface itself and its dispatch branches are
   removed later, in the final cleanup PR.
5. Integration tests covering self-calls and non-self-calls for Go modules
   on the new path.

End-state of PR 1: Go SDK migrated end-to-end. Module-SDKs untouched. Schema
subcommands available for downstream PRs. Legacy Go path retained for
rollback but clearly deletable.

A Go-module user with self-calls enabled (via `moduleSource.withExperimentalFeatures`
or the corresponding `dagger init` / `dagger develop` flag) can already
exercise the new flow after PR 1.

### PR 2 — delete legacy Go path

After a release cycle of soak, a small PR removes the `//go:build
legacy_typedefs` files, the `--legacy-typedefs` flag, and unused helpers
(`ensureDaggerPackage`, the `packages.Load`-specific `loadPackage`, etc.).
Clean diff, low risk.

### PR 3..N — module-SDK migrations (one PR per SDK)

Suggested order, by integration-test maturity:

- TypeScript
- Python
- PHP
- Elixir
- Java
- Rust
- Dotnet
- Cue

Each PR:

- SDK dev toolchain builds `cmd/codegen` and ships it in the SDK source tree.
- SDK runtime stops calling `ModuleTypes*` / `--register`. The SDK's own
  codegen does Phase 1 natively, subprocess-calls `codegen merge-schema` for
  Phase 2, then runs its bindings generator for Phase 3/4.
- SDK's module-side `moduleTypes` implementation deleted.
- Integration tests green before merge.

### Final PR — engine cleanup

- Delete `ModuleTypes` interface from `core/sdk.go`.
- Delete `AsModuleTypes` from `SDK` interface and all impls.
- Delete `core/sdk/module_typedefs.go`.
- Remove `"moduleTypes"` from `sdkFunctions`.
- Remove `AsModuleTypes` branches in `runModuleDefInSDK`,
  `runGeneratedCodeDirectory`, `initializeSDKModule`.

Single commit, easy to revert if something is missed.

### Gates between steps

- Each SDK migration must pass `core/integration` module tests including the
  `TestSelfCalls` suite (`core/integration/module_test.go:6397`).
- Performance sanity: typedef-extraction wall-clock (engine perspective) must
  not regress on Python/TS/…; Go should improve. Track in each PR description.
- This design doc is updated alongside each PR as the source of truth for
  rollout status.

## Testing strategy

### Unit tests (PR 1)

- `cmd/codegen/schematool/`: golden-file merge tests. Cover object with
  functions, interface, enum, conflicting type name (error case), empty
  module, module with only constructor.
- `cmd/codegen/schematool/` inspect subcommand tests against a canned
  introspection JSON (list-types, has-type, describe-type).
- `cmd/codegen/generator/go/astscan/`: AST extraction tests against a corpus
  of minimal Go packages:
  - single struct + method
  - multiple structs with cross-references
  - interface with methods
  - string-based enum constants
  - typed return values (`*dagger.Container`, `[]*dagger.File`,
      `context.Context`)
  - unknown external type → clean error
  - deliberately-unsupported patterns (type alias to dagger type, generics
      involving dagger types) → clean error with guidance

### Integration tests (PR 1, Go SDK)

- The full existing `core/integration/module_test.go` suite runs unchanged
  on the new path. `TestSelfCalls` is the key gate.
- Parity compare: same modules built with `--legacy-typedefs` and default.
  `dagger.gen.go` should be byte-identical; divergence must be explained.
- New module covering self-calls + enum + interface + constructor in one
  shape so AST extraction exercises the surface.
- Real-world sanity: run the `.dagger/` and `toolchains/*-sdk-dev/` Dagger
  modules against the new path.

### Performance sanity (PR 1)

- Measure `asModule` wall-clock for a Go module at cold cache. New path
  should be ≥ as fast as legacy (the `packages.Load` call is skipped —
  expected 2–10× improvement).
- Capture numbers in the PR description; block merge on a regression.

### Module-SDK PRs

- Each SDK's PR runs its language-specific `module_test.go` section plus the
  `TestSelfCalls` suite for that SDK.

## Risks and mitigations

| Risk | Likelihood | Mitigation |
|---|---|---|
| AST analyzer misses an edge case that `packages.Load` caught | Medium | Legacy path retained in PR 1 behind flag; parity test compares outputs; can flip default back quickly. |
| `schematool.Merge` produces subtly different introspection JSON than engine-side append of `mod` to `Deps` | Medium | Parity test: (a) engine-appends mod the old way and dumps introspection, (b) `schematool.Merge` with the same mod types, compare. Must match. |
| Binary-distribution multi-arch gap for some SDK | Medium | Per-SDK PRs explicitly touch dev-toolchain + release pipeline; no hand-waving. |
| Self-calls semantics diverge because generated bindings bake types at codegen time while engine-side `Deps` now excludes self | Low | Deps-change was only for introspection; external queries still see module types via normal registration. Explicit test: external caller introspects a self-calls module before and after, types match. |
| Non-self-calls Go modules regress because flow changes for them too | Low | Phase 2 is skipped when self-calls off; entrypoint dispatch falls through to the already-tested `Runtime` + empty-fn path. |
| Unknown Go type expressions (e.g. generics, type aliases) surface as errors where they used to work | Medium | Crisp error message naming the unsupported construct; fix per case if a real user hits it. Prior usage in module typedefs is rare. |
| Release coordination slippage for module-SDKs leaves the engine carrying both paths longer than expected | Low (organizational) | Each SDK migration is its own PR; engine cleanup is final-PR-only. Old path disappears only when the last SDK lands, no sooner. |

### Not a risk, worth noting

- No CLI/UX visibility for end users. `dagger call` / `dagger init` /
  `dagger develop` behavior is unchanged. Risk of user-facing churn is
  near-zero.
- Cache-key behavior: `runGeneratedCodeDirectory`'s content-scoped digest
  path stays intact. Codegen output digest changes only if the generated
  code changes — the desired behavior.

## Open items to confirm during implementation

- Exact flag name and gating mechanism for the legacy Go path
  (`--legacy-typedefs` CLI flag vs env var vs build tag — leaning CLI flag
  for runtime togglability during parity testing, then deletion).
- Precise name of the new package (`cmd/codegen/schematool/` vs alternatives).
- Whether `codegen merge-schema` should validate the input TypeDefs JSON
  against a schema file (probably yes, for error-message quality).
