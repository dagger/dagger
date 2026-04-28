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
dagger call --progress=dots engine-dev test --run 'TestWorkspace/TestWorkspaceArgNotExposedAsCLIFlag' --pkg ./core/integration/
dagger --progress=dots check test-split:test-workspaces
git diff --check
```

## Remaining work / watch points

### Active `FromID` straggler decision

A tracked code grep still finds:

```text
modules/evaluator/main.go: dag.LoadEvalWorkspaceEvalFromID(dagger.EvalWorkspaceEvalID(id))
```

This module is pinned to `engineVersion: v0.20.6` and uses its ignored,
locally generated SDK under `modules/evaluator/internal/dagger`, which
still has the old per-type ID API. A direct change to `dagger.ID` /
`dagger.Ref` does not compile against that generated SDK. Decide whether
old-version modules are out of scope, or migrate/regenerate this module
before changing it.

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
