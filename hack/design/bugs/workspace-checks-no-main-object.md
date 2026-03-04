# Workspace Checks/Generators: "no main object" Error

## Status

Done

## What

`dagger check -l` and `dagger generate -l` crash with:

```
checks from module "dagger-cli": "dagger-cli": no main object
```

The error fires immediately after module loading completes. It affects any workspace where a module has a dependency whose **alias differs from its intrinsic name** (e.g., `engine-dev/dagger.json` declares dependency `"name": "dagger-cli"` pointing to source `../cli-dev`, whose own `dagger.json` says `"name": "cli-dev"`).

Expected behavior:

- `dagger check -l` lists all checks defined by workspace modules.
- `dagger generate -l` lists all generators defined by workspace modules.
- Neither command should inspect dependency-only modules at all.

## Why

Two issues interacting:

### 1. `MainObject()` uses post-namespacing `obj.Name` for lookup

When a module is loaded, its type definitions go through `namespaceTypeDef` → `namespaceObject`. For the main object, this rewrites `obj.Name` to match the module's **final name** (NameField), while `obj.OriginalName` keeps the SDK-registered name.

`MainObject()` then looks up by `mod.OriginalName` (the intrinsic dagger.json name) using `ObjectByName()`, which compares against `obj.Name` (the post-namespacing name). When those names diverge, the lookup fails.

Concrete trace for the `dagger-cli` case:

```
Module loaded via withName("dagger-cli"):
  mod.NameField      = "dagger-cli"
  mod.OriginalName   = "cli-dev"     (from dagger.json)

SDK produces main object:
  obj.Name           = "CliDev"      (PascalCase of "cli-dev")
  obj.OriginalName   = "CliDev"

namespaceObject("CliDev", "dagger-cli", "cli-dev"):
  → detects main object (TrimPrefix("CliDev", "CliDev") == "")
  → rewrites obj.Name = gqlObjectName("dagger-cli") = "DaggerCli"
  → obj.OriginalName stays "CliDev"

MainObject() → ObjectByName("cli-dev"):
  → gqlObjectName("cli-dev") = "CliDev"
  → compares with gqlObjectName(obj.Name) = gqlObjectName("DaggerCli") = "DaggerCli"
  → "CliDev" ≠ "DaggerCli" → NOT FOUND → "no main object"
```

### 2. Workspace checks/generators iterate dependency modules

The workspace `checks()` and `generators()` resolvers in `core/schema/workspace.go` called `served.Mods()`, which returns **all** served modules — including dependencies served with `skipConstructor=true`. This exposed dependency modules (which may have aliased names) to `NewModTree()` → `MainObject()`, triggering the crash.

Before the workspace-first refactor, `dagger check` loaded a single module directly via `loadModule()` and only queried that module's checks. The new workspace path queries `CurrentServedDeps()` and iterates everything, including deps that were never iterated before.

## Task List

- [x] Reproduce in playground: `cd src/dagger && dagger check -l` → crash.
- [x] Trace error to `NewModTree` → `MainObject` → `ObjectByName` name mismatch.
- [x] Identify that `namespaceObject` rewrites `obj.Name` but `MainObject` lookups use `obj.OriginalName`.
- [x] Identify that `served.Mods()` includes dependency-only modules.
- [x] Fix `MainObject()` to compare against `obj.OriginalName`.
- [x] Add `PrimaryMods()` to `ServedMods` and use it in checks/generators.
- [x] Validate both `dagger check -l` and `dagger generate -l` in playground.

## Investigation Log

- 2026-03-04: Reproduced in playground. Error: `checks from module "dagger-cli": "dagger-cli": no main object`.
- 2026-03-04: Traced the `"dagger-cli"` module to `engine-dev/dagger.json` dependency entry `{"name": "dagger-cli", "source": "../cli-dev"}`. The source module's own name is `"cli-dev"`.
- 2026-03-04: Found that `namespaceObject` in `core/module.go:779` rewrites the main object's `Name` from `"CliDev"` to `"DaggerCli"` (matching the dependency alias), but `OriginalName` stays `"CliDev"`.
- 2026-03-04: `MainObject()` calls `ObjectByName(mod.OriginalName)` which compares `gqlObjectName(obj.Name)` — a post-namespacing value — against `gqlObjectName("cli-dev")`. Mismatch.
- 2026-03-04: Also found that `workspace.checks()` and `workspace.generators()` iterate `served.Mods()`, which includes dependency-only modules. These should never contribute checks/generators to the workspace.
- 2026-03-04: Implemented two-part fix and validated both commands in playground. Both succeed.

## Root Cause

Two issues combined:

1. `MainObject()` used `ObjectByName()` which compares against `obj.Name` — a post-namespacing value that reflects the module's final (possibly aliased) name. But the lookup key is `mod.OriginalName` — the intrinsic name from `dagger.json`. When a dependency is aliased, these diverge and the lookup fails.

2. Workspace `checks()`/`generators()` iterated all served modules via `Mods()`, including dependency-only modules that were never meant to contribute checks or generators. This exposed aliased dependency modules to `MainObject()`, triggering the crash.

Before workspace-first, `dagger check` loaded a single module and queried only its checks — dependency modules were never iterated for checks/generators.

## Fix

Three targeted changes:

- file: `core/module.go`
  - Add `ObjectByOriginalName()`: looks up objects by `obj.OriginalName` instead of `obj.Name`.
  - Change `MainObject()` to call `ObjectByOriginalName()` instead of `ObjectByName()`.
  - `ObjectByName()` is left unchanged — it is used elsewhere for lookups that correctly need the namespaced name (e.g., `modtree.go:583` matching return types by their namespaced GQL name).

- file: `core/served_mods.go`
  - Add `PrimaryMods()`: returns only modules with `skipConstructor == false` (directly-loaded modules, not dependencies).

- file: `core/schema/workspace.go`
  - Change `checks()` and `generators()` to iterate `served.PrimaryMods()` instead of `served.Mods()`.

Result: dependency modules are excluded from workspace checks/generators, and `MainObject()` correctly resolves the main object regardless of module aliasing.

## Test Notes

Validated through playground runs:

1. `dagger check -l`: lists all 60+ checks from workspace modules, no errors.
2. `dagger generate -l`: lists all 8 generators from workspace modules, no errors.

The fix is also structurally safe:
- `ObjectByOriginalName()` compares `obj.OriginalName` (always the SDK-registered name, never rewritten) against `mod.OriginalName` (always from `dagger.json`). These are guaranteed to match for the main object.
- `PrimaryMods()` uses the existing `skipConstructor` flag which already distinguishes directly-loaded modules from dependency-only modules.
