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
  sdk             Install and manage SDKs (the modules that author other modules)
  cloud           Manage Dagger Cloud (login, integrations, etc.)
  workspace, ws   Inspect or configure your workspace (cwd, remotes, config, etc.)

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
| 4 | Specialized toolboxes | `api`, `module`, `sdk`, `cloud`, `workspace` |
| 5 | Utility / meta | `help`, `version` |

Visual separation does the disambiguation work that noun-prefix grouping (`dagger workspace X`, `dagger mod Y`) tried to do structurally. Group 4 clusters the major namespaces — `api`, `module`, `sdk`, `cloud`, `workspace` — each with its own subcommand surface. Group 5 is utility; group 3 is the daily-loop module verbs; group 2 is the three shipping fundamentals plus activity.

## Flag rename: `--mod` → `--load-module`

The old `--mod` carried two unrelated meanings: "load a module for this invocation" (consumer) and "select a module to operate on" (author). That conflation is what caused PR #13226's deps/engine commands to be cut. The rename names the actual job; authoring commands take no module-targeting flag at all (see [decision context](#per-command-decision-context)).

| Before | After |
|--------|-------|
| `-m`, `--mod string` | `-m`, `--load-module string` |
| `-M`, `--no-mod` | `-M`, `--no-load-module` |

`--load-module` was chosen over `--with-module` because Dagger's `WithX` API methods are chainable — `--with-module X --with-module Y` would carry a "load both" implication the flag cannot honor (it is single-valued). `--load-module` is verb-form and singular by reading.

Authoring commands (`dagger module deps`, `dagger module engine`) take **no** module-targeting flag. They operate on the `dagger-module.toml` reachable from cwd. To target a sibling, `cd` first. This matches `cargo add` / `npm install` / `go get`.

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
| `module init` | Requested explicitly. Replaces a top-level `dagger init` that briefly existed in early drafts (workspace creation goes implicit on first install instead). `dagger module init` matches `cargo init` / `npm init` muscle memory for scaffolding. **Form: `dagger module init <sdk> <name> [--path=<dir>] [SDK-SPECIFIC FLAGS]`.** SDK is positional, not a flag — the user types the SDK they're using, which becomes the dispatch target. The CLI loads the named SDK (must already be installed via `dagger sdk install`), introspects its `initModule` function, and surfaces SDK-specific args as typed flags (`--go-version=1.22`, `--package-name=foo`, etc.). Generic args the engine owns: `name` positional + optional `--path` flag. Implementation: engine handles workspace bookkeeping (config updates, `dagger-module.toml`, install entries); SDK's `initModule` returns a `Changeset` with any additional files to layer in. See [SDK module interface](#sdk-module-interface). |
| `module deps` / `module engine` | Restored from PR #13226's pre-rollback state. The original work was rolled back because there was no clean home for it under the old `dagger mod` — adding it created the consumer/author conflation problem. The redesign's whole architecture (separate `dagger module` group, `--load-module` rename, no module-targeting flag on authoring commands) is what makes restoring them honest. The rebalance principle (CLI owns shared operations, SDK owns specialized ones — see [SDK module interface](#sdk-module-interface)) puts `deps` and `engine` clearly on the CLI side: editing dagger-module.toml's deps list or engineVersion is 100% identical across SDKs, so duplicating it inside each SDK would be the kind of duplication the new architecture is meant to avoid. |
| `module sdk` | Thin wrapper that dispatches to the current module's SDK. Form: `dagger module sdk <subcommand> <args>`. Locates the cwd's `dagger-module.toml` (walking up, stopping at the workspace root), computes the module's workspace-relative path, then scans the workspace's `[[modules.*.as-sdk.modules]]` for an entry whose `path` matches. The parent `modules.<NAME>` is the SDK; the wrapper runs `dagger call <NAME> <subcommand> <args>`. The SDK association lives in workspace config, not in `dagger-module.toml` (per the runtime/SDK split — see [SDK module interface](#sdk-module-interface)). Available subcommands depend entirely on what the SDK exposes — no CLI-side contract beyond "you're an installed module with functions." Examples: `dagger module sdk python-version 3.13`, `dagger module sdk setup-template legacy`, `dagger module sdk go-mod-tidy`. This is the per-module escape hatch for SDK-specific operations; the CLI provides discovery and orchestration, the SDK provides everything else. |
| `sdk` (group) | First-class top-level group for SDK management. Workshopped against the alternatives `dagger install <ref> --as-sdk` and folding everything under `dagger module` — `sdk` won on four points: (1) it's a namespace with room to grow (`install`, `list`, `search`, `module-options`, `client-options`); (2) built-in disambiguation — `dagger sdk install go` is unambiguous where `dagger install go` would force a `-sdk` suffix or sdks.json hijacking of the install verb; (3) shorter to type than `--as-sdk`; (4) surfaces SDKs as a first-class concept in `--help`. The semantic split with the `module` group: SDK is the tool, module is the thing the SDK creates. `dagger sdk install` adds the SDK to the workspace and marks it; `dagger module init <sdk> <name>` uses an installed SDK to create a module. |
| `sdk install` | Alias-aware install via the embedded `sdks.json` registry. `dagger sdk install go` → resolves `go` to `github.com/dagger/go-sdk`, installs as a module entry, and adds the empty `[modules.go-sdk.as-sdk]` table as the "this install is an SDK" marker. Workspace install name is the basename of the canonical ref (`go-sdk`), same rule as the generic install verb — aliases stay a parse-time convenience and do not propagate into workspace state. Direct refs work too (`dagger sdk install github.com/foo/sdk` → installed as `[modules.sdk]` by basename, marker added). Generic `dagger install <ref>` does NOT mark anything as an SDK; the marker is opt-in via the `sdk install` verb. |
| `sdk uninstall` | Remove an SDK install. Refuses by default if any modules or clients are authored under it (`[[modules.<sdk>.as-sdk.modules]]` / `.clients` non-empty) — orphaning them would leave the workspace pointing at an uninstalled SDK. `--force` overrides; orphaned entries become inert and must be cleaned up by hand. |
| `sdk list` / `sdk search` | `list` enumerates installed SDKs (entries in `[modules.*]` with the `as-sdk` marker). `search [query]` queries a discoverability registry for SDKs specifically — separate from `dagger search`, which searches the general module registry. The two registries have overlapping but distinct shapes (`sdks.json` has descriptions and aliases; the general registry doesn't have aliases). |
| `sdk module-options` / `sdk client-options` | Discovery verbs that surface the SDK-specific flags for `dagger module init <sdk> ...` and `dagger api client init <sdk> ...`. Same content as `dagger module init <sdk> --help`, but named explicitly because surfacing "which flags can I pass when I init a Go module" as a focused query is genuinely useful — and the `dagger sdk` group is the discoverable home for it. Implementation: introspect the named SDK's `initModule` / `initClient` schema, print arg list. |
| `cloud` | Initially in group 5 (meta) with `login`/`logout` as top-level peers. Moved login/logout *under* cloud (rare-use verbs nest). Then `cloud` itself moved from group 5 to group 4 — it's structurally a major namespace, not a meta verb. |
| `cloud integration` | Original `dagger integration` was singleton-shaped (`accounts`, `setup`) — one provider type, list its accounts. Requested redesign to mutable shape (`create`, `rm`, `list`): each configured integration is a discrete entry; `list` enumerates them (optionally filtered by type), replacing the old "list accounts of provider X" framing. Folded under `cloud` per usefulness × simplicity — integrations are configured occasionally, so they nest. |
| `cloud check` | Replaces `dagger workspace autocheck` (which was just on/off for the selected remote). Mutable shape `{on, off, list, status NAME}` proposed during the cloud restructure. Naming intentionally overlaps with top-level `check`: different concepts at different levels — top-level = run local checks, `cloud check` = manage Cloud-side automated runs. Acceptable. |
| `workspace` (group) | Killed in the first flat-redesign sweep, then reintroduced after observing that the namespace itself does load-bearing work: `dagger workspace config` reads as "advanced workspace plumbing" without the verbs having to carry the signal alone. Slimmed to plumbing only (config, cwd, root, config-file, remote, remotes). Bare invocation prints a digest — this absorbed and dropped a briefly-proposed `dagger status` verb. Moved from group 1 to group 4 because it's structurally a namespace, not a single inspection verb. |
| `exec` | Initially hidden as "niche." Pushback restored it to visible in group 5 (utility). Then folded under `api` — `dagger api exec` reads correctly: it's "exec a command with a Dagger API session attached." Belongs alongside `api query` and `api call`, all variations on "use the API directly." Group 5 reverts to just `help` and `version`. |
| `call` (hidden) | `dagger function call` was killed when the `function` group was dissolved. `dagger api call` makes the most semantic sense (it's "an API call" and clusters with `query` and `functions`). `dagger call` kept as a hidden top-level alias for muscle memory — and to keep the top-level `call` slot reserved for a future higher-level porcelain (tentative name: `dagger do`). |
| `shell` (hidden) | Kept reachable, absent from `--help`. Slot stays open to promote or deprecate later based on usage. |
| `env` (removed) | Originally a top-level group with `create` / `list` / `rm`. Removed entirely after recognizing that `env` is *strictly a path prefix in workspace config* (`env.<name>.modules.<m>.settings.<k>`), not a first-class concept. CRUD happens via `dagger settings --env <name>` (typed) and `dagger workspace config` (raw). Discoverability moves into the `--env` flag's description, which names the file path explicitly. This eliminates one corner of the "workspace vs env vs --env vs settings" four-way confusion cold-read v2 flagged. |
| `--load-module` / `-m` | The old `--mod` carried two unrelated meanings (load a module vs. select a module to edit). This is what caused the PR #13226 deps/engine work to be cut — they reused `--mod` to mean "module to edit," which collided. Workshopped: `--load-module`, `--with-module`, `--from-module`. `--with-module` rejected — Dagger's `WithX` API methods are chainable, readers would expect `--with-module X --with-module Y` to compose, but the flag is single-valued. `--load-module` chosen as "safer" — no chain implication, no overload, explicit verb. `-m` short form preserved. |
| `--env` (flag) | Description rewritten to teach the overlay model: envs are paths under `env.<name>.*` in workspace config. The flag description doubles as the discovery affordance for env overlays now that the top-level `env` group is gone. |

## Subcommand structures

The five group commands (`api`, `module`, `sdk`, `cloud`, `workspace`) each own their own subcommand surface.

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
  client     Manage typed clients generated against the Dagger API
  exec       Run a command with a connected Dagger session (DAGGER_SESSION_PORT/TOKEN injected)
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
  init <sdk> <name>   Initialize a new module using the named installed SDK.
                      <sdk> must already be installed via `dagger sdk install`.
                      Defaults to .dagger/modules/<name>; override with --path.
                      SDK-specific flags (e.g., --go-version) come from the
                      SDK's initModule signature; see `dagger sdk module-options
                      <sdk>` for the full list.
  deps                Manage this module's dependencies
  engine              Manage this module's required engine version
  sdk                 Run SDK-specific commands against this module (dispatched
                      to the SDK that authors this module, looked up via the
                      workspace [[modules.*.as-sdk.modules]] entries)
```

### `dagger sdk`

```
Install and manage SDKs (the modules that author other modules).

SDKs are workspace modules whose role is to scaffold/codegen other things:
new Dagger modules (`dagger module init`) or typed clients against the
Dagger API (`dagger api client init`). An install becomes an SDK when added
through this group — `dagger sdk install go` marks the install with the
[modules.go.as-sdk] table that `dagger module init` / `dagger api client
init` use to dispatch.

AVAILABLE COMMANDS
  install <name-or-ref>          Install an SDK and mark it. Alias-resolving:
                                 `dagger sdk install go` resolves "go" via
                                 the embedded sdks.json registry.
  uninstall <name>               Remove an SDK install. Refuses if anything is
                                 authored under it unless --force.
  list                           List installed SDKs (entries with the as-sdk
                                 marker).
  search [query]                 Discover SDKs in the SDK registry.
  module-options <sdk>           Show the SDK-specific flags accepted by
                                 `dagger module init <sdk> ...`.
  client-options <sdk>           Show the SDK-specific flags accepted by
                                 `dagger api client init <sdk> ...`.
```

#### `dagger module deps`

```
Manage this module's dependencies, as declared in dagger-module.toml.

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
| `sdk install` (alias resolution, registry lookup) | CLI | Stable across SDKs; UX layer only |
| `init` dispatch (parse `<sdk>` positional, introspect SDK's `initModule`/`initClient`, surface typed flags) | CLI | Generic dispatch wrapper; no SDK knowledge baked in |
| `init` workspace bookkeeping (config updates, `dagger-module.toml`, install entries) | Engine | One atomic Changeset; no SDK-side workspace knowledge |
| `init` runtime resolution | SDK (optional) | Via `targetRuntime`; defaults to the SDK's own installed ref |
| `init` SDK-specific files (e.g. `main.go`, `package.json`, language-specific scaffolding) | SDK (optional) | Via `initModule` / `initClient` returning a `Changeset` the engine merges in |
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
  { "name": "go",     "description": "Official Dagger SDK for Go",     "repo": "github.com/dagger/go-sdk",     "aliases": ["golang"] },
  { "name": "python", "description": "Official Dagger SDK for Python", "repo": "github.com/dagger/python-sdk", "aliases": ["py"] }
]
```

Resolution rules for `dagger sdk install <value>`:

- Contains `/` or `@` → full ref, no resolution.
- Otherwise → look up name (then aliases, then repo basename as a compatibility fallback) in `sdks.json`, swap in the canonical ref.
- 0 / 1 / >1 matches: error / resolve / ambiguous-error.

Workspace install name is always the basename of the canonical ref (`go-sdk`, `python-sdk`, etc.) — same rule the generic `dagger install` verb uses. Aliases are a **CLI-side, parse-time** mechanism for `dagger sdk install` only: `dagger.toml`, `dagger-module.toml`, the engine, and SDK modules themselves never see the alias — neither as the install name nor anywhere else. The basename is what becomes the dispatch key for `dagger module init <sdk> ...`. Adding a new SDK alias is a registry data change, not a CLI release.

`dagger install` (the generic install verb) does NOT consult `sdks.json` — SDKs come in via `dagger sdk install`. This keeps `dagger install <name>` unambiguous and reserves the alias namespace for explicit SDK installs.

The SDK registry is separate from the general module registry to keep namespaces clean (`github.com/dagger/go` the toolchain and `github.com/dagger/go-sdk` the SDK can both legitimately want the short name "go" depending on context; scoping the SDK alias mechanism to its own registry avoids the collision).

### SDK contract: `initModule` / `initClient`

SDKs may implement two optional functions that the engine calls during init. Both return a `Changeset` the engine merges with its own:

```graphql
extend type GoSdk {
  """
  Optional. Returns a Changeset with any SDK-specific files to layer onto
  the new module's path (e.g. main.go, go.mod, .gitignore).
  When absent, the engine produces only the engine-owned files
  (dagger-module.toml + workspace config updates).

  SDK-specific args declared here become typed CLI flags on
  `dagger module init <sdk> <name>`. The CLI introspects this function's
  schema to build its flag set.
  """
  initModule(
    ws: Workspace!,
    name: String!,
    path: String!,
    # SDK-specific args, e.g.:
    goVersion: String,
    cgoEnabled: Boolean,
  ): Changeset!

  """
  Optional. Returns a Changeset with the generated client at `path`,
  bound to the target `module`. CLI surfaces SDK-specific args (here,
  `goModule`) as typed flags on `dagger api client init <sdk>`.
  """
  initClient(
    ws: Workspace!,
    path: String!,
    module: String!,
    # SDK-specific args, e.g.:
    goModule: String,
  ): Changeset!

  """
  Optional. Runtime ref to write into the new module's dagger-module.toml.
  When absent, the engine defaults to the SDK's own installed ref — i.e.
  this SDK IS the runtime. See targetRuntime section above.
  """
  targetRuntime: String!
}
```

All three are optional, but each carries capability semantics:

- **`initModule` not implemented** → this SDK does not support module authoring. `dagger module init <sdk>` is not registered for it, so trying that path fails like any other missing command. The SDK is fine for whatever it DOES support (client generation, just being a workspace module, etc.) — it just can't seed new modules. Direct engine/API callers still get an explicit `"<sdk> does not support module init"` validation error.
- **`initClient` not implemented** → this SDK does not support typed client generation. `dagger api client init <sdk>` is not registered for it; direct engine/API callers still get `"<sdk> does not support client init"`.
- **`targetRuntime` not implemented** → the engine defaults to the SDK's own installed ref. The SDK *is* the runtime. (This is the only one where absence is a sensible default rather than "feature off.")

So presence-of-function is the capability flag. A "module-only SDK" (Go SDK today) implements `initModule` but not `initClient`. A hypothetical "client-only SDK" — say a thin wrapper that generates OpenAPI-style typed bindings against a remote Dagger module but doesn't author new modules — implements `initClient` but not `initModule`. A full SDK implements both.

`dagger sdk module-options <sdk>` and `dagger sdk client-options <sdk>` reflect this directly: they error with the same unsupported-capability language when the SDK lacks the corresponding initializer, and otherwise print the extra flags (or "No SDK-specific flags" when there are none). `dagger sdk list` could surface a per-SDK capability column (M/C) so users see what's supported at a glance.

**Why Changeset, not Directory.** The SDK can lay files anywhere in the workspace, not just at the new module's path — useful for monorepo-level edits like adding a workspace `.gitignore` entry, updating a top-level `package.json`, or seeding a `tsconfig.json` extension. The Changeset language makes the SDK's contribution composable with the engine's. Engine validates that SDK Changesets don't touch engine-owned files (`dagger.toml`, `dagger-module.toml`).

**Typed args, not freeform options.** Each SDK declares its arguments in the function signature. The CLI introspects the schema and surfaces them as flags:

```bash
dagger module init go my-thing --go-version=1.22 --cgo-enabled=false
dagger api client init typescript ./lib/cli ./modules/api --package-name=@my-app/client
```

`dagger sdk module-options go` and `dagger sdk client-options go` query the same schema and print the flag list as a discovery affordance. No `--option K=V` freeform escape hatch — the SDK either declares an arg or doesn't accept it.

### `dagger module init`

```bash
dagger module init <sdk> <name> [--path=<dir>] [SDK-SPECIFIC FLAGS]
```

The CLI is a thin dispatch wrapper. Steps:

1. **CLI resolves `<sdk>` against the workspace.** Looks for `[modules.<sdk>]` with the `as-sdk` marker. Errors out if not found, with a hint: `"<sdk> is not installed as an SDK in this workspace; run`dagger sdk install <sdk>`first."`
2. **CLI introspects the SDK's `initModule`** schema. Maps positional `<name>` to the function's `name` arg, optional `--path` to `path`, and any extra SDK args (e.g. `--go-version`) to their schema counterparts.
3. **CLI defaults `--path`** to `.dagger/modules/<name>` if not supplied.
4. **CLI invokes the engine:**

   ```graphql
   dag.currentWorkspace().moduleInit(
     sdk:  "<sdk-name>",
     name: "<name>",
     path: "<workspace-relative path>",
     args: { goVersion: "1.22", cgoEnabled: false }   # SDK-specific
   ): Changeset!
   ```

5. **CLI applies the Changeset** via the standard apply path (`handleChangesetResponseAt`), honoring `--auto-apply`.

The engine's `Workspace.moduleInit` performs:

1. **Look up the installed SDK** by name in workspace config; load it.
2. **Determine the runtime ref** by introspecting the SDK module's `targetRuntime` field. If absent, default to the SDK's own installed ref.
3. **Build the module config** at `<path>/dagger-module.toml` with `name` and `runtime`.
4. **Record the authoring relationship** by appending `[[modules.<sdk-name>.as-sdk.modules]] path = "<path>"` to the SDK's role data.
5. **If `path` is the default** (under `.dagger/modules/`), also add `[modules.<name>] source = "<path>"` so the new module is installed in the same workspace. Custom paths skip this — the user is managing layout deliberately.
6. **The SDK must implement `initModule`.** The CLI only registers `dagger module init <sdk>` for SDKs with this capability. The engine still validates direct calls and errors with `"<sdk> does not support module init"` if not. When implemented, the engine calls it with `(ws, name, path, …sdk-args)` and merges the returned Changeset.
7. **Return** the combined Changeset of all the above.

The returned Changeset is the full set of workspace edits, including the SDK's contribution. No filesystem write happens until the caller applies it; the caller can preview and abort. `--auto-apply` skips the preview prompt.

**Refuses if the SDK isn't installed.** Unlike earlier drafts, the engine does NOT auto-install the SDK on init. Installation is the user's deliberate action via `dagger sdk install <sdk>`. Init is dispatch-only.

**Atomicity.** All workspace mutations are in one Changeset — both engine bookkeeping and SDK-contributed files. The engine validates the entire plan before any disk write.

**Future: default SDK inference.** When the workspace has exactly one SDK installed, the user could drop the `<sdk>` positional: `dagger module init my-thing`. When multiple SDKs are installed, the user could declare a `default-sdk` in `dagger.toml`. Not in scope for CLI 1.0; non-breaking follow-up.

**No workspace creation cascade.** `dagger module init` is dispatch-only. A fresh directory must run `dagger sdk install <sdk>` first; that creates the workspace config, installs the SDK, and stamps the `[modules.<sdk>.as-sdk]` marker that init dispatches through.

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

This subsection is implemented in this PR. The old hidden `dagger client …` group is replaced by `dagger api client …`, and client state moves out of module config into workspace config.

**Placement under `dagger api`.** A generated client is, semantically, a persistent typed binding to the Dagger API. `dagger api` is the existing top-level group for "interact with the Dagger API" — adding "manage persistent typed bindings to it" fits naturally and avoids inventing a top-level `dagger client` noun.

```bash
dagger api query '{...}'                                   # one-shot raw query (existing)
dagger api client init <sdk> <path> <module> [SDK-SPECIFIC FLAGS]
dagger api client list
```

**One client = one module's bindings.** A client is generated typed access to exactly one module's API. Need bindings for two modules? Two client entries. Need both surfaces in one host language? Two entries with the same SDK, different paths.

Composes at the engine layer: when a function from one bound module returns a type owned by another (e.g., `cli.db()` returning `postgres.Database`), the client emits an opaque handle. The engine dispatches calls on that handle to the right module's runtime. If the host code wants typed access to the postgres surface, add another `[[modules.typescript-sdk.as-sdk.clients]]` entry pointing at postgres. Bindings are independently regeneratable; no SDK has to walk dependency graphs.

```toml
[modules.typescript-sdk]
source = "github.com/dagger/typescript-sdk@v1.2.3"

[[modules.typescript-sdk.as-sdk.clients]]
path = "./lib/cli"
module = ".dagger/modules/cli"

[[modules.typescript-sdk.as-sdk.clients]]
path = "./lib/db"
module = "github.com/dagger/postgres@v1.2.3"   # pinned at add time
```

Fields:

- `path` — output directory, workspace-relative. CLI-interpreted.
- `module` — the single module this client binds to. Accepts a workspace-relative path or a canonical ref, using the same resolution as `[modules.X].source` in `dagger.toml` (no new rules introduced). External refs are pinned at add time; refresh with `dagger update`.

The targeted module does **not** need to be in `[modules.*]`. A client can bind to an external module the workspace doesn't otherwise consume.

**SDK-specific args (e.g. `package-name`, `go-module`)** are declared on the SDK's `initClient` function and become typed CLI flags. They are NOT persisted as freeform table fields — the SDK reads them at generation time from its own arguments, and the persisted entry is the minimal `{path, module, pin?}` shape above.

**CLI verbs.**

```bash
dagger api client init <sdk> <path> <module> [SDK-SPECIFIC FLAGS]
dagger api client list
```

- `<sdk>` is the workspace-installed SDK name (set via `dagger sdk install`). Required positional.
- `<path>` is the workspace-relative output directory. Required positional.
- `<module>` is the bound module — path or ref, same resolution as `[modules.X].source`. Required positional.
- SDK-specific flags (e.g. `--package-name`, `--go-module`) come from the SDK's `initClient` function signature; see `dagger sdk client-options <sdk>` for the list.

No `rm` verb. Clients are generated files: remove them with `rm -rf <path>` and delete the `[[modules.<sdk>.as-sdk.clients]]` entry from `dagger.toml` if you want it gone permanently. Leaving the entry while deleting the directory means the next `dagger generate` recreates the files at that path — intentional, since regen is idempotent.

`dagger generate` (existing top-level) regenerates every registered client alongside module codegen.

**Engine API.**

```graphql
extend type Workspace {
  """
  Initialize a generated client at `path`, generated by the named installed
  SDK and bound to the named module. Returns a Changeset with the new
  [[modules.<sdk>.as-sdk.clients]] entry plus the SDK's generated files.
  Errors if <sdk> isn't installed as an SDK in this workspace.
  """
  clientInit(
    sdk: String!,
    path: String!,
    module: String!,
    args: JSON,         # SDK-specific args, forwarded to initClient
  ): Changeset!
}
```

The engine validates the SDK is installed-as-SDK, resolves `module` to a `ModuleSource`, and calls the SDK's `initClient(ws, path, module, …args)` if implemented. The returned Changeset is merged with the engine's `[[as-sdk.clients]]` config update.

If the SDK does NOT implement `initClient`, the CLI does not register `dagger api client init <sdk>`. Same capability rule as `initModule` — presence of the function on the SDK is what makes the verb available. The engine still validates direct calls and errors with `"<sdk> does not support client init"`.

**Decisions.**

- **Re-targeting after creation.** Re-run `dagger api client init <sdk> <path> ...` at the same `<path>`; the new entry replaces the old one in the returned Changeset.
- **Fields on `[[modules.<sdk>.as-sdk.clients]]`.** Minimal: `path`, `module`, `pin`. SDK-specific values flow through the function call, not through the persisted entry.
- **`dagger api client list` vs `dagger installed --clients`.** Keep `dagger api client list` for discoverability.
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

- Whether `dagger search` surfaces SDKs alongside other modules. Tentative: yes.
- Whether the runtime-as-builtin-name namespace (`"go"`, `"python"`, …) is a closed set defined by the engine or extensible. Today's answer: closed, matching what the engine bundles. Extension would mean reserving a name for a future runtime module, which is unnecessary while engine builtins exist.

## Discrete changes from current CLI

Status legend: ✅ shipped on this branch | 🟡 partially shipped | ⬜ designed, not yet implemented.

### Shipped — top-level surface

- ✅ **`dagger setup`** — three-step doctor verb (Cloud login → migrate → recommend).
- ✅ **`dagger installed`** — lists installed modules from `dagger.toml`.
- ✅ **`dagger install` / `dagger uninstall` / `dagger update` / `dagger search` / `dagger settings` / `dagger activity`** — all hoisted from `dagger workspace` and `dagger mod` to top level.
- ✅ **`dagger workspace` (bare invocation)** — prints workspace digest (cwd, root, current remote, installed-modules summary). No subcommand needed.
- ✅ **5-group `--help` rendering** — `rootCmd.AddGroup(...)` + per-command `GroupID` + custom `cmdShortWrappedListByGroups` template helper. Five visual groups separated by blank lines: setup / daily flow / workspace management / specialized toolboxes / utility. Subcommand help (e.g., `dagger module --help`) falls back to the leaf-then-parent rendering since those don't define groups.

### Shipped — `dagger module`

- ✅ **`dagger module init`** — scaffolds a new module. **Current shape: `dagger module init <sdk> <name> [--path=<dir>] [SDK-SPECIFIC FLAGS]`.** `<sdk>` is the workspace install name created by `dagger sdk install`; installed SDKs are registered as `init` child commands only when the SDK exposes `initModule`. The CLI introspects extra `initModule` args, exposes them as typed flags, sends changed values through `Workspace.moduleInit(args: JSON)`, and the engine merges the SDK-returned `Changeset` with its own workspace bookkeeping.
- ✅ **`dagger module deps {add, rm, list}`** — restored from PR #13226's pre-rollback state.
- ✅ **`dagger module engine {require, require-current, require-latest, required}`** — restored from the same commit.
- ✅ **`dagger module sdk`** — wrapper that dispatches `dagger call <current-module's-sdk> <subcommand>`. Looks up the SDK from `[[modules.*.as-sdk.modules]]` workspace entries.

### Shipped — `dagger api`

- ✅ **`dagger api call`** — moved from `dagger function call`.
- ✅ **`dagger api functions`** — moved + renamed from `dagger function list`.
- ✅ **`dagger api query`** — unchanged.
- ✅ **`dagger api exec`** — moved from top-level `dagger exec` (with `run` / `r` aliases preserved under the new path). Short description sharpened to "Run a command with a connected Dagger API session". Top-level `dagger exec` is gone.
- ✅ **`dagger api client init` / `dagger api client list`** — replaces the old hidden `dagger client` group. Client entries live in `[[modules.<sdk>.as-sdk.clients]]`; `dagger generate` regenerates them. **Current shape: `dagger api client init <sdk> <path> <module> [SDK-SPECIFIC FLAGS]`.** `<sdk>` is the workspace install name created by `dagger sdk install`; installed SDKs are registered as `init` child commands only when the SDK exposes `initClient`. The CLI introspects extra `initClient` args as typed flags; `--sdk`, `--module`, and `--option` are gone.

### Shipped — `dagger sdk`

- ✅ **`dagger sdk install <name-or-ref>`** — alias-resolving install via `sdks.json`. Workspace install name is the basename of the canonical ref (`go-sdk`), same rule as the generic install verb — aliases stay parse-time-only. Writes the empty `[modules.<name>.as-sdk]` marker that `dagger module init <sdk>` / `dagger api client init <sdk>` dispatch on. Engine method: `Workspace.install(asSdk: true)` — same call as the generic install with the marker arg.
- ✅ **`dagger sdk uninstall <name>`** — CLI-side refuse-if-authored against the on-disk config (no session bootstrap to read TOML), `--force` overrides; files on disk are left untouched.
- ✅ **`dagger sdk list`** — reads `dagger.toml`, prints installs where the as-sdk marker is set. Columns: NAME / SOURCE / M / C (M = authored modules, C = generated clients).
- ✅ **`dagger sdk search [query]`** — lists embedded `sdks.json` entries; substring match on name / description / alias / repo.
- ✅ **`dagger sdk module-options <sdk>` / `dagger sdk client-options <sdk>`** — validates that the named install carries the as-sdk marker, introspects the SDK's `initModule` / `initClient` function, and prints the SDK-specific flags accepted by the corresponding init command.

### Shipped — `dagger cloud`

- ✅ **`dagger cloud integration {create, rm, list}`** — mutable shape (was `setup`, `accounts`).
- ✅ **`dagger cloud check {on, off, list, status}`** — replaces `dagger workspace autocheck`; mutable shape supports per-name addressing.

### Shipped — `dagger workspace`

- ✅ Slimmed to plumbing only: `config`, `config-file`, `cwd`, `remote`, `remotes`, `root`.

### Shipped — config files

- ✅ **`dagger-module.toml` is engine-only.** `[runtime]` table replaces `sdk` field; `Pin` field added for content-addressed reproducibility. `Config` / `Debug` / `Experimental` per-module fields dropped (self-calls graduates to a runtime-capability check; other fields move to language-toolchain files).
- ✅ **`[modules.*.as-sdk]` sub-table** — SDK installs and authoring entries nest under the regular module entry. No parallel `[sdks.*]` top-level section. Settings unify on `[modules.X.settings]`.
- ✅ **Legacy `dagger.json` migration** — `dagger setup` migrates the `sdk` field into `runtime` + adds the `[modules.<sdk>.as-sdk.modules]` authoring entry.

### Shipped — schema

- ✅ **`Workspace.moduleInit`** — returns `Changeset!` (was `String!`); atomic preview-and-apply via `handleChangesetResponseAt`. `sdk` is now the installed SDK name, not a source ref; the SDK must already carry the `as-sdk` marker. No callers in main; Go SDK regen pending so CLI currently calls via raw GraphQL.
- ✅ **`Workspace.clientInit` / `Workspace.clientGenerate`** — new engine methods backing `dagger api client init` and `dagger generate`. `clientInit` uses the installed SDK entry and records the generated client under `[[modules.<sdk>.as-sdk.clients]]`.
- ✅ **`targetRuntime` introspection wiring** — `core.RuntimeTarget` interface added and surfaced via `SDK.AsRuntimeTarget()`. When an SDK module exposes a `targetRuntime: String!` field on its main object, the engine calls it at `dagger module init` time and writes the returned value into the new module's `dagger-module.toml [runtime] source`. Self-hosting SDKs (today's common case) don't implement it; the engine falls back to the SDK's own installed ref. The persisted lazy-SDK path threads the capability through `persistedModuleSourceSDKCapabilities.RuntimeTarget`.
- ✅ **Self-calls graduation** — per-module `experimental.SELF_CALLS` flag still in the schema for back-compat but the engine no longer consults it. Runtime-capability check (does the SDK implement `moduleTypes`?) replaces it.

### Shipped — flag rename

- ✅ **`--mod` → `--load-module`** and **`--no-mod` → `--no-load-module`** (`-m` / `-M` short forms preserved).

### Shipped — removed verbs

- ✅ `dagger mod` group, `dagger function` / `fn` group, top-level `dagger env`, top-level `dagger integration`, `dagger migrate`, `dagger workspace {init, migrate, install, uninstall, update, activity, autocheck}`, `dagger checks` alias, hidden `dagger config` alias, `dagger recommend`.

### Shipped — hidden aliases

- ✅ **`dagger call`** — hidden alias to `dagger api call`.
- ✅ **`dagger shell`** — hidden, reachable.

### ⬜ Not yet implemented — handoff to follow-up PRs

Tracked as implementation tasks #120–#130 with body-level notes.

#### SDK contract

- ⬜ **Concrete SDK `initModule` / `initClient` implementations** — the core CLI/engine contract is wired, but each SDK still needs to add the initializer functions it actually supports and regenerate its bindings. Task #129.

#### Ergonomic follow-ups

- ⬜ **Default-SDK inference** — when a workspace has exactly one installed SDK, let the user drop the `<sdk>` positional on `dagger module init`. Non-breaking; rules sketched in the SDK contract section.
- ⬜ **SDK codegen regen across the 6 SDKs** — the CLI calls `Workspace.moduleInit` / `clientInit` via raw GraphQL (`dag.Do`) today because the Go SDK binding still has the pre-Changeset signature. Collapse to typed calls once regenerated.

## Status

Substantial portion shipped on this branch (`design/cli-1.0` → PR #13392). The runtime/SDK split, `as-sdk` schema, Changeset-returning `moduleInit` / `clientInit`, SDK capability dispatch, and SDK-specific init flags are all in. Follow-up work is concentrated on concrete SDK initializer implementations, `targetRuntime`, default-SDK inference, and SDK codegen regeneration.
