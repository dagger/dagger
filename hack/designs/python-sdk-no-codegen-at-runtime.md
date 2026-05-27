# Python SDK: no codegen at runtime (drop the AST analyzer)

## Goal

Bring the Python SDK to parity with the Go SDK's no-codegen-at-runtime
model, using Python's *real* language model (the interpreter) instead of
the static AST analyzer:

1. **Use the language model.** Discover the module's types by
   **importing + introspecting** it (`inspect` / `typing.get_type_hints`),
   not by parsing source with `ast`. This reads runtime values
   (`logging.INFO == 20`) and sees dynamically-added members — the two
   classes of bug the AST analyzer cannot handle (#13234 and the dynamic
   decorator / functional-enum cases).
2. **Generate a static entrypoint**, like Go's generated `invoke()`:
   pre-serialized TypeDefs for the "def" phase + a static dispatch table
   for the "invoke" phase.
3. **Self-calls without engine `moduleTypes`.** Extract the module's own
   types **codegen-side** and merge them into the schema via the shared
   engine `schematool` API (`dag.Schema(...).Merge(...)`), exactly as Go
   does — no engine-side `asModule` build+run.
4. **As little analysis at runtime as possible.** The def phase becomes
   "replay pre-computed TypeDefs" (zero analysis). The invoke phase only
   imports the module and dispatches (no `get_type_hints`/AST at runtime).
5. **No codegen at runtime.** All analysis/codegen happens at
   `dagger develop` / `generate`; runtime trusts committed files
   (`codegen.legacyCodegenAtRuntime=false`), mirroring Go.

**Net effect:** the `dagger.mod._analyzer` package is **deleted**.

## Why this is feasible (and Python-native, not "like Go" mechanically)

Go cannot import a module whose function bodies reference not-yet-generated
self-call bindings — the type-checker rejects the whole package. So Go must
analyze statically (`go/types`). **Python can import it**, because
`dag.my_module().foo()` lives in a function *body* that is not executed at
import — only the signatures/decorators run. So Python can use its richest
model (execution) at generate time, get real values, and still emit a fully
static runtime artifact.

The runtime-introspection machinery already exists and is currently used on
the **invoke** path; it is simply not used to build TypeDefs:

- `_converter.to_typedef(annotation)` — live Python type → `dagger.TypeDef`
  (handles primitives, lists, enums, `Scalar`, module objects, interfaces).
  **Currently unused for registration.**
- `_resolver.ObjectType` / `Function` / `Constructor`, `_arguments.Parameter`
  — already extract fields, functions, args, defaults, docs, deprecation,
  `DefaultPath`/`Ignore`/`Name` from live decorated classes via
  `get_type_hints` + `inspect.signature`.
- `Module._objects` / `Module._enums` — the live registry populated by the
  `@object_type` / `@enum_type` decorators at import time.

## Background

Current pipeline (this branch), per the reference map:

- **Def phase:** `runtime.py` → `cli.main` → `Module.serve()` →
  `Module._typedefs()` → `_discovery.ast_register()` → `analyze_module`
  (pure AST) → `register_from_metadata` → `dag.module().with_object(...)`.
  This is the source of #13234 (AST can't evaluate `logging.INFO`).
- **Invoke phase:** `load_module()` imports the user package (decorators
  populate `Module._objects` with live `ObjectType`/`Function`), then
  `Module.invoke()` dispatches via `_objects`.
- **Codegen (`sdk/python/runtime/main.go::WithSDK`):** runs
  `codegen generate -i /schema.json -o /gen.py` against the base+deps
  schema. No self-calls gating, no static entrypoint.

Go reference (this branch), to mirror:

- **Codegen-side self-types:** `generate_module.go` loads the package
  (`go/packages`), `ModuleIntrospectionJSON` walks `go/types` to emit the
  module's own types as introspection JSON, then
  `dag.Schema(introspectionJSON).Merge(moduleTypesJSON, moduleName)`
  (engine `core/schematool.go`) produces a merged schema; binding codegen
  runs once against it. Go opts **out** of engine `asModule` moduleTypes
  (`goSDK.AsModuleTypes() → (nil,false)`).
- **Static dispatch:** generated `dagger.gen.go` carries `invoke()` — a
  `switch parentName { switch fnName … }` table plus a `case ""` arm that
  returns `dag.Module().WithObject(...)…` (the def phase). No runtime
  source analysis.
- **Skip codegen at runtime:** `go_sdk.go::useRuntimeCodegen()` reads
  `CodegenConfig.LegacyCodegenAtRuntime`; `baseWithoutCodegen` +
  `requireGeneratedFiles` build straight from committed files.

## Proposal

### Generate-time pipeline (`dagger develop` / `generate`)

Orchestrated by `sdk/python/runtime/main.go` (the Go driver has `dag`, so
it owns the `schematool` merge call, exactly like Go codegen):

```text
                 introspectionJSON (base + deps)            user source
                          │                                     │
                          ▼                                     ▼
   (1) codegen generate -i base.json -o gen.py        [base+deps bindings]
                          │
                          ▼
   (2) uv sync  (install user deps; needed to import)
                          │
                          ▼
   (3) python -m dagger.mod._introspect emit \         ← IMPORT + introspect
         --output /module-types.json                     (live _objects →
                          │                                introspection JSON)
                          ▼
   (4) [Go driver] dag.Schema(base.json)               ← engine schematool,
           .Merge(/module-types.json, modName)            shared with Go
           .Contents()  → /extended.json
                          │
                          ▼
   (5) codegen generate -i /extended.json -o gen.py    ← bindings WITH self-types
                          │
                          ▼
   (6) python -m dagger.mod._introspect entrypoint \   ← emit static entrypoint
         --module-types /module-types.json \             (typedef-builder code +
         --output src/<pkg>/_dagger_main.py              dispatch table)
                          │
                          ▼
   (7) commit gen.py + _dagger_main.py
```

- **Self-calls OFF:** skip (4) and the second (5); a single
  `codegen generate` against the base schema, plus (3) and (6). The module
  types are still introspected (for the static entrypoint) but not merged
  into the bindings.
- **Greenfield `dagger init`:** the user package is the template; step (3)
  introspects it (one object, no functions) → trivial entrypoint. Same as
  Go's empty-module case.

Steps (3) and (6) are the only new Python execs. Both **import the user
module** — which is exactly what makes runtime values and dynamic members
visible. Deps must be installed first (step 2); this is the same
requirement Go has (it must compile).

### Runtime (module call) — minimal analysis

The committed `_dagger_main.py` is the entrypoint. It does **no** AST and
**no** `get_type_hints`:

- **Def phase (`parentName == ""`):** replay pre-computed TypeDef builder
  calls — generated code of the form
  `dag.module().with_object(dag.type_def().with_object("Foo")
  .with_function(dag.function("bar", …).with_arg("level", …,
  default_value=JSON("20"))) )…`. The default `20` was resolved at generate
  time by introspection, so #13234 is fixed *and* the def phase is static.
- **Invoke phase (`parentName != ""`):** a static dispatch table
  `{("Foo","bar"): _invoke_Foo_bar, …}`; each thunk imports the user class
  (module import is unavoidable to obtain the callable), deserializes args
  with pre-known converters, calls the method, serializes the result. No
  per-call introspection.

This mirrors Go's generated `invoke()` one-to-one.

### Components

#### A. `dagger.mod._introspect` — new generate-time CLI (replaces `_analyzer`)

`sdk/python/src/dagger/mod/_introspect/__main__.py`, two subcommands:

- `emit` — import the module (reuse `cli.load_module`), walk
  `Module._objects` / `Module._enums`, serialize to **`schematool`
  `ModuleTypes` introspection JSON** (same shape Go emits). New serializer
  `live_to_introspection_json(module)` built on the existing `to_typedef` /
  `ObjectType` / `Function` / `Parameter` accessors.
- `entrypoint` — emit the static `_dagger_main.py` from the same live
  introspection: (a) a `_typedefs()` that builds the TypeDef chain with all
  values baked in, (b) a dispatch table over `(parentName, fnName)`.

Both run *after* `uv sync`, so user deps import cleanly.

#### B. `sdk/python/runtime/main.go` — orchestration + skip-codegen

- `WithSDK` gains the generate-time pipeline above: base codegen → (self
  calls) emit moduleTypes → `dag.Schema().Merge()` → regenerate → emit
  entrypoint. Gated on `m.SelfCallsEnabled` for the merge; the entrypoint
  emit is unconditional.
- `Codegen` / `ModuleRuntime` gain the `legacyCodegenAtRuntime=false`
  short-circuit (`requireGeneratedFiles` checks committed `sdk/**` +
  `src/<pkg>/_dagger_main.py`; build straight from committed files),
  mirroring `go_sdk.go`.
- The Python SDK stops implementing the engine `moduleTypes` capability
  (drop `ModuleTypesExp` / `AsModuleTypes`), so the engine no longer does
  `asModule` build+run for Python — self-types are handled codegen-side.

#### C. Python runtime (`sdk/python/src/dagger/mod/`)

- `_module.py::serve()` def phase delegates to the committed
  `_dagger_main.py` typedefs instead of `ast_register`. Invoke path
  unchanged (still dispatches via `_objects`, or via the generated table).
- `cli.py` entrypoint resolution prefers the generated `_dagger_main.py`.
- **Delete** `dagger.mod._analyzer` (parser, resolver, metadata, namespace,
  registration, visitors) and `_discovery.ast_register`.

#### D. `cmd/dagger` + engine

- `dagger init --sdk=python` writes `codegen.legacyCodegenAtRuntime=false`
  - `automaticGitignore=false`, mirroring the Go default.
- `sdk/python/runtime` itself commits its generated files and opts in.

### Invariants

- **Self-calls default off = no binding change**; merge step skipped.
- **Runtime never analyzes source** (no AST, no `get_type_hints` for the
  schema). Def phase = replay baked TypeDefs.
- **Generate-time introspection is the single source of truth** for both
  the bindings' self-types and the static entrypoint — they can't drift.
- **Real Python semantics preserved**: defaults like `logging.INFO`,
  dynamically-added functions, and functionally-built enums all surface
  because generate-time *executes* the module.

### Edge cases / honest limitations

- **Import-time self-calls** (a self-call evaluated at module/class scope,
  not in a function body) can't be introspected before bindings exist.
  Pathological; Go can't do it either. Surface a clear error.
- **Deps required at generate time.** A module with broken/missing deps
  fails `uv sync` before introspection. Same as Go needing to compile.
- **Generate-time cost** rises (import + two codegen passes for self-calls
  modules). It's develop-time only; runtime gets faster (static def phase).

## Non-goals

- Changing other SDKs (TS/Java/…).
- A general "static type-checker" path (mypy/pyright) — it can't evaluate
  runtime values, so it wouldn't fix the bugs.
- Removing the runtime module import on the invoke path (unavoidable).

## Testing

- **Unit (Python):** `live_to_introspection_json` over fixtures (objects,
  fields, functions, enums, constructors, `Annotated` metadata, defaults
  incl. `logging.INFO`, dynamic decorator, functional enum). Static
  entrypoint round-trip: generate → import → def phase equals direct
  introspection.
- **Differential:** keep `test_daggerverse_corpus.py` but point both sides
  at runtime introspection (the oracle becomes the implementation) — or
  retire it once AST is gone; decide during rollout.
- **Integration (Go):** `TestSelfCalls/python` passes via codegen-side
  merge; new `#13234` regression (`logging.INFO` default reaches the schema
  as `20`); dynamic-decorator module exposes its runtime-added function;
  skip-codegen-at-runtime default + missing-file error + regen-after-edit.

## Rollout (stg patches)

1. `sdk(python): live introspection → schematool ModuleTypes JSON`
   (`_introspect emit`, the `live_to_introspection_json` serializer; unit
   tests). No pipeline change yet.
2. `sdk(python): static entrypoint emitter` (`_introspect entrypoint`,
   `_dagger_main.py` shape; def-phase builder + dispatch table; unit
   tests).
3. `sdk(python/runtime): wire generate-time pipeline` (base→emit→merge→
   regenerate→entrypoint in `WithSDK`; self-calls gating; Python drops
   engine `moduleTypes`).
4. `sdk(python): def phase replays committed entrypoint; delete`_analyzer``
   (serve/_typedefs/cli; remove the AST package + `ast_register`).
5. `sdk(python/runtime): honor legacyCodegenAtRuntime` (Codegen/
   ModuleRuntime short-circuit, `requireGeneratedFiles`).
6. `cmd/dagger: dagger init --sdk=python writes codegen config` + opt in
   `sdk/python/runtime` itself.
7. `core/integration: python self-calls + #13234 + skip-codegen tests`.

Each patch: builds, tests green, `Signed-off-by: Yves Brissaud
<yves@dagger.io>`, no `Co-Authored-By`. Draft PR opened after patch 1;
CI iterated to green via the cache-expert trace-replay workflow.
