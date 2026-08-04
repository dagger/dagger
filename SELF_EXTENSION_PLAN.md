# Self-Extension: LLM-Returning Tools as Continuations

Status: IMPLEMENTED. See the "As built" section at the end for what changed
relative to this design, and what is still open.

## Problem

The agent env is a static composition: modules are composed onto the LLM once,
at session setup. The agent cannot install a new module mid-conversation, and
after editing one of its own modules (self-repair, e.g. fixing
`modules/editor`) there is no way to reload it — the session must be
restarted, losing all context.

## Idea

Extend the tool-result state convention with one more ring. Today
(`core/mcp.go` `applyStateReturn`):

| Tool returns | Semantics |
|---|---|
| `String`     | a report — context only |
| `Changeset`  | overlay onto the bound workspace |
| `Workspace`  | *replace* the bound workspace |
| **`LLM`**    | **replace the conversation — env, tools, prompts, history** |

An LLM-returning tool acts as a continuation: the loop resumes from the
returned LLM instead of the one that made the call. Since the LLM binds the
workspace, this strictly subsumes workspace replacement.

The loop's accumulator already IS an immutable LLM value, so this exposes the
thing the loop threads rather than inventing a new mechanism.

## Passing the current LLM: required

Flagship use cases (install, reload) are *transforms*, not hand-offs. Tools
and system prompts live on the LLM, so a tool that cannot see the current LLM
can only build a fresh conversation — which is just `delegate` again. Instead:

- Auto-fill a hidden `llm: LLM!` arg exactly like `Workspace!` args today
  (see `core/schema/agents.go` for the bound-workspace fill): snapshot of the
  conversation up to and including the in-flight tool call, hidden from the
  model.
- `install` then composes naturally in a module:
  `base.withWorkspace(ws.withModule(path))` → recompose agents → return.
- `reload` is the same tool pointed at the current workspace state — the
  self-repair loop (edit module, reload, keep going) with no restart.

### v1 guardrail: lineage

Require the returned LLM to be derived from the passed-in one (ancestry
check on the ID chain). Keeps the tool a *transform* — the env owner's
prompts and the history cannot be silently swapped out. Relax later for the
spicier uses: self-compaction (`llm.compacted`), persona/phase switching,
sub-agent hand-*back*.

## Semantics to nail down

1. **In-flight turn.** On success, the loop appends the tool-result message
   to the RETURNED LLM and continues from there. On error, the old LLM
   stands — failure containment for free (installing a module that fails to
   load = failed tool call, agent still alive). Validate the returned LLM
   eagerly: load its env/tools at swap time, not lazily on the next turn.
2. **Toolset refresh.** The loop must re-derive the tool list from the env
   each step after a swap. If it already rebuilds per step this is free;
   if cached, it maps onto MCP's `tools/list_changed`.
3. **Parallelism.** Changesets merge via octopus; LLMs do not merge. At most
   one LLM-returning call per batch (reject or serialize the rest).
4. **Persistence.** `step()` persists workspace rebinds via a
   `withWorkspace` selector so history rebuilds converge; the LLM swap needs
   the equivalent (record the continuation so session replay lands on it).
5. **Compose idempotence** (the expected friction): `agents.compose` ADDS
   middlewares onto a base, so recomposing onto an LLM that already carries
   tools double-stacks them. Options:
   - idempotent/replacing composition (compose keyed by module identity,
     or `withoutTools` first);
   - compose fresh + graft history (`fresh.withHistoryFrom(old)`-style).
   Decide before implementing `reload`.

## Implementation sketch

Engine side:
- `applyStateReturn` (`core/mcp.go`): add the `dagql.ObjectResult[*core.LLM]`
  arm — lineage check, eager env validation, swap, persist.
- Hidden `LLM!` arg auto-fill in the tool schema (mirror the `Workspace!`
  auto-fill path in `core/llm_object_tools.go` / `core/schema/agents.go`).
- Loop adoption in `core/llm.go`: continue from the returned LLM; refresh
  tools; enforce one-per-batch.

Module side (exercises it end-to-end):
- An `installer` (or extension of `editor`) module:
  `install(module: String!): LLM!` and `reload(): LLM!`.

QA:
- tui-qa driving `dagger agent` against a from-source engine (engine-lab):
  watch a mid-conversation tool appear live after `install`, and a module
  edit take effect after `reload`.
- Unit tests around applyStateReturn arm: success swap, error containment,
  lineage rejection, batch restriction.

## As built

Landed as designed, with three deviations worth knowing:

1. **The lineage guardrail is history-based, not ID ancestry.** A dagql
   `Result`'s `ID()` is an opaque runtime handle (`call.NewEngineResultID`);
   `Receiver()`/`Args()` panic on it, so there is no ID chain to walk. Worse,
   an LLM is usually transformed by being PASSED to something
   (`agents.compose(base: llm)`) rather than received by it, so even in recipe
   form the ancestor sits in an argument literal, not the receiver chain.
   `continuesHistory` (core/mcp.go) instead requires the returned LLM's message
   history to EXTEND the current one, compared by value. That is the property
   the guardrail actually protects — env, tools and system prompts stay free to
   change, which install/reload need. Relaxing the history rule is still what
   self-compaction (`llm.compacted`) would take.

2. **Compose idempotence** (§5) is resolved on the module side:
   `install`/`reload` call `withoutSystemPrompts` before recomposing, so
   repeated reloads don't stack duplicate prompts and `withTools`' one-binding-
   per-type rule handles the tools. The cost, documented on both tools: system
   prompts added OUTSIDE the agent composition are dropped.

3. **Persistence** (§4) needs no new selector. The continuation is the receiver
   of the turn's `withToolResult` selectors, so the transform is already part
   of the resulting LLM's ID and replay lands on it.

`step()` now materializes `withResponse` BEFORE dispatching tool calls (so the
continuation is handed the conversation up to and including its own call) and
appends the turn's results to the continuation. When a swap happens the
workspace/bound-tool persistence is skipped: whatever the continuation binds is
already in its own ID.

Still open:
- Live QA against a real model was NOT completed. tui-qa binds a cached engine,
  not one rebuilt from the workspace, so its `dagger agent` never saw the new
  tools; engine-lab's client has the right engine but no LLM credentials.
  Verified instead by (a) composing the dev-env `editor:agent` through the
  engine-lab CLI and reading `LLM.tools` — `install` and `reload` are generated,
  with the `llm` argument hidden from the model — and (b) the replay-driven
  integration tests below.
- Tests: `TestContinuesHistory`/`TestMessagesEqual`/`TestSummarizeToolsetChange`
  (core/llm_continuation_test.go) and `TestLLM/TestToolReturningLLMContinues`
  (core/integration/llm_object_tools_test.go, 5 subtests over the
  `workspace-tool-return` fixture) cover success swap, error containment,
  lineage rejection and the one-per-batch restriction.

