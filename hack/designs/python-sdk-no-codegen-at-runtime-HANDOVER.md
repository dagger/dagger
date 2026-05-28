# Python SDK no-codegen-at-runtime — HANDOVER

Last updated: 2026-05-28.

Continuation notes for the "Python SDK: no codegen at runtime / drop the AST
analyzer" work. Read this with the two companion docs in this directory:

- `python-sdk-no-codegen-at-runtime.md` — the design.
- `python-sdk-no-codegen-at-runtime-plan.md` — the 7-phase implementation plan.

## Goal (one paragraph)

Bring the Python SDK to the Go SDK's no-codegen-at-runtime model. Replace the
static AST analyzer (`dagger.mod._analyzer`) with the language's real model —
**import + introspect the live module at generate time** — emit a **static
entrypoint** (`_dagger_main.py`) the runtime replays with no analysis, do
self-calls **codegen-side** (merge the module's own types into the schema via
the engine `schematool`), and skip codegen at runtime. This fixes #13234
(`logging.INFO` default recorded as `"INFO"` → engine `strconv.ParseInt`
failure) and the dynamic-decorator / functional-enum gaps by construction,
because introspection executes the code.

## Coordinates

- **Branch:** `workspace-py-no-codegen-at-runtime` (StGit stack).
- **Based on:** `workspace-go-no-codegen-at-runtime` (the Go no-codegen stack +
  the workspace refactor; has the engine `schematool` API). On `origin`
  (`eunomie/dagger`) at the same SHA.
- **Worktree (this machine):** `…/dagger-worktrees/workspace-py-no-codegen-at-runtime`.
- **Remote:** pushed to `origin` = `git@github.com:eunomie/dagger.git`. HEAD ==
  origin (fully pushed).
- **Draft PR:** dagger/dagger #13235 (base `main`, head
  `eunomie:workspace-py-no-codegen-at-runtime`). Stacked on the unmerged Go
  base, so the diff shows that base too; review the Python commits.

## Resume on a new machine

```bash
# clone the fork (or add it as a remote), then:
git fetch origin
git worktree add <path> workspace-py-no-codegen-at-runtime   # or: git checkout
cd <path>
# StGit: the stack is already applied; `stg series` should show the patches below.
cd sdk/python && uv run pytest tests/mod/ -q                  # 15 introspect tests + rest, green
```

Dev-engine testing (Go driver / engine changes) needs a built dev engine — see
"Verification workflow" below. The persistent `dagger-engine.dev` container is
machine-local and must be rebuilt on the new machine (`hack/dev` / playground).

## Current state

StGit stack (bottom → top), **all pushed**:

| Patch | Phase | Status |
| --- | --- | --- |
| `design-no-codegen-at-runtime` | docs | done |
| `plan-no-codegen-at-runtime` | docs | done |
| `py-introspect-serialize` | 1 | done — `_introspect` pkg: `type_ref`, `live_to_introspection_json`, `emit` CLI. **#13234 fix proven in a unit test.** |
| `py-introspect-entrypoint` | 2 | done — `render_entrypoint` (+ `_runtime.typedefs_from_json`), `entrypoint` CLI. |
| `py-introspect-merge` | 3a | done — `_introspect merge` subcommand (Python-side schematool merge). |
| `py-runtime-codegen-wip` | 3b | **WIP, compiles (`go build`), UNVERIFIED end-to-end.** `Codegen` installs the module + runs `_introspect entrypoint` to emit `src/<pkg>/_dagger_main.py`. |

- **Tests:** 15 `_introspect` unit tests green; lint/format clean.
- **CI (PR #13235):** all `python-sdk:*` and `markdown-lint:*` checks GREEN.
  Other red/pending checks are noise from the unmerged Go/workspace base, not
  this work.

## Key architecture decisions (already settled)

1. **Merge runs in Python, not the Go driver.** The schematool API
   (`dag.schema(json).merge(types, name).contents()`) IS in the Python user
   client (`sdk/python/src/dagger/client/gen.py`, via core's `coremod`), but is
   NOT in the python-sdk _runtime module's_ Go client (and bumping its
   `engineVersion` does not expose it). So the Go driver needs **no** schematool
   regen; the `_introspect merge` subcommand does it. This was the big unblock.
2. **Drop the engine `asModule moduleTypes` path for Python** (mirror Go's
   `AsModuleTypes → (nil,false)`); self-types are handled codegen-side. _Not yet
   done_ — Phase 3 task.
3. **`_analyzer` (AST) is deleted** in Phase 4.

## ⚠️ Immediate next step: re-run the Phase 3 verification (result was lost)

The last action was an end-to-end playground/`with-dev` run whose output didn't
survive the machine handover — **Phase 3b is unverified.** Re-run it first.

The **module-creation/generate flow changed in the workspace refactor** (no
`dagger init --sdk=python`). The working flow (must be in a **git repo**):

```bash
cd /tmp && rm -rf wstest && mkdir wstest && cd wstest && git init -q
dagger workspace init --here
dagger install github.com/dagger/python-sdk          # resolves to local SDK in the dev engine
dagger call python-sdk init --name=test             # scaffolds the module
# edit src/test/*.py to add a function, then:
dagger generate python-sdk                          # = old `dagger develop`; runs SDK Codegen
# check the generated entrypoint:
find . -name _dagger_main.py | grep -v /sdk/
```

Run these via the dev engine: `hack/with-dev sh <script>` (use
`"$DAGGER_BIN_ROOT/dagger"` inside the script — PATH can pick up a non-dev
`dagger`). Verify `src/test/_dagger_main.py` exists and embeds
`"defaultValue": "20"` for a `level: int = logging.INFO` function.

**Known blocker to expect:** a `logging.INFO`-default module currently FAILS to
load during `generate` with `strconv.ParseInt: parsing "INFO"` — because the
module's **def-phase still runs the AST analyzer** (`_module.py::_typedefs →
ast_register`) during load, before Codegen's entrypoint step. So:

- Verify Phase 3b first with a **plain default** (e.g. `msg: str = "hi"`) to
  confirm `_dagger_main.py` is generated at all.
- The `logging.INFO` case only fully works once **Phase 4** makes the def-phase
  stop using AST (see below). Phases 3 and 4 are intertwined for #13234.

## Remaining work

### Phase 3 — finish the generate-time pipeline (driver)

File: `sdk/python/runtime/main.go`.

- Verify the WIP `Codegen` entrypoint generation (above).
- Add the **self-calls merge** to the pipeline: after `emit` → run
  `_introspect merge` (or call it) → regenerate `gen.py` against the merged
  schema → then `entrypoint`. Gate the merge on self-calls.
- **Self-calls gating signal:** the Go driver's module client lacks
  `experimentalFeatureEnabled`. Decide how Python learns self-calls is on
  (engine arg like Go's `--self-calls`, or always-emit/merge). TBD.
- Drop `ModuleTypesExp` and make Python not advertise the engine `moduleTypes`
  capability (so `core/schema/modulesource.go`'s `asModule` self-append is
  skipped for Python). Mirror `core/sdk/go_sdk.go`.

### Phase 4 — runtime def-phase; delete `_analyzer`

Files: `sdk/python/src/dagger/mod/_module.py`, `cli.py`, delete `_analyzer/**`
and `_discovery.ast_register`.

- **Recommended def-phase logic** (resolves the #13234 bootstrapping): in
  `_module.py::_typedefs()`, if the committed `_dagger_main.py` exists → replay
  it (`await _dagger_main.typedefs()`); **else fall back to runtime
  introspection** (import the module + `live_to_introspection_json` →
  `typedefs_from_json`). Either path executes code (reads `logging.INFO` = 20)
  or replays pre-resolved values — **never AST**. This makes `generate` work on
  problematic modules (first load uses runtime introspection) and keeps the fast
  static path once committed.
- Then delete `_analyzer` + `ast_register` + their tests
  (`test_ast_analyzer.py`, `test_differential.py` — retire; `_runtime_introspect.py`
  is reused by the `_introspect` tests).

### Phase 5 — honor `legacyCodegenAtRuntime`

`main.go`: `Codegen`/`ModuleRuntime` short-circuit + `requireGeneratedFiles`,
mirroring `core/sdk/go_sdk.go:250-519`. Build from committed files when opted in.

### Phase 6 — init defaults + self-host

`cmd/dagger`: write `codegen.{legacyCodegenAtRuntime,automaticGitignore}=false`
for python init; commit `sdk/python/runtime`'s own generated files.

### Phase 7 — integration tests

`core/integration/module_python_no_codegen_test.go`, using the
`moduleFixture` + `daggerFunctions`/`daggerCall` + `configFile` harness (see
`core/integration/module_runtime_codegen_test.go` for the Go pattern). Cover:
python self-calls; #13234 (`logging.INFO` reaches schema as `20`); a
dynamically-added function appears; skip-codegen default / missing-files error.

## Map of what's implemented (Python `_introspect` package)

`sdk/python/src/dagger/mod/_introspect/`:

- `_typeref.py` — `type_ref(annotation, *, optional)` → introspection `TypeRef`
  dict. Mirrors `_converter.to_typedef` classification. Scalars are
  `String/Int/Float/Boolean/Void`; kinds UPPERCASE; non-optional wraps in
  `NON_NULL`.
- `serialize.py` — `live_to_introspection_json(module, *, main_object_name,
  module_name)` → schematool `Response` JSON (same shape Go's emitter produces),
  walking `Module._objects`/`_enums`. Defaults come from the live
  `Parameter.default_value` (cattrs) → `logging.INFO` serializes as `"20"`.
- `entrypoint.py` — `render_entrypoint(...)` → source of `_dagger_main.py`
  (embeds the serialized ModuleTypes JSON; `typedefs()` calls
  `typedefs_from_json`).
- `_runtime.py` — `typedefs_from_json(module_types)`: generic, committed replay
  of the JSON into engine TypeDefs (def phase). `dag` imported lazily
  (import-safe). **Engine-verified in integration, not unit-tested.**
- `__main__.py` — CLI: `emit`, `merge`, `entrypoint` subcommands. `emit`/`entrypoint`
  import the user module (need `uv sync` first); `merge` connects to the engine.

Reuse points (existing runtime-introspection layer, the foundation):
`_resolver.ObjectType/Function/Constructor`, `_arguments.Parameter`,
`_converter.to_typedef`, `Module._objects`/`_enums`, `cli.load_module`.

Go reference (mirror these): `cmd/codegen/generator/go/generate_module.go`,
`…/templates/introspect_emit.go`, `…/templates/modules.go` (static
`invoke`/dispatch), `core/schematool.go` + `core/schema/schematool.go`,
`core/sdk/go_sdk.go` (skip-codegen), `core/schema/modulesource.go` (asModule
self-append; `isSelfCallsEnabled`).

## Gotchas / learnings

- **Workspace CLI:** module creation is `dagger install github.com/dagger/python-sdk`
  - `dagger call python-sdk init --name=NAME`; generate is `dagger generate
  python-sdk`. Needs a **git repo** + `dagger workspace init --here` first
  (else "no current workspace: workspace not loaded"). Plain `dagger generate`
  (no generator name) on a bare dir does nothing.
- **Dev engine is slow here (~10 min cold build, aarch64 sandbox).** Use
  `hack/dev <cmd>` to build+load the persistent `dagger-engine.dev`, then
  `hack/with-dev <cmd>` for fast iteration (the engine persists). Playground:
  `.claude/skills/engine-dev-testing/with-playground.sh` (set
  `PLAYGROUND_TIMEOUT=900`; default 300s times out mid-build). Don't `go build`
  the engine directly — but `go build ./...` inside `sdk/python/runtime` is a
  fine fast compile check for driver edits.
- **CI:** find failing checks with `gh pr checks 13235 --repo dagger/dagger`;
  for Dagger Cloud checks, map to a trace via the cache-expert skill
  (`references/debugging.md`) and `dagger trace <id>`. markdownlint config is
  `.markdownlint.yaml`; run `npx markdownlint-cli2 --fix <doc>` on new docs.
- **StGit:** commit with `stg new <name> -m "...\n\nSigned-off-by: Yves
  Brissaud <yves@dagger.io>"` then `git add` + `stg refresh`. To fold a fix into
  a lower patch: `stg refresh -p <patch> -- <file>` (stage first;
  `--index` if the index is dirty). No `Co-Authored-By` lines.
