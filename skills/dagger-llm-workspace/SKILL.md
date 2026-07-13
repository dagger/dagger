---
name: dagger-llm-workspace
description: How Dagger's LLM ↔ Workspace binding actually resolves — the two-workspace model, where a module/generator/tool sees the *current* vs the *frozen* workspace, the seed-vs-propagate mechanism across the module boundary, the lazy-changeset gotcha, and the from-source dev-engine QA loop. Use when working on Dagger's LLM/Workspace/module-context code (core/mcp.go, core/llm*.go, core/workspace*.go, core/modfunc.go, core/sdk.go, generators/checks/agents/services, engine/server/session*.go) or debugging why module/generator/tool code sees stale vs current workspace state.
---

# Dagger LLM / Workspace development

Hard-won, counterintuitive knowledge about how the LLM's Workspace flows through
the engine. The recurring bug class: **code reads a *frozen* workspace when it
should see the agent's *current* (edited) one.** Regular (non-LLM) usage never
notices, because nothing advances the workspace in-session without a side effect —
so "the ambient workspace" and "the workspace I'm operating on" coincide. The LLM's
in-memory overlay is the first thing that makes them diverge, which is why these
bugs only surface for agents.

## The two workspaces (internalize this first)

- **`llm.workspace` / `mcp.workspace`** (`core/llm.go`, `core/mcp.go`) — the LLM's
  **bound** workspace. It **advances** as edit/write tools apply overlay changesets
  (`MCP.applyChangeset`). This is the agent's live, evolving state.
- **`Server.CurrentWorkspace(ctx)` = `client.workspace`** (`engine/server/session_workspaces.go`)
  — the per-client workspace cached at session load. **Frozen** (invalidated only by
  config changes). This is what `currentWorkspace` returns by default.

When something looks "over-cached" (a generate re-runs as a 0.0s cache hit with a
stale result), the cache is almost always *correct* — the real inputs never changed
because the code resolved the frozen workspace. Don't chase caching; chase which
workspace was resolved.

## How a Workspace gets resolved (the matrix)

When a module function / generator / tool needs "the workspace," it comes from one of:

1. **Auto-injected `Workspace!` arg** — filled by `loadWorkspaceArg` (`core/modfunc.go`),
   which **prefers `WorkspaceFromContext(ctx)`**, else falls to `currentWorkspace`.
   This is the idiomatic path: resolved server-side at the call boundary and folded
   into the callee's call ID (cache-sound). **Prefer this.** A `@generate`/`@check`
   function with a required `Workspace!` arg is fine (it's exempt from the
   requires-args skip).
2. **`currentWorkspace` field** — resolver (`core/schema/workspace.go`) prefers
   `WorkspaceFromContext`, else `Server.CurrentWorkspace`. Reading it *directly inside
   a module body* is a footgun (caching, and the binding often isn't present there).
   Use the arg instead.
3. **`@defaultPath(path: ".")`** — resolves to the **module's own source dir**
   (`.dagger/modules/<name>`), **not** the workspace root. It cannot read workspace
   files. Use a `Workspace!` arg to read the workspace.

## Seed vs. propagate (the key mental model)

`WorkspaceToContext(ctx, ws)` (`core/workspace_context.go`) is a Go `context.Value`.
Two things must both happen for a callee to see the right workspace:

**Seeding** — where the workspace enters a call chain:
- MCP tool dispatch (`core/mcp.go`, ~line 634): binds `m.workspace` for the tool call.
  This also covers `applyChangeset`, which runs *under the same `toolCtx`*.
- Group runs: `GeneratorGroup.Run` / `CheckGroup.Run` / `AgentGroup.Compose` /
  `UpGroup.Run` thread a transient `BoundWorkspace` — the workspace `.generators(W)`
  etc. was called *on*, re-derived from the call ID. This is what makes a **raw**
  `W.generators().run` (no tool binding, e.g. forced in the main session or a plain
  API query) run against `W`.

**Propagating** — a Go ctx value does **not** survive the module execution boundary.
A module body runs in a **nested client session** (`ServeHTTPToNestedClient`, entered
via `runtime.Call`), which starts a fresh Go context — so a binding set upstream is
gone inside the module body and its nested cross-module calls (the receiver object
`currentNode` has to ride `fnCall` for the same reason). To cross it, the workspace is
carried **per call**: `modfunc.Call` reads `WorkspaceFromContext` → threads it through
`runtime.Call` → the nested client's `client.workspaceContext` → `WorkspaceFromContext`
gains a **server-side fallback** to `Server.CurrentWorkspaceContext`. This restores the
invariant **"module A calling module B runs B against A's workspace."**

This entire propagation machinery is a re-derivation of what the eliminated `Env` type
had (`EnvToContext`/`EnvFromContext` with a `Server.CurrentEnv` fallback, carried via
`runtime.Call`; removed in `4a3e58e1b`). **If you touch cross-boundary workspace flow,
mirror the Env plumbing** — grep `git log -S EnvFromContext` for the exact shape.

Consequence worth knowing: because MCP seeds tools and the carry propagates, an LLM tool
that calls a dependency (auto-injected `Workspace!`) works via the carry *without*
`BoundWorkspace`; and the LLM→generate path works via the `toolCtx` binding at
`applyChangeset` *without* `BoundWorkspace` too. `BoundWorkspace` is only load-bearing
for the direct `W.generators(explicitOverlay).run` API contract (no tool binding). Don't
assume which mechanism carries a given path — **A/B test it** (below).

## The lazy-changeset gotcha

A tool that returns an object (e.g. a `Changeset`) is **lazy**.
`workspace.generators(include).run.changes` builds a lazy chain; **nothing runs in the
tool call.** `.run` (the generator execution) fires when MCP **forces** the changeset in
`MCP.applyChangeset` — a *separate* dagql op, but one that runs under `toolCtx` (so the
tool binding is present at force time). When reasoning about "what workspace does X see,"
always ask: **where and when is X actually forced, and is the binding present in that
session/ctx?** The workspace an operation reads is fixed when its inputs are *resolved*
(often at force time), not when the lazy value is constructed.

## Debugging methodology

- **Prefer a deterministic raw-query repro over driving an LLM.** e.g.
  `currentWorkspace.withNewFile("input.txt","B").generators(include:["mod:gen"]).run.changes.layer.file(path:"output.txt").contents`.
  Have the generator *encode what it read* into its output so it's observable.
  Caveat: a raw query has **no** MCP tool binding — only the group `BoundWorkspace`
  seeds it — so a raw-query repro and the real LLM path can diverge. Test the exact
  path you care about; when unsure, do both.
- **A/B a fix by disabling it** (`if false && …`), rebuilding, and comparing outputs.
  Do **not** trust reasoning about ctx propagation across the module boundary — it is
  genuinely subtle and easy to get wrong (I confidently mis-called the LLM→generate case
  until the A/B disproved it). The engine rebuild is cheap relative to being wrong.
- **`dagger trace <id> --org <org> -vvvv --progress=plain 2>trace.txt`**, then grep
  (traces are large — never dump into context). Reads: `DONE [0.0s]` = cache hit;
  `no(digest: xxh3:…)` renders a value by its digest — compare a workspace arg's digest
  across two calls to see whether the inputs actually changed. `--org` matters (traces
  are org-scoped; wrong org silently returns 0 spans).

## From-source dev-engine QA (no local container runtime)

This host can't run a local engine. Essentials + gotchas:

- Build the dev CLI once: `DAGGER_CLOUD_ENGINE=1 dagger call cli dev-binaries --platform=current export --path=./bin`.
- Bring up a tunneled from-source engine (rebuild after **any** core change; Go layer
  invalidates, ~1–3 min):
  `DAGGER_CLOUD_ENGINE=1 dagger call engine-dev service --name=X up --ports=23234:1234`.
  The `up` process may return while the **cloud** service keeps holding the tunnel; a
  stale one blocks the port (`bind: address already in use`). `pkill -f "engine-dev service"`
  and confirm `23234` is free before restarting; use a fresh `--name` each time.
- Point the dev CLI at it: `export _EXPERIMENTAL_DAGGER_RUNNER_HOST=tcp://127.0.0.1:23234`.
  A dev CLI can't use the cloud engine directly — it must talk to the tunnel.
- Drive the agent TUI over **tmux** with **`DAGGER_PROGRESS=tty`** (both mandatory — a real
  pty, and the agent-detected report frontend panics in interactive prompt mode). LLM auth
  conveys for free from `~/.config/dagger`.

## Other gotchas

- **Workspace-module discovery needs a git repo.** In a non-git directory,
  `Workspace.generators`/`checks`/`agents` enumerate **zero** items with no error. `git init`
  + commit a scratch fixture before driving `generate`/`check`/`agent` against it.
- **Core-object args (including `Workspace`) transmit to modules as the *full recipe ID***
  (`PrimitiveType.ConvertToSDKInput` returns the `dagql.ID` as-is → `json.Marshal` →
  `id.Encode()` → the whole DAG). A deep overlay workspace ⇒ a large payload per nested
  call. A digest-reference-then-rehydrate transport would shrink the wire with no replay
  impact (the receiver expands to the identical recipe ID from the shared session cache).
  Not yet done — worth a separate transport change if it bites.

## See also (in the repo)

- `hack/designs/workspace-agents.md` — the as-built design: workspace binding,
  object tools, `@agent` plugins, and the propagation semantics (seed vs
  propagate, the `BoundWorkspace` backstop, the cross-module carry).
