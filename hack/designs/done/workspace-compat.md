# Workspace Compat Mode + Toolchain Removal

## Status: In Progress

## Problem

When a user has a legacy `dagger.json` (with `toolchains` or `source != "."`), Dagger
currently blocks with `ErrMigrationRequired` and refuses to run until `dagger migrate`
is executed. This is a bad UX — the project works fine, it just uses deprecated config
patterns.

Meanwhile, the module loading pipeline carries significant complexity for
toolchain-awareness (`ToolchainRegistry`, `ConfigToolchains`, `IsToolchain`,
`integrateToolchains`, `CreateProxyField`, etc.) that is dead weight in the
post-migration world where toolchains are just workspace modules.

## Design

### Core Insight

Toolchains are a **workspace-level** concern, not a module-level concern. The legacy
system conflated them by putting toolchains inside `dagger.json` (module config). The
new workspace system correctly separates them into `.dagger/config.toml`. Rather than
maintaining both code paths, we:

1. Remove toolchain-awareness from the module loading pipeline entirely
2. Handle legacy toolchains at the workspace loading level (compat mode)
3. Remove the monolithic "migration required" gate

### Three Independent Changes

**A. Remove toolchain-awareness from module loading.**
The module loading pipeline (`moduleSource -> asModule`) no longer reads, resolves, or
integrates toolchains from `dagger.json`. The `toolchains` field is still parsed (JSON
compat) but ignored during loading. This deletes substantial code from
`modulesource.go`, `module.go`, and removes `toolchain.go` entirely.

**B. Add compat-mode toolchain extraction in workspace loading.**
In `detectAndLoadWorkspaceWithRootfs()`, when no `.dagger/config.toml` exists but a
nearby `dagger.json` has toolchains, extract them as workspace-level `pendingModule`
entries with config defaults derived from customizations. ~20 lines. Uses shared
helpers with `Migrate()` for DRY.

**C. Remove `CheckMigrationTriggers` as a loading gate.**
The implicit CWD module check loads `dagger.json` modules unconditionally — no trigger
check. `dagger migrate` still exists as an opt-in upgrade, but is no longer required
to run Dagger.

### Decomposed "Migration" Concept

Instead of a monolithic `CheckMigrationTriggers` that blocks on either toolchains or
non-dot source, each legacy feature is handled independently:

| Legacy feature | Action at load time | Action at migrate time |
|----------------|--------------------|-----------------------|
| `toolchains[]` | Extract as workspace-level modules | Convert to `config.toml` entries |
| `source != "."` | Just works (module loading handles it) | Relocate files to `.dagger/modules/` |

Neither feature blocks loading. Both are addressed by `dagger migrate`.

### Warning Logic

Two structural warnings based on workspace detection state:

- No `.dagger/` found, no `dagger.json` nearby:
  `info: "No workspace configured. Using <path>"`

- No `.dagger/` found, `dagger.json` nearby:
  `warn: "Inferring workspace configuration from <path/to/dagger.json>. Run 'dagger migrate' soon."`

No feature-specific checks in the warning logic. Purely structural.

### Flow Diagram

```
detectAndLoadWorkspaceWithRootfs()
     |
     v
  Detect()
     |
     v
  ws.Initialized?
   /        \
 yes         no
  |           |
  v           v
(normal)   FindUp("dagger.json")
              |
              v
           found?
            / \
          yes  no
           |    |
           v    v
        parse  info: "No workspace configured"
           |
           v
        warn: "Inferring workspace config from <path>"
           |
           v
        has toolchains?
          / \
        yes  no
         |    |
         v    v
   for each: (skip)
     add to pendingModules
     with ConfigDefaults
         |
         v
  +----------------------------------------------+
  |  Gather modules (common code, runs always):  |
  |                                              |
  |  (1) workspace config modules (if any)       |
  |  (2) implicit CWD module <-- picks up the    |
  |      project module, no trigger gate         |
  |  (3) extra modules (-m flag)                 |
  +----------------------------------------------+
         |
         v
  ensureModulesLoaded()
  (loads each pendingModule via moduleSource -> asModule;
   toolchains field in dagger.json harmlessly ignored)
```

### DRY: Shared Helpers

```
Shared between Migrate() and compat mode:
+------------------------------------------------+
| parseLegacyConfig(data) -> legacyConfig         |  (json.Unmarshal)
| extractConfigDefaults(customizations) -> map    |  (constructor defaults)
| analyzeCustomizations(toolchains) -> warnings   |  (already exists)
+------------------------------------------------+

Real migration only:                Compat mode only:
+----------------------------+     +----------------------------+
| Rewrite dagger.json        |     | Add to pendingModules      |
| Move source files          |     | (no file I/O)              |
| Write config.toml          |     +----------------------------+
| Delete old files           |
| Introspect constructors    |
+----------------------------+
```

### What Gets Deleted (Task 5)

**Data structures:**
- `ModuleSource.ConfigToolchains` / `ModuleSource.Toolchains` (`core/modulesource.go`)
- `Module.Toolchains *ToolchainRegistry`, `Module.IsToolchain` (`core/module.go`)
- `Module.ContextSource` (`core/module.go`)
- `ToolchainRegistry` / `ToolchainEntry` (entire `core/toolchain.go`)

**Logic in `core/schema/modulesource.go`:**
- Toolchain source resolution during load (~770-785)
- `integrateToolchains()` (~3497-3570, called at ~3655)
- `extractToolchainModules()` (~3114-3123)
- `applyArgumentConfigToFunction()` (~3125-3175)
- `createNoSDKModuleWithToolchains()` (~3399-3495)
- "no SDK but has toolchains" branch (~3647)
- `withToolchains`, `withUpdateToolchains`, `withoutToolchains` API (~176-192)
- Toolchain config reconstruction on write (~2100-2170)

**Logic in `core/module.go`:**
- `isToolchainModule()` and type validation exceptions (~699-752)

**Logic in `core/modulesource.go`:**
- `ConfigToolchains` / `Toolchains` in Clone()
- `ModuleRelationTypeToolchain` in `RelatedModules()` / `SetRelatedModules()`
- Toolchain entries in digest computation

**Kept for JSON compat (but ignored):**
- `ModuleConfig.Toolchains` field in `modules/config.go` — parsed but not acted on

## Tasks

### Dependency graph

```
#1 Extract shared helpers
 |
 v
#2 Compat-mode toolchain extraction
 |
 +-----------------+
 |                 |
 v                 v
#3 Remove         #5 Remove toolchain-awareness
 trigger gate         from module loading
 |                    (big cleanup)
 +------+             |
 |      |             |
 v      v             |
#4     #6             |
Warnings  Migrate cmd |
 |      |             |
 +------+------+------+
               |
               v
              #7 Tests
```

### Task list

- [x] **#1: Extract shared helpers from Migrate()** — Extract `parseLegacyConfig()` and
  `extractConfigDefaults()` from `core/workspace/migrate.go` so both real migration
  and compat mode can use them.

- [x] **#2: Add compat-mode toolchain extraction** (blocked by #1) — In
  `detectAndLoadWorkspaceWithRootfs()`, when `!ws.Initialized` and nearby dagger.json
  has toolchains, extract them as workspace-level `pendingModule` entries using shared
  helpers.

- [x] **#3: Remove CheckMigrationTriggers as a loading gate** (blocked by #2) — Remove
  the migration trigger block from `detectAndLoadWorkspaceWithRootfs()`. The implicit
  CWD module check loads dagger.json modules unconditionally. Remove `handleMigration()`
  from `session.go`.

- [x] **#4: Add workspace loading warnings** (blocked by #3) — Emit info/warn messages
  based on workspace detection state. No `.dagger/` + no dagger.json = info. No
  `.dagger/` + dagger.json = warn with migrate nudge.

- [x] **#5: Remove toolchain-awareness from module loading** (blocked by #2) — The big
  cleanup. Remove `ConfigToolchains`, `Toolchains`, `ToolchainRegistry`, `IsToolchain`,
  `integrateToolchains`, `CreateProxyField`, `extractToolchainModules`,
  `applyArgumentConfigToFunction`, `createNoSDKModuleWithToolchains`, toolchain API
  resolvers, type validation exceptions. Keep `ModuleConfig.Toolchains` for JSON parse
  compat but ignore it.

- [x] **#6: Update dagger migrate command** (blocked by #3) — Simplify detection
  (structural check: dagger.json exists + no config.toml). Remove `AutoMigrate` from
  `ClientMetadata`. Remove `handleMigration()`. Migration is explicitly invoked, not
  auto-triggered.

- [x] **#7: Update tests** (blocked by #4, #5, #6) — Add compat mode tests (legacy
  dagger.json with toolchains loads correctly, warnings emitted). Update tests that
  relied on `CheckMigrationTriggers` blocking. Remove tests for deleted toolchain
  code.

### Further simplification opportunities

- [ ] **#8: Simplify `ModuleRelationType` abstraction** — The `ModuleRelationType`
  enum, `moduleRelationTypeAccessor`, `GetRelatedModules()`, and `SetRelatedModules()`
  abstraction was designed to share code between dependency and toolchain operations.
  Now that toolchains are removed, only dependencies remain. This abstraction can be
  collapsed: replace `GetRelatedModules()`/`SetRelatedModules()` with direct
  `Dependencies` field access, remove the accessor pattern and enum.

- [ ] **#9: Review `ContextSource` on Module** — The `ContextSource` field on `Module`
  was used for both toolchain context and blueprint context. With toolchain use removed,
  review whether this field can be simplified or removed.
