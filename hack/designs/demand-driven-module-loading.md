# Demand-Driven Workspace Module Loading

*Alternative to [#13380](https://github.com/dagger/dagger/pull/13380) (`workspace-load-single-module`)*

## Table of Contents

- [Problem](#problem)
- [Solution](#solution)
- [Why this is safe without SingleQuery](#why-this-is-safe-without-singlequery)
- [Core Concept](#core-concept)
- [What each command gains](#what-each-command-gains)
- [Phase 2: `dagger call` / `dagger functions`](#phase-2-dagger-call--dagger-functions)
- [Comparison with #13380](#comparison-with-13380)
- [Implementation](#implementation)
- [Edge cases](#edge-cases)
- [Status](#status)

## Problem

1. **One-shot loading** — the first schema-needing request loads *every* workspace
   module (`ensureModulesLoaded`, sticky `modulesLoaded` flag in
   `engine/server/session_workspaces.go`). `dagger call good verify` pays for
   every module in the workspace.
2. **Fragility** — one broken module fails the whole load, for every request in
   the session. For `dagger generate` this includes the module you were trying
   to fix by running generate.
3. **Narrowing is SingleQuery-only** — `narrowPendingWorkspaceModulesForSingleQuery`
   drops pending modules based on root fields, which is only safe under the
   one-request promise. `call`/`generate`/`check`/`up` are structurally
   multi-request and can't use it.
4. **#13380 pushes the signal client-side** — it adds a public
   `currentTypeDefs(include:)` argument plus CLI changes to send hints,
   requiring a full multi-SDK regen. Feedback: too much client-side, and
   "if they don't declare SingleQuery, how do you know it's safe to narrow?"

## Solution

Make workspace module loading **additive and demand-driven** instead of
one-shot. The engine computes the **demand set** — the modules a request can
possibly touch — loads only those, and serves them additively into the
client's schema. The demand source is the natural one for each query shape:
the `currentWorkspace` resolvers' own typed `include` arguments (no request
inspection at all), or a root-field peek (the parse already exists:
`dagql.PeekRootFields`) for queries whose schema depends on the module.
Modules are never dropped: anything not yet demanded stays *pending* and
loads when a later request needs it.

Narrowing becomes **deferral, not exclusion**. No client contract, no API
change, no SDK regen for phase 1.

## Why this is safe without SingleQuery

The schema is already rebuilt per request from whatever is served
(`client.servedMods.Schema(ctx)`, `engine/server/session.go:1397`), and
`serveModule` is per-module, idempotent, and conflict-checked
(`session.go:1570`). So the session schema is allowed to *grow* between
requests today — module SDKs and `Module.serve` rely on this.

With one-shot loading, narrowing is a bet that no later request needs the
dropped modules — only SingleQuery can keep that promise. With additive
loading there is no bet: a later request that references a not-yet-loaded
module triggers its load before validation. The schema is monotonically
increasing and every request sees at least what it demands.

## Core Concept

Demand-driven loading has two entry points, depending on whether the request
can even *validate* without the modules:

**1. Resolution-time loading — the entire `currentWorkspace` API.** Every
`Workspace` field returns core types (`GeneratorGroup`, `CheckGroup`,
`WorkspaceModule`, …): a `currentWorkspace { generators(include: ["good"]) }`
request validates against the core schema with zero modules loaded. So these
need **no request peeking at all** — the `generators`/`checks`/`services`
resolvers already receive `include` as a native typed argument
(`core/schema/workspace.go:784`), and they enumerate modules through
`currentWorkspacePrimaryModules` → `CurrentServedDeps(ctx)`
(`workspace.go:1173`), which reads whatever is served *at resolution time*.
The resolvers just gain a load hook at entry:

```go
// top of checks/generators/services resolvers:
if err := query.Server.EnsureWorkspaceModules(ctx, include); err != nil { ... }
// existing enumeration via currentWorkspacePrimaryModules picks up
// the freshly served modules — no further resolver changes.
```

`EnsureWorkspaceModules` matches the patterns against pending module *names*
(known from config without loading): a pattern whose first segment names a
module demands just that module; a bare token that isn't a module name (an
entrypoint-proxied item) or an unmatched pattern falls back to loading
everything — same semantics as #13380's filter. Empty `include` loads all.
This follows the existing `Query.Server` hook precedent (`CurrentWorkspace`).
GraphQL has already resolved variables into the args, so the peek's variable
handling disappears too.

**2. Pre-request loading — module root fields.** For execution queries like
`{ good { verify } }` the *schema itself* depends on the module, so loading
must happen before validation. `serveQuery` keeps a (generalized) root-field
peek for this path only:

```go
// serveQuery, per request (only while client.pendingModules is non-empty):
ensureWorkspaceLoaded(ctx, client)          // unchanged: detect + gather pending
demand := computeModuleDemand(r, client)    // peek root fields → pending subset
ensureModulesLoaded(ctx, client, demand)    // load + serve only the demand set
schema := client.servedMods.Schema(ctx)     // unchanged: rebuilt per request
```

| Request shape | Mechanism | Demand |
|---|---|---|
| Root field matches a pending module name | peek | that module |
| Root field unknown (entrypoint-proxied item) | peek | entrypoint module (existing fallback logic) |
| `currentWorkspace { generators\|checks\|services(include: [...]) }` | resolver | modules matching the patterns |
| `currentWorkspace { generators() }` bare, `dagger generate` no args | resolver | all pending |
| `currentWorkspace { moduleList \| cwd \| file \| directory \| config* \| ... }` | — | none — these read config/host state, not modules |
| `currentTypeDefs`, `__schema`, `__type` | peek | all pending |
| Unparseable body, non-query operation, anything unrecognized | peek | all pending (conservative) |

`currentWorkspace` drops off the `rootFieldsRequireFullWorkspaceSchema`
denylist entirely: its resolvers are the demand source.

A mid-request load mutates `client.servedMods` while the request executes;
that's safe because the request that triggered it cannot reference module
fields (they weren't in its validated schema), and the next request rebuilds
the schema as always. The hook takes the same locks as module loading today.

State machine in `session_workspaces.go`:

- Replace `modulesLoaded bool` + sticky `modulesErr` with per-module state
  (*pending → loading → served | failed*), singleflight keyed by module
  identity so concurrent requests don't double-load.
- A failed module only fails requests that demand it. Full-schema requests
  (bare `dagger functions`, `__schema`) still surface it — same UX as #13380's
  "full listing loads everything" behavior, but it falls out of the model
  instead of being a special case.
- Extra modules (`-m`) stay eagerly loaded: they were explicitly requested.
- Entrypoint arbitration needs no loading: ambient entrypoint flags are static
  workspace config, and blueprint nomination only happens for extras
  (`resolveModuleLoad` expands blueprints/toolchains for `Kind == Extra` only),
  which are eager. The arbitration winner is computable up front.

## What each command gains

**Phase 1 is engine-only.** Zero CLI changes, zero API changes:

| Command | Today (main) | Phase 1 |
|---|---|---|
| `dagger generate good` | loads all | loads `good` — the CLI **already sends** `generators(include: ["good"])` as the command's functional argument; the resolver narrows from it, no peek involved |
| `dagger check good:*` / `dagger up good` | loads all | loads `good` (same: `include` is already in the query) |
| `dagger query '{ good { verify } }'` | narrowed only with `--single-query` | narrowed for every client, root-field demand |
| `dagger call good verify` | loads all | unchanged (see phase 2) — but a broken sibling module still breaks it only at the `currentTypeDefs` step, same as today |
| bare `dagger functions`, `shell`, `mcp` | loads all | loads all (correct: they need everything) |

The SingleQuery narrowing path is subsumed: `claimSingleQueryRequest` stays
(it's a protocol contract), but the narrowing logic unifies into demand
computation.

## Phase 2: `dagger call` / `dagger functions`

`call`/`functions <module>` build their command tree from a
`currentTypeDefs(returnAllTypes: true, hideCore: true)` introspection
(`cmd/dagger/typedefs.graphql`). That request genuinely carries no module
signal — no engine-side cleverness can extract one. The signal must enter the
request; the question is where. Options, all riding safely on phase 1:

1. **`currentTypeDefs(include:)`** — #13380's approach. Cleanest read, but a
   core API change: `currentTypeDefs` is in every SDK's generated client
   (public + runtime), so it costs a full multi-SDK regen.
2. **Workspace-scoped introspection** — give the experimental Workspace API a
   per-module typedefs path, e.g. `currentWorkspace { moduleList(module:
   "good") { typeDefs {...} } }`, resolved through the same config-aware
   pendingModule load path (settings, entrypoint, legacy policy intact —
   plain `moduleSource().asModule` would lose `dagger.toml` settings and
   double-load under a different dagql identity). Demand peek: `[good]`.
   CLI swaps its typedefs query for the targeted case. Keeps the change off
   `currentTypeDefs` and inside the experimental surface.
3. **Pure-CLI composition** — `moduleList` → `moduleSource(src).asModule.serve()`
   → `currentTypeDefs` (which already returns only *served* deps). Zero engine
   change, but re-implements engine load policy client-side and loses settings
   fidelity. Rejected: this is exactly the "too much CLI-side" direction.

Recommendation: ship phase 1 alone first — it covers `generate`/`check`/`up`
and execution queries with no client change — then decide between (1) and (2)
for `call`. Note that `currentTypeDefs` returning only served deps means
whichever option is picked, the resolver itself needs no change: loading the
right module before resolution is sufficient.

Option (1) is implemented, with three demand rules learned the hard way:

- **Name normalization** — the CLI's first positional token is the *command*
  name, i.e. kebab-case (`cliName`): module `goodMod` is called as
  `dagger call good-mod ...`. Demand matching kebab-normalizes both sides
  (same canonical form as `ModTreePath.Glob`/`CliCase`); raw comparison only
  works for single-word module names, which is exactly what made the original
  tests pass while real workspaces loaded everything.
- **Entrypoint demand** — when a workspace entrypoint is configured, its
  functions are proxied onto the Query root, so the first token may be one of
  them rather than a module name. The typedefs demand therefore always
  includes the entrypoint module, and a token naming no module narrows to the
  entrypoint alone instead of loading everything (no entrypoint → load all,
  as before). Selector (`generate`/`check`/`up`) demand is unchanged: items
  there are module-namespaced, so an unmatched pattern still loads all.
- **Engine compatibility** — engines predating `currentTypeDefs(include:)`
  reject any document naming the argument at validation, even with a null
  variable. The CLI keeps the plain typedefs document for unscoped
  introspections, sends a derived include-scoped document only when a hint
  exists, and falls back to the plain document (load-everything, the old
  behavior) when the engine reports the argument as unknown.

## Comparison with #13380

| | #13380 | This design |
|---|---|---|
| Narrowing signal | `include` hint args, incl. new `currentTypeDefs(include:)` | the query itself (root fields + existing `include` args) |
| Safety in open sessions | narrowing drops pending modules; relies on the hint being right | deferral — later requests load on demand |
| Request peeking | AST peek incl. variable resolution for `currentWorkspace` and `currentTypeDefs` shapes | only for module root fields; `generate`/`check`/`up` narrow from typed resolver args, no AST inspection |
| CLI changes | `generate`/`check`/`up`/`call` pass hints | none (phase 1) |
| API changes | `currentTypeDefs(include:)` | none (phase 1) |
| SDK regen | all SDKs + runtimes | none (phase 1) |
| `call`/`functions` narrowed | yes | phase 2 decision |
| Broken module blocks | fixed for hinted commands | fixed for any request not demanding it |
| `dagger query` (non-single) | not covered | covered |

The two are complementary rather than competing: #13380's
`PeekWorkspaceSelectorInclude` is reused verbatim as a demand rule, and its
`currentTypeDefs(include:)` becomes phase 2 option 1 — with the additive
engine underneath answering the safety objection.

## Implementation

1. `engine/server/session.go` — peek every request while pending modules
   exist (skip entirely once drained, and for clients with
   `LoadWorkspaceModules=false`); replace the SingleQuery-only narrowing call
   with demand computation.
2. `engine/server/session_workspaces.go` — `ensureModulesLoaded(ctx, client,
   demand)`: per-module load state + singleflight; serve incrementally via the
   existing `serveModule`; per-module (non-sticky) errors; precomputed
   entrypoint arbitration.
3. `core/schema/workspace.go` + `core/*.go` — add the
   `Query.Server.EnsureWorkspaceModules(ctx, include)` hook (implemented by
   `daggerClient`, precedent: `CurrentWorkspace`) and call it at the top of
   the `checks`/`generators`/`services` resolvers. No dagql AST peek for
   include args is needed — the resolvers' typed args carry them. Drop
   `currentWorkspace` from `rootFieldsRequireFullWorkspaceSchema`.
4. Tests — reuse #13380's `TestWorkspace{Generate,Check,Up}NarrowsToRequestedModule`
   fixtures; add multi-request session tests (narrow request → broaden
   request → full introspection), concurrent-demand tests, and broken-module
   tests asserting failure is scoped to demanding requests.

## Edge cases

- **Resolver-time loading shifts cache identity** (found the hard way): a
  resolver cached with `CurrentSchemaInput` (e.g. `currentTypeDefs`) keys on
  the schema digest *before* it runs. With pre-request loading that digest
  reflected the workspace's modules and was unique per workspace; with
  resolver-time loading it is always the core schema, so sessions in
  different workspaces issuing the same introspection would share a cache
  entry. Any field that both loads modules in its resolver and caches its
  result must mix the client into its cache key (`PerClientInput`) — or
  already be per-call, as the `currentWorkspace` chain is.
- **Peek cost**: one GraphQL parse per request, only while pending modules
  exist; body already read+restored by the existing peek helper.
- **Aliases/fragments**: `collectRootFields` resolves fragments and uses field
  names, not aliases; anything unrecognized falls back to demand-all.
- **Schema determinism**: serve order may differ from config order across
  demand patterns; `SchemaBuilder.With` dedup/conflict semantics are
  order-stable for distinct names, and same-name conflicts error identically
  regardless of order (`isSameModuleReference`). Tests should pin this.
- **Nested clients**: inherit the parent workspace binding; module-runtime
  clients don't load workspace modules and never peek.
- **Variables**: root field names can't be variables in GraphQL; `include`
  values can — the #13380 peek already resolves them.

## Status

Prototyped as a 7-patch series on the `module-loading` branch — phase 1
(engine-only) and phase 2 option 1 (`currentTypeDefs(include:)` + CLI hint)
in independently revertible patches. Verified against a dev engine: the
narrowing integration tests plus the full `TestWorkspace`/`TestGenerators`
suites pass. SDK client regeneration for the new argument is deliberately
left out of the series.
