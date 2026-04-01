# Handoff: Artifacts + Execution Plans

Preparation notes for handing off implementation of [artifacts.md](./artifacts.md)
and [plans.md](./plans.md).

## Recommendations for Open Questions

### Artifacts Q2: Cross-artifact reference ordering + filter system

> "How cross-artifact reference ordering interacts with the filter system."

**Recommendation: Defer to Collections integration.**

Before collections, the only dimension is `module`. Modules don't reference
each other through the Artifacts system — they're independent workspace
entries. Cross-artifact references become relevant when a collection item
(e.g., a GoModule) references another collection item (e.g., GoTests within
it), which is a Collections-era concern.

For the initial implementation:
- `check` verb compilation does NOT recurse through artifact references.
- Each module's checks are independent actions in the plan.
- Cross-artifact ordering is a known extension point for when Collections land.

**Decision needed:** Confirm this scoping. If cross-module check ordering is
needed now (e.g., "check module A before module B"), that should be expressed
through explicit `Action.withAfter` rather than implicit reference traversal.

### Artifacts Q3: Schema-path notation (`go:lint`) vs typed filters

> "Whether schema-path notation (go:lint) should be deprecated in favor of
> typed filters."

**Recommendation: Keep both, with typed filters as the primary model.**

Rationale:
- Schema-path notation (`go:lint`) is the current UX for `dagger check`.
  Removing it is a breaking change with no migration path until collections
  provide typed filter values.
- Typed filters (`--module=go`) are the target model. They become the
  primary UX once Artifacts ships.
- Schema-path selection narrows scope first, then typed filters shape
  within that scope. They compose — no conflict.

For the initial implementation:
- `dagger check go:lint` continues to work (pattern-based `include` on
  `Workspace.checks`, bridged to Artifacts internally).
- `dagger check --module=go` works as the new typed filter.
- Deprecation of schema-path notation is deferred to post-Collections
  when typed filters can fully replace it.

**Decision needed:** Confirm this approach, or decide to make a clean break.

### Artifacts Q4: `--mod` vs `--module`

> "`--mod` (global module loader) vs `--module` (artifact dimension filter)"

**Recommendation: Rename `--mod` to `--load` (or similar) as part of the
Artifacts implementation.**

Rationale:
- `--mod/-m` currently means "load this module as an extra module". It's a
  global CLI flag for module loading.
- `--module` will mean "filter artifacts to this module". It's a dimension
  filter generated from `Artifacts.dimensions`.
- Having both `--mod` and `--module` will confuse users.
- Since workspace-plumbing moved module loading to the engine, `--mod` is
  already legacy-feeling. Renaming it now avoids confusion.

For the initial implementation:
- Rename `--mod/-m` to `--load` (or `--with-module`, or another name).
- `--module` is the artifact dimension filter.
- Old `--mod` could be kept as a hidden alias for one release.

**Decision needed:** Pick the new name for the old `--mod` flag.

### Plans Q1: Transition path from CheckGroup to Plan

> "Exact transition path from CheckGroup to Plan."

**Recommendation: Clean internal replacement, backward-compatible CLI.**

Rationale:
- CheckGroup is an engine-internal type. It's exposed in the GraphQL API
  (`Workspace.checks → CheckGroup`), but the CLI is the only real consumer.
- The Plan type subsumes CheckGroup's functionality: it's a DAG of actions
  that can be listed, inspected, and executed.
- A shim or parallel codepath adds complexity for no real benefit — there
  are no known third-party consumers of the CheckGroup API.

Transition:
1. Implement `Artifacts` and `Plan`/`Action` types in the engine.
2. `Workspace.artifacts.check` compiles to a `Plan`. Internally this does
   the same work as `NewCheckGroup` — walks modules, collects checks —
   but produces a Plan instead of a CheckGroup.
3. Replace `Workspace.checks()` with `Workspace.artifacts.check` in the
   engine schema. `Workspace.checks()` becomes a deprecated compat wrapper
   that internally calls `artifacts.check`.
4. Update the CLI `dagger check` to use the new `Workspace.artifacts` path.
5. Remove CheckGroup once the CLI migration is complete.

This is a clean break internally but preserves `dagger check` UX. The
CheckGroup GraphQL API is experimental, so breaking it is acceptable.

**Decision needed:** Confirm clean break is OK, or require a compat period.

## Initial Dimensions (Pre-Collections)

The `module` dimension is the only dimension before Collections land.

Source: `currentWorkspacePrimaryModules()` in
`core/schema/workspace.go:542` already enumerates installed workspace
modules. This is the backing data for the `module` dimension.

Concretely:
- `Artifacts.dimensions` returns `[{name: "module", keyType: String}]`
- `Artifacts.filterBy("module")` → all artifacts, one per module
- `Artifacts.filterBy("module", ["go"])` → artifacts from the "go" module
- `Artifacts.items` → one `Artifact` per module
- Each `Artifact` wraps a module and exposes its check/generate/ship/up
  handlers as actions

When Collections land later, they plug in as additional dimension
providers — e.g., a Go module's `GoTests` collection adds a `go-test`
dimension.

## Implementation Scope

### What to Build

**Engine types** (in `core/`):
- `Artifact` — wraps a module (later: any artifact). Fields: key, ancestors,
  fields, actions, action(name).
- `Artifacts` — filterable view. Methods: filterBy, filterCheck,
  filterGenerate, filterShip, filterUp, dimensions, items, actions,
  action(name), check, generate, ship, up.
- `ArtifactDimension` — name + keyType.
- `ArtifactCoordinate` — dimension + value.
- `FieldValue` — name + typeDef + json + display.
- `Action` — artifacts + name + function + after + withAfter + run.
- `Plan` — nodes + run.

**Engine schema** (in `core/schema/`):
- `artifactsSchema` — installs all the above types with dagql.
- Wire `Workspace.artifacts` field.

**CLI** (in `cmd/dagger/`):
- Update `dagger check` to use `workspace.artifacts.check.run` path.
- Add `--module` filter flag (generated from dimensions).
- Add `--plan` flag to display plan without executing.
- Add `dagger list` command (or update existing) for dimension discovery.
- Keep schema-path args as compat bridge.

### What NOT to Build

- Collection-provided dimensions (go-module, go-test, etc.) — deferred.
- Cross-artifact reference traversal in verb compilation — deferred.
- `dagger ship` / `dagger up` CLI commands — Plan type supports them, but
  CLI commands are out of scope for initial Artifacts.
- Structured per-action results — deferred (void return is fine).
- Verb policy (require check before ship, etc.) — deferred.

## Suggested Task Breakdown

### Phase 1: Core Types (engine)

1. Define `Artifact`, `ArtifactDimension`, `ArtifactCoordinate`, `FieldValue`
   types in `core/artifacts.go`.
2. Define `Action` type in `core/action.go` with `withAfter` builder.
3. Define `Plan` type in `core/plan.go` with DAG walk and parallel execution.
4. Define `Artifacts` type in `core/artifacts.go` with filter chain and verb
   compilation.
5. Wire `Artifacts` to workspace module discovery
   (`currentWorkspacePrimaryModules`).

### Phase 2: Schema + Wiring (engine)

6. Create `core/schema/artifacts.go` — install all types with dagql.
7. Add `Workspace.artifacts` field to `workspaceSchema`.
8. Verb compilation: `Artifacts.check` walks modules, collects check
   handlers, produces a Plan with one Action per check (or batched).
9. Same for `generate` (simpler — no recursion).

### Phase 3: CLI

10. Add `--module` flag to `dagger check`, generated from
    `workspace.artifacts.dimensions`.
11. Add `--plan` flag to display plan nodes without executing.
12. Update `dagger check` to use `workspace.artifacts.check.run` internally.
13. Add `dagger list` / `dagger list modules` using `workspace.artifacts`.
14. Keep pattern args (`dagger check go:lint`) as compat bridge via
    `include` filtering.

### Phase 4: Cleanup

15. Deprecate `Workspace.checks()` / `Workspace.generators()` (or make them
    thin wrappers over Artifacts).
16. Remove CheckGroup type once nothing references it.

## Dependencies and Ordering

```
Phase 1 (types)
  │
  ▼
Phase 2 (schema + wiring)
  │
  ▼
Phase 3 (CLI)
  │
  ▼
Phase 4 (cleanup)
```

Phases 1-2 are engine work. Phase 3 is CLI work. Phase 4 can happen
incrementally.

Within Phase 1, Action depends on Artifact (actions target artifacts),
and Plan depends on Action (plans are DAGs of actions). Artifacts depends
on all of them (it compiles to Plans).
