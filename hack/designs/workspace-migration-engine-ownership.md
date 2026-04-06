# Workspace Migration + Compat Contract

*Builds on [Workspace Foundation + Compatibility](/Users/shykes/git/github.com/dagger/dagger_workspace/hack/designs/workspace-foundation-compat.md)*

## Status

Locked.

## Summary

This document separates two different behaviors.

First, as a temporary compatibility stopgap, an eligible legacy `dagger.json` can stand in for a missing workspace config. The engine projects it into a compat workspace (`CompatWorkspace`), uses that result at runtime, and persists that same result during `dagger migrate`.

Second, as a permanent convenience unrelated to migration, the engine separately detects the nearest `dagger.json` near the caller and loads it as the CWD module. If that module is distinct from the compat workspace's main module, it wins as the active entrypoint for the invocation.

Legacy `blueprint` and `toolchains` fields are interpreted only while building the compat workspace. Generic module loading otherwise ignores them.

## Problem

1. **Explicit migration is still CLI-owned**: `dagger migrate` currently performs local detection, local rewrite orchestration, and post-rewrite engine follow-up.
2. **Compat and migration do not share one canonical result**: the engine's legacy compat path and the on-disk migration path still model the legacy project differently.
3. **Legacy fields still leak past compat**: deprecated `dagger.json` fields like `blueprint` and `toolchains` still affect generic module loading, instead of being confined to compat-only workspace projection.

## Decision

The legacy `dagger.json` compatibility story is defined by a single engine-owned concept: the compat workspace (`CompatWorkspace`).

The engine will:

- detect either a normal workspace or a compat workspace
- use `CompatWorkspace` directly for runtime compatibility
- persist that same `CompatWorkspace` through `Workspace.migrate()`

The CLI will not own migration orchestration. It will call `Workspace.migrate()`, render the returned migration list, combine the returned changesets, and reuse the normal changeset preview/apply flow.

## Core Concepts

### Compat Workspace

`CompatWorkspace` is the in-memory workspace-shaped result inferred from an eligible legacy `dagger.json`.

It is not a legacy module. It is not a second-class workspace. It is the compatibility projection used by both runtime compat mode and explicit migration.

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

### CWD Module

The CWD module is the nearest `dagger.json` found by find-up from the caller's working directory.

It is a permanent convenience unrelated to migration.

It is detected separately from the compat workspace.

### Relationship

Workspace detection has exactly two phases:

1. Detect ambient workspace context.
2. Detect the CWD module near the caller.

These are separate concerns.

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

### 2. CWD Module Detection

After ambient workspace detection, the engine separately detects the CWD module:

```text
find-up nearest dagger.json
  -> load as CWD module
```

This step has no eligibility filter.

The CWD module is orthogonal to ambient workspace detection.

### 3. Double-Load Rule

If the CWD module and the compat workspace's projected main module refer to the same module, the engine loads it once.

### 4. Entrypoint Rule

Inside `CompatWorkspace`:

- if legacy `blueprint` exists, the blueprint is the compat entrypoint
- otherwise, the projected main module is the compat entrypoint

If there is also a separate CWD module:

- the CWD module wins as the active entrypoint for the invocation
- `CompatWorkspace` remains the ambient workspace context

If the CWD module is the same as the compat main module:

- it is loaded once
- it keeps the entrypoint role it already had through `CompatWorkspace`

### 5. Legacy Field Scope

Legacy `dagger.json` fields:

- `blueprint`
- `toolchains`

are interpreted only while building `CompatWorkspace`.

Generic module loading must otherwise ignore them. Warning-only behavior is acceptable, but they must not carry ordinary module-loading semantics outside compat projection.

This is an intentional target behavior change from the current code.

### 6. Migration Equivalence

This must hold:

```text
compat mode
==
migrate in memory, then load
```

In other words, explicit migration persists the same `CompatWorkspace` that runtime compat mode would have built in memory.

## API

```graphql
extend type Workspace {
  """
  Compute all explicit migrations needed for the current workspace.

  Returns an empty list when no migration is needed.
  """
  migrate: [WorkspaceMigration!]!
}

type WorkspaceMigration {
  """
  Stable migration code identifying the migration flow.
  New Dagger versions may introduce additional codes.
  """
  code: String!

  """
  Generic summary of the migration's purpose and impact.
  """
  description: String!

  """
  Non-fatal warnings raised while planning this migration.
  """
  warnings: [String!]!

  """
  Filesystem changes needed for this migration.
  """
  changes: Changeset!
}
```

## Engine Contract

The engine guarantees:

- every returned `WorkspaceMigration.changes` is based on the same pre-migration workspace state
- the returned list can be merged into a single `Changeset` without conflicts
- an empty list means "no migration needed"
- warnings are informational; they do not block application

If a migration wants to create `.dagger/migration-report.md`, that file is part of `changes`. It is not modeled as separate API metadata.

## CLI

`dagger migrate` is a thin wrapper:

```go
func daggerMigrate(ctx context.Context, dag *dagger.Client) error {
	migrations := dag.CurrentWorkspace().Migrate()
	if len(migrations) == 0 {
		fmt.Println("No migration needed.")
		return nil
	}

	for _, migration := range migrations {
		renderMigrationHeader(migration.Code, migration.Description)
		renderWarnings(migration.Warnings)
	}

	combined := dag.Changeset().WithChangesets(mapMigrationsToChangesets(migrations))
	return renderOrApplyChangeset(combined)
}
```

User-facing flow:

```text
$ dagger migrate
MIGRATION legacy-dagger-json
Convert legacy dagger.json compatibility workspace to .dagger/config.toml
WARNING: 2 migration gaps need manual review

[changeset preview here]

Apply changes? [y/N]
```

With `-y`, the combined changeset is auto-applied through the normal changeset path.

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

then

engine/server
└─ detect CWD module
   ├─ find-up nearest dagger.json
   ├─ dedupe if it is the compat main module
   └─ otherwise load as CWD module

then

engine/server
└─ resolve entrypoint
   ├─ CWD module wins if distinct
   └─ otherwise CompatWorkspace entrypoint applies
```

Target explicit migration path:

```text
dagger migrate
└─ currentWorkspace.migrate()
   ├─ detect workspace boundary
   ├─ parse legacy dagger.json -> modules.ModuleConfig
   ├─ build CompatWorkspace
   ├─ convert CompatWorkspace into WorkspaceMigration values
   └─ return metadata + changesets

CLI
└─ render migration list
└─ merge changesets
└─ preview/apply
```

## Implementation Guidance

1. Introduce a single shared engine-owned `CompatWorkspace` result in `core/workspace`.
2. Build `CompatWorkspace` from `modules.ModuleConfig`.
3. Make ambient workspace detection produce either:
   - a normal workspace
   - a compat workspace
   - or no workspace
4. Keep CWD-module detection separate from ambient workspace detection.
5. Remove the special "legacy extras plus separate implicit root module" split from compat handling.
6. Add `Workspace.migrate()` in `core/schema/workspace.go`.
7. Make `Workspace.migrate()` persist the same `CompatWorkspace` used by runtime compat mode.
8. Keep `dagger migrate` generic. It must not perform `os.Stat`, `LocalMigrationIO`, or migration-specific filesystem orchestration.
9. Stop giving legacy `blueprint` and `toolchains` fields generic module-loading semantics outside compat projection.
10. Keep migration-specific lock/report generation inside the engine-side migration planner.

## Non-Goals

- This document does not lock the exact internal Go fields of `CompatWorkspace`.
- This document does not require multiple migration flows in the initial implementation; a single `legacy-dagger-json` flow is acceptable.
- This document does not rename existing `config.toml` `blueprint = true` surface by itself, though the longer-term direction is to align that with `entrypoint`.

## Notes

- Returning a list from `Workspace.migrate()` is intentional. It leaves room for future independent migration flows without changing the top-level API.
- Returning `Changeset!` directly from `Workspace.migrate` is too thin; the CLI needs code, description, and warnings for legible UX.
- This document intentionally replaces the older "migration ownership only" framing. Compat behavior and the legacy root-module model are now in scope here because they are required to make migration unambiguous.

---

- Previous: [Workspace Foundation + Compatibility](/Users/shykes/git/github.com/dagger/dagger_workspace/hack/designs/workspace-foundation-compat.md)
- Next: none
