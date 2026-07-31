# Recipe Replay: Loading A Saved ID

This document describes what happens when a recipe-form ID is loaded, why some
recorded calls must be re-executed rather than served from the dagql cache, and
how `FieldSpec.NotReplayable` decides which.

The source of truth is the code, mainly:

- `dagql/server.go` (`Server.LoadType`, `recipeLoadState`)
- `dagql/objects.go` (`FieldSpec.NotReplayable`)
- `dagql/cache_inputs.go`
- `core/schema/host.go`
- `core/schema/workspace.go`
- `core/container.go` (`Container.Build`)

This doc focuses on:

- the difference between handle-form and recipe-form IDs
- why replaying a recorded call is not the same as making it
- the two impure things a saved ID can reach
- how the taint is computed and why it propagates upward
- why the rule is scoped to cross-session replay

See `internal-docs/session_resources.md` for the neighbouring rule about
secrets and sockets, which solves a related problem at the value level rather
than the replay level.

## Two Forms Of ID

An ID is either a **handle** or a **recipe**.

A handle is an opaque `engineResultID` pointing at a result that already exists
in this engine. Loading one is a table lookup: `Server.LoadType` dispatches on
`id.IsHandle()` and goes straight to `LoadResultByResultID`. Nothing is
re-derived, and nothing below applies.

A recipe is the call structure itself — receiver, field, args, implicit
inputs — all the way down. Loading one walks that structure and reconstitutes
each call.

The generic `id` field returns a **handle** by default. Recipe form is opt-in,
via the internal `recipe: true` argument. So the ordinary pattern of taking an
ID from one call and passing it to another never enters the recipe loader.

## Who Loads Recipes

Recipe loads are not a niche path. Two producers matter today.

**`LLM.portableID`** returns a self-contained recipe so a conversation can be
saved to disk and resumed later, possibly against a different engine. This is
the cross-session case.

**Dockerfile builds** synthesize a recipe and load it. `llbtodagger` translates
the marshalled LLB graph into a chain of Dagger calls — `from`,
`withEnvVariable`, `withWorkdir`, `withExec` — rooted at the build context, then
`Container.Build` loads the result. The build context has to be recipe-form
because llbtodagger *appends* to it, and you cannot append to an opaque handle;
the synthesized ID also describes work that has not run yet, so it could not be
a handle even in principle. This is a same-session case, and a hot one.

## Why Replay Is Not The Same As The Original Call

When a call is made normally, its implicit inputs are resolved fresh:
`PerCallInput` mints a new random value, `PerSessionInput` reads the current
session, `PerClientInput` reads the current client.

When a recorded call is *replayed*, they are not. `loadedResultCallFromRecipeID`
copies the recorded implicit inputs verbatim, so a replayed call reproduces the
exact digest the original minted.

That is what makes replay work at all — it is how a saved conversation comes
back as the same conversation. It is also the whole problem: the recorded digest
is a durable cache key, and it stays valid forever regardless of whether the
value behind it still means anything.

Compounding this, `recipeLoadState.loadRecipeVertex` performs its digest lookup
**before loading the call's inputs**. A hit therefore short-circuits the entire
subtree beneath that node. A single cached ancestor can prevent an impure call
underneath it from ever being reached.

## The Two Impure Things A Saved ID Can Reach

Most of what a recipe records is pure: `withEnvVariable`, `withPrompt`,
`withToolResult`. Replaying those from cache is correct and desirable.

Two things are not.

**A client-bound workspace.** `Query.currentWorkspace` resolves the *calling
client's* workspace, and the resulting `core.Workspace` carries that client's
`ClientID`. Client IDs only resolve inside their own session:
`SpecificClientMetadata` looks one up via `clientFromIDs(currentSessionID,
clientID)`. A workspace replayed into another session therefore carries a
client that can never be routed, and every consumer that switches into the
workspace's owning client fails:

```
workspace client metadata: failed to retrieve session main client:
client "..." not found
```

**A host read.** `Host.directory` reads the live filesystem. A recorded read is
a snapshot of one moment, not a reproducible value, but its digest does not
change when the tree does.

Note what is *not* on this list. The `Workspace` read APIs — `directory`,
`file`, `glob`, `search` — are dispatchers, not impurities. A git-backed or
synthetic workspace resolves through `resolveRootfsFromDirectory` to a
content-addressed tree that replays perfectly. Only the client-local branch
reaches the host, and that already goes through `Host.directory`. Marking the
workspace APIs would declare a whole reproducible API unreplayable to cover one
branch.

## The Rule

`FieldSpec.NotReplayable` marks a field whose result must not be served to a
replayed call. `recipeLoadState.notReplayable` skips both cache lookups for such
a call, and for **every recorded call that depends on one**.

The upward propagation is load-bearing, not defensive. Because the digest lookup
precedes input loading, marking only the impure node would achieve nothing: an
ancestor whose digest is still cached would be served wholesale and the marked
call underneath it would never be reached. The taint is computed structurally
over the recipe, evaluates nothing, and is memoized per load.

## Scoping To Cross-Session Replay

The property is not "this call can never be replayed". It is "this call cannot
be replayed **across a session boundary**". Replaying a host read inside the
session that made it is fine: the client is still alive, and the host view has
not been swapped out from under the recipe. It is crossing the boundary that
turns the recorded digest into a stable key for a value that no longer means
anything here.

So a marked call is only tainted when the `cachePerSession` stamp recorded on it
differs from the loading session. Fields that are marked must therefore also
declare `PerSessionInput`, so the stamp exists to compare; a marked call with no
stamp is treated as tainted, which is safe but silently costs cache hits.

Session — not client — is the right granularity, matching
`clientFromIDs(sessionID, clientID)`. A module client's recorded host read
replayed by the main client of the same session stays on its normal cache path.

Without this scoping the rule is far too broad. Dockerfile builds load a
synthesized chain rooted at the build context, which is frequently
`host.directory`; tainting that unconditionally propagates up every translated
instruction and costs cache hits on every build from a host directory.

## Marked Fields Today

| Field | Reason |
|-------|--------|
| `Query.currentWorkspace` | Result carries the calling client's ID, which only resolves inside its own session. |
| `Host.directory` | Reads the live host filesystem; a recorded read is a snapshot, not a reproducible value. |

## Relationship To Session Resources

Session resources (`internal-docs/session_resources.md`) solve a related problem
one layer down: a *value* that depends on a session-local resource is stamped
with a handle, and a cache hit is rejected unless the requesting session holds
that handle. The requirement propagates through result dependency edges.

The two rules are complementary rather than alternatives.

| | Session resources | NotReplayable |
|---|---|---|
| Operates on | values | recorded calls |
| Propagates via | result dependency edges | recipe structure |
| Enforced at | candidate filter | recipe load |
| Covers | any lookup, including same-session cross-client | replay across a session boundary |

The distinction matters in practice. A workspace handle would gate the workspace
result, but a `Workspace.directory` read returns the *inner* `host.directory`
result, which carries no dependency edge back to the workspace — so the resource
requirement does not reach it. The recipe-level rule does, because the host read
is structurally present in the recipe regardless of which value it flowed out
of.

## Adding A Marked Field

Before marking a field, check whether it is genuinely impure or merely
*sometimes* impure. If some inputs make it reproducible — as with the workspace
read APIs — mark the impure thing it delegates to instead.

When marking one:

1. Add `NotReplayable("<reason>")` with a reason a reader can act on.
2. Ensure the field also declares `dagql.PerSessionInput`, or every replay of it
   is tainted regardless of session.
3. Remember the cost is not local. The taint propagates to every recorded call
   that depends on it, so a field near the root of common recipes is expensive.

## Testing

`core/integration/llm_resume_test.go` is the worked example. Two things there
are worth copying into any test that exercises replay.

Teardown order is a real variable, not harness detail. `Cache.ReleaseSession`
collects results that drop to zero owners, so a save/exit/resume sequence with
nothing else running garbage-collects the offending value and re-resolves it
live — passing for a reason unrelated to correctness. Keeping the saving session
alive is what makes the replay hit the cached value.

Content-addressed collisions cross temp directories. Two tests that write
byte-identical trees can share cache entries even in separate workdirs, which
both masks and manufactures failures. Give each scenario distinctive contents.
