# Handoff: unified ID + `node(id:)` migration

DESIGN DOC: https://gist.github.com/vito/158619f941f529244dda00b0d45a8a1c

## Current state

The `interfaces` branch has been merged with `upstream/main` and the
unified-ID/interface work is still the active direction:

- per-type `loadFooFromID(id:)` schema fields stay removed;
- per-type `FooID` SDK/schema scalars stay removed in favor of the
  unified `ID` scalar;
- object re-entry goes through Global Object Identification via
  `node(id:)` plus an inline fragment;
- object/interface ID arguments are represented as `ID` with
  `@expectedType`, so clients and the CLI can recover the intended
  object/interface type;
- module interfaces are first-class dagql/GraphQL interfaces, not
  wrapper objects or `asFoo`/`loadFooFromID` shims.

The SDK clients and generated code have been regenerated after the
merge. Rust and TypeScript have also had follow-up fixes applied. Treat
the current generated files as the post-merge baseline rather than as
pending regeneration work.

Go and Python SDK module codegen now have backwards-compatibility facades for
module sources declaring an engine version before the unified-ID/interface
cutover (`v0.21.0`). These facades do not restore legacy schema globally; they
render old source symbols over the new graph:

- per-object aliases/classes such as `type ContainerID = ID` in Go and
  `class ContainerID(Scalar)` in Python;
- `LoadFooFromID` / `load_foo_from_id` helpers implemented with `node(id:)`
  plus an inline fragment;
- legacy concrete query-builder structs/classes for GraphQL interfaces;
- per-interface ID aliases/classes such as `DepCustomIfaceID = ID` or
  `DepCustomIfaceID(Scalar)`;
- Go module-local interface aliases/helpers such as `CustomIfaceID = dagger.ID`
  and `LoadCustomIfaceFromID`.

The generators select this mode from the effective introspection schema
version. Python reads `__schemaVersion` directly from the introspection JSON;
Go module generation overrides that version from the module source's declared
`engineVersion` because its generator runs outside the Python-style SDK module
wrapper. This keeps the compatibility decision tied to the same version/view
model as GraphQL introspection rather than teaching SDK generators to parse
`dagger.json` themselves.

## Upstream merge notes

`upstream/main` was merged into `interfaces` in `5d4f187a4`. The detailed
semantic conflict notes are in `CONFLICT_RESOLUTION.md`; the parts that
matter for continuing work are:

- upstream's dagql cache/result-call model was ported forward:
  attached `dagql.ObjectResult[...]` metadata, `ID() (*call.ID, error)`,
  result-call provenance, server forking, and operation-lease paths are
  now part of this branch;
- the interface/ID architecture from `interfaces` was preserved:
  no `InterfaceAnnotatedValue`, no interface wrapper fallback, no
  `DynamicID`, no generated `asFoo` fields, and no automatic
  `loadFooFromID` registration;
- `node(id:)` is the object loader/meta field, including telemetry;
- module/type metadata should be carried as attached results and loaded
  through the current dagql server/cache when needed.

Some files referenced by older notes no longer exist after the merge.
Most importantly, `core/typedef_from_schema.go` was removed. Unified ID
argument reconstruction now lives in:

- `core/schema/coremod.go` — shared introspection `TypeRef` → `TypeDef`
  conversion, including `@expectedType` handling;
- `core/schema/module.go` — live `Query` typedef reconstruction for the
  CLI/workspace entrypoint path.

The "remaining conflicts" section in `CONFLICT_RESOLUTION.md` describes
the stop point when that file was written; later commits resolved those
conflicts and regenerated the SDK outputs.

## Recently fixed

### Go SDK compatibility for old module source

Committed as:

```text
8669190d2 fix(go sdk): support legacy ID helpers
```

Pre-cutover Go module source that directly references old generated symbols
now compiles after regeneration against the new engine. Examples covered by
the fix include:

```go
id, err := dag.Container().From("alpine").ID(ctx)
return dag.LoadContainerFromID(dagger.ContainerID(id)).
    WithExec([]string{"echo", "ok"}).
    Stdout(ctx)
```

and dependency/interface patterns like:

```go
id, err := iface.(interface {
    ID(context.Context) (CustomIfaceID, error)
}).ID(ctx)
loadedIface := dag.LoadDepCustomIfaceFromID(dagger.DepCustomIfaceID(id))
```

Implementation notes:

- `core/sdk/go_sdk.go` passes `src.Self().EngineVersion` into
  `codegen generate-module` and `generate-typedefs`;
- `cmd/codegen` uses `ModuleGeneratorConfig.EngineVersion` as the effective
  compatibility version when present;
- Go templates use a `LegacyGoSDKCompat` predicate with cutover
  `v0.21.0-0`, so `v0.20.x` modules get the facade while `v0.21.0-dev`
  builds get the modern surface;
- `Node` intentionally stays on the modern Go interface surface even in
  legacy mode because generic `dagger.Load`/`dagger.Ref` use it as their
  loadable constraint.

### Python SDK compatibility for old module source

Python codegen now reads the raw introspection `__schemaVersion` and gates a
legacy facade on versions before `v0.21.0`. The facade restores the old Python
source names without restoring old GraphQL fields:

```python
id_ = await dag.container().from_("alpine").id()
return await dag.load_container_from_id(dagger.ContainerID(id_)).stdout()
```

The generated `load_foo_from_id` methods call `Context.select_id`, which builds
`node(id:) { ... on Foo { ... } }`, and legacy `id()` methods return generated
`FooID(Scalar)` classes. Interface IDs and interface load helpers are generated
as well, returning the concrete hidden `_FooClient` query builder while keeping
the public Protocol type annotation.

`codegen.ast.insert_stubs` now also preserves directive AST nodes for object and
interface fields, field arguments, and input fields. This matters because
`graphql.build_client_schema` drops Dagger's custom directive applications, and
Python needs `@expectedType` both for modern object-typed ID arguments and for
legacy `FooID` signatures.

The workspace split now passes.

The original failure was a Dang runtime path still synthesizing
`loadWorkspaceFromID`/`load<Type>FromID`. `core/sdk/dang_helpers.go` now
converts object ID strings into a typed `GraphQLValue` backed by:

```graphql
node(id: $id) { ... on ExpectedType { ... } }
```

instead of constructing removed loader fields.

The follow-on `TestWorkspaceArgNotExposedAsCLIFlag` failure was caused by
live `Query` typedef reconstruction losing `@expectedType` information.
That made an auto-injected `Workspace` constructor argument look like a
plain scalar `ID`/string flag, so `dagger call magic --help` exposed
`--source`. `currentQueryTypeDef` now uses the same `resolveArgTypeDef`
path as the rest of core schema introspection, and `resolveIDScalar`:

- recognizes the bare unified ID scalar even when canonicalized as `Id`;
- rebuilds object/interface typedefs from `@expectedType`;
- preserves optionality;
- recurses through list wrappers like `[ID!]!`.

Additional stale active call sites were moved to the new API:

- `cmd/dagger/checks.graphql` uses `node(id:)` for `CheckGroup`;
- `cmd/dagger/up.graphql` uses `node(id:)` for `UpGroup`;
- `dagql/idtui/patch.go` uses aliased `node(id:)` for `Changeset`;
- `sdk/go/client_test.go` uses `Ref[T](client, id)` instead of generated
  `Load*FromID` helpers.

These fixes were committed as:

```text
8d8c8271a fix: load unified ID refs via node
```

## Validation already run

Passing locally after the recent fixes:

```bash
go test -run '^$' ./core/schema ./core/sdk ./cmd/dagger ./dagql/idtui ./core/integration
(cd sdk/go && go test -run '^$' ./...)
go test ./core/schema
go test ./cmd/codegen/... ./core/sdk
(cd sdk/python && uv run ruff check codegen/src/codegen/cli.py codegen/src/codegen/generator.py codegen/src/codegen/ast.py tests/codegen/test_generator.py)
(cd sdk/python && uv run pytest tests/codegen/test_generator.py -q)
./hack/with-dev go test -v -count=1 -run 'TestLegacy/TestLegacyGoSDKLoadFromIDCompat' ./core/integration/
./hack/with-dev go test -v -count=1 -run 'TestLegacy/TestLegacyPythonSDKLoadFromIDCompat' ./core/integration/
./hack/with-dev go test -v -count=1 -run 'TestInterface/TestIfaceBasic/go' ./core/integration/
./hack/with-dev go test -v -count=1 -run 'TestInterface/TestIfaceBasic/python' ./core/integration/
dagger call --progress=dots engine-dev test --run 'TestWorkspace/TestWorkspaceArgNotExposedAsCLIFlag' --pkg ./core/integration/
dagger --progress=dots check test-split:test-workspaces
git diff --check
```

## Remaining work / watch points

### SDK backwards-compatibility follow-up

Go now has the intended compatibility model for regenerated pre-cutover
modules: keep the schema hard-cut over to unified `ID`, `node(id:)`, and real
GraphQL interfaces, but generate legacy SDK source symbols as a facade over
that graph when the introspection/view version is before `v0.21.0`.

Python now follows this policy too. TypeScript should follow the same policy
rather than restoring `loadFooFromID` fields or per-type ID scalars globally.
For each remaining SDK:

- make the generator receive/preserve the introspection `__schemaVersion`
  (or the existing effective schema-version equivalent);
- gate legacy generated source surface on `< v0.21.0`;
- implement typed-ID/load-helper compatibility by selecting `node(id:)` with
  the expected type, not by querying removed root fields;
- keep modern output unchanged for `v0.21.0` and newer modules;
- add regression fixtures using unchanged old module source, especially for
  interface arguments/returns and ID round-tripping.

Known plumbing status from the audit:

- Python now has the same regenerated-old-module compatibility model as Go.
  Its codegen reads `__schemaVersion`, generates pre-`v0.21.0` typed ID
  classes such as `ContainerID`, makes legacy `id()` methods return those
  classes, and emits `load_foo_from_id` helpers that call `Context.select_id`
  (`node(id:)` plus inline fragment) instead of removed root fields. It also
  preserves directive AST stubs for object/interface fields, field args, and
  input fields so `@expectedType` survives `graphql.build_client_schema`.
- TypeScript already has schema-version conditional infrastructure in its
  templates/codegen path; extend it with the equivalent legacy facade where
  old TS source referenced typed IDs/load helpers or interface wrappers.
- PHP also ignores `__schemaVersion`, and Java should prefer
  `__schemaVersion` from introspection JSON when present. These are not the
  immediate ask, but they are the same class of follow-up.

### Active `FromID` straggler handling

A tracked code grep may still find old-version modules such as:

```text
modules/evaluator/main.go: dag.LoadEvalWorkspaceEvalFromID(dagger.EvalWorkspaceEvalID(id))
```

This pattern is now intentionally supported for regenerated Go modules that
declare `engineVersion: v0.20.6`. Do not mechanically rewrite such source to
`dagger.ID`/`dagger.Ref` unless the module is being moved to the modern
`v0.21.0` SDK surface. If it fails, first check whether its ignored generated
SDK under `internal/dagger` is stale and needs regeneration.

### Broad grep noise

Broad `FromID` searches also find historical docs, SDK codegen tests, and
old-version/generated or untracked module outputs. Do not treat every hit
as an immediate blocker. Prioritize active runtime/schema paths and
tracked code that builds against the current regenerated SDKs.

Useful focused grep for active tracked code:

```bash
git ls-files -z '*.go' '*.graphql' '*.dang' |
  xargs -0 rg -n '\bLoad[A-Za-z0-9]*FromID\s*\(|\bload[A-Za-z0-9]*FromID\b|[A-Z][A-Za-z0-9]*ID!|\bdagger\.[A-Z][A-Za-z0-9]*ID\b'
```

### If more CLI/type issues appear

Look first at:

- `core/schema/coremod.go` for introspection type conversion and unified
  ID `@expectedType` handling;
- `core/schema/module.go` for live `Query` typedef construction;
- `cmd/dagger/flags.go` and `cmd/dagger/functions.go` for CLI flag
  creation and selection logic.

The expected invariant is: GraphQL exposes `ID` plus `@expectedType`,
while TypeDefs used by module/CLI ergonomics recover the richer
object/interface type before deciding how to parse flags or values.
