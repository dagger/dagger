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
  search        Search for modules you can install
  settings      Get or set module settings (use --env for an env overlay)

  api             Interact with the Dagger API (advanced)
  module          Author a module: edit dependencies, engine version, etc.
  cloud           Manage Dagger Cloud (login, integrations, etc.)
  workspace, ws   Inspect or configure your workspace (cwd, remotes, config, etc.)

  exec         Run a command with a connected Dagger session (DAGGER_SESSION_PORT/TOKEN injected)
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
| 3 | Workspace management | `install`, `uninstall`, `installed`, `update`, `search`, `settings` |
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
| `setup` | Considered `doctor` (per `brew doctor` / `npm doctor` / `flutter doctor` precedent). Vetoed — the precedent doesn't feel intuitive enough. Final framing: idempotent doctor command, not a one-shot wizard. "Ensure" implies safe to run anytime. `setup` owning environment maintenance is what lets `update` be unambiguously about module versions (resolves "does update mean update Dagger?"). Concrete shape (phase 5): three sequential steps, each independently prompted — (1) Cloud login if not authenticated, (2) workspace migration if a legacy dagger.json is present, (3) module recommendations based on workspace files (resurrecting the recommend scan logic that was previously a standalone `dagger mod recommend`). Skippable per-step at the prompt; `--auto-apply` accepts all; non-interactive default is to skip mutating steps. |
| `check` | Cold-read first-instinct reached for `run` / `ci` / `test`. Pushback held: GitHub "Checks" is universal CI vocabulary (required status checks, the Checks API, red/green PR gates), so the muscle memory exists even when not first-instinct. Description was sharpened to that vocabulary. `checks` alias dropped — one name per concept. |
| `generate` | Cold-read flagged "Generate assets of your project" as opaque ("codegen? static site assets? module bindings?"). Sharpened to name "derived files" with concrete examples. Part of the three-shipping-fundamentals framing (`check` = verify, `generate` = derive, `up` = serve) — verbs that every shop maps to regardless of stack. |
| `up` | Adversarial reviewer flagged collision with `docker compose up` semantics. Collision is intentional — `dagger up` does mean what `docker compose up` means. Description names local-development as the use case to distinguish from `check`. |
| `activity` | Originally lived as `dagger workspace activity`. Hoisted to top-level after asking what happened to it during the workspace-plumbing punt. Proposed `dagger cloud activity` (cluster with Cloud) — rejected: OSS/Cloud purity is not load-bearing for placement. This established the broader **usefulness × simplicity** principle: hot Cloud verbs surface at top-level, rare ones nest under `cloud`. |
| `install` / `uninstall` | Bikeshed: `install` / `uninstall` vs `add` / `rm`. Initial argument: `add` / `rm` for symmetry with `module deps add`. Reversed after weighing the cold-read first-instinct reach for `install` (npm muscle memory). The asymmetry is actually honest — consumer verbs match consumer ecosystems (npm/pip/apt), authoring verbs match authoring ecosystems (cargo/yarn). Aliased to `i` and `un` to match `npm i` / `npm un`. |
| `installed` | Started as `dagger modules` (plural). Killed for typing collision with `module` (singular) — adjacent groups + tab completion at `mod<TAB>`. Tried to subsume into a `dagger list` slot (modules-v2 was at one point going to own it) — both ideas dropped. Tried `dagger status --modules` — burying a daily read under a multi-purpose verb is wrong. Past-participle `installed` reads as "show me what's installed" — precedent in `pip list`, `gem list --installed`. |
| `update` | Cold-read flagged ambiguity (update modules? update Dagger CLI?). Resolved structurally: `setup` owns environment maintenance, so `update` is strictly module-version refresh. |
| `list` / `ls` (cut) | Was reserved earlier in the redesign for the modules-v2 artifacts framework (PR #12900) — general-purpose enumeration over filter dimensions. Cut from this proposal: the modules-v2 work owns that verb and slot on its own timeline; bundling a placeholder here added a dangling pointer (`"see modules-v2"`) with nothing self-contained for users to read. If modules-v2 lands, it can claim the slot directly; if it changes shape, nothing here needs revisiting. |
| `search` | Verb-as-action. Uncontested. |
| `settings` | Initial conflation with `config` was corrected. They are not the same: `settings` is schema-aware editor for module-declared settings paths (`modules.<m>.settings.<k>`); `config` is raw `dagger.toml` editor for any path. Different audiences. Resolution is namespace, not verb rename: raw `config` moves to `dagger workspace config` (clearly signaled as advanced by the prefix), and `settings` stays at top-level as the daily verb. `--env <name>` scopes the write to that env's overlay. |
| `api` | Initial push to sharpen "Interact with the Dagger API" was rejected. "Dagger has an API → you can query it" is common knowledge for the audience that should be reaching for `api`; and the group has multiple modes (raw query, function call, introspection), so any specific framing would either mislead or pile up nouns. Resolution: top-level mockup gets `(advanced)` tag for signal-and-skip; cobra Long description carries a teaching beat ("Every Dagger command runs against a GraphQL API served by the engine, combining Dagger's core types with module schema extensions") plus a docs link. Two layers, two audiences. |
| `module` | Original Dagger `mod` group was the *consumer* plural ("modules in the ecosystem"). The redesign nuked it and reintroduced singular `dagger module` as the *authoring* lane ("the module under my cursor"). Singular vs plural carries the consumer/author distinction; the verbs underneath differ accordingly. Considered `mod dev` as a nested sub-group inside the old plural — pivoted to the cleaner singular-noun split. |
| `module init` | Requested explicitly. Replaces a top-level `dagger init` that briefly existed in early drafts (workspace creation goes implicit on first install instead). `dagger module init` matches `cargo init` / `npm init` muscle memory for scaffolding. **Implementation depends on the SDK module interface — see [SDK module interface](#sdk-module-interface).** Form: `dagger module init <name> --sdk=<name> [--path=<dir>]`. No SDK-specific configuration flags at init time — kept minimal. Auto-installs the SDK if not already in the workspace; uses a SDK-internal template convention to scaffold. Post-init SDK-specific operations live under `dagger module sdk` (below). |
| `module deps` / `module engine` | Restored from PR #13226's pre-rollback state. The original work was rolled back because there was no clean home for it under the old `dagger mod` — adding it created the consumer/author conflation problem. The redesign's whole architecture (separate `dagger module` group, `--load-module` rename, no module-targeting flag on authoring commands) is what makes restoring them honest. The rebalance principle (CLI owns shared operations, SDK owns specialized ones — see [SDK module interface](#sdk-module-interface)) puts `deps` and `engine` clearly on the CLI side: editing dagger-module.toml's deps list or engineVersion is 100% identical across SDKs, so duplicating it inside each SDK would be the kind of duplication the new architecture is meant to avoid. |
| `module sdk` | Thin wrapper that dispatches to the current module's SDK. Form: `dagger module sdk <subcommand> <args>`. Locates the cwd's `dagger-module.toml` (walking up, stopping at the workspace root), computes the module's workspace-relative path, then scans the workspace's `[[modules.*.as-sdk.modules]]` for an entry whose `path` matches. The parent `modules.<NAME>` is the SDK; the wrapper runs `dagger call <NAME> <subcommand> <args>`. The SDK association lives in workspace config, not in `dagger-module.toml` (per the runtime/SDK split — see [SDK module interface](#sdk-module-interface)). Available subcommands depend entirely on what the SDK exposes — no CLI-side contract beyond "you're an installed module with functions." Examples: `dagger module sdk python-version 3.13`, `dagger module sdk setup-template legacy`, `dagger module sdk go-mod-tidy`. This is the per-module escape hatch for SDK-specific operations; the CLI provides discovery and orchestration, the SDK provides everything else. |
| `cloud` | Initially in group 5 (meta) with `login`/`logout` as top-level peers. Moved login/logout *under* cloud (rare-use verbs nest). Then `cloud` itself moved from group 5 to group 4 — it's structurally a major namespace, not a meta verb. |
| `cloud integration` | Original `dagger integration` was singleton-shaped (`accounts`, `setup`) — one provider type, list its accounts. Requested redesign to mutable shape (`create`, `rm`, `list`): each configured integration is a discrete entry; `list` enumerates them (optionally filtered by type), replacing the old "list accounts of provider X" framing. Folded under `cloud` per usefulness × simplicity — integrations are configured occasionally, so they nest. |
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
  list      List configured integrations (optionally filtered by type)
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

Authoring a Dagger module requires an SDK. SDKs are themselves Dagger modules (e.g., `github.com/dagger/go-sdk`), installed into the workspace alongside ordinary modules. The CLI doesn't ship SDK-specific knowledge. The engine owns the operations that mutate workspace state. Each SDK declares only what is genuinely SDK-specific.

The runtime/SDK split is foundational to this section and worth stating explicitly:

- **Runtime** is the engine's low-level concern: how a module's types and functions get loaded and executed. Runtime modules implement `moduleRuntime(modSource, introspectionJSON): Container!`. The contract is stable.
- **SDK** is the user's high-level concern: developer experience for authoring modules in a language. SDKs handle codegen, scaffolding, idiomatic bindings.
- A single SDK module can fulfill both roles (today's common case). Decoupling is opt-in: an SDK that delegates execution to a separate runtime module declares it via `targetRuntime`.

The split shows up in the file layout: `dagger-module.toml` is engine state (carries `runtime`, no `sdk`), `dagger.toml` is workspace state (records SDK installs and what they manage). Engine reads engine files. Tooling reads tooling files. The two never need to be reconciled.

The line between CLI-owned, engine-owned, and SDK-owned operations:

| Operation | Owner | Why |
|---|---|---|
| `init` user intent (aliases, defaults, prompts) | CLI | Stable across SDKs; UX layer only |
| `init` orchestration (install SDK, write files, update workspace) | Engine | One atomic Changeset; composes correctly without a CLI-side failure window |
| `init` runtime resolution | SDK (optional) | Via `targetRuntime`; defaults to the SDK's own installed ref |
| `generate` | SDK | 100% SDK-specific; `dagger generate` discovers and dispatches |
| `deps`, `engine` | CLI | Edits `dagger-module.toml` fields; no SDK involvement |
| SDK-specific operations (e.g., `python-version`, `go-mod-tidy`) | SDK | Routed through `dagger module sdk <subcommand>` wrapper |

### Module config: `dagger-module.toml` is engine-only

Each module's `dagger-module.toml` carries the information the engine needs to load and execute the module. The SDK association is a dev-experience concern, not an engine concern, and lives in the workspace config — not here.

```toml
# dagger-module.toml
name = "api"
runtime = "go"                            # which runtime executes this module
engineVersion = "v1.0.0"

[[dependencies]]
source = "github.com/dagger/wolfi"
```

The `runtime` field resolves to either:

- A **builtin runtime name** (`"go"`, `"python"`, `"typescript"`) — the engine's bundled runtime for that language.
- A **canonical module ref** (`"github.com/dagger/go-sdk@v1.2.3"`) — the engine loads the named module as the runtime. The module must implement `moduleRuntime(modSource, introspectionJSON): Container!`.

Heuristic: no `/` and no `@` → builtin name; otherwise module ref.

`dagger-module.toml` is portable. Copy a module directory into a different workspace and the engine still knows how to execute it from `runtime` alone — the SDK association does not need to travel with it.

There is no `sdk` field in `dagger-module.toml`. A previous draft of CLI 1.0 carried both `runtime` and `sdk` at this layer; on reflection the runtime/SDK distinction is real but belongs in the workspace, not the module.

### Workspace config: SDK installs and authoring nest under `[modules.*]`

An SDK is a module whose role in the workspace is to author other modules. There is no parallel `[sdks.*]` top-level section; every install — regular module or SDK module — lives under `[modules.*]`. SDK-specific data nests in an `as-sdk` sub-table on the module entry.

```toml
# Installed modules — consumed dependencies, available to call.
[modules.dagger-cloud]
source = "github.com/dagger/dagger-cloud@v0.5"

# Installed module that's also acting as an SDK. The as-sdk sub-table
# lists what it authors/manages here.
[modules.go-sdk]
source = "github.com/dagger/go-sdk@v1.2.3"

[modules.go-sdk.settings]
strict-build = true

[[modules.go-sdk.as-sdk.modules]]
path = ".dagger/modules/api"

[[modules.go-sdk.as-sdk.modules]]
path = "libs/shared"

# A module both authored here AND installed here gets entries in BOTH
# places: its own [modules.<name>] install AND a path under the parent
# SDK's [[modules.<sdk>.as-sdk.modules]] authoring list.
[modules.api]
source = ".dagger/modules/api"

# SDK-managed non-module targets (e.g., a TypeScript client generated
# into a Next.js app). Same as-sdk nesting; clients live there too.
[[modules.typescript-sdk.as-sdk.clients]]
path = "app/lib/dagger-client"
module = ".dagger/modules/api"
```

Section semantics:

| Section / key | Meaning |
|---|---|
| `[modules.X]` | Module X is **installed** in this workspace (available to call as a dependency). Engine state. |
| `[modules.X.settings]` | Workspace-scoped settings for module X. Same location regardless of whether X is a regular module or an SDK. |
| `[modules.X.as-sdk]` | Module X is installed *as an SDK*. The presence of this sub-table marks the role; its contents list what X authors here. |
| `[[modules.X.as-sdk.modules]]` | This workspace **authors** the module at `path` using SDK X. |
| `[[modules.X.as-sdk.clients]]` | This workspace **generates** the client at `path` using SDK X, bound to the named module. |

Install ≠ develop, still:

- Install lives in the top-level `[modules.*]` entry (regular or as-sdk).
- Develop (authoring) lives in `[[modules.<sdk>.as-sdk.modules]]` — the SDK's role data.
- A locally-authored module that's also installed here gets entries in both places. A locally-authored module that's *not* installed (e.g., an SDK under development that ships to others) gets only the `[[as-sdk.modules]]` entry.

The unification removes the settings bifurcation that a parallel `[sdks.*]` section would have caused: `dagger settings <name>` reads/writes `[modules.<name>.settings]` for every install, no engine-side branching between regular modules and SDKs.

Lookups: "what's installed?" → `[modules.*]`. "What am I authoring with go-sdk?" → `[[modules.go-sdk.as-sdk.modules]]`. "What settings does go-sdk have?" → `[modules.go-sdk.settings]`. One namespace, one settings location.

### SDK module: `targetRuntime` field

To make the runtime/SDK split operational, an SDK module may expose:

```graphql
extend type SdkModule {
  """
  Runtime this SDK targets. Either a builtin runtime name (e.g. "go") or a
  canonical module ref. When omitted, the engine defaults to the SDK's own
  installed ref — i.e. this SDK module is both the SDK and the runtime.
  """
  targetRuntime: String!
}
```

The default — "this SDK is also its runtime" — is the common case. Today's SDK modules do everything in one module (codegen + runtime) and do not need to declare `targetRuntime` to keep working. The field is needed only when an SDK wants to **delegate** runtime execution to a separate module — e.g., a minimal experimental SDK producing code that the canonical runtime knows how to execute.

The engine runtime contract is unchanged: `moduleRuntime(modSource, introspectionJSON): Container!` is what defines what a runtime module **is**. `targetRuntime` is a pointer to one, not a definition of one. Different verb (be vs target), same noun. The two fields rarely coexist on a single module: self-hosting SDKs implement `moduleRuntime` and omit `targetRuntime`; delegating SDKs declare `targetRuntime` and skip `moduleRuntime`.

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

Aliases are a **CLI-side, parse-time** mechanism. `dagger.toml`, `dagger-module.toml`, the engine, and SDK modules themselves never see the alias — only canonical refs land in `[modules.<sdk-name>].source` and `runtime`. Adding a new SDK alias is a registry data change, not a CLI release.

The SDK registry is separate from the general module registry to keep namespaces clean (`github.com/dagger/go` the toolchain and `github.com/dagger/go-sdk` the SDK can both legitimately want the short name "go" depending on context; scoping the SDK alias mechanism to its own registry avoids the collision).

### `dagger module init`

```bash
dagger module init <name> --sdk=<sdk-name-or-ref> [--path=<dir>]
```

The CLI parses user intent. The engine does the work. Steps:

1. **CLI resolves `--sdk`.** If it contains `/` or `@`, treat as full ref. Otherwise look up the name (then aliases) in `sdks.json`. Pass the canonical ref to the engine.
2. **CLI defaults `--path`** to `.dagger/modules/<name>` if not supplied.
3. **CLI invokes the engine:**

   ```graphql
   dag.currentWorkspace().moduleInit(
     name: "<name>",
     sdk:  "<canonical sdk ref>",
     path: "<workspace-relative path>",
   ): Changeset!
   ```

4. **CLI applies the Changeset** via the standard apply path (`handleChangesetResponseAt`), honoring `--auto-apply`.

The engine's `Workspace.moduleInit` performs:

1. **Install the SDK in the workspace** if not already present (idempotent). Adds `[modules.<sdk-name>]` to `dagger.toml` — the SDK is just a module install like any other, distinguished only by its `as-sdk` sub-table.
2. **Determine the runtime ref** by introspecting the installed SDK module's `targetRuntime` field. If the field isn't declared, default to the SDK's own installed ref.
3. **Build the module config** at `<path>/dagger-module.toml` with `name` and `runtime`.
4. **Record the authoring relationship** by appending `[[modules.<sdk-name>.as-sdk.modules]] path = "<path>"` to the SDK's role data.
5. **If `path` is the default** (under `.dagger/modules/`), also add `[modules.<name>] source = "<path>"` so the new module is installed in the same workspace. Custom paths skip this — the user is managing layout deliberately.
6. **Return a `Changeset`** of all the above.

The returned Changeset is the full set of workspace edits. No filesystem write happens until the caller applies it; the caller can preview and abort. `--auto-apply` skips the preview prompt.

**Atomicity.** All workspace mutations are in one Changeset. Previous drafts of this command sequenced multiple engine calls (install SDK, write module config, install new module) and inherited the standard "step 1 succeeded, step 2 failed" wedged-state failure mode. Composing into one Changeset eliminates that window — the engine validates the entire plan before any disk write happens.

**Workspace creation cascade.** If there's no workspace yet, the engine initializes one (same implicit-workspace behavior as `dagger install`'s first run). A fresh directory + `dagger module init my-mod --sdk=go` yields workspace + SDK install + module scaffold + workspace install in one command.

The CLI ships no SDK-specific code. The engine ships no SDK alias knowledge. Each layer owns what is stable at its layer.

### Workspace-scoped SDK settings

The SDK can declare workspace-level settings (defaults applied to new modules of that SDK, or other workspace-wide config):

```bash
dagger settings python-sdk default-python-version 3.13
dagger settings go-sdk strict-build true
```

These are stored under `[modules.<sdk>.settings]` in `dagger.toml` — the same location regular modules use. Because SDKs are just modules (with an `as-sdk` sub-table marking the role), `dagger settings` reads and writes one location regardless of role, and the existing `moduleList` introspection covers both.

### Generated clients (`dagger api client`)

Today's `dagger client install/uninstall/list/update` records clients inside a module's config (`moduleSource.withClient(...)`) — the same per-module locality we're moving away from for modules themselves. In the workspace-first model, generated clients are SDK-managed bindings to the Dagger API surface exposed by selected workspace modules, recorded in workspace config.

This subsection sketches the future shape. It is not in scope for the CLI 1.0 PR itself; the current `dagger client …` group stays as-is (and stays hidden) and gets ported once this section graduates from sketch.

**Placement under `dagger api`.** A generated client is, semantically, a persistent typed binding to the Dagger API. `dagger api` is the existing top-level group for "interact with the Dagger API" — adding "manage persistent typed bindings to it" fits naturally and avoids inventing a top-level `dagger client` noun.

```bash
dagger api query '{...}'                    # one-shot raw query (existing)
dagger api client add <sdk> --path=<dir> --module=<n>...
dagger api client list
dagger api client rm <name-or-path>
```

**One client = one module's bindings.** A client is generated typed access to exactly one module's API. Need bindings for two modules? Two client entries. Need both surfaces in one host language? Two entries with the same SDK, different paths.

Composes at the engine layer: when a function from one bound module returns a type owned by another (e.g., `cli.db()` returning `postgres.Database`), the client emits an opaque handle. The engine dispatches calls on that handle to the right module's runtime. If the host code wants typed access to the postgres surface, add another `[[modules.typescript-sdk.as-sdk.clients]]` entry pointing at postgres. Bindings are independently regeneratable; no SDK has to walk dependency graphs.

```toml
[modules.typescript-sdk]
source = "github.com/dagger/typescript-sdk@v1.2.3"

[[modules.typescript-sdk.as-sdk.clients]]
path = "./lib/cli"
module = ".dagger/modules/cli"
package-name = "@my-app/dagger-cli-client"     # SDK-specific freeform, per entry

[[modules.typescript-sdk.as-sdk.clients]]
path = "./lib/db"
module = "github.com/dagger/postgres@v1.2.3"   # pinned at add time
```

Fields:

- `path` — output directory, workspace-relative. CLI-interpreted.
- `module` — the single module this client binds to. Accepts a workspace-relative path or a canonical ref, using the same resolution as `[modules.X].source` in `dagger.toml` (no new rules introduced). External refs are pinned at add time, same model `[modules.X].source` uses; refresh with `dagger update`.
- SDK-specific freeform fields (`package-name`, `go-module`, …) — SDK-defined, CLI passes through verbatim.

The targeted module does **not** need to be in `[modules.*]`. A client can bind to an external module the workspace doesn't otherwise consume. Workspace install is a separate concern (it's about what's available to `dagger call` / dependency resolution for other workspace modules); client targeting is independent. This matches the dependency model `[[dependencies]]` already uses inside `dagger-module.toml`.

**CLI verbs.**

```bash
dagger api client add <sdk> --path=<dir> --module=<path-or-ref> \
                            [--name=<n>] [--option KEY=VAL ...]
dagger api client rm <name-or-path>
dagger api client list
```

- `<sdk>` is alias or canonical ref, resolved via `sdks.json`. The SDK must implement `generateClient(...)`.
- `--path` is workspace-root-relative. Required.
- `--module` is the single bound module — path or ref, same resolution as `[modules.X].source`. Required.
- `--name` defaults to the basename of `--path`. Addresses the entry for `rm` later.
- `--option KEY=VAL` (repeatable) sets SDK-specific freeform fields. CLI does no validation; the SDK either accepts or rejects at generation time.

`dagger generate` (existing top-level) regenerates every registered client alongside module codegen.

**Engine API.**

```graphql
extend type Workspace {
  """
  Register a generated client target with the given SDK, bound to the given
  module, and produce its initial output. Returns a Changeset with the new
  [[modules.<sdk>.as-sdk.clients]] entry plus the generated files at `path`.
  External refs are pinned at this call.
  """
  clientAdd(
    sdk: String!,
    path: String!,
    module: String!,
    name: String,
    options: [SdkOption!],
  ): Changeset!

  """
  Remove a generated client registration by name or path. By default leaves
  the generated files in place (the host project may still need them);
  `removeFiles: true` includes file deletion in the returned Changeset.
  """
  clientRemove(name: String!, removeFiles: Boolean): Changeset!
}
```

Same shape as `Workspace.moduleInit`: returns a Changeset, caller previews and applies via `handleChangesetResponseAt`. Single atomic write.

**SDK contract.** Already exists: SDKs implementing client generation expose `generateClient(modSource, introspectionJSON, outputDir): Directory!` (`core/sdk.go::ClientGenerator`). `Workspace.clientAdd`:

1. Resolves `module` to a `ModuleSource` (local path or pinned remote ref).
2. Loads its introspection JSON.
3. Calls `generateClient(modSource, introspection, path)` → `Directory`.
4. Merges the returned Directory into the Changeset at `path`, alongside the new `[[modules.<sdk>.as-sdk.clients]]` config entry.

The SDK receives one module's surface and emits one client. It does not walk dependency graphs, does not know about workspace state, and does not write files. Engine-smart, CLI-dumb, SDK-dumb.

Freeform `options` forward to a future `generateClient(..., options: [SdkOption!])` overload once per-SDK option needs are concrete. Until then, options pass through to be persisted on the entry but the SDK is not wired to consume them at generation time.

**Open questions.**

- **Re-targeting after creation.** If the user wants to point an existing client at a different module ref (e.g., upgrade `postgres@v1` → `@v2`), do they `rm` + `add` or get a `dagger api client update --module=<new-ref>`? Update is friendlier; rm-add is simpler.
- **Final fields on `[[modules.<sdk>.as-sdk.clients]]`.** Shape firms once two concrete client SDKs (TypeScript, Go) implement against it. Today's sketch reserves `path`, `module`, freeform SDK fields.
- **`dagger api client list` vs `dagger installed --clients`.** Latter avoids a dedicated verb for a list operation under `api`; former is more discoverable. Probably keep `list` for symmetry with `add`/`rm`.
- **`removeFiles` default.** Host projects often check generated files into VCS, so deletion has real consequences. Defaulting to false (registration-only) keeps the safer behavior.
- **Regeneration drift.** If the host has edited the generated dir, does `dagger generate` overwrite, refuse, or merge? Same question that exists for module codegen today; whatever answer lands there applies here.

### Per-module SDK operations: `dagger module sdk`

The wrapper for SDK-specific operations on the current module:

```bash
dagger module sdk python-version 3.13
dagger module sdk setup-template legacy
dagger module sdk go-mod-tidy
```

Internally:

1. Locate the cwd's `dagger-module.toml` (walking up, stopping at the workspace root).
2. Compute the module's workspace-relative path.
3. Load workspace config; scan `[[modules.*.as-sdk.modules]]` for an entry whose `path` matches. The parent `modules.<NAME>` (the entry that has the as-sdk sub-table) is the SDK that manages this module.
4. Dispatch `dagger call <NAME> <subcommand> <args>`.

The dispatch reads workspace config, not `dagger-module.toml` (per the runtime/SDK split — the SDK association is workspace-level). If the module isn't found in any `[[modules.*.as-sdk.modules]]`, the wrapper errors: authored modules must be registered in the workspace.

Available subcommands depend entirely on what the SDK exposes. The CLI surface is dynamic per module — `dagger module sdk --help` invoked in a Go module shows go-sdk's functions; the same command in a Python module shows python-sdk's. That dynamism is OK because it's bounded to one wrapper command; users learn the structure once.

### What's *not* in this interface

- No CLI-side schema introspection at init time. The CLI doesn't read SDK schemas to generate dynamic flags. If an SDK needs a setting tuned, the user runs `dagger module sdk <verb>` after init.
- No `sdk` field in `dagger-module.toml`. SDK association is workspace state.
- No `dagger module settings` verb. Workspace-level SDK settings live under `dagger settings <sdk>`; per-module SDK operations live under `dagger module sdk`. No third verb for "per-module SDK settings."
- No CLI-side filesystem writes during `module init`. All mutations flow through the engine's Changeset.

### Open questions in this section

- Final shape of `[[modules.<sdk>.as-sdk.clients]]`. The use case (e.g., typescript-sdk generating a typed client into a Next.js app) is clear at the conceptual level; concrete fields will be defined when the client implementation lands. Kept as a placeholder for now because the right generalization is hard to see in advance of two concrete instances.
- Whether `dagger search` surfaces SDKs alongside other modules. Tentative: yes.
- Whether the runtime-as-builtin-name namespace (`"go"`, `"python"`, …) is a closed set defined by the engine or extensible. Today's answer: closed, matching what the engine bundles. Extension would mean reserving a name for a future runtime module, which is unnecessary while engine builtins exist.

## Discrete changes from current CLI

Implementation checklist. Items grouped by type; each is a discrete unit of work.

### New commands (need implementation)

- [x] **`dagger setup`** — top-level idempotent doctor verb. Three steps with per-step confirmation: Cloud login, workspace migration, recommended modules.
- [ ] **`dagger installed`** — top-level. Lists installed modules from `dagger.toml`. Likely a thin wrapper over existing workspace introspection.
- [ ] **`dagger module init`** — thin CLI wrapper over the engine's `Workspace.moduleInit`. CLI resolves the `--sdk` alias, defaults `--path`, calls the engine, applies the returned `Changeset`. Engine writes `dagger-module.toml` (with `runtime` only) and updates `dagger.toml` (`[modules.<sdk>]` install + `[[modules.<sdk>.as-sdk.modules]]` authoring + optional `[modules.<new-module>]` install). See [SDK module interface](#sdk-module-interface).
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
- [ ] `dagger integration accounts` → folded into `dagger cloud integration list` (the mutable model lists integration entries, replacing the singleton "list accounts of provider X" semantic).
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

- [x] **`list, ls`** cut from the proposal. The modules-v2 effort owns that slot on its own timeline.
- [ ] **Workspace concept is referenced everywhere (`--env`, `setup`, `-W`) but never defined.** Cold-read v2 and v3 both flagged that newcomers can't form a mental model from the top-level help alone. Likely fix: one-sentence definition at the top of `dagger workspace --help`.
- [ ] **`exec` description ("Execute a command in a Dagger session") is too vague.** "Session in what?" Needs sharpening.

## Status

Proposed.
