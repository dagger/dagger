# Workspace Agents: the LLM as a Workspace-bound agent

As-built design for the agent architecture on this branch: the LLM binds to the
session's **Workspace**, acts through **object tools**, composes installable
**`@agent` plugins**, and its edits are **seeded** into the tool calls and
group runs that act on its behalf. This consolidates and supersedes the earlier
working docs for the individual pieces (the LLM object-tools proposal, the
`@agent` plugins proposal, and the workspace/generator sync investigation).

## 1. The Workspace binding

The LLM operates on the same `core.Workspace` a CLI user sees — not a separate
I/O construct:

- `LLM.withWorkspace` / `LLM.workspace` bind the workspace explicitly.
  `Query.llm` starts unbound; the CLI and agent composition select a workspace.
- `MCP.baseServer` (`core/mcp.go`) selects the **base schema for core tools and
  dispatch**: the current client's served modules when unbound, the owning
  client's served modules for a live workspace, or just core for a value
  workspace. Value-workspace modules load from their trees during explicit
  agent composition. Bound object tools retain their own defining schemas.
- The LLM's edits stage into the workspace's in-memory **overlay**
  (`Workspace.withChanges` etc., from the workspace overlay & export branch); a
  single `Workspace.export` writes the accumulated diff to the local checkout.
  In the shell, `ctrl+s` drives export with a diff preview. Host writes happen
  *only* there — no tool can write to the host directly.
- `Workspace!`-typed tool/function arguments auto-inject from the bound
  workspace (matched by type, `FunctionArg.IsWorkspace`), so they never appear
  in a tool schema and a module function never has to plumb one by hand.

The former `Env` type — the prompt-I/O bag the LLM used to bind against — is
gone. The workspace binding took over its two real jobs (giving the model
mutable state, and scoping what it can reach), and object tools (§2) took over
tool generation. Unlike `Env`, the bound value does not auto-propagate across
module boundaries; §4 describes the explicit-pass contract that replaced that.

## 2. Object tools: objects as tools and state

The LLM acts through the methods of the objects it's bound to via
`LLM.withTools(object, except: [...])` (`core/llm_object_tools.go`):

- Every **eligible method** of a bound object becomes a tool: name = method
  name, description = method doc, parameters = the method's scalar args
  (object-typed args are allowed only when optional; `Workspace!` args
  auto-inject and stay invisible).
- A tool call selects the method on its bound object as receiver, against the
  workspace-served schema with the workspace threaded into context.
- Returns route by type:

  | Return type | Behavior |
  |---|---|
  | the bound object's own type | **rebind** it as the new state, recorded field-wise (below); the tool's `print` output is the response |
  | `Changeset` | apply to the workspace overlay; return the patch summary |
  | `Workspace` | **rebind the LLM's workspace** to it, with a before/after diff summary |
  | `LLMContent` | return its text and media blocks as the tool result's content, after the tool's `print` output |
  | scalar / `[scalar]` | return the value |
  | any other object | `sync()` it and return the logs it emitted |
  | `Void` / null | return logs, else `(done)` |

- **The object is the agent's state.** A state update is a method that returns
  a new `self`; `step()` persists the transition as a `.withTools` selector on
  the LLM's ID (the same shape it uses to persist a `Changeset` overlay via
  `withWorkspace`), so state transitions are ordinary selectors — durable and
  reconstructable when loaded. At most one binding per object type is kept.
- **State is recorded by what it is, not by the call that produced it** — see
  "Recording state" below.
- To the *model*, objects are never named, passed, or returned as handles;
  binding is author-side. There is no `Type#N` registry and no free-form
  script surface. Host-writing fields (`export`) are simply not reachable:
  they are not bound-object methods.
- When two bound objects expose the same method name, the last `withTools`
  wins; a bound method also overrides a builtin of the same name.

### Recording state

A recorded state is loaded again wherever the conversation is: by the first
tool dispatch of a resumed session, and by `__rebindState(previous:)` when a
tool reload recomposes the agent. Whatever its recipe names runs again
whenever that load misses the cache. So the recipe must say what the state
*is*, never which call produced it: the methods that return a new state are
typically `@cache(Never)` (`start`, `restart`, `spawn`), and replaying them
starts engines and hires workers again.

**Why module objects need this, and core objects don't.** An SDK returns a
module object as its fields, as JSON; the engine materializes them as a new
result of the method call, so the returned object's recipe is
`recv!method(…)`. A function that returns a core object (a `Workspace`,
`Changeset`, `LLM`, `Directory`) instead returns a result it built with core
calls, and the call adopts that result, recipe included (see where `dagql`'s
cache completes a call whose value is already an attached result). Its recipe
is that chain of core calls (`ws!withNewFile(…)`) and does not name the module
call. It does re-run the core work on a cold load, which is why a `Changeset`
is applied as a patch rendered against the workspace (`MCP.applyChangeset`).
Other core returns, and bound core objects, rely on core calls being hermetic.

**What is recorded.** A same-type return of a module object becomes
`root!__withField(name:, value:)!…` (`core/object_state.go`):

- `__withField` is an engine-owned field installed on every module object type
  beside `__rebindState`. It is not in `TypeDef.Functions`, so it never becomes
  a tool, an entrypoint proxy or an SDK method. It is a pure, persistable
  setter that clones the receiver with one field replaced, and carries the
  module's `Module`/`ModuleProvider`, so its frames keep module provenance and
  `withTools` can resolve their type without loading them.
- `value` is a `StateValue`: exactly one of `scalar` (JSON), `node` (an ID),
  `list` or `entries` (key/value), or none of them for null, mirroring how a
  module object is persisted (`persistedModuleObjectValue`). `node` is a real
  ID argument, so a reference is an edge of the recipe and is rewritten from
  handle form to recipe form when the recipe is persisted, rather than an
  opaque string. `StateValue` is in no schema: engine selects and recipe replay
  decode it from the field spec, so it never surfaces in introspection or
  generated SDKs.
- The **root** is the previous state's recipe with its `__withField` and
  `__rebindState` frames peeled off: the object the composition bound, or after
  a tool reload, the new revision's initial object. The frames set each field
  that differs from the root, once, in name order. The chain is thus at most one
  frame per field, values a later step overwrote leave the recipe together with
  their producers, and equal states on one root share a recipe however they
  were reached. The root is in the previous state's own receiver chain, so
  selecting on it is a cache hit.
- **What counts as a change** is decided against the receiver, not the root:
  references compare by identity (the result for handle-form IDs, the recipe
  digest otherwise), everything else by canonical JSON. A return equal to its
  receiver records nothing, so the binding, and the LLM's recipe, do not
  advance for an idempotent call. A field the return drops is recorded as null,
  since `__withField` can only set.
- A module object's producing call is never recorded. A state that cannot be
  encoded fails the tool call; `@collection` objects are refused outright (their
  identity includes a `CollectionBase` that is not a field, and a bound
  collection has no author methods to call). Core objects have no fields to
  diff and are rebound as returned.

The producing call is not lost: it is the tool call's own span in the trace.
Only the recipe forgets it.

**Loading.** `LLM.withTools(object:)` is a lazy reference: loading an LLM never
loads its tool state, and the first dispatch on a binding loads it. A warm cache
(the same engine, or a persisted result) hits on the last frame's digest and
evaluates nothing else; a cold one (another engine, an evicted result) walks the
chain, loading the root and every reference each frame sets, which is why the
chain is rebuilt from its root rather than appended to: an appended chain loads
every value a field ever held. Each frame attaches its references exactly as
an SDK-returned object's are attached.

**The boundary.** References keep their own recipes. A field holding a module
object that a `@cache(Never)` method returned still records that call, and a
cold load of the current state runs it; a core object a module function
returned, like a `Directory`, records the core calls that built it, as above.
The state is plain data plus references; what loading a reference costs is the
reference's business, and within a session the cache covers it.

**Known costs.** A map-valued field is recorded whole whenever it changes, so a
map growing by one entry per step costs O(N²) frame payload over a session;
entry-level setters would make that linear. `__rebindState` still loads the
previous object on recompose, where it could instead re-root the frames onto
the new revision's initial object.

### Binding self via `Query.currentNode`

A module's agent entrypoint binds itself with `withTools(currentNode, except:
["agent"])`, not `withTools(self, ...)`: in a Dang module `self` is
reconstructed from serialized state and has no engine-side identity, and Dang
only coerces objects to typed IDs (`withTools` takes `ID! @expectedType(name:
"Node")`). `Query.currentNode` returns the object that *received the call* —
the live receiver with its dagql ID, state and all — threaded onto the active
`FunctionCall` (`ModuleFunction.Call` records it as `parentTyped`) and carried
over the per-session `fnCall` channel into the module's nested client. It
errors at the top level, where there is no receiver.

### Why not per-field tools, and why not a scripting harness

The original per-field MCP surface was abandoned for token bloat (~213k tokens
for core alone); that objection disappears when tools come from a small,
author-curated object. The intermediate design — the model writing Dang
scripts against the schema (`dang_eval` + schema disclosure) — asked the model
to be a Dang programmer before it could act, and models kept stumbling on the
return contract even with prompt guidance. Direct tools from bound objects
supply the affordances up front; Dang remains the language modules are
*authored* in, not the harness the model operates.

## 3. `@agent` plugins: installable agents via `dagger agent`

Dagger modules are installable as LLM agent plugins:

```sh
cd ~/src/proj
dagger install github.com/vito/editor           # Read/Write/Edit tools
dagger install github.com/vito/dagger-go@go-doc # goDoc tool
dagger agent                                    # prompt with all of them composed
```

- **Contract:** an `@agent` function is a middleware `agent(base: LLM!): LLM!`.
  The base is a single required `LLM!` argument identified **by type, not
  name** (mirroring `Workspace!` matching; `base` is just the recommended
  name). It must return `LLM!`, may take auto-injected `Workspace!` args, and
  must not declare any other required arg — validated at module load
  (`core/module.go`).
- **Discovery** mirrors `@check`/`@generate`/`@up` end to end: an `IsAgent`
  marker on `Function`, directive registration, SDK wiring, and rollup across
  the current module + installed deps via the mod tree
  (`core/modtree.go`; `Workspace.agents`, `core/schema/agents.go`).
- **Composition:** `AgentGroup.compose` (`core/agents.go`) folds one
  workspace-bound LLM (fresh `Query.llm()`) through each selected middleware in
  alphabetical `module:fn` order, passing it explicitly to the base arg. The
  base is *not* ambient — unlike a workspace, an LLM is always handed in
  explicitly; the only seeding happens at the composition entrypoint.
- On a tool-name collision, ALL tools of every bound object involved are
  served under namespaced names (`<object>_<tool>`, e.g. `tuiQa_start` and
  `engineLab_start`) so nothing is silently shadowed, and a warning names the
  collision and the namespaced names. Objects with no collisions keep bare
  tool names — the common case stays terse.
- `@agent` methods are auto-excluded from `withTools` toolsets, so authors
  don't need `except: ["agent"]` for the standing-in-it case.
- **CLI:** `dagger agent` composes all installed agents and drops into the
  prompt; `dagger agent editor go-doc:goDoc` selects a subset
  (colon-qualified with bare-name fallback, inherited from `dagger check`);
  `dagger agent -l` lists. Selection and composition live server-side so the
  composed LLM is reconstructable from its ID.

## 4. Workspace propagation: who sees which workspace

There are two workspace notions in a session, and keeping them straight is
what makes agent behavior predictable:

1. the LLM's **bound** workspace (`llm.workspace` / `mcp.workspace`), which
   advances as tools apply overlay changesets;
2. the per-client **frozen** workspace (`Server.CurrentWorkspace`), cached at
   session load and invalidated only by config changes.

The bound (live, edited) workspace is **seeded** into the calls that act on
the agent's behalf, and stops at the module execution boundary:

- **Seeding.** MCP tool dispatch threads the bound workspace into the direct
  tool call's context, and group runs re-seed the workspace they were called
  on: `Workspace.generators` / `.checks` / `.agents` / `.services` stash their
  `parent` workspace on the group as a transient `BoundWorkspace`, and each
  group's run/compose threads it via `WorkspaceToContext` before invoking each
  leaf — so a *direct* `W.generators(overlay).run` (raw API query, `dagger
  generate`/`check` against an overlay) resolves leaves against that overlay
  rather than the frozen base. Only the first hop — the direct tool call and
  layer-1 group leaves — sees the bound workspace this way.
- **No cross-module carry.** The bound workspace does **not** propagate across
  the module execution boundary. A module function calling another module must
  pass a `Workspace` **explicitly** as an argument; an omitted auto-injected
  `Workspace!` errors instead of silently falling back to the caller's session
  workspace (`loadWorkspaceArg` / `callerInModuleFunction` in
  `core/modfunc.go`). This is the contract adopted from #13229: workspace flow
  between modules is always visible in the call.
- **Frozen-workspace inheritance stays.** A nested client still inherits its
  parent's *session* workspace (`inheritWorkspaceBinding` →
  `Server.CurrentWorkspace`, `engine/server/session_workspaces.go`). This is a
  separate, engine-internal discovery mechanism — e.g. `currentModuleAsSDK`
  (`core/schema/module_as_sdk.go`) finding the workspace a module is installed
  in during nested SDK codegen. Module *source* stays session-served (frozen);
  a generator reading its own module directory via `@defaultPath` is
  unaffected.
- **`currentWorkspace` is hidden from module SDKs** (#13659): modules cannot
  call `dag.CurrentWorkspace()` at all — it's a compile error. A module
  receives the Workspace via a declared arg, never by reading
  `currentWorkspace`. (Engine-internal resolvers may still read
  `Server.CurrentWorkspace` directly, which is what frozen inheritance
  serves.)

The mental model: a workspace is **seeded** into a call chain (MCP seeds tools
at dispatch; groups seed their leaves via `BoundWorkspace`), and past that it
is **passed explicitly** — never carried implicitly across a module boundary.

## 5. Pointers

- Object tools: `core/llm_object_tools.go`, `core/mcp.go` (`applyStateReturn`,
  `rebindWorkspace`), `core/integration/llm_object_tools_test.go`
- `currentNode`: `core/schema/module.go`, `core/modfunc.go`
- `@agent`: `core/agents.go`, `core/schema/agents.go`, `core/modtree.go`,
  `internal/cmd/dagger/agent.go`, `core/integration/agents_test.go`
- Propagation: `core/workspace_context.go` (seeding), `core/modfunc.go`
  (`loadWorkspaceArg` / `callerInModuleFunction` — the explicit-pass condition),
  `engine/server/session_workspaces.go` (frozen inheritance),
  `core/integration/generators_test.go`
  (`TestWorkspaceGeneratorsSeeOverlayEdits` and the
  `generator-workspace-sync` fixtures)
- Known gap: the `modules/evals` suite still references the removed `dag.Env()`
  and needs migrating to the workspace/object-tools model.
