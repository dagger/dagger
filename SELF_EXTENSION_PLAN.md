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

### Guardrails: no lineage gate

A lineage requirement (the returned LLM must derive from the passed-in one)
was designed, built, and then REMOVED. It was the wrong shape:

- It is inconsistent with the state-return convention it extends.
  `rebindWorkspace` accepts ANY workspace; what makes that safe is not
  prevention but VISIBILITY — the model reads a patch summary of what changed.
- The tools are written by the env's author. Refusing a "suspicious" history
  protects nobody from anybody.
- It blocks the uses this mechanism exists for: self-compaction
  (`llm.compacted`), summarize-and-restart, sub-agent hand-*back*.
- Its very first real use tripped it as a false positive: `reload` strips and
  re-adds every module's system prompts, which live in `Messages`.

What guards a swap instead:

1. **Eager validation** of the returned LLM's env/tools, so a broken env is a
   failed tool call rather than a bricked loop.
2. **One continuation per turn** — LLMs do not merge the way Changesets do.
3. **Visibility** — the adoption summary tells the model which tools came and
   went, and whether the conversation history was replaced. Any LLM may be
   adopted; a swap is never silent.


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
  arm — eager env validation, swap, summary, persist.
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
  summary content, batch restriction.

## As built

Landed as designed, with these deviations worth knowing:

1. **No lineage guardrail.** It was built first as a history-based check
   (ID ancestry is not walkable: a dagql `Result`'s `ID()` is an opaque runtime
   handle, and an LLM is usually transformed by being PASSED to something —
   `agents.compose(base: llm)` — so the ancestor sits in an argument literal,
   not the receiver chain). Then it was deleted outright, per the reasoning in
   "Guardrails: no lineage gate" above: visibility, not prevention. What is left
   is eager validation, one-per-turn, and the adoption summary — which now also
   reports `Conversation history replaced: N messages -> M messages.` when the
   current history is not a prefix of the adopted one (`historyPreserved` in
   core/mcp.go is a summary input, not a gate).

2. **Protocol coherence for the in-flight tool results.** The one real
   mechanical issue the lineage check papered over: `step()` appends the turn's
   tool results to the ADOPTED LLM, and providers require every tool_result to
   follow the matching tool_use (keyed by call ID). An adopted conversation need
   not contain this turn's call at all — self-compaction legitimately drops it.
   `toolResultSelectors` (core/llm.go) therefore appends an orphaned result as a
   plain user message (`[continued via tool <name>]\n<result>`) instead of a
   protocol-invalid tool-result block; results whose call IS present (the
   install/reload case, where history is preserved) append normally.

3. **Compose idempotence** (§5) is resolved on the module side:
   `install`/`reload` call `withoutSystemPrompts` before recomposing, so
   repeated reloads don't stack duplicate prompts and `withTools`' one-binding-
   per-type rule handles the tools. The cost, documented on both tools: system
   prompts added OUTSIDE the agent composition are dropped.

4. **Persistence** (§4) needs no new selector. The continuation is the receiver
   of the turn's result selectors, so the transform is already part of the
   resulting LLM's ID and replay lands on it.

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
- Tests: `TestHistoryPreserved`/`TestMessagesEqual`/`TestSummarizeToolsetChange`/
  `TestSummarizeContinuation`/`TestToolResultSelectors`
  (core/llm_continuation_test.go) and `TestLLM/TestToolReturningLLMContinues`
  (core/integration/llm_object_tools_test.go, 5 subtests over the
  `workspace-tool-return` fixture) cover success swap, error containment,
  adoption of a history-replacing conversation (with the dangling tool result
  carried as a plain message) and the one-per-batch restriction.
- Unblocked by dropping the lineage gate, not yet built: self-compaction
  (`llm.compacted`), summarize-and-restart, sub-agent hand-back.

## Follow-ups from first real-world use (live install/reload session)

The feature was exercised in anger: a live agent session reloaded itself to
gain the `contributor` module's `contribute` tool mid-conversation, then used
reload twice more to pick up config and code fixes. Findings:

1. **Reload syncs from disk, not the agent's pending overlay.**
   `Workspace.reloaded` + `agents.compose` re-read module source AND settings
   from the host checkout — verified empirically: the agent's staged edits to
   dagger.toml and to a module's source were both invisible to recomposition
   until the user saved the workspace to disk. This breaks the self-repair
   loop the feature exists for (edit module → reload → new behavior, fully
   in-session). Module/config loading needs to resolve through the workspace
   overlay.
2. **`cmd://` secrets should trim trailing whitespace.** `gh auth token` (and
   most credential commands) emit a trailing newline; git's credential helper
   tolerates it, but HTTP Authorization headers reject it ("invalid header
   field value"). engine/client/secretprovider/cmd.go should TrimSpace, as
   credential tooling conventionally does. (Worked around in
   modules/contributor's publish script meanwhile.)
3. **Cosmetic:** one recomposition reported "Conversation history replaced:
   196 -> 197 messages" for what was semantically a pure extension — the
   prefix check may be tripping on system-prompt-adjacent block equality;
   worth a look.


