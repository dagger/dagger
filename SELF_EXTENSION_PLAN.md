# Self-Extension: LLM-Returning Tools as Continuations

Status: PARKED — design agreed in principle, not yet started.

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
