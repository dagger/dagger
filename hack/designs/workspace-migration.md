# Workspace Migration

## Status

Locked.

## Summary

As a temporary compatibility stopgap, an eligible legacy `dagger.json` can stand in for a missing workspace config. The engine projects it into a compat workspace (`CompatWorkspace`), uses that result at runtime, and persists that same result during `dagger migrate`.

Legacy `blueprint` and `toolchains` fields are interpreted only while building the compat workspace. Generic module loading otherwise ignores them, except for optional deprecation warnings.

Explicit migration persists the same compat workspace that runtime compat mode would build in memory. `dagger migrate` is therefore a thin CLI wrapper over `Workspace.migrate()`.

`Workspace.migrate()` operates on the compat workspace already attached to that `Workspace`. It does not rediscover legacy config from disk.

Migration planning and migration application are separate. Product code has one migration applier only: the `Changeset` returned from `Workspace.migrate()`.

## Problem

1. `dagger migrate` is still CLI-owned.
2. Compat mode and explicit migration still do not share one canonical result.
3. Legacy `blueprint` and `toolchains` still leak past compat into generic module loading.

## Decision

The legacy `dagger.json` compatibility story is defined by one engine-owned concept: the compat workspace (`CompatWorkspace`).

The engine will:

- detect either a normal workspace or a compat workspace
- use `CompatWorkspace` directly for runtime compatibility
- persist that same `CompatWorkspace` through `Workspace.migrate()`
- apply migration only through returned `Changeset` values

The CLI will not own migration orchestration. It will call `Workspace.migrate()`, rely on engine telemetry for human-readable migration progress, and reuse the normal changeset preview/apply flow for the returned combined changeset.

## Compat Workspace

`CompatWorkspace` is the in-memory workspace-shaped result inferred from an eligible legacy `dagger.json`.

Conceptually:

```text
legacy dagger.json
    ->
modules.ModuleConfig
    ->
CompatWorkspace
```

`CompatWorkspace` contains:

- the projected workspace config
- the projected main module
- the compat entrypoint decision
- migration warnings and reportable gaps

The exact internal Go shape is not part of this document, but it must be one shared engine-owned result used by both compat mode and explicit migration planning.

## Behavior

### 1. Compat Workspace Detection

The engine detects ambient workspace context in this order:

```text
find-up .dagger/config.toml
  -> normal workspace

else find-up eligible legacy dagger.json
  -> CompatWorkspace

else
  -> no workspace
```

An eligible legacy `dagger.json` is one where any of the following is true:

- `source != "."`
- `toolchains` is non-empty
- `blueprint` is set

If a legacy `dagger.json` is found but is not eligible, it does not create ambient workspace context.

### 2. Compat Entrypoint

Inside `CompatWorkspace`:

- if legacy `blueprint` exists, the blueprint is the compat entrypoint
- otherwise, the projected main module is the compat entrypoint

### 3. Legacy Field Scope

Legacy `dagger.json` fields:

- `blueprint`
- `toolchains`

are interpreted only while building `CompatWorkspace`.

Generic module loading may still parse and round-trip those fields, but outside compat-workspace construction they have no runtime effect. In particular, ordinary `asModule()` / module serving / extra-module loading must not load related modules or entrypoint behavior from them. A deprecation warning is acceptable.

This is an intentional target behavior change from the current code.

### 4. Simplification Opportunity

Today, generic module loading still carries legacy blueprint/toolchain runtime behavior. That includes parsing those fields into ordinary module-loading state, resolving related legacy modules, and routing blueprint/toolchain behavior during generic module load.

Once legacy `blueprint` and `toolchains` are confined to compat-workspace construction, that generic runtime behavior becomes dead weight and should be deleted.

The target is:

- compat workspace construction keeps the legacy behavior
- generic module loading keeps only parse/round-trip support, plus optional warning behavior
- generic blueprint/toolchain routing code is removed

This simplification is intentional. It is not just cleanup left for later.

### 5. Migration Equivalence

This must hold:

```text
compat mode
==
migrate in memory, then load
```

In other words, explicit migration persists the same `CompatWorkspace` that runtime compat mode would have built in memory.

### 6. Generic Dedupe

This document does not define module deduplication.

Compat-workspace loading and migration must participate in the same generic module-deduplication mechanism used by the rest of workspace loading. They must not introduce a compat-specific dedupe rule.

## API

```graphql
extend type Workspace {
  """
  Compute the explicit migration plan for the current workspace.
  """
  migrate: WorkspaceMigration!
}

type WorkspaceMigration {
  """
  Filesystem changes for the full migration plan.
  """
  changes: Changeset!

  """
  Logical migration steps, each identified by a stable code.
  """
  steps: [WorkspaceMigrationStep!]!
}

type WorkspaceMigrationStep {
  """
  Stable code identifying this logical migration step.
  New Dagger versions may introduce additional codes.
  """
  code: String!

  """
  Generic summary of this step's purpose and impact.
  """
  description: String!

  """
  Non-fatal warnings raised while planning this step.
  """
  warnings: [String!]!

  """
  Filesystem changes for this step.
  """
  changes: Changeset!
}
```

## Engine Contract

The engine guarantees:

- `WorkspaceMigration.changes` is based on the pre-migration workspace state
- every returned step is based on that same pre-migration workspace state
- `WorkspaceMigration.changes` is equivalent to merging all step changesets in order
- `WorkspaceMigration.steps = []` and an empty `WorkspaceMigration.changes` means "no migration needed"
- warnings are informational; they do not block application

If a migration wants to create `.dagger/migration-report.md`, that file is part of `changes`. It is not modeled as separate API metadata.

Product code must not contain a second migration applier that writes files directly. Any non-changeset applier may exist only in test-only helpers.

## CLI

`dagger migrate` is a thin wrapper:

```go
func daggerMigrate(ctx context.Context, dag *dagger.Client) error {
	changes := dag.CurrentWorkspace().Migrate().Changes()
	if changes.IsEmpty() {
		fmt.Println("No migration needed.")
		return nil
	}

	return renderOrApplyChangeset(changes)
}
```

User-facing flow:

```text
$ dagger migrate
[telemetry spans from engine]
[telemetry/console warnings from engine]

[changeset preview here]

Apply changes? [y/N]
```

With `-y`, the combined changeset is auto-applied through the normal changeset path.

The CLI does not need to fetch migration step metadata for this flow. Human-readable progress, step descriptions, and warnings come from engine telemetry spans and engine-emitted warnings during migration planning.

## Architecture

Target runtime path:

```text
engine/server
└─ detect workspace boundary
   ├─ .dagger/config.toml found
   │  └─ load normal workspace
   └─ else eligible legacy dagger.json found
      ├─ parse -> modules.ModuleConfig
      ├─ build CompatWorkspace
      └─ load CompatWorkspace as ambient workspace
```

Target explicit migration path:

```text
dagger migrate
└─ currentWorkspace.migrate()
   ├─ read attached CompatWorkspace from current Workspace
   ├─ convert CompatWorkspace into WorkspaceMigration
   ├─ emit migration spans and warnings
   └─ return combined changeset + step metadata

CLI
└─ preview/apply combined changeset
```

## Implementation Guidance

1. Introduce a single shared engine-owned `CompatWorkspace` result in `core/workspace`.
2. Build `CompatWorkspace` from `modules.ModuleConfig`.
3. Construct `CompatWorkspace` complete in one helper, including provenance such as legacy config path and project root when available. Callers must not patch those fields later.
4. Make ambient workspace detection produce either:
   - a normal workspace
   - a compat workspace
   - or no workspace
5. Attach the originating `CompatWorkspace` to the loaded `Workspace`.
6. Add `Workspace.migrate()` in `core/schema/workspace.go`.
7. Make `Workspace.migrate()` persist the same attached `CompatWorkspace` used by runtime compat mode. It must not rediscover legacy config from disk.
8. Keep `dagger migrate` generic. It must not perform `os.Stat`, `LocalMigrationIO`, or migration-specific filesystem orchestration.
9. Keep one migration applier in product code: the changeset path. Direct host-mutation migration helpers must not remain in product code.
10. Stop giving legacy `blueprint` and `toolchains` fields generic module-loading semantics outside compat projection.
11. Delete the resulting dead generic blueprint/toolchain routing code.
12. Keep migration-specific lock/report generation inside the engine-side migration planner.

## Non-Goals

- This document does not define CWD-module behavior.
- This document does not lock the exact internal Go fields of `CompatWorkspace`.
- This document does not require multiple migration steps in the initial implementation; a single `legacy-dagger-json` step is acceptable.
- This document does not define legacy `dagger.json` blueprint behavior outside compat projection.

## Notes

- `Workspace.migrate()` returns one migration plan per workspace. Future expansion happens inside `steps`, not by returning multiple top-level migrations.
- Returning `Changeset!` directly from `Workspace.migrate` is too thin; the CLI still needs per-step code, description, and warnings for legible UX.
