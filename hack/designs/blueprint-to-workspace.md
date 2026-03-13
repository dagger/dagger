# Entrypoint Module (formerly "Blueprint / Alias")

## Status: Design partially implemented, key question unresolved

## Background

### Historical lineage

The concept of a "default module whose functions appear at the top level" has gone through
several naming iterations:

1. **`alias = true`** — original workspace config key, hoists a module's functions to Query root
2. **`blueprint = true`** — renamed from `alias` to better describe the intent (platform teams
   centrally configuring projects)
3. **`entrypoint = "module-name"`** — proposed replacement: a single top-level config key
   instead of a per-module boolean flag

### What happened with schema-level aliasing

The `alias`/`blueprint` approach worked by **schema stitching** — hoisting the designated
module's functions directly into the GraphQL `Query` type. This caused 3 categories of breakage:

1. **Recursive calls** — Module `foo` with function `foo` → `Query.foo` calls `Query.foo.foo`
   → infinite recursion
2. **Dependency clobbering** — Module defines function `dep`, also has installed module named
   `dep` → the function shadows the dependency's constructor
3. **Core API shadowing** — Module defines `container` function → `container.from()` now calls
   the module's function instead of Core's

These breakages were pervasive across the test suite and fundamentally unfixable without
abandoning schema-level hoisting.

### Current consensus

Schema-level aliasing is abandoned. Instead:

- The engine exposes `Workspace.defaultModule: String` — returns the name of the entrypoint
  module (set from the config's `blueprint = true` flag via `findBlueprint()`)
- The **CLI** queries `defaultModule` and prepends it to user queries, making it appear as
  though the module's functions are at the top level
- The config should use a **top-level `entrypoint`** key instead of per-module `blueprint` flag,
  since only one module can be the entrypoint (avoids "what if multiple are set to true?")

### What's implemented vs what's agreed

| Aspect | Agreed design | Current code |
|--------|--------------|--------------|
| Config syntax | `entrypoint = "module-name"` (top-level) | `blueprint = true` (per-module) |
| Config struct | `Entrypoint string` on workspace config | `Blueprint bool` on `ModuleEntry` |
| Engine API | `Workspace.defaultModule` | Implemented and working |
| CLI handling | Queries `defaultModule`, prepends to queries | Partially implemented in `module_inspect.go` |
| Schema hoisting | None (abandoned) | `AutoAlias` mechanism still exists but causes breakage |

**The config and struct naming needs to be updated to match the agreed design.**

## Unresolved: Client fragmentation

The CLI-side approach has a significant open concern: **every client must implement the
"defaultModule dance"** — query `Workspace.defaultModule`, then prepend it to every query.

This creates fragmentation risk:
- `dagger call`, `dagger functions`, `dagger shell`, `dagger check`, `dang`, MCP servers,
  and any third-party client all need to independently implement this
- Each command might implement it slightly differently, or not at all
- A client that just connects and introspects the schema won't see the "right" thing

### Possible resolutions (not yet decided)

1. **Accept the fragmentation** — it's a small amount of client logic, and the concept is
   easy to explain. Every client already queries the schema; querying one more field is minimal.

2. **Connection-level parameter** — tell the engine at connect time "resolve bare function
   names against this module first". The engine handles routing transparently. Clients that
   don't set it get the full namespace view; clients that do get the entrypoint experience.

3. **Query-routing below the schema** — the engine redirects unnamespaced function calls to
   the entrypoint module without merging types into Query. Avoids the schema breakage while
   keeping clients simple.

4. **Introspection metadata** — keep the full namespaced schema, but mark the entrypoint
   module distinctly in introspection results so clients can discover it without a separate
   query to `Workspace.defaultModule`.

**This question must be resolved before the config naming is finalized, since the mechanism
affects what the config key means.**

## Still valid: `+defaultPath` Compat

The `+defaultPath` compat design is orthogonal to the entrypoint question and remains valid.

`legacy-default-path = true` in `config.toml` opts a module into the old behavior where
`+defaultPath` resolves from the workspace root instead of the module's own source. This is
set automatically by `dagger migrate` for migrated blueprints and toolchains. New modules
should use the Workspace API instead.

## Completed tasks (from earlier `alias → blueprint` rename)

The following tasks were completed as part of the original blueprint port. They remain valid
regardless of how the entrypoint question is resolved — the internal `Blueprint` flag on
`ModuleEntry` is the mechanism that `findBlueprint()` uses to populate `DefaultModule`.

- [x] Rename `alias` -> `blueprint` in workspace config structs and TOML
- [x] Add legacy blueprint parsing in `core/workspace/legacy.go`
- [x] Compat-mode blueprint extraction in `detectAndLoadWorkspaceWithRootfs()`
- [x] Update migration for blueprints in `core/workspace/migrate.go`
- [x] Remove blueprint-awareness from module loading (ContextSource cleanup)
- [x] Add `legacy-default-path` compat
- [x] Update CLI
- [x] Tests

## Remaining tasks

- [ ] **Resolve the client fragmentation question** (see "Unresolved" section above)
- [ ] **Update config syntax**: once the mechanism is decided, replace per-module
  `blueprint = true` with top-level `entrypoint = "module-name"` (or whatever the final
  config surface looks like)
- [ ] **Update `ModuleEntry` struct**: remove `Blueprint bool`, add entrypoint to workspace
  config level
- [ ] **Clean up `AutoAlias` remnants**: the schema-hoisting mechanism is abandoned but
  code references remain
