# Port Blueprints to Workspace API

## Status: Implementing

## Prior Art

The toolchain-to-workspace port (`hack/designs/done/workspace-compat.md`) is the template for
this work. It followed the same pattern: extract a module-level concept to workspace-level,
add compat mode for legacy configs, clean up the module loading pipeline. Study that design
and its commits before starting.

## Problem

Blueprints are a module-level concept (`dagger.json` `"blueprint"` field) that replaces a
module's SDK with another module's SDK. But the actual intent is workspace-level: a platform
team centrally configuring many similar projects without boilerplate.

The historical lineage makes this clear: blueprint (single) -> toolchains ("stackable
blueprints") -> workspace modules. Toolchains have already been ported to workspace. Blueprints
should follow.

Meanwhile, the workspace config key `alias` (which hoists a module's functions to the Query
root) lacks a good name. The correct name is `blueprint` — it describes the intent of the
primary use case. The internal mechanism name `AutoAlias` on Module stays as-is.

## Design

### Core Insight

A blueprint is just a workspace module with `blueprint = true` in `config.toml`. The
module's functions are aliased to the Query root. It accesses project files through the
workspace API, not through `ContextSource` injection.

The entire module-level blueprint subsystem (`Blueprint`, `ConfigBlueprint`, `ContextSource`,
`isBlueprintMode`, `withBlueprint`, `loadBlueprintModule`) becomes unnecessary.

### Naming

| Layer | Current | New |
|-------|---------|-----|
| config.toml key | `alias` | `blueprint` |
| `ModuleEntry` field | `Alias` | `Blueprint` |
| `pendingModule` field | `Alias` | `Blueprint` |
| `Module` field | `AutoAlias` | `AutoAlias` (unchanged) |

### `+defaultPath` Compat: `legacy-default-path`

The `+defaultPath` annotation on module function arguments has ambiguous resolution behavior:

- **Regular modules:** `+defaultPath` resolves relative to the module's own source directory.
- **Old blueprint/toolchain modules:** `+defaultPath` resolves relative to the downstream
  module's context directory (via `ContextSource` injection).

This ambiguity is what breaks old blueprints/toolchains in the workspace world — not
`+defaultPath` itself. New behavior should be unambiguous: `+defaultPath` always resolves
from the module's own source. `ContextSource` is removed.

For legacy modules that depend on the old behavior, a separate config key
`legacy-default-path = true` opts into compat mode:

```toml
# New blueprint: +defaultPath resolves from the blueprint's own source.
# Uses workspace API for project file access.
[modules.my-platform]
source = "github.com/platform/bp"
blueprint = true

# Legacy blueprint: +defaultPath resolves from workspace root.
[modules.old-platform]
source = "github.com/old-platform/bp"
blueprint = true
legacy-default-path = true

# Legacy toolchain: same compat behavior (not a blueprint, just needs old path resolution).
[modules.old-lint]
source = "github.com/old-toolchain/lint"
legacy-default-path = true
```

At the implementation level:
- `ContextSource` is removed from `Module` entirely.
- When resolving `+defaultPath` for a module with `legacy-default-path = true`, use the
  workspace root directory instead of the module's own source.
- `dagger migrate` auto-sets `legacy-default-path = true` for migrated blueprints and
  toolchains.
- Emit a deprecation warning: `"module uses legacy-default-path; port to workspace API and
  remove this flag."`

### Compat Mode

In `detectAndLoadWorkspaceWithRootfs()`, when no `.dagger/config.toml` exists but a nearby
`dagger.json` has a `blueprint` field:

1. Extract the blueprint as a workspace-level `pendingModule` with `Blueprint: true` and
   `LegacyDefaultPath: true`
2. CWD module loads normally — module loading sees `blueprint` set + no SDK and treats it as
   a no-op (no error, no functions). Same pattern as toolchains: parsed but ignored.

This requires a small exception to the "SDK always required" rule in module loading: if `sdk`
is not set but `blueprint` is set in the config, skip silently instead of erroring. The SDK
check lives in `moduleSourceAsModule()` in `core/schema/modulesource.go` — look for where
it returns an error when `SDKImpl` is nil.

### What Gets Deleted

**ModuleSource fields** (`core/modulesource.go`):
- `ConfigBlueprint` and `Blueprint` fields
- Their handling in `Clone()` and `CalcDigest()`

**Module field** (`core/module.go`):
- `ContextSource` field and `GetContextSource()` method
- `ContentDigestCacheKey()` — replace ContextSource usage with Source
- All ContextSource callers: search for `ContextSource` across the codebase. Key sites are
  in `core/modfunc.go` (function execution context, git loading, local module checks),
  `core/llm.go` (module validation), and `core/schema/modulesource.go` (the `_contextDirectory`,
  `_contextDirectoryFile`, `_contextGit`, `_contextGitEntries` resolvers — these use
  `mod.Self().ContextSource.Value.Self()` to load files). All should use `Source` directly.

**Module loading** (`core/schema/modulesource.go`):
- `isBlueprintMode` in `moduleSourceAsModule()` — this is where `src` gets swapped with
  `src.Self().Blueprint` and `originalSrc` is preserved for `ContextSource`
- `loadBlueprintModule()` — called during source initialization alongside dependency loading
- `moduleSourceWithBlueprint()` — GraphQL resolver, validates blueprint/SDK exclusivity
- `moduleSourceWithoutBlueprint()` — clears Blueprint and ConfigBlueprint
- `moduleSourceWithUpdateBlueprint()` — reloads blueprint from latest version
- `createBaseModule()` — takes both `src` and `originalSrc` params; with blueprint gone, the
  `originalSrc` parameter and the ContextSource setup become unnecessary
- Blueprint validation constraints in config loading (search for "blueprint and sdk can't
  both be set")

**GraphQL schema** (`docs/docs-graphql/schema.graphqls`):
- `blueprint` field on ModuleSource
- `withBlueprint`, `withoutBlueprint`, `withUpdateBlueprint` mutations

**Kept for JSON compat (but ignored):**
- `ModuleConfig.Blueprint` field in `core/modules/config.go` — parsed but not acted on

### CLI Changes

`dagger init --blueprint github.com/platform/bp` changes meaning:
- Old: creates `dagger.json` with `"blueprint"` field, module has no SDK
- New: adds blueprint as workspace module in `config.toml` with `blueprint = true`

The current implementation in `cmd/dagger/module.go` calls `modSrc.WithBlueprint()` (a
GraphQL API that gets deleted). The new implementation should write to `config.toml` directly,
similar to how `dagger install` adds a module entry. Look at `cmd/dagger/workspace.go` for the
`dagger install` implementation as a pattern.

### Migration Changes

`dagger migrate` handles legacy `dagger.json` with blueprint:
- Extract blueprint to `config.toml` as workspace module with `blueprint = true` and
  `legacy-default-path = true`
- Remove `blueprint` field from `dagger.json` (module is guaranteed to have no SDK)

`dagger migrate` also sets `legacy-default-path = true` on migrated toolchains (already
handled as workspace modules, just needs the new flag).

The migration logic lives in `core/workspace/migrate.go` (`Migrate()` function). Shared
helpers for parsing legacy configs are in `core/workspace/legacy.go`. Follow the existing
`ParseLegacyToolchains()` pattern for blueprint extraction.

## Tasks

### Dependency graph

```
#1 Rename alias -> blueprint in config
 |
 v
#2 Add legacy blueprint parsing to legacy.go
 |
 +-----------------+
 |                 |
 v                 v
#3 Compat-mode    #5 Remove blueprint-awareness
 blueprint         from module loading
 extraction        (big cleanup: ContextSource,
 |                  isBlueprintMode, APIs)
 |                 |
 v                 v
#4 Update         #6 legacy-default-path compat
 migration         (resolve +defaultPath from
 |                  workspace root when set)
 |                 |
 +--------+--------+
          |
          v
         #7 Update CLI (dagger init --blueprint)
          |
          v
         #8 Tests
```

### Task list

- [x] **#1: Rename `alias` -> `blueprint` in workspace config** -- Rename `ModuleEntry.Alias`
  to `Blueprint`, update TOML tag, serialization, parsing, validation. Rename
  `pendingModule.Alias` to `Blueprint`. Keep `Module.AutoAlias` unchanged (mechanism name).
  Files: `core/workspace/config.go` (`ModuleEntry`, `SerializeConfig`,
  `SerializeConfigWithHints`, `validateConfigKey`, `parseValueString`),
  `engine/server/session.go` (`pendingModule` struct, `detectAndLoadWorkspaceWithRootfs`,
  `resolveAndServeModule`), `core/workspace/migrate.go` (`Migrate`,
  `generateMigrationConfigTOML`).

- [x] **#2: Add legacy blueprint parsing** (blocked by #1) -- Add blueprint extraction to
  `core/workspace/legacy.go` shared helpers. Parse `"blueprint"` field from legacy
  `dagger.json`, produce workspace module entry with `Blueprint: true` and
  `LegacyDefaultPath: true`. Follow the existing `ParseLegacyToolchains()` pattern. The
  legacy config struct `legacyConfig` in `legacy.go` needs a `Blueprint` field added.

- [x] **#3: Add compat-mode blueprint extraction** (blocked by #2) -- In
  `detectAndLoadWorkspaceWithRootfs()` in `engine/server/session.go`, when `!ws.Initialized`
  and nearby `dagger.json` has a blueprint, extract it as a workspace-level `pendingModule`
  with `Blueprint: true` and `LegacyDefaultPath: true`. Follow the existing toolchain
  extraction block in the same function. CWD module still loads normally (no-op due to
  blueprint + no SDK).

- [x] **#4: Update migration for blueprints** (blocked by #3) -- In `Migrate()` in
  `core/workspace/migrate.go`, extract legacy `dagger.json` blueprint to `config.toml`
  workspace module with `blueprint = true` and `legacy-default-path = true`. Remove blueprint
  field from `dagger.json`. Also add `legacy-default-path = true` to migrated toolchains.

- [ ] **#5: Remove blueprint-awareness from module loading** (blocked by #2) -- The big
  cleanup. See "What Gets Deleted" section above for the full list. Key functions to modify:
  `moduleSourceAsModule()` (remove `isBlueprintMode` and `originalSrc` swap),
  `createBaseModule()` (remove `originalSrc` parameter and ContextSource setup). Replace all
  `ContextSource` usage with `Source` across `core/module.go`, `core/modfunc.go`,
  `core/llm.go`, `core/schema/modulesource.go`. Add the "blueprint+no-SDK = silent skip"
  exception in `moduleSourceAsModule()`. Keep `ModuleConfig.Blueprint` in
  `core/modules/config.go` for JSON parse compat.

- [ ] **#6: Add `legacy-default-path` compat** (blocked by #5) -- Add `LegacyDefaultPath bool`
  to `ModuleEntry` in `core/workspace/config.go` and `pendingModule` in
  `engine/server/session.go`. Flow it through to the loaded module (new field on `Module`).
  The `+defaultPath` resolution happens during function argument default injection — search
  for `defaultPath` in `core/modfunc.go` and `core/schema/modulesource.go` to find where
  the default directory is loaded. When `LegacyDefaultPath` is set, resolve relative to the
  workspace root instead of the module's own source. Emit deprecation warning.

- [ ] **#7: Update CLI** (blocked by #3, #5) -- In `cmd/dagger/module.go`, `dagger init
  --blueprint` now adds a workspace module entry in `config.toml` with `blueprint = true`
  instead of calling `WithBlueprint()` on the module source. Follow the `dagger install`
  pattern in `cmd/dagger/workspace.go` for writing to config.toml.

- [ ] **#8: Tests** (blocked by #4, #5, #6, #7) -- Add compat mode tests (legacy `dagger.json`
  with blueprint loads correctly). Test `legacy-default-path` resolves from workspace root.
  Update tests that relied on module-level blueprint APIs. Remove tests for deleted blueprint
  code (look in `core/integration/blueprint_test.go`). Update config tests in
  `core/integration/workspace_test.go` for `alias` -> `blueprint` rename.

## Resolved Questions

- **`dagger init --blueprint` + existing workspace:** Same behavior as before. `--blueprint`
  is irrelevant to workspace initialization.
- **`dagger update` for blueprint modules:** Not a concern — workspace doesn't support
  updating yet (needs a lockfile for pinning). `withUpdateBlueprint` just gets deleted.
- **Compat shim for file access:** Solved by `legacy-default-path` config key. Old
  blueprint/toolchain modules that rely on `+defaultPath` resolving from the project context
  get `legacy-default-path = true` set during migration. New modules use the workspace API.
  `ContextSource` is removed entirely.
