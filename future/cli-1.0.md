# CLI 1.0: Top-level command surface

A redesign of the Dagger CLI's user-facing command surface for 1.0. Collapses the workspace/module namespace duality, hoists daily-use verbs to top-level, introduces a dedicated module-authoring lane, and renames a load-bearing flag to remove a long-standing semantic conflation.

## Table of Contents
- [Problem](#problem)
- [Solution](#solution)
- [Target `--help`](#target---help)
- [Group rationale](#group-rationale)
- [Flag rename: `--mod` → `--load-module`](#flag-rename---mod---load-module)
- [Module authoring lane: `dagger module`](#module-authoring-lane-dagger-module)
- [Removed, demoted, and renamed](#removed-demoted-and-renamed)
- [Notes on individual verbs](#notes-on-individual-verbs)
- [Subcommand structures](#subcommand-structures)
- [Status](#status)

## Problem

1. **`dagger mod` carries two meanings.** Its subcommands mix workspace-consumer verbs (`install`, `uninstall`, `list`, `search`, `recommend`) with module-authoring verbs (`deps add`, `engine require`) — the same noun, two unrelated subjects. The `--mod` flag's overloaded role (load a module vs. select a module to edit) is the load-bearing evidence; this is what caused the deps/engine work to be rolled back from PR #13226 before merge.

2. **`dagger workspace` is redundant for the hot path.** Most workspace commands map to operations users run constantly (install, uninstall, update). Burying them behind a `workspace` noun-prefix is taxation on every invocation and runs against the precedent of cargo, npm, and git, which all leave the project noun implicit.

3. **No authoring lane exists.** The CLI has no clean home for commands that mutate a single `dagger.json`. The deps/engine work had nowhere to go, so it was crammed into `dagger mod` and ultimately cut.

4. **`--mod`/`-m` predates the workspace concept.** It was named when "the module" was the only operating subject. With workspaces and modules-under-authorship now distinct concepts, the flag actively misleads.

5. **`init` lives under `workspace`** with a meaning that pre-empts the more natural "scaffold a new module" reading users reach for from npm/cargo muscle memory.

6. **Generic verb risk is unmanaged.** Naked top-level verbs (`install`, `update`, `search`) read out of context until the user reads the help text. Today's CLI mitigates this by noun-prefix grouping. A flat redesign has to do that work elsewhere — in verb naming, description text, and visual grouping.

## Solution

Adopt a flat top-level verb surface for the consumer hot path. Eliminate `dagger mod` and `dagger workspace` from the visible command tree. Introduce `dagger module` as the dedicated authoring lane, with `init`, `deps`, and `engine` subcommands. Introduce `dagger setup` (write side of workspace state — ensure the environment works) and `dagger status` (read side — show workspace state) as paired inspection/maintenance verbs. Rename `--mod`/`-m` to `--load-module`/`-m` to name the flag's actual job.

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

Visual separation does the disambiguation work that noun-prefix grouping (`dagger workspace X`, `dagger mod Y`) tried to do structurally. The result: fewer keystrokes per invocation and one source of truth for command discovery (the top-level `--help`).

The most prominent two groups (2 and 3) cover the daily loop. Group 4 is the more deliberate "I'm reaching for something specific" lane. Group 5 is meta — about the user's session and tool state, not the project.

## Flag rename: `--mod` → `--load-module`

| Before | After |
|--------|-------|
| `-m`, `--mod string` | `-m`, `--load-module string` |
| `-M`, `--no-mod` | `-M`, `--no-load-module` |

The old name conflated "load a module for this invocation" with "select a module to operate on." `--load-module` names the flag's actual job: load a module so its functions are available to the current command. It cannot be misread as authoring-related.

`--load-module` was chosen over `--with-module` because Dagger's `WithX` API methods are chainable; `--with-module X --with-module Y` would carry a "load both" implication that the flag cannot honor (it is single-valued). `--load-module` is verb-form and singular by reading, removing the implication.

Short form `-m` is preserved for muscle memory. The flag is wired through `moduleAddFlags` in `cmd/dagger/module.go`; the rename is a single funnel-point change plus reference updates in docs and tests.

Authoring commands (`dagger module deps`, `dagger module engine`) take **no** module-targeting flag. They operate on the `dagger.json` reachable from cwd. To target a sibling, `cd` first. This matches `cargo add` / `npm install` / `go get` and keeps the authoring lane's semantics airtight: it only ever edits the module that is *here*, never a remote ref, never `core`.

## Module authoring lane: `dagger module`

The `dagger module` group is the dedicated authoring lane. It operates on a single local `dagger.json` (cwd default).

```
dagger module init
dagger module deps   { add, rm, list }
dagger module engine { require, require-current, require-latest, required }
```

Future authoring commands extend here without further namespace churn: `dagger module sdk`, `dagger module codegen`, etc.

There is no `dagger modules` (plural) command. Listing installed modules is part of `dagger status` output — this avoids the typing-risk a singular/plural pair would have introduced, and keeps the listing alongside the rest of workspace state.

## Removed, demoted, and renamed

| Command | Disposition | Why |
|---------|-------------|-----|
| `dagger mod` (group) | Removed | Conflated two subjects; verbs hoisted flat |
| `dagger workspace` (group, hot-path) | Reshaped | Hot-path verbs (`install`, `uninstall`, `update`) hoisted flat; plumbing (`config`, `cwd`, `migrate`, etc.) kept under a slimmer `dagger workspace` — namespace acts as a "this is advanced" signal |
| `dagger init` | Removed | Workspace creation goes implicit; module scaffolding lives at `dagger module init` |
| `dagger status` | Removed | Bare `dagger workspace` (no args) prints the digest; individual fields are subcommands (`dagger workspace cwd`, etc.) |
| `dagger migrate` | Removed | Legacy migration is past the point where any visibility is justified |
| `dagger config` (hidden top-level alias) | Removed | Now `dagger workspace config` (visible, properly signaled as advanced) |
| `dagger env` (group) | Removed | `env` is a path prefix in workspace config (`env.<name>.modules.<m>.settings.<k>`), not a first-class concept. Create/edit via `dagger settings --env <name>`; list/inspect via `dagger workspace config env`; remove via `dagger workspace config --rm env.<name>` |
| `dagger recommend` | Removed | Generic verb without clear subject at top-level; users reach `search` first anyway |
| `dagger checks` (alias) | Removed | One name (`check`) per concept; aligns with GitHub "Checks" vocabulary |
| `dagger modules` | Removed | Listing installed modules lives in `dagger installed` (past-participle reads as "show me what's installed"); avoids the typing collision with `dagger module` (singular, authoring). The top-level `list` slot is reserved by modules-v2 / artifacts.md for *artifact* dimensions, which do not include installed modules themselves |
| `dagger function` (group) | Removed | `function call` folded into `dagger api call` (with hidden top-level alias `dagger call`); `function list` folded into `dagger api functions` |
| `dagger integration` (top-level) | Moved | Now `dagger cloud integration` (with mutable shape: `create`, `rm`, `list`, `accounts`) |
| `dagger call` | Hidden | Top-level alias for `dagger api call`; reserves the top-level slot for a future `dagger do` |
| `dagger shell` | Hidden | Reachable at top-level but absent from `--help`; promote or deprecate later |
| `--mod`, `--no-mod` (flags) | Renamed | `--load-module`, `--no-load-module` |

Workspace plumbing lives in the slimmer `dagger workspace` namespace:
- `cwd`, `root`, `config-file`, `remote`, `remotes` → individual `dagger workspace <field>` subcommands (and rolled up in bare `dagger workspace` digest)
- `config` → `dagger workspace config` (raw `dagger.toml` editing; distinct from `settings`, which manages module-declared knobs — the namespace IS the disambiguator)
- `autocheck` → moved to `dagger cloud check` (mutable shape: `on`, `off`, `list`, `status`)

## Notes on individual verbs

**`setup`** — Idempotent doctor command, not a one-shot wizard. "Ensure" implies it can be run anytime: it does whatever's needed to bring the workspace into a working state, and no-ops what's already fine. Owning "make sure the environment works" also resolves the `update` ambiguity (see below). `setup` is the *write* side of workspace state; `workspace` is the *read* side and the home for advanced workspace plumbing.

**`workspace`** (alias `ws`) — Inspection and plumbing for the workspace itself. Bare invocation (`dagger workspace`) prints a digest of cwd, root, current remote, and installed modules. Subcommands provide scriptable single-field reads (`dagger workspace cwd`, `dagger workspace remotes`, etc.) and admin writes (`dagger workspace config`, `dagger workspace migrate`). The namespace itself does load-bearing work: it signals "you're poking at workspace internals," which keeps `dagger workspace config` clearly distinct from `dagger settings` (module-declared knobs) without needing the verbs alone to disambiguate.

**`installed`** — Lists installed modules in the workspace. Past-participle reads as "show me what's installed" — precedent in `pip list`, `gem list --installed`. Top-level because it's a daily-frequency read; named `installed` rather than `modules` (plural) to avoid the typing-risk with `module` (singular authoring group).

**`list`** (alias `ls`) — General-purpose enumeration over the artifacts framework defined in [modules-v2 / artifacts.md](https://github.com/dagger/dagger/pull/12900). Form: `dagger list <dimension>`. Built-in: `dagger list types`. Per-dimension: `dagger list go-test`, `dagger list go-module`, etc. Dimensions are *artifacts extracted from module schemas* (tests, Go modules referenced by your Dagger modules, etc.) — **not** the installed Dagger modules themselves. To enumerate installed modules, use `dagger status --modules`. The top-level `list` slot is named here because modules-v2 owns it; the cli-1.0 redesign does not redefine it.

**`check` / `generate` / `up` — the three shipping fundamentals.** Every software shop, no matter how esoteric the stack or workflow, performs three categories of operation: verifying code (`check`), producing derived artifacts (`generate`), and running services for development (`up`). These verbs are top-level because they name those categories universally; the module ecosystem provides the per-stack implementations. `check` specifically aligns with GitHub Checks vocabulary (the red/green gates on every PR), so CI integration reads naturally. The "your project" phrasing across all three descriptions reinforces the universality: this is *your* shop's stuff, mapped through Dagger.

**`update`** — Scoped strictly to refreshing installed-module state (lockfile, pinned refs). Self-upgrade of the Dagger CLI is *not* this command — `setup` owns whatever environment maintenance is needed. The clean split between `setup` (environment) and `update` (module versions) removes the "does `update` mean update Dagger?" trap.

**`install`** — Module installation into the workspace, not Dagger installation. Description must make this unambiguous; the verb alone carries package-manager baggage from npm/apt/pip. Aliased to `i` to match `npm i` muscle memory. `uninstall` is similarly aliased to `un`.

**`module`** — Authoring group. Always operates on cwd's `dagger.json`. Subcommands: `init` (scaffold a new module), `deps` (manage dependencies), `engine` (manage required engine version). `init` lives here, not top-level: workspace creation is implicit, and module scaffolding is the npm-init/cargo-init muscle memory users reach for first.

**`search`** — Searches the module registry. Verb-as-action; near-universal CLI idiom (`apt search`, `brew search`).

**`api`** — Every Dagger command ultimately runs against a GraphQL API served by the engine, combining Dagger's core types with schema extensions loaded from modules. This group surfaces direct access for scripting and advanced automation. Three subcommands: `query` (raw GraphQL), `call` (function call porcelain — clusters here rather than a near-empty `function` group), `functions` (introspection). Top-level mockup keeps the `(advanced)` tag for signal-and-skip; the teaching beat lives in the cobra Long description so it only appears when someone curious clicks through. Most users will never type these.

**`call`** — Hidden top-level alias for `dagger api call`. Preserves muscle memory for users who type `dagger call <fn>` daily, without burning the top-level slot. The slot is intentionally reserved for a future `dagger do` command (a higher-level porcelain that `call` is currently doing the work of).

**`shell`** — Hidden top-level. Reachable but absent from `--help`. Keeping the slot open lets us promote or deprecate later based on usage.

**`exec`** — Execute a command in a Dagger session. Niche but useful; lives in group 5 (utility) where its low-traffic status doesn't crowd the daily-loop verbs above.

**`settings`** — Module-declared settings. Form: `dagger settings [module] [key] [value]`. Each installed module exposes its own settings schema; this is the verb that tunes them. With `--env <name>`, scopes the write to that env's overlay (i.e., stores at `env.<name>.modules.<m>.settings.<k>` instead of the base `modules.<m>.settings.<k>`). Distinct from `dagger workspace config` — `settings` is the friendly, schema-aware path; `workspace config` is the raw editor for the whole tree.

**`activity`** — Shows recent runs/traces for this workspace. Promoted to top-level because it is a daily observation verb. Requires Cloud, but that fact is not load-bearing for placement (see principle below).

**Design principle: usefulness wins over OSS/Cloud purity.** Cloud-requiring commands (`activity`) live at top-level when they earn the slot by frequency or importance, not by being part of a Cloud namespace. The reverse also holds: rarely-used Cloud verbs (`login`, `logout`, `integration`, `check`) nest under `cloud` because they are infrequent in practice, not because they are Cloud-specific. The placement axis is *usefulness × simplicity*, not *OSS vs Cloud*.

**`cloud`** — Manages Dagger Cloud. Subcommands include auth (`login`, `logout`), `billing`, `org`, `integration` (configure Cloud integration providers — mutable shape: `create`, `rm`, `list`, `accounts`), and `check` (Cloud-side automated checks — mutable shape: `on`, `off`, `list`, `status`). All live here because they are configured occasionally rather than invoked daily.

## Subcommand structures

Each top-level group that owns its own leaf commands. Verb-only commands (`install`, `check`, `setup`, `status`, etc.) take no subcommands and are not listed here.

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

USAGE
  dagger workspace [command]

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

Operates on the dagger.json reachable from the current directory.

AVAILABLE COMMANDS
  init    Initialize a new module in the current directory
  deps    Manage this module's dependencies
  engine  Manage this module's required engine version
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
Manage Cloud integration providers (mutable: configured providers come and go).

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

## Status

Proposed.
