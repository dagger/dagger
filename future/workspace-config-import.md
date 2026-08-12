# Workspace Config Import

author: eunomie
created: 2026-08-12
status: draft

## Progress

| Field | Value |
| --- | --- |
| Phase | 2 — implementation plan |
| Branch | `feature-dev-a451d460` |
| Base | `upstream/main` @ `49a8726ee` |
| PR | _not opened yet_ |
| Last green SHA | _none_ |

## Problem

Teams that run Dagger across many repositories duplicate the same `dagger.toml`
in every one of them: the same toolchain modules, the same pinned tool versions,
the same module settings, the same env definitions. When a version moves, every
repository has to be edited.

This is a confirmed want, not a hypothetical one. From Discord:

- _vindico_: "I'm currently putting duplicated workspace config in a lot of repos."
- _chrisp6229_: asked to publish standard workspace configs per project type
  (go / node / terraform) and have downstream repos point at one, so tool
  versions are managed in one place.
- _shykes_: "As long as we don't go crazy with the config layering and other
  complications, it seems reasonable. Like a basic import feature in dagger.toml."

There is no mechanism today. `dagger.toml` is read from exactly one file; the
only layering that exists is the user-level overlay (`[workspaces.*]` in the
user config) and env overlays (`[env.<name>]`), both of which are local to the
machine or to the file itself.

## Goals

- A workspace can declare **one** git ref whose `dagger.toml` is loaded and
  merged **underneath** its own config.
- The current workspace wins on every conflict, with a merge rule that is
  written down per config section rather than left to whatever the code does.
- The resolved import is pinned in `dagger.lock` like every other git ref this
  repo resolves, and refreshes under `dagger update` / `--lock=live`, replays
  from the lock under `--lock=frozen`.
- Only _remote_ module references survive the import. A local module reference
  in the imported config must not become loadable in the importing workspace.
- The merged result is what read surfaces (`dagger config`, module listing, env
  listing) report, so the effect is visible rather than silent.

## Non-goals (YAGNI)

- **No multi-import composition.** One import per workspace, full stop. No list,
  no ordered layer stack, no diamond resolution.
- **No transitive imports.** If the imported workspace itself declares `import`,
  that is an error (see [Decision: transitive imports](#decision-transitive-imports)),
  not a second layer.
- **No local-path imports.** `import = "../base"` is rejected. Imports point at
  published configs.
- **No new lock operation kind.** The import reuses the existing `git.head` /
  `git.ref` / `git.tag` lock entries.
- **No import of the base workspace's local module tree**, generated clients, or
  SDK-managed authoring state.
- **No write-through.** `dagger install`, `dagger config <key> <value>` and every
  other mutation keep writing the _local_ `dagger.toml` only. The import is
  read-only input.

## Proposed approach

### Config surface

A new top-level scalar key in `dagger.toml`:

```toml
import = "github.com/acme/dagger-base@v1.2.0"

[modules.go]
settings.goVersion = "1.24"
```

The value is the same shape of git ref the workspace already accepts for
`-W <ref>` (`github.com/acme/base@v1`, `https://host/repo.git#refs/heads/main`,
`…#main:subdir`). It resolves to a _workspace root_, not to a module: the
imported repository (or subdir) is detected as a workspace and its `dagger.toml`
is read from there.

Scalar rather than `[import] source = "…"`: there is exactly one field, the pin
lives in `dagger.lock` by design, and "a basic import feature" is the explicit
brief. Growing it into a table later is a compatible change only if the scalar
form stays accepted, so this is a real one-way door — accepted deliberately.

### Where it happens

```mermaid
graph TD
    A["dagger.toml (current workspace)"] -->|"import = ref"| B[resolve git ref]
    B --> C["git tree @ commit<br/>lock: git.ref/git.head entry"]
    C --> D["detect workspace root<br/>read its dagger.toml"]
    D --> E{"imported config<br/>declares import?"}
    E -->|yes| F["error: nested imports<br/>are not supported"]
    E -->|no| G[merge]
    A --> G
    G --> H{"module entry from the import<br/>with a local source?"}
    H -->|yes| I["error: local module<br/>cannot be imported"]
    H -->|no| J[effective workspace config]
```

Layering, lowest to highest — the import slots in _below_ everything that
exists today, so no current precedence changes:

```mermaid
graph BT
    I["imported dagger.toml"] --> R["repo dagger.toml"]
    R --> U["user-level overlay<br/>(~/.config [workspaces.*])"]
    U --> E["--env overlay"]
    E --> F["effective config"]
```

Two engine-side choke points consume the merged config:

1. `engine/server/session_workspaces.go` — workspace module loading, merged
   before the user overlay and the env overlay are applied.
2. `core/schema/workspace_config.go:readWorkspaceConfig` — the shared read path
   behind `dagger config`, `Workspace.modules`, `Workspace.envList`,
   `Workspace.sdks`, `currentModule.asSDK`.

Plus `moduleSourceSchema.workspaceModuleSourceByName`, which resolves
`dagger call -m <name>` against the cwd workspace config before a workspace is
built, and reads `dagger.toml` directly.

Config **writers** (`loadWorkspaceConfigForOverlay`) keep parsing the raw local
file. Reads are effective; writes are local. That is the same split the env
overlay already uses.

### Merge semantics, per section

`current` is the importing workspace's own `dagger.toml`; `imported` is the
resolved one. "Set" means non-zero for scalars, non-empty for lists and maps.

| Section / key | Rule |
| --- | --- |
| `import` | Never inherited. The current workspace's own value stays in the effective config so the source is visible. An `import` in the imported config is an error. |
| `ignore` | Current replaces when set; otherwise the imported list. No union — a union would make it impossible for a downstream repo to drop an inherited pattern. |
| `defaults_from_dotenv` | Current wins when `true`. `false` is indistinguishable from unset in the parsed config, so a downstream repo cannot turn off an inherited `true` (see [Risks](#risks)). |
| `check-generated` | `*bool`; current wins when non-nil, otherwise the imported value. |
| `modules.<name>` | Merged **per field**, not wholesale, so a downstream repo can override one setting without repeating `source`. |
| `modules.<name>.source` | Current wins when set. When the current entry sets `source`, its `pin` travels with it and the imported `pin` is discarded — the same coupling `applyModuleOverlays` already uses for env overlays. |
| `modules.<name>.pin` | Current wins when set, unless overridden by the source coupling above. |
| `modules.<name>.settings.<key>` | Merged key by key; current wins per key. |
| `modules.<name>.entrypoint`, `legacy-default-path` | Current wins when `true`; `false` reads as unset. |
| `modules.<name>.{up,generate,check}.skip` | Current replaces when non-empty. |
| `modules.<name>.as-sdk` | Current replaces wholesale when present. From the import, the marker and `name` are kept; `as-sdk.modules` and `as-sdk.clients` are **dropped** — they are workspace-root-relative paths into the _base_ repo's tree, which is exactly the local state an import must not reach into. |
| `env.<name>` | Merged per env name; an env that only the import defines is selectable downstream. |
| `env.<name>.modules.<mod>` | Same field rules as `modules.<name>` (source/pin coupling, settings key by key). |
| `ports.<host>` | Current replaces wholesale per host port. A `PortMapping` is one unit (service + port); half-overriding it yields a mapping nobody wrote. |

### Limitation 1 — one import, no chain {#decision-transitive-imports}

**Decision: an `import` key in the imported config is an error**, reported at
workspace load with both refs named.

Silently ignoring it would hand the downstream user a config that is missing
whatever the base author expected to inherit, with no signal — the failure would
surface much later as a missing module or a wrong setting. An error is
actionable and fails closed, which is what this repo does elsewhere. The cost is
that a base config author cannot themselves import; that is the scope control
being asked for, and the error says so.

### Limitation 2 — no local modules from the import

Enforced explicitly, in the merge function, **after** the merge:

> any module entry that survived from the imported config (the current workspace
> does not define a `source` for that name) whose effective source is a local
> ref → error naming the entry, the import ref, and the offending source.

It is deliberately _not_ left to fall out of resolution. It does not: local
module sources are resolved by `workspace.ResolveModuleEntrySource(configDir, …)`
against the _importing_ workspace's config dir, so `source = "modules/ci"`
inherited from a base repo would silently resolve to `modules/ci` **in the
importing repo** — loading a different module, or failing with a confusing
"module not found" that names a path the user never wrote. Checking after the
merge means an entry the downstream repo overrides with its own source is fine,
because the imported entry did not survive.

The same check covers env-overlay module sources (`env.<name>.modules.<mod>.source`).
`as-sdk.modules` / `as-sdk.clients` paths are dropped by the merge rule above, so
they cannot leak. `ports.<host>.backendService` names a module, not a path, so it
follows whatever that module resolved to.

### Lockfile

No new lock entry kind. The import ref is resolved through the ordinary
`git(url).head` / `git(url).ref(name)` dagql path, which already:

- writes a `git.head` / `git.ref` entry into `dagger.lock` (namespace `""`,
  inputs `[remote, name]`, policy `pin` for tags and `float` otherwise);
- is refreshed by `core.UpdateWorkspaceLock` — so `dagger update` and
  `--lock=live` refresh an import with zero new code;
- replays the stored pin under `--lock=frozen`, and errors when the entry is
  missing, exactly like every other frozen lookup;
- under `--lock=pinned`, reuses the stored pin for a tag ref and re-resolves a
  branch/HEAD ref, which is this repo's current behavior for _all_ git refs, not
  something this feature chooses.

So the round trip is: fresh resolve → `git.ref` entry written on session flush →
frozen replay reads the pin and never touches the remote.

### CLI-visible behavior

- `dagger config` (no key) prints the **merged** config, with the current
  workspace's `import = "…"` line still in it — so the merged view names its own
  source. This matches the existing precedent: `dagger config` already reports
  the effective view when a user overlay or `--env` is in play.
- `dagger config <key>` reads the merged value, same rule.
- `dagger config import <ref>` / `--unset` set and clear the key through the
  existing dotted-key machinery.
- Import resolution runs inside a telemetry span named
  `importing workspace config: <ref>`, mirroring the existing
  `applying env: <name>` span, so it is visible in the TUI and in traces and any
  fetch latency is attributed.
- The raw local file remains available: `dagger workspace config-file` prints its
  path, and it is what every write touches.

## Alternatives considered

**A general `extends`/layer list.** Explicitly rejected upstream ("as long as we
don't go crazy with the config layering"). Multi-layer merge forces an ordering
model, conflict-precedence rules across layers, and diamond resolution — all
before anyone has asked for a second layer.

**Import at the module level (`[modules.X] from = "…"`)**. Solves nothing the
existing remote module source doesn't; the duplication complained about is the
_set_ of modules and their settings, not one entry.

**Store the import pin inline in `dagger.toml` (`import-pin = "sha"`),** like
`[modules.X].pin`. Rejected: the workspace design states `dagger.toml` carries no
resolved versions — those live in `dagger.lock` — and the task explicitly asks
for the lockfile. Reusing `git.ref` entries also gets `dagger update` for free.

**Merge wholesale per module entry instead of per field.** Simpler rule, but it
forces a downstream repo that wants to bump one setting to copy the module's
`source` too, which re-introduces exactly the duplication this feature removes.

**Silently skip local module entries from the import** instead of erroring.
Rejected: the downstream user then sees a module that the base repo's README
promises simply not exist, with nothing to grep for.

**Resolve the import in the CLI** rather than the engine. The CLI has no git
resolution or lockfile machinery; the engine has both, and the merged config has
to be right for module loading, which is engine-side anyway.

## Affected components

| Area | Change |
| --- | --- |
| `core/workspace/config.go` | `Config.Import` field; merge + validation function; serialization; `setConfigValue` case |
| `core/workspace/config_test.go` | merge table, precedence, local-module rejection, nested-import rejection |
| `core` (new file, e.g. `core/workspace_import.go`) | resolve an import ref: parse → clone tree → detect workspace → read + parse `dagger.toml` |
| `engine/server/session_workspaces.go` | `parseWorkspaceRemoteRef` / `cloneGitTree` delegate to the new `core` helpers; merge imported config during workspace load |
| `core/schema/workspace_config.go` | merge in `readWorkspaceConfig` (read choke point) |
| `core/schema/modulesource.go` | merge in `workspaceModuleSourceByName` |
| `core/integration/workspace_config_test.go` (or a new `workspace_import_test.go`) | multi-workspace fixture over a git service |
| `docs/` | `dagger.toml` reference gains `import` |

Untouched on purpose: every config **write** path, the lockfile format, the
`Workspace` GraphQL surface (no new field — the merged config flows through
existing fields).

## Testing

Unit (`core/workspace`), on the pure merge function:

- every row of the merge table above, including the source/pin coupling;
- current-wins precedence for each section;
- a local module source surviving from the import → error;
- an env-overlay local source surviving from the import → error;
- a local source in the import that the current config overrides → no error;
- `as-sdk.modules` / `as-sdk.clients` dropped from the import, marker kept;
- imported config declaring `import` → error.

Integration (`core/integration`), with the base workspace served by
`gitSmartHTTPServiceDirAuth` at an IP-addressable URL — the pattern
`workspaceSelectionRemoteRef` already uses, which is reachable from the engine:

1. **Merge**: importing workspace declares `import` plus one setting override;
   `dagger config` shows the base's modules with the override applied, and
   `dagger call <imported-module>` runs.
2. **Conflict**: same module name in both → the current workspace's `source`
   wins; settings merge key-wise.
3. **Local module blocked**: base config declares a local module → load fails
   with the explicit error, and the failure does _not_ resolve a same-named
   directory in the importing repo (the mis-resolution case is asserted
   directly).
4. **No chain**: base config itself declares `import` → explicit error.
5. **Lockfile round trip**: fresh run writes a `git.ref`/`git.head` entry for the
   import into `dagger.lock`; a second run under `--lock=frozen` reproduces the
   same merged config; a lock whose import entry is removed fails under frozen.
6. **Env from import**: an env defined only in the base config is selectable with
   `--env` downstream.

## Risks

- **`defaults_from_dotenv` cannot be turned off downstream.** The field is a
  plain `bool`, so `false` and unset are the same value after parsing. Narrow,
  documented; the fix (presence tracking or `*bool`, as `check-generated` already
  does) is a separate change.
- **Workspace load grows a network round trip** when an import is declared: a
  ref resolution plus a git tree fetch, both dagql-cached and both skipped
  entirely when no `import` key exists. Under `--lock=frozen` the ref resolution
  is replaced by the stored pin.
- **A read surface reached before the workspace is built** (`workspaceModuleSourceByName`)
  now performs git access. Failure there must degrade to a clear error, not a
  silent "module not found".
- **`dagger config` becoming an effective view** may surprise someone expecting
  a `cat` of the file. Mitigated by the `import` line staying visible, by the
  same behavior already existing for `--env` and user overlays, and by
  `dagger workspace config-file`.
- **Init flows against an imported SDK.** `loadWorkspaceConfigForOverlay` is a
  write path and stays unmerged, so `dagger module init <sdk>` will not find an
  SDK that only the import installs. Known gap, called out in the PR; the
  workaround is to install the SDK locally, which is what the entry would have
  to record anyway since init writes SDK-managed paths into the local config.

## Related prior art

- `hack/designs/workspace.md` — `dagger.toml` shape, env overlays, and the
  "`dagger.toml` carries no resolved versions" rule this feature follows.
- `hack/designs/lockfile.md` — lookup entries, policy, and lock modes reused
  verbatim here.
- `future/done/2026-05-27-workspace-disable-inheritance.md` — a _different_
  inheritance: runtime workspace authority across module calls. Unrelated
  mechanism, but the same instinct — inheritance must be explicit and bounded.
- `future/synthetic-workspace.md` — `GitRef.asWorkspace`; the same "a git ref can
  denote a workspace" idea the import ref relies on.

## Implementation plan

### Patch series

| # | Patch | Scope |
| --- | --- | --- |
| 1 | `workspace-import-config` | `core/workspace`: the `import` key, merge + validation, unit tests |
| 2 | `workspace-import-resolve` | `core`: remote workspace ref parsing/cloning moved out of `engine/server`, plus the imported-config loader |
| 3 | `workspace-import-load` | `engine/server`: resolve and merge during workspace load |
| 4 | `workspace-import-reads` | `core/schema`: merge in the read choke points |
| 5 | `workspace-import-tests` | `core/integration`: multi-workspace fixture and the six cases |
| 6 | `workspace-import-docs` | `dagger.toml` reference gains `import` |

Each patch builds and tests on its own. 1 and 2 carry their own unit tests; 3
and 4 are wiring covered by 5.

### Patch 1 — `core/workspace`

`config.go`:

- `Config.Import string` with `json:"import,omitempty" toml:"import,omitempty"`,
  declared first in the struct so `validTOMLFieldNames` lists it naturally.
- `SerializeConfig`: emit `import = "…"` before `ignore`, followed by a blank
  line, matching how the other top-level scalars render.
- `cloneConfig`: copy `Import`.
- `setConfigValue`: an `"import"` case, single-segment only, string value —
  mirroring the `"ignore"` case's shape.
- `config_document.go:configDocumentMap`: `values["import"]` when non-empty, so
  document-preserving updates and `deleteRemovedConfigRoots` handle it like the
  other scalars.

New `core/workspace/import.go`:

```go
// MergeImportedConfig layers current on top of imported and returns the
// effective config. imported may be nil (nothing to merge).
func MergeImportedConfig(imported, current _Config) (_Config, error)
```

Behavior, in order:

1. `imported.Import != ""` → `ErrNestedWorkspaceImport`, naming both refs.
2. Clone `imported` as the base; drop `Import` from it; drop every
   `AsSDK.Modules` / `AsSDK.Clients` while keeping the marker and `Name`.
3. Apply `current` field by field per the merge table, tracking which module
   names (and `env.<name>.modules.<mod>` overlays) still carry a source that
   came from `imported`.
4. For each of those, classify the source with
   `gitref.FastKindCheck(source, pin)`. `KindLocal` → error naming the module,
   the source and the import ref. `KindGit` and `KindUnknown` pass:
   `github.com/acme/base` — the most common remote form — classifies as
   `KindUnknown`, so requiring `KindGit` would reject the normal case.
   `core/workspace` may import `core/gitref` (leaf package, no cycle), and this
   is deliberately the _same_ classifier `FastModuleSourceKindCheck` and the
   module loader use, so validation cannot drift from resolution.
5. `current.Import` is preserved on the result.

Errors are typed (`NestedImportError`, `LocalImportedModuleError`) so the load
paths can wrap them with the config path without string matching.

Tests in `core/workspace/import_test.go`: one table-driven test per merge-table
row, plus the error cases and a round trip
(`SerializeConfig` → `ParseConfig` preserves `Import`).

### Patch 2 — `core`

New `core/workspace_import.go` (package `core`):

- `ParseWorkspaceRemoteRef(ctx, ref) (WorkspaceRemoteRef, error)` and
  `CloneWorkspaceGitTree(ctx, dag, cloneRef, version) (Directory, GitRef, error)`
  — moved verbatim from `engine/server/session_workspaces.go`
  (`parseWorkspaceRemoteRef`, `cloneGitTree`), which then delegates. One
  implementation, no behavior change; the move is what lets `core/schema` and
  `engine/server` share it.
- `LoadImportedWorkspaceConfig(ctx, dag _dagql.Server, ref string) (_workspace.Config, error)`:
  reject a `gitref.KindLocal` ref up front; parse; clone; `workspace.DetectInRoot`
  inside the tree at the ref's subdir; error when the imported tree has no
  `dagger.toml`; read and `ParseConfig` it. Wrapped in a span named
  `importing workspace config: <ref>`.

The clone runs through the ordinary `git(url).head` / `.ref(name)` dagql
selectors, which is what makes the lockfile entry appear — no lock code here.

### Patch 3 — `engine/server/session_workspaces.go`

In `detectAndLoadWorkspaceWithRootfs`, inside the `loadModules` block and
_before_ `ApplyUserOverlay`:

```go
if wsConfig != nil && wsConfig.Import != "" {
    imported, err := core.LoadImportedWorkspaceConfig(ctx, client.dag, wsConfig.Import)
    if err != nil { return err }
    wsConfig, err = workspace.MergeImportedConfig(imported, wsConfig)
    if err != nil { return err }
}
```

Placement matters and is load-bearing:

- after `client.workspace = coreWS`, so the workspace lock binding exists and
  the git resolution's lock entry lands in this workspace's `dagger.lock`;
- before the user overlay and the env overlay, so the layering is
  import → repo → user → env.

### Patch 4 — `core/schema`

- `workspace_config.go:readWorkspaceConfig` — after `ParseConfig`, when
  `cfg.Import != ""`, resolve and merge with the same two calls. This is the
  single read choke point behind `dagger config`, `Workspace.modules`,
  `Workspace.envList`, `Workspace.sdks` and `currentModule.asSDK`, so all of
  them report the merged view from one change.
- `modulesource.go:workspaceModuleSourceByName` — same, so `-m <name>` resolves
  a module the import contributes.
- `workspace_builders.go:loadWorkspaceConfigForOverlay` is deliberately **not**
  touched: writes stay local. A comment states that.

### Patch 5 — `core/integration/workspace_import_test.go`

Fixture helper, following `workspaceSelectionRemoteRef`:

```go
func workspaceImportBaseRef(ctx, t, c, base *dagger.Directory) string
```

serving the base workspace over `gitSmartHTTPServiceDirAuth` and returning an
IP-addressed `http://…/repo.git@main` — reachable from the engine, which the
`ExperimentalServiceHost` form is not, since the engine resolves the import
itself.

The six cases from [Testing](#testing), each a `t.Run` in one suite test, run
against a container workspace whose `dagger.toml` carries the import.

### Patch 6 — docs

`docs/` `dagger.toml` reference: the `import` key, the merge order, the two
limitations, and the fact that the pin lives in `dagger.lock`.

### Verification

```sh
go build ./...
go test ./core/workspace/...
golangci-lint run core/workspace/... core/schema/... engine/server/...
dagger call engine-dev test --pkg="./core/integration" --run='^TestWorkspaceImport'
```

### Open questions taken as decided

- **Key name**: scalar `import`. Table form is the alternative and is a one-way
  door; recorded in [Config surface](#config-surface).
- **Nested import**: error, not ignore.
- **Local module from the import**: error at merge, not silent skip.
- **`dagger config` view**: effective/merged, consistent with `--env`.
- **Remote (`-W <ref>`) workspaces that declare an import**: resolved the same
  way. They have no host lock binding, so under `pinned` the lookup degrades to
  live and under `frozen` it reads the remote's committed `dagger.lock` — the
  existing behavior for every other lookup in a remote workspace, inherited, not
  redesigned.
