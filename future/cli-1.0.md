# CLI 1.0: Top-level command surface

A redesign of the Dagger CLI's user-facing command surface for 1.0. Untangles the workspace/module namespace duality, hoists daily-use verbs to top-level, introduces a dedicated module-authoring lane, and renames a load-bearing flag to remove a long-standing semantic conflation.

## Table of Contents
- [Problem](#problem)
- [Solution](#solution)
- [Target `--help`](#target---help)
- [Group rationale](#group-rationale)
- [Flag rename: `--mod` → `--load-module`](#flag-rename---mod---load-module)
- [Per-command decision context](#per-command-decision-context)
- [Subcommand structures](#subcommand-structures)
- [SDK module interface](#sdk-module-interface)
- [Discrete changes from current CLI](#discrete-changes-from-current-cli)
- [Status](#status)

## Problem

1. **`dagger mod` carries two meanings.** Its subcommands mix workspace-consumer verbs (`install`, `uninstall`, `list`, `search`, `recommend`) with module-authoring verbs (`deps add`, `engine require`) — the same noun, two unrelated subjects. The `--mod` flag's overloaded role (load a module vs. select a module to edit) is the load-bearing evidence; this is what caused the deps/engine work to be rolled back from PR #13226 before merge.

2. **`dagger workspace` is redundant for the hot path.** Most workspace commands map to operations users run constantly (install, uninstall, update). Burying them behind a `workspace` noun-prefix is taxation on every invocation and runs against the precedent of cargo, npm, and git.

3. **No authoring lane exists.** The CLI has no clean home for commands that mutate a single `dagger.json`. The deps/engine work had nowhere to go, so it was crammed into `dagger mod` and ultimately cut.

4. **`--mod`/`-m` predates the workspace concept.** It was named when "the module" was the only operating subject. With workspaces and modules-under-authorship now distinct concepts, the flag actively misleads.

5. **Generic verb risk is unmanaged.** Naked top-level verbs (`install`, `update`, `search`) read out of context until the user reads the help text. Today's CLI mitigates this by noun-prefix grouping. A flat redesign has to do that work elsewhere — verb naming, description text, visual grouping.

## Solution

Adopt a flat top-level verb surface for the consumer hot path. Eliminate `dagger mod` from the visible tree and slim `dagger workspace` to plumbing only. Introduce `dagger module` as the dedicated authoring lane (subcommands: `init`, `deps`, `engine`). Introduce `dagger setup` as the idempotent "ensure environment works" verb. Rename `--mod`/`-m` to `--load-module`/`-m`.

Organize the top-level surface into five visually separated groups, each with one coherent theme. Use descriptions, not noun-prefixes, to disambiguate.

## Target `--help`

```
A tool to run composable workflows in containers

USAGE
  dagger [options] [subcommand | file...]

AVAILABLE COMMANDS
  setup        Ensure Dagger is properly set up and operational in the workspace

  check        Verify your project — tests, linters, type checks, security scans, etc.
  generate     Generate derived files for your project — code, SDKs, types, docs, etc.
  up           Run your project's services for local development — databases, APIs, dev servers, etc.
  activity     Show recent activity (runs, traces, etc.) for this workspace

  install, i    Install a module into your workspace
  uninstall, un Uninstall a module from your workspace
  installed     List installed modules
  update        Refresh installed-module state
  list, ls      List artifacts (types, tests, etc.) — see modules-v2
  search        Search for modules you can install
  settings      Get or set module settings (use --env for an env overlay)

  api             Interact with the Dagger API (advanced)
  module          Author a module: edit dependencies, engine version, etc.
  cloud           Manage Dagger Cloud (login, integrations, etc.)
  workspace, ws   Inspect or configure your workspace (cwd, remotes, config, etc.)

  exec         Execute a command in a Dagger session
  help         Help about any command
  version      Print dagger version

OPTIONS
  -y, --auto-apply               Automatically apply changes
  -d, --debug                    Show debug logs and full verbosity
      --env string               Apply (or write to) a named env overlay. Envs are
                                 paths under env.<name>.* in workspace config; see
                                 `dagger workspace config env`
  -i, --interactive              Spawn a terminal on container exec failure
  -m, --load-module string       Use a one-off module (local path or git ref)
      --no-load-module           Don't load any module for this command
  -W, --workspace string         Select the workspace location to load from
                                 (local path or git ref)
```

## Group rationale

| Group | Theme | Commands |
|-------|-------|----------|
| 1 | First contact | `setup` |
| 2 | Daily project flow | `check`, `generate`, `up`, `activity` |
| 3 | Workspace management | `install`, `uninstall`, `installed`, `update`, `list`, `search`, `settings` |
| 4 | Specialized toolboxes | `api`, `module`, `cloud`, `workspace` |
| 5 | Utility / meta | `exec`, `help`, `version` |

Visual separation does the disambiguation work that noun-prefix grouping (`dagger workspace X`, `dagger mod Y`) tried to do structurally. Group 4 clusters the four major namespaces — `api`, `module`, `cloud`, `workspace` — each with its own subcommand surface. Group 5 is utility; group 3 is the daily-loop module verbs; group 2 is the three shipping fundamentals plus activity.

## Flag rename: `--mod` → `--load-module`

The old `--mod` carried two unrelated meanings: "load a module for this invocation" (consumer) and "select a module to operate on" (author). That conflation is what caused PR #13226's deps/engine commands to be cut. The rename names the actual job; authoring commands take no module-targeting flag at all (see [decision context](#per-command-decision-context)).

| Before | After |
|--------|-------|
| `-m`, `--mod string` | `-m`, `--load-module string` |
| `-M`, `--no-mod` | `-M`, `--no-load-module` |

`--load-module` was chosen over `--with-module` because Dagger's `WithX` API methods are chainable — `--with-module X --with-module Y` would carry a "load both" implication the flag cannot honor (it is single-valued). `--load-module` is verb-form and singular by reading.

Authoring commands (`dagger module deps`, `dagger module engine`) take **no** module-targeting flag. They operate on the `dagger.json` reachable from cwd. To target a sibling, `cd` first. This matches `cargo add` / `npm install` / `go get`.

## Per-command decision context

What we considered, debated, changed, and decided for each command. Not a description; an account of design pressure.

| Command | Notes |
|---|---|
| `setup` | Considered `doctor` (per `brew doctor` / `npm doctor` / `flutter doctor` precedent). Vetoed — the precedent doesn't feel intuitive enough. Final framing: idempotent doctor command, not a one-shot wizard. "Ensure" implies safe to run anytime. `setup` owning environment maintenance is what lets `update` be unambiguously about module versions (resolves "does update mean update Dagger?"). |
| `check` | Cold-read first-instinct reached for `run` / `ci` / `test`. Pushback held: GitHub "Checks" is universal CI vocabulary (required status checks, the Checks API, red/green PR gates), so the muscle memory exists even when not first-instinct. Description was sharpened to that vocabulary. `checks` alias dropped — one name per concept. |
| `generate` | Cold-read flagged "Generate assets of your project" as opaque ("codegen? static site assets? module bindings?"). Sharpened to name "derived files" with concrete examples. Part of the three-shipping-fundamentals framing (`check` = verify, `generate` = derive, `up` = serve) — verbs that every shop maps to regardless of stack. |
| `up` | Adversarial reviewer flagged collision with `docker compose up` semantics. Collision is intentional — `dagger up` does mean what `docker compose up` means. Description names local-development as the use case to distinguish from `check`. |
| `activity` | Originally lived as `dagger workspace activity`. Hoisted to top-level after asking what happened to it during the workspace-plumbing punt. Proposed `dagger cloud activity` (cluster with Cloud) — rejected: OSS/Cloud purity is not load-bearing for placement. This established the broader **usefulness × simplicity** principle: hot Cloud verbs surface at top-level, rare ones nest under `cloud`. |
| `install` / `uninstall` | Bikeshed: `install` / `uninstall` vs `add` / `rm`. Initial argument: `add` / `rm` for symmetry with `module deps add`. Reversed after weighing the cold-read first-instinct reach for `install` (npm muscle memory). The asymmetry is actually honest — consumer verbs match consumer ecosystems (npm/pip/apt), authoring verbs match authoring ecosystems (cargo/yarn). Aliased to `i` and `un` to match `npm i` / `npm un`. |
| `installed` | Started as `dagger modules` (plural). Killed for typing collision with `module` (singular) — adjacent groups + tab completion at `mod<TAB>`. Tried to subsume into `dagger list` (modules-v2) — overreach: `dagger list` is for filter-flag vocabulary discovery, not installed-module enumeration. Tried `dagger status --modules` — burying a daily read under a multi-purpose verb is wrong. Past-participle `installed` reads as "show me what's installed" — precedent in `pip list`, `gem list --installed`. |
| `update` | Cold-read flagged ambiguity (update modules? update Dagger CLI?). Resolved structurally: `setup` owns environment maintenance, so `update` is strictly module-version refresh. |
| `list` / `ls` | Owned by modules-v2 (PR #12900) for general-purpose enumeration over the artifacts framework. Workshopped renaming to free the slot: `select` (SQL-shaped, exact semantic match against the spec's relational grid), `find`, `filter`. All rejected once the actual use case surfaced — `list` is filter-flag *vocabulary discovery* ("what values can I plug into `--go-test=...`?"), not column projection. `list` is the right verb. **The "see modules-v2" pointer in the current description is a known dangling reference; needs self-contained replacement before this lands.** |
| `search` | Verb-as-action. Uncontested. |
| `settings` | Initial conflation with `config` was corrected. They are not the same: `settings` is schema-aware editor for module-declared settings paths (`modules.<m>.settings.<k>`); `config` is raw `dagger.toml` editor for any path. Different audiences. Resolution is namespace, not verb rename: raw `config` moves to `dagger workspace config` (clearly signaled as advanced by the prefix), and `settings` stays at top-level as the daily verb. `--env <name>` scopes the write to that env's overlay. |
| `api` | Initial push to sharpen "Interact with the Dagger API" was rejected. "Dagger has an API → you can query it" is common knowledge for the audience that should be reaching for `api`; and the group has multiple modes (raw query, function call, introspection), so any specific framing would either mislead or pile up nouns. Resolution: top-level mockup gets `(advanced)` tag for signal-and-skip; cobra Long description carries a teaching beat ("Every Dagger command runs against a GraphQL API served by the engine, combining Dagger's core types with module schema extensions") plus a docs link. Two layers, two audiences. |
| `module` | Original Dagger `mod` group was the *consumer* plural ("modules in the ecosystem"). The redesign nuked it and reintroduced singular `dagger module` as the *authoring* lane ("the module under my cursor"). Singular vs plural carries the consumer/author distinction; the verbs underneath differ accordingly. Considered `mod dev` as a nested sub-group inside the old plural — pivoted to the cleaner singular-noun split. |
| `module init` | Requested explicitly. Replaces a top-level `dagger init` that briefly existed in early drafts (workspace creation goes implicit on first install instead). `dagger module init` matches `cargo init` / `npm init` muscle memory for scaffolding. **Implementation depends on the SDK module interface — see [SDK module interface](#sdk-module-interface).** Form: `dagger module init <name> --sdk=<name> [--path=<dir>]`. No SDK-specific configuration flags at init time — kept minimal. Auto-installs the SDK if not already in the workspace; uses a SDK-internal template convention to scaffold. Post-init SDK-specific operations live under `dagger module sdk` (below). |
| `module deps` / `module engine` | Restored from PR #13226's pre-rollback state. The original work was rolled back because there was no clean home for it under the old `dagger mod` — adding it created the consumer/author conflation problem. The redesign's whole architecture (separate `dagger module` group, `--load-module` rename, no module-targeting flag on authoring commands) is what makes restoring them honest. The rebalance principle (CLI owns shared operations, SDK owns specialized ones — see [SDK module interface](#sdk-module-interface)) puts `deps` and `engine` clearly on the CLI side: editing dagger-module.toml's deps list or engineVersion is 100% identical across SDKs, so duplicating it inside each SDK would be the kind of duplication the new architecture is meant to avoid. |
| `module sdk` | Thin wrapper that dispatches to the current module's SDK. Form: `dagger module sdk <subcommand> <args>`. Reads cwd's `dagger-module.toml`, finds the `sdk` field, and effectively runs `dagger call <sdk-ref> <subcommand> <args>`. Available subcommands depend entirely on what the current module's SDK exposes — no CLI-side contract beyond "you're an installed module with functions." Examples: `dagger module sdk python-version 3.13`, `dagger module sdk setup-template legacy`, `dagger module sdk go-mod-tidy`. This is the per-module escape hatch for SDK-specific operations; the CLI provides discovery and orchestration, the SDK provides everything else. |
| `cloud` | Initially in group 5 (meta) with `login`/`logout` as top-level peers. Moved login/logout *under* cloud (rare-use verbs nest). Then `cloud` itself moved from group 5 to group 4 — it's structurally a major namespace, not a meta verb. |
| `cloud integration` | Original `dagger integration` was singleton-shaped (`accounts`, `setup`). Requested redesign to mutable shape (`create`, `rm`, `list`, `accounts`). Folded under `cloud` per usefulness × simplicity — integrations are configured occasionally, so they nest. |
| `cloud check` | Replaces `dagger workspace autocheck` (which was just on/off for the selected remote). Mutable shape `{on, off, list, status NAME}` proposed during the cloud restructure. Naming intentionally overlaps with top-level `check`: different concepts at different levels — top-level = run local checks, `cloud check` = manage Cloud-side automated runs. Acceptable. |
| `workspace` (group) | Killed in the first flat-redesign sweep, then reintroduced after observing that the namespace itself does load-bearing work: `dagger workspace config` reads as "advanced workspace plumbing" without the verbs having to carry the signal alone. Slimmed to plumbing only (config, cwd, root, config-file, remote, remotes). Bare invocation prints a digest — this absorbed and dropped a briefly-proposed `dagger status` verb. Moved from group 1 to group 4 because it's structurally a namespace, not a single inspection verb. |
| `exec` | Initially hidden as "niche." Pushback restored it to visible in group 5 (utility) where its low traffic doesn't crowd the daily-loop verbs above. Hidden ≠ niche — group 5 is exactly where niche-but-real verbs belong. |
| `call` (hidden) | `dagger function call` was killed when the `function` group was dissolved. `dagger api call` makes the most semantic sense (it's "an API call" and clusters with `query` and `functions`). `dagger call` kept as a hidden top-level alias for muscle memory — and to keep the top-level `call` slot reserved for a future higher-level porcelain (tentative name: `dagger do`). |
| `shell` (hidden) | Kept reachable, absent from `--help`. Slot stays open to promote or deprecate later based on usage. |
| `env` (removed) | Originally a top-level group with `create` / `list` / `rm`. Removed entirely after recognizing that `env` is *strictly a path prefix in workspace config* (`env.<name>.modules.<m>.settings.<k>`), not a first-class concept. CRUD happens via `dagger settings --env <name>` (typed) and `dagger workspace config` (raw). Discoverability moves into the `--env` flag's description, which names the file path explicitly. This eliminates one corner of the "workspace vs env vs --env vs settings" four-way confusion cold-read v2 flagged. |
| `--load-module` / `-m` | The old `--mod` carried two unrelated meanings (load a module vs. select a module to edit). This is what caused the PR #13226 deps/engine work to be cut — they reused `--mod` to mean "module to edit," which collided. Workshopped: `--load-module`, `--with-module`, `--from-module`. `--with-module` rejected — Dagger's `WithX` API methods are chainable, readers would expect `--with-module X --with-module Y` to compose, but the flag is single-valued. `--load-module` chosen as "safer" — no chain implication, no overload, explicit verb. `-m` short form preserved. |
| `--env` (flag) | Description rewritten to teach the overlay model: envs are paths under `env.<name>.*` in workspace config. The flag description doubles as the discovery affordance for env overlays now that the top-level `env` group is gone. |

## Subcommand structures

The four group commands (`api`, `module`, `cloud`, `workspace`) each own their own subcommand surface.

### `dagger api`

```
Every Dagger command — check, up, generate, even install — ultimately
runs against a GraphQL API served by the Dagger engine, combining
Dagger's core types with schema extensions loaded from modules. The
`api` group surfaces direct access for scripting and advanced
automation. Most users will never type these commands.

See https://docs.dagger.io/api for the full overview.

AVAILABLE COMMANDS
  call       Call one or more functions, interconnected into a pipeline
  functions  List available functions
  query      Send raw GraphQL queries to a dagger engine
```

### `dagger workspace` (alias: `ws`)

```
Inspect or configure your workspace. Bare invocation prints a digest.

AVAILABLE COMMANDS
  cwd          Print the workspace cwd
  root         Print the workspace root
  remote       Print the current selectable remote
  remotes      List selectable remotes
  config-file  Print the workspace config file path
  config       Get or set raw dagger.toml values (advanced)
```

### `dagger module`

```
Author a module: scaffold, edit dependencies, engine version, etc.
Operates on the dagger-module.toml reachable from the current directory.

AVAILABLE COMMANDS
  init      Initialize a new module in the current directory (requires --sdk)
  deps      Manage this module's dependencies
  engine    Manage this module's required engine version
  sdk       Run SDK-specific commands against this module (dispatched to the
            module's declared SDK)
```

#### `dagger module deps`

```
Manage this module's dependencies, as declared in dagger.json.

AVAILABLE COMMANDS
  add   Add one or more dependencies to the module
  rm    Remove one or more dependencies from the module
  list  List the current module's dependencies
```

#### `dagger module engine`

```
Manage the engine version this module requires.

AVAILABLE COMMANDS
  require          Set the module's required engine version
  require-current  Set the required engine version to the currently running engine
  require-latest   Set the required engine version to the latest released version
  required         Print the module's required engine version
```

### `dagger cloud`

```
Manage Dagger Cloud.

AVAILABLE COMMANDS
  login        Log in to Dagger Cloud
  logout       Log out of Dagger Cloud
  billing      Manage Dagger Cloud billing
  org          Manage Dagger Cloud organizations
  integration  Manage Cloud integration providers
  check        Manage Cloud-side automated checks for this workspace
```

#### `dagger cloud integration`

```
Manage Cloud integration providers. Mutable shape — configured providers
come and go.

AVAILABLE COMMANDS
  create    Create a new integration
  rm        Remove an integration
  list      List configured integrations
  accounts  List accounts visible to an integration
```

#### `dagger cloud check`

```
Manage Cloud-side automated checks for this workspace.

AVAILABLE COMMANDS
  on      Enable a Cloud-side check (by name)
  off     Disable a Cloud-side check (by name)
  list    List Cloud-side checks for this workspace
  status  Show the status of a Cloud-side check (by name)
```

## SDK module interface

Authoring a Dagger module requires an SDK. SDKs are themselves Dagger modules (e.g., `github.com/dagger/go-sdk`), installed into the workspace with `dagger install`. The CLI doesn't ship SDK-specific knowledge; the SDK provides everything language-specific. The CLI provides orchestration that's the same across all SDKs.

The line between CLI-owned and SDK-owned operations:

| Operation | Owner | Why |
|---|---|---|
| `generate` | SDK | 100% SDK-specific (each language emits its own bindings); `dagger generate` discovers and dispatches to every installed SDK's generate function automatically |
| `deps`, `engine` | CLI | 100% identical across SDKs (just editing dagger-module.toml fields); SDK duplication would be the kind the new architecture is meant to avoid |
| `init` (orchestration) | CLI | Shared: install SDK if needed, create dagger-module.toml, hook up workspace |
| `init` (templates) | SDK convention | SDK-internal; CLI doesn't ship per-SDK templates |
| SDK-specific operations (e.g., `python-version`, `go-mod-tidy`) | SDK | Routed through `dagger module sdk <subcommand>` wrapper |

### Module declares its SDK

Each module's `dagger-module.toml` carries two fields:

```toml
# dagger-module.toml
name = "api"
runtime = "go"                            # for the engine: which runtime executes this module
sdk = "github.com/dagger/go-sdk"          # for tooling: which SDK manages authoring
engineVersion = "v1.0.0"

[[dependencies]]
source = "github.com/dagger/wolfi"
```

- `runtime` answers the engine's question: how do I execute this?
- `sdk` answers the tooling's question: who manages authoring for this?

Splitting these resolves the conflation that `next`'s `runtime.source` had. The same SDK can target different runtimes over time; multiple SDKs can target the same runtime; neither case breaks anything because the two fields are independent.

The `sdk` field stores the **canonical full ref**. Short forms like `--sdk=go` are resolved to the canonical ref at parse time and never propagate further.

### SDK alias registry

A separate registry file (working name: `sdks.json`, distinct from the general module registry that powers `dagger search`) maps short names to canonical SDK refs:

```json
[
  { "name": "go",     "repo": "github.com/dagger/go-sdk",     "aliases": ["golang"] },
  { "name": "python", "repo": "github.com/dagger/python-sdk", "aliases": ["py"] }
]
```

Resolution rules for `--sdk=<value>`:
- Contains `/` or `@` → full ref, no resolution.
- Otherwise → look up name (then aliases) in `sdks.json`.
- 0 / 1 / >1 matches: error / resolve / ambiguous-error.

Aliases are a **CLI-side, parse-time** mechanism. `dagger.toml`, the engine, and SDK modules themselves never see the alias — only canonical refs. Adding a new SDK alias is a registry data change, not a CLI release.

The SDK registry is separate from the general module registry to keep the namespaces clean (e.g., `github.com/dagger/go` the toolchain and `github.com/dagger/go-sdk` the SDK can both legitimately want the short name "go" depending on the context; scoping the SDK alias mechanism to its own registry avoids that collision entirely).

### `dagger module init`

```bash
dagger module init <name> --sdk=<sdk-name-or-ref> [--path=<dir>]
```

Steps:
1. Resolve `--sdk` via `sdks.json` if a short name; otherwise treat as a full ref.
2. If the SDK isn't already installed in this workspace, install it (pinned to a resolved version, just like `dagger install` would). Output narrates the resolution: `"Installing github.com/dagger/go-sdk@v1.2.3..."`.
3. Create `<path>/dagger-module.toml` with `name`, `runtime` (derived from the SDK), `sdk` (canonical full ref), and a starting `engineVersion`.
4. Apply the SDK's template convention to scaffold language-specific source files.
5. Result is returned as a Changeset and applied through the standard changeset apply flow (with `--auto-apply` honored).

`--path` defaults to `.dagger/modules/<name>`. No SDK-specific config flags exist on `init` itself; per-module SDK operations happen post-init via `dagger module sdk`.

**Workspace creation cascade.** If there's no workspace yet, `dagger module init` also creates one (same implicit-workspace behavior as `dagger install`'s first run). Net: a fresh directory + `dagger module init my-mod --sdk=go` = workspace, SDK install, and module scaffold in one command.

### Workspace-scoped SDK settings

The SDK can declare workspace-level settings (defaults applied to new modules of that SDK, or other workspace-wide config):

```bash
dagger settings python-sdk default-python-version 3.13
dagger settings go-sdk strict-build true
```

These are stored under `[modules."<sdk>".settings]` in `dagger.toml`. Same mechanism that all `dagger settings` already uses; no schema invention needed beyond what's already there.

### Per-module SDK operations: `dagger module sdk`

The wrapper for SDK-specific operations on the current module:

```bash
dagger module sdk python-version 3.13          # current module's SDK
dagger module sdk setup-template legacy
dagger module sdk go-mod-tidy
```

Internally: read cwd's `dagger-module.toml`, find `sdk`, dispatch `dagger call <that-sdk> <subcommand> <args>`. Available subcommands depend entirely on what the SDK exposes. The CLI surface is therefore *dynamic per module* — `dagger module sdk --help` invoked in a Go module shows go-sdk's functions; same command in a Python module shows python-sdk's. That dynamism is OK because it's bounded to one wrapper command; users learn the structure once.

### What's *not* in this interface

- No CLI-side schema introspection at init time. The CLI doesn't read SDK schemas to generate dynamic flags. If an SDK needs a setting tuned, the user runs `dagger module sdk <verb>` after init.
- No standard per-module SDK settings location in `dagger-module.toml`. SDKs decide where to put their state (in their own files inside the module, in dagger-module.toml itself, wherever). The CLI doesn't reserve a section.
- No `dagger module settings` verb. Workspace-level SDK settings live under `dagger settings <sdk>`; per-module SDK operations live under `dagger module sdk`. No third verb for "per-module SDK settings."

### Open questions in this section

- Exact template convention (how does the SDK tell `init` "here's the source skeleton"). Two reasonable shapes: a SDK function returning a Directory, or a SDK function that takes a path and writes directly. Pinning this is a Phase-2-or-later decision.
- Whether `dagger search` surfaces SDKs alongside other modules. Tentative: yes.

## Discrete changes from current CLI

Implementation checklist. Items grouped by type; each is a discrete unit of work.

### New commands (need implementation)

- [ ] **`dagger setup`** — top-level idempotent doctor verb. Ensures workspace config exists, auth is valid, engine is reachable. Safe to re-run.
- [ ] **`dagger installed`** — top-level. Lists installed modules from `dagger.toml`. Likely a thin wrapper over existing workspace introspection.
- [ ] **`dagger module init`** — scaffolds a new module: requires `--sdk=<name>`, auto-installs the SDK if needed, writes `dagger-module.toml` with `runtime`+`sdk` fields, applies the SDK's template convention. See [SDK module interface](#sdk-module-interface).
- [ ] **`dagger module sdk`** — wrapper that dispatches `dagger call <current-module's-sdk> <subcommand>`. New verb. See [SDK module interface](#sdk-module-interface).
- [ ] **`sdks.json` registry + alias resolver** — new CLI-side data file and resolver function. Used by `--sdk=` flag. See [SDK module interface](#sdk-module-interface).
- [ ] **`dagger cloud integration create`** — new (currently only `setup` exists, which becomes `create`).
- [ ] **`dagger cloud integration rm`** — new.
- [ ] **`dagger cloud integration list`** — new.
- [ ] **`dagger cloud check {on, off, list, status}`** — new shape. Today's `autocheck` is just on/off for the selected remote; new shape lets you address checks by name and list/inspect.
- [ ] **`dagger workspace` (bare, no subcommand)** — new behavior: print digest of workspace state (cwd, root, current remote, installed modules summary). Today, bare `dagger workspace` prints help.

### Restore from PR #13226 pre-rollback

- [ ] **`dagger module deps {add, rm, list}`** — restore from commit `89054a4` (PR #13226's "move deps add/rm to updatedConfigDirectory api"). Already present on local experimental branch.
- [ ] **`dagger module engine {require, require-current, require-latest, required}`** — restore from same commit.

### Hoists (existing functionality, new top-level location)

- [ ] `dagger workspace install` → `dagger install` (visible top-level, with `i` alias). Today a hidden shim `moduleDepInstallCmd` exists; promote, alias, and remove the workspace subcommand.
- [ ] `dagger workspace uninstall` → `dagger uninstall` (with `un` alias). Same pattern as `install`.
- [ ] `dagger workspace update` → `dagger update`. Hidden shim `moduleUpdateCmd` exists; promote.
- [ ] `dagger workspace activity` → `dagger activity`.
- [ ] `dagger workspace settings` (hidden) → `dagger settings` (visible, canonical). The visible top-level `settings` already exists as a hidden alias; just unhide.
- [ ] `dagger mod search` → `dagger search`.

### Moves (reparenting within the tree)

- [ ] `dagger function call` → `dagger api call`. Subcommand moves from `function` group to `api` group.
- [ ] `dagger function list` → `dagger api functions`. Move + rename to plural noun (matches the listing-from-the-loaded-module semantic).
- [ ] `dagger integration accounts` → `dagger cloud integration accounts`. Move from top-level `integration` to under `cloud`.
- [ ] `dagger integration setup` → `dagger cloud integration create`. Move + rename (matches the new mutable shape).
- [ ] `dagger workspace autocheck` → `dagger cloud check`. Move + expand from boolean to mutable shape (see "New commands" above).

### Removals (from visible surface)

- [ ] **`dagger mod`** (alias group) — remove. Was the consumer-plural group; replaced by singular `dagger module` (different content). Note: `dagger module` as the *consumer* plural alias to `dagger mod` also goes — the noun gets reassigned.
- [ ] **`dagger function` / `dagger fn`** — remove the group entirely. Subcommands moved.
- [ ] **`dagger env`** — remove the group entirely. Env is a path prefix in workspace config; CRUD via `dagger settings --env` and `dagger workspace config`. The flag `--env` survives and its description teaches the model.
- [ ] **`dagger integration`** (top-level) — remove. Moved under `cloud`.
- [ ] **`dagger workspace init`** — remove. Workspace creation goes implicit on first `install`.
- [ ] **`dagger workspace migrate`** — remove. Legacy migration is past the point where any visibility is justified.
- [ ] **`dagger recommend`** — remove. Generic verb without clear subject at top-level; users reach `search` first.
- [ ] **`dagger checks`** alias — remove. One name (`check`) per concept.
- [ ] **`dagger config`** (hidden top-level alias) — remove. Replaced by visible `dagger workspace config`.
- [ ] **`dagger modules`** (plural, briefly considered) — never lands. Listing handled by `dagger installed`.
- [ ] **`dagger status`** (briefly considered) — never lands. Workspace digest handled by bare `dagger workspace`.

### Hidden top-level aliases (already hidden today; confirm or set)

- [ ] **`dagger call`** — keep hidden; alias to `dagger api call`. Today's behavior: not in visible `--help`. Verify it still routes correctly after `function call` → `api call` move.
- [ ] **`dagger shell`** — keep hidden; reachable, absent from `--help`.

### Flag renames

- [ ] **`--mod` → `--load-module`** in `moduleAddFlags` (`cmd/dagger/module.go:39`). Single funnel-point change.
- [ ] **`--no-mod` → `--no-load-module`** in the same funnel.
- [ ] **Update references in docs and tests** for both flag names.
- [ ] **Verify `-m` and `-M` short forms** still work post-rename.

### Description updates

Top-level mockup (Short descriptions in cobra):

- [ ] `check`: "Verify your project — tests, linters, type checks, security scans, etc."
- [ ] `generate`: "Generate derived files for your project — code, SDKs, types, docs, etc."
- [ ] `up`: "Run your project's services for local development — databases, APIs, dev servers, etc."
- [ ] `install`: "Install a module into your workspace"
- [ ] `uninstall`: "Uninstall a module from your workspace"
- [ ] `installed`: "List installed modules"
- [ ] `update`: "Refresh installed-module state"
- [ ] `search`: "Search for modules you can install"
- [ ] `settings`: "Get or set module settings (use --env for an env overlay)"
- [ ] `activity`: "Show recent activity (runs, traces, etc.) for this workspace"
- [ ] `module`: "Author a module: edit dependencies, engine version, etc."
- [ ] `workspace`: "Inspect or configure your workspace (cwd, remotes, config, etc.)"
- [ ] `cloud`: "Manage Dagger Cloud (login, integrations, etc.)"
- [ ] `api`: "Interact with the Dagger API (advanced)"
- [ ] `setup`: "Ensure Dagger is properly set up and operational in the workspace"

Long descriptions (cobra Long, shown in `dagger X --help`):

- [ ] `api` Long: teaching beat + docs link (see Subcommand structures section above).
- [ ] `workspace` Long: clarify bare-invocation digest behavior and distinguish from `settings`.
- [ ] `module` Long: clarify cwd-based targeting (no `--mod` flag for authoring).
- [ ] `settings` Long: clarify `--env <name>` scoping and distinguish from `workspace config`.

Flag descriptions:

- [ ] `--load-module`: "Use a one-off module (local path or git ref)"
- [ ] `--no-load-module`: "Don't load any module for this command"
- [ ] `--env`: rewrite to name the file-path model (`env.<name>.*` in workspace config) and point at `dagger workspace config env` for inspection.

### Known unfixed items before this lands

- [ ] **`list, ls` description has a dangling "see modules-v2" pointer.** Users won't have that doc available. Needs self-contained text.
- [ ] **Workspace concept is referenced everywhere (`--env`, `setup`, `-W`) but never defined.** Cold-read v2 and v3 both flagged that newcomers can't form a mental model from the top-level help alone. Likely fix: one-sentence definition at the top of `dagger workspace --help`.
- [ ] **`exec` description ("Execute a command in a Dagger session") is too vague.** "Session in what?" Needs sharpening.

## Status

Proposed.
