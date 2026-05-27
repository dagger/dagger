# Python SDK no-codegen-at-runtime — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans (inline, stg patches) or superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Replace the Python SDK's AST analyzer with generate-time runtime introspection + a static entrypoint, so the engine receives correct (runtime-resolved) typedefs, self-calls work via codegen-side schema merge, and no analysis/codegen runs at module call time.

**Architecture:** At `dagger develop`/`generate`: import the user module and introspect live decorated classes → emit `schematool` ModuleTypes JSON → (self-calls) `dag.Schema().Merge()` codegen-side → regenerate `gen.py` → emit a committed `_dagger_main.py` carrying serialized typedefs + dispatch. At runtime: the def phase replays serialized typedefs (zero analysis); invoke imports + dispatches.

**Tech stack:** Python 3.10+ (`inspect`, `typing.get_type_hints`, cattrs), the existing `dagger.mod._resolver`/`_converter`/`_arguments` runtime-introspection layer, Go driver `sdk/python/runtime/main.go`, engine `core/schematool.go` (shared `Schema.Merge`), `cmd/codegen` introspection types.

**Reference anchors (mirror these):**

- Go codegen-side self-types: `cmd/codegen/generator/go/generate_module.go:103-145`, `cmd/codegen/generator/go/templates/introspect_emit.go:24-386`.
- Engine merge API: `core/schematool.go:80` (`Schema.Merge`), `core/schema/schematool.go:39-53` (dagql fields `schema`/`merge`/`contents`).
- Introspection JSON shape: `cmd/codegen/introspection/introspection.go` (`Response`/`Schema`/`Type`/`Field`/`InputValue`/`TypeRef`/`Types`).
- Go static dispatch: `cmd/codegen/generator/go/templates/modules.go` (`invokeSrc`/`fillObjectFunctionCase`).
- Go skip-codegen: `core/sdk/go_sdk.go:250-519` (`useRuntimeCodegen`/`baseWithoutCodegen`/`requireGeneratedFiles`), config `core/modules/config.go:283-333`.
- Reuse (Python live introspection): `_resolver.ObjectType` (`.fields`, `.functions`), `_resolver.Function` (`.name`,`.parameters`,`.return_type`,`.doc`,`.deprecated`,`.check`/`.generate`/`.service`), `_arguments.Parameter` (`.name`,`.default_value` (dagger.JSON),`.default_path`,`.default_address`,`.ignore`,`.deprecated`,`.is_optional`), `Module._objects`/`Module._enums`, `_converter.to_typedef`.

---

## File structure

**Create (Python):**

- `sdk/python/src/dagger/mod/_introspect/__init__.py` — package; exports `live_to_introspection_json`, `render_entrypoint`.
- `sdk/python/src/dagger/mod/_introspect/_typeref.py` — live Python type → introspection `TypeRef` dict.
- `sdk/python/src/dagger/mod/_introspect/serialize.py` — `live_to_introspection_json(module) -> dict` (ModuleTypes Response).
- `sdk/python/src/dagger/mod/_introspect/entrypoint.py` — `render_entrypoint(module) -> str` (source of `_dagger_main.py`).
- `sdk/python/src/dagger/mod/_introspect/__main__.py` — CLI: `emit` and `entrypoint` subcommands.
- `sdk/python/src/dagger/mod/_introspect/_runtime.py` — committed-side helpers: `typedefs_from_json(data) -> Coroutine[dagger.ModuleID]` (replay) used by the generated entrypoint.
- Tests: `sdk/python/tests/mod/_introspect/test_typeref.py`, `test_serialize.py`, `test_entrypoint.py`.

**Modify (Python):**

- `sdk/python/src/dagger/mod/_module.py` — `_typedefs()` loads committed serialized typedefs instead of `ast_register`.
- `sdk/python/src/dagger/mod/cli.py` — entrypoint resolution prefers generated `_dagger_main.py`; drop `register_with_ast`/`--register`.
- Delete `sdk/python/src/dagger/mod/_analyzer/**` and `_discovery.ast_register` (+ its tests).

**Modify (Go driver / engine):**

- `sdk/python/runtime/main.go` — `WithSDK` pipeline; `Codegen`/`ModuleRuntime` short-circuit + `requireGeneratedFiles`; drop `ModuleTypesExp`.
- `core/sdk/python_sdk.go` (or the Python module-SDK capability site) — Python no longer advertises `moduleTypes`.
- `cmd/dagger/module.go` — `dagger init --sdk=python` writes `codegen.{legacyCodegenAtRuntime,automaticGitignore}=false`.

**Tests (Go):** `core/integration/module_python_no_codegen_test.go`.

---

## Phase 1 — live introspection → ModuleTypes JSON (stg: `py-introspect-serialize`)

Goal: a pure function producing the same introspection JSON shape Go's emitter does, from live decorated classes. No engine connection required (builds dicts).

### Task 1.1: TypeRef mapping

**Files:** Create `sdk/python/src/dagger/mod/_introspect/_typeref.py`; Test `sdk/python/tests/mod/_introspect/test_typeref.py`.

- [ ] **Step 1: Write failing tests** (`test_typeref.py`)

```python
from dagger.mod._introspect._typeref import type_ref
import dagger

def _kind(ref): return ref["kind"]

def test_str_is_nonnull_scalar():
    ref = type_ref(str, optional=False)
    assert ref == {"kind": "NON_NULL", "ofType": {"kind": "SCALAR", "name": "String", "ofType": None}}

def test_optional_str_strips_nonnull():
    ref = type_ref(str, optional=True)
    assert ref == {"kind": "SCALAR", "name": "String", "ofType": None}

def test_list_of_int():
    ref = type_ref(list[int], optional=False)
    assert ref == {"kind": "NON_NULL", "ofType": {"kind": "LIST", "name": None,
        "ofType": {"kind": "NON_NULL", "ofType": {"kind": "SCALAR", "name": "Integer", "ofType": None}}}}

def test_dagger_object():
    ref = type_ref(dagger.Container, optional=True)
    assert ref == {"kind": "OBJECT", "name": "Container", "ofType": None}
```

- [ ] **Step 2: Run, verify fail** — `cd sdk/python && uv run pytest tests/mod/_introspect/test_typeref.py -q` → FAIL (module missing).
- [ ] **Step 3: Implement `type_ref`** — map a live annotation to an introspection `TypeRef` dict. Mirror `cmd/codegen/generator/go/templates/introspect_emit.go:24-110` for kind/optionality rules: scalars `String/Integer/Float/Boolean`; `list[T]` → `LIST(ofType=...)`; module/dagger objects → `OBJECT`; enums → `ENUM`; interfaces → `INTERFACE`; dagger scalar subclasses → `SCALAR`. Non-optional wraps in `NON_NULL`. Reuse `beartype.door.TypeHint` (as `_arguments.Parameter` does) for unwrapping `T | None` and `list[T]`, and `_converter`/`_resolver` helpers (`is_id_type_subclass`, `get_object_type`, `issubclass(.., enum.Enum)`, `issubclass(.., Scalar)`) to classify the leaf.
- [ ] **Step 4: Run, verify pass.**
- [ ] **Step 5:** `git add` + `stg refresh` (patch created in Task 1.3 step; until then keep staged).

### Task 1.2: serializer over live module

**Files:** Create `sdk/python/src/dagger/mod/_introspect/serialize.py`; Test `sdk/python/tests/mod/_introspect/test_serialize.py`.

Test harness: reuse the import-a-temp-module pattern from `tests/mod/_runtime_introspect.py` (write source → import → read `cls.__dagger_module__`).

- [ ] **Step 1: Write failing tests** — assert the Response shape for a representative module, including the bug cases:

```python
from dagger.mod._introspect.serialize import live_to_introspection_json
from tests.mod._runtime_introspect import import_module_source  # add a small helper if absent

SRC = '''
import logging, dagger
from dagger import function, object_type, enum_type

@enum_type
class Lang(dagger.Enum):
    GO = "go"

@object_type
class Test:
    @function
    def echo(self, level: int = logging.INFO) -> int:
        return level
'''

def test_response_has_object_and_query():
    mod = import_module_source(SRC, main="Test", module_name="test")
    resp = live_to_introspection_json(mod, module_name="test")
    schema = resp["__schema"]
    names = {t["name"] for t in schema["types"]}
    assert {"Test", "Lang", "Query"} <= names

def test_default_is_runtime_resolved():
    # The whole point: logging.INFO must serialize as 20, not "INFO".
    mod = import_module_source(SRC, main="Test", module_name="test")
    resp = live_to_introspection_json(mod, module_name="test")
    test = next(t for t in resp["__schema"]["types"] if t["name"] == "Test")
    echo = next(f for f in test["fields"] if f["name"] == "echo")
    level = next(a for a in echo["args"] if a["name"] == "level")
    assert level["defaultValue"] == "20"
```

- [ ] **Step 2: Run, verify fail.**
- [ ] **Step 3: Implement `live_to_introspection_json(module, *, module_name)`** — walk `module._objects` (each `ObjectType`): emit an introspection `Type` with `kind:"OBJECT"`/`"INTERFACE"`, `fields` from `ObjectType.fields` (`Field`) + `ObjectType.functions` (`Function`). Each `Function` → introspection `Field` with `args` from `Function.parameters` (`Parameter`): `name=param.name`, `type=type_ref(param.resolved annotation, optional=param.is_optional)`, `defaultValue=str(param.default_value)` (the cattrs-serialized `dagger.JSON`, already runtime-resolved → fixes #13234). Walk `module._enums` → `kind:"ENUM"` with `enumValues`. Build a `Query` type with the module constructor field (name = `to_lower_camel(module_name)`, type `NON_NULL(OBJECT(MainObject))`, args from the main object constructor). Return `{"__schema": {"queryType": {"name": "Query"}, "types": [...], "directives": []}, "__schemaVersion": ...}`. Match field/JSON casing to `introspection.go` exactly.
- [ ] **Step 4: Run, verify pass.**

### Task 1.3: `emit` CLI + package wiring

**Files:** Create `_introspect/__init__.py`, `_introspect/__main__.py`.

- [ ] **Step 1: Write failing test** (`test_serialize.py::test_emit_cli_writes_json`) — invoke `python -m dagger.mod._introspect emit --output X` against a temp installed package via subprocess; assert valid JSON with the object.
- [ ] **Step 2: Run, verify fail.**
- [ ] **Step 3: Implement `__main__.py`** — `emit` subcommand: reuse `cli.load_module()` to import + get the `Module`, call `live_to_introspection_json`, write to `--output`/stdout. Flags mirror the design (`--module-name`, `--main-object` default from `DAGGER_MODULE`/`DAGGER_MAIN_OBJECT` env).
- [ ] **Step 4: Run, verify pass.**
- [ ] **Step 5: Commit** — `git add sdk/python/src/dagger/mod/_introspect sdk/python/tests/mod/_introspect && stg new py-introspect-serialize -m "sdk(python): live introspection -> schematool ModuleTypes JSON\n\nSigned-off-by: Yves Brissaud <yves@dagger.io>" && stg refresh`. Verify: `uv run pytest tests/mod/_introspect -q` PASS.

---

## Phase 2 — static entrypoint emitter (stg: `py-introspect-entrypoint`)

Goal: emit a committed `_dagger_main.py` that (def) replays serialized typedefs and (invoke) dispatches — zero runtime analysis for def.

### Task 2.1: JSON→typedefs replay helper (committed-side runtime)

**Files:** Create `_introspect/_runtime.py`; Test `tests/mod/_introspect/test_entrypoint.py`.

- [ ] **Step 1: Write failing test** — `typedefs_from_json(resp_dict)` builds a `dagger.ModuleID` from a ModuleTypes Response by replaying `dag.module().with_object(dag.type_def()...)`. Test requires an engine connection (mark with the existing engine fixture used by `tests/mod/test_results.py`); assert `await typedefs_from_json(resp)` returns an id and that round-tripping it back via introspection yields the same object/function/arg names + the `20` default.
- [ ] **Step 2: Run, verify fail.**
- [ ] **Step 3: Implement `typedefs_from_json`** — generic replay of the Response dict into engine TypeDefs (inverse of Task 1.2; mirror `_analyzer/registration.py:_resolved_type_to_typedef` but reading the introspection `TypeRef` dict instead of `ResolvedType`). This is the def-phase engine; pure data → builder calls.
- [ ] **Step 4: Run, verify pass.**

### Task 2.2: entrypoint source renderer

**Files:** Create `_introspect/entrypoint.py`; extend `__main__.py` with `entrypoint` subcommand.

- [ ] **Step 1: Write failing test** — `render_entrypoint(module, module_name)` returns Python source that: embeds the serialized ModuleTypes JSON (from Task 1.2), defines `async def _typedefs()` calling `typedefs_from_json(EMBEDDED)`, and defines `DISPATCH = {("Test","echo"): ("test.main","Test","echo"), ...}`. Assert the rendered source `compile()`s and contains the embedded `"20"` default and the dispatch entry.
- [ ] **Step 2: Run, verify fail.**
- [ ] **Step 3: Implement `render_entrypoint`** — produce source embedding `json.dumps(live_to_introspection_json(...))` as a literal + a dispatch table mapping `(parent_api_name, fn_api_name)` → import coordinates. Add `entrypoint` subcommand to `__main__.py` writing to `--output` (default `src/<pkg>/_dagger_main.py`).
- [ ] **Step 4: Run, verify pass.**
- [ ] **Step 5: Commit** — `stg new py-introspect-entrypoint -m "sdk(python): static entrypoint emitter (_dagger_main.py)\n\nSigned-off-by: ..."`; `uv run pytest tests/mod/_introspect -q` PASS.

---

## Phase 3 — wire generate-time pipeline in the Go driver (stg: `py-runtime-pipeline`)

**Files:** Modify `sdk/python/runtime/main.go`; regenerate `sdk/python/runtime/dagger.gen.go`.

- [ ] **Step 1:** In `WithSDK` (`main.go:390`), after base `codegen generate`, add execs (design pipeline steps 3-6): `uv run python -m dagger.mod._introspect emit --output /module-types.json`; then (only if `m.SelfCallsEnabled`) Go-side `dag.Schema(introspectionJSON).Merge(File("/module-types.json").Contents, m.ModName).Contents()` → write `/extended.json` → re-run `codegen generate -i /extended.json`; then `uv run python -m dagger.mod._introspect entrypoint --output src/<pkg>/_dagger_main.py`. Mirror the orchestration shape of Go `generate_module.go:116-141` (emit→merge→regenerate) but driver-side.
- [ ] **Step 2:** Add `SelfCallsEnabled` population in `Load` via `modSource.experimentalFeatureEnabled("SELF_CALLS")` (getter already exists on this branch — confirm in `core/schema/modulesource.go`).
- [ ] **Step 3:** Drop `ModuleTypesExp` (`main.go:195-211`) and make the Python SDK not advertise the `moduleTypes` capability (so engine `core/schema/modulesource.go:2428` `AsModuleTypes()` is false for Python → no `asModule` build+run). Mirror Go `core/sdk/go_sdk.go:52` (`AsModuleTypes → nil,false`).
- [ ] **Step 4:** `cd sdk/python/runtime && dagger develop` (regen `dagger.gen.go`); `go build ./...`.
- [ ] **Step 5: Verify** with the playground (engine-dev-testing skill): init a python module with a `logging.INFO` default + a self-call, run `dagger functions` → no error; `dagger call` works. Capture logs.
- [ ] **Step 6: Commit** `stg new py-runtime-pipeline -m "sdk(python/runtime): generate-time introspect+merge+entrypoint pipeline\n\nSigned-off-by: ..."`.

---

## Phase 4 — runtime def phase replays entrypoint; delete `_analyzer` (stg: `py-drop-ast`)

**Files:** Modify `sdk/python/src/dagger/mod/_module.py`, `cli.py`; delete `_analyzer/**`, `_discovery.ast_register`, AST tests.

- [ ] **Step 1: Write failing test** — `tests/mod/test_static_entrypoint.py`: generate `_dagger_main.py` for a fixture, import it, assert `_typedefs()` returns typedefs equal to direct live introspection; assert dispatch resolves `(parent, fn)` to the right callable.
- [ ] **Step 2: Run, verify fail.**
- [ ] **Step 3:** Change `_module.py::_typedefs()` (`:109-119`) to import the committed `_dagger_main` and `await _dagger_main._typedefs()` (no `ast_register`). Update `cli.py` entrypoint resolution to prefer `_dagger_main`. Remove `register_with_ast`/`--register`.
- [ ] **Step 4:** `git rm -r sdk/python/src/dagger/mod/_analyzer`; remove `ast_register` from `_discovery.py`; delete `tests/mod/test_ast_analyzer.py`, `test_differential.py` (or repoint — see self-review note). Keep `_runtime_introspect.py` helper if reused by `_introspect` tests.
- [ ] **Step 5: Run** `uv run pytest tests/mod -q` → PASS (no AST tests).
- [ ] **Step 6: Commit** `stg new py-drop-ast -m "sdk(python): def phase replays static entrypoint; drop AST analyzer\n\nSigned-off-by: ..."`.

---

## Phase 5 — honor `legacyCodegenAtRuntime` (stg: `py-skip-codegen`)

**Files:** Modify `sdk/python/runtime/main.go`; regen.

- [ ] **Step 1:** Add `LegacyCodegenAtRuntime` field + populate in `Load` from `modSource.codegenConfig()` (mirror `core/sdk/go_sdk.go:359-365` `useRuntimeCodegen`; the `ModuleCodegenConfig` dagql type + getter exist on this branch — confirm in `core/schema/modulesource.go`/`core/modules/config.go:283`).
- [ ] **Step 2:** `Codegen`/`Common` short-circuit: when opted in, skip `WithSDK` execs and call `requireGeneratedFiles` (check `sdk/**` + `src/<pkg>/_dagger_main.py`); build straight from committed files. Mirror `go_sdk.go:434-519` (`baseWithoutCodegen`) + `:384` (`requireGeneratedFiles`).
- [ ] **Step 3:** Regen `dagger.gen.go`; `go build ./...`.
- [ ] **Step 4: Verify** via playground: opted-in module with committed files runs `dagger call` with no codegen exec in the trace; deleting `_dagger_main.py` yields the actionable "run `dagger develop`" error.
- [ ] **Step 5: Commit** `stg new py-skip-codegen -m "sdk(python/runtime): honor legacyCodegenAtRuntime\n\nSigned-off-by: ..."`.

---

## Phase 6 — init defaults + self-host (stg: `py-init-defaults`)

**Files:** Modify `cmd/dagger/module.go`; `sdk/python/runtime/dagger.json` + commit its generated files.

- [ ] **Step 1:** Extend the init helper that writes codegen config to cover `python` (mirror the Go default site; confirm exact function in `cmd/dagger/module.go` on this branch). `dagger init --sdk=python` writes `codegen.legacyCodegenAtRuntime=false` + `automaticGitignore=false`.
- [ ] **Step 2:** Opt `sdk/python/runtime` itself in (commit its `sdk/**` + `_dagger_main.py`), mirroring how the Go SDK runtime self-hosts.
- [ ] **Step 3: Commit** `stg new py-init-defaults -m "cmd/dagger: dagger init --sdk=python writes codegen config; self-host python runtime\n\nSigned-off-by: ..."`.

---

## Phase 7 — integration tests (stg: `py-integration-tests`)

**Files:** Create `core/integration/module_python_no_codegen_test.go`.

- [ ] **Step 1:** `TestPythonSelfCalls` — a Python module whose function calls `dag.test().container_echo()`; run with self-calls; assert it works (mirror `TestSelfCalls/python` if a fixture exists).
- [ ] **Step 2:** `TestPythonRuntimeValuedDefault` (#13234 regression) — function with `level: int = logging.INFO`; `dagger functions` succeeds and the arg default is `20` in the schema.
- [ ] **Step 3:** `TestPythonDynamicMembers` — module with a decorator that adds a `@function` at import; assert the function appears in `dagger functions` (proves runtime introspection beats AST).
- [ ] **Step 4:** `TestPythonSkipCodegenDefault` / `MissingFilesError` / `RegenAfterEdit` — mirror `core/integration/module_runtime_codegen_test.go` (Go).
- [ ] **Step 5: Run** the targeted tests via `dagger call ... engine-dev test --run='TestPython...'` (engine-dev-testing skill). PASS.
- [ ] **Step 6: Commit** `stg new py-integration-tests -m "core/integration: python no-codegen-at-runtime + #13234 + self-calls tests\n\nSigned-off-by: ..."`.

---

## Self-review

- **Spec coverage:** points 1-5 of the design map to phases 1-2 (language model + entrypoint), 3 (self-calls codegen-side merge), 4 (drop AST + runtime def replay), 5 (skip codegen), 6 (init/no-codegen-at-runtime), 7 (tests). ✓
- **Differential test fate:** decided in Phase 4 step 4 — **retire** `test_ast_analyzer.py`/`test_differential.py` (AST gone). The `_introspect` unit tests + integration tests replace their coverage. The `_runtime_introspect.py` helper is repurposed for the `_introspect` tests. (If desired, keep one differential test comparing `live_to_introspection_json` against `typedefs_from_json` round-trip — noted, optional.)
- **Type consistency:** `live_to_introspection_json(module, *, module_name)` and `typedefs_from_json(data)` are inverses over the same Response dict; `type_ref(annotation, optional)` is the shared leaf mapper used by Task 1.2 and validated standalone in Task 1.1.
- **Open verification (do first in Phase 3/5):** confirm on THIS branch the exact names of `experimentalFeatureEnabled`/`codegenConfig` getters, the `ModuleCodegenConfig` dagql type, and the `cmd/dagger/module.go` init-config helper — the design assumes they exist (they're present for Go); adjust task wording to the real symbols when implementing.

## Risks / honest notes

- Generate-time now imports the user module (needs `uv sync` first) and runs two codegen passes for self-calls modules. Develop-time only.
- Import-time self-calls (self-call at module/class scope) are unsupported — surface a clear error (Go can't do it either).
- The `_dagger_main.py` invoke path still imports the module and uses the decorator-populated `_objects` for arg binding; that's minimal, not zero. A future optimization can bake arg converters into the entrypoint (out of scope here).
