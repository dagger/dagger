# HANDOFF: async agents — spawn pivot + remaining work

Continuation notes for the async-agents work. Orientation: read
`hack/designs/async-agents.md` first — §1–§8 design, §9 ratified semantics,
§11 implementation status. Everything below reflects the branch AFTER the
live QA session that shook out modules/staff; the big in-flight item is now
the **spawn pivot** (a ratified-in-conversation design change, not yet
implemented or written into the design doc).

## State of the branch

- **ID-returning verbs (old Task A): DONE.** All imperative Agent verbs
  return `ID!` with `@expectedType` via the `agentSelfID` pattern
  (core/schema/agent.go:130–141); `send` pins through `message(id:)` and
  returns the pinned message ID (:188–235). Tests, SDK codegen, and the CLI
  rewire landed with it.
- **modules/staff (old Task B): DONE and live-tested.** Built per plan (NOT
  registered in the repo dagger.toml; load with `dagger -m ./modules/staff`).
  A full live QA session (spawn/status/read/ask/collect/interrupt→resume/
  askChief round-trip/dismiss) passed, after two fixes (commit `60b1c42`):
  - Every side-effecting or live-reading tool needed
    `@cache(policy: FunctionCachePolicy.Never)` — without it dagql replays
    identical-arg calls (zero-arg `status` could never observe a state
    transition). Same idiom as engine-lab/editor/contributor.
  - Name reuse after dismiss hit the STOPPED tombstone (see pivot below);
    worked around with a `Random.string` nonce threaded through the
    `chiefLine(boss:, nonce:)` self-call. The pivot deletes this.
- **Uncommitted local changes that are NOT this work** (do not fold into
  commits): `dagger.toml` registers staff in env.dev (how the QA session
  bound it); `core/integration/agent_runtime_test.go` `$ws: WorkspaceID!` →
  `$ws: ID!`. Both are the user's.

## THE PIVOT: `LLM.asAgent(name)` → `LLM.spawn` (DO THIS FIRST)

**Decision (user-ratified in conversation):** replace the pure value
constructor `asAgent(name: String!): Agent!` with an effectful, ID-returning
verb — `spawn(name: String): ID! @expectedType(name: "Agent")` — that mints
a **unique Agent instance per call**, pinned by re-exec like `send`. Nothing
here has shipped, so the API break is free; this is the last cheap moment.

### Why (compressed from a three-agent research effort)

- Live failure: agent identity = `ContentPreferredDigest` of the composition
  chain (name included), registry `AgentRuntimes.entries` keyed by it
  (core/agent.go:200–218); stop leaves the tombstone IN the keyed slot
  (:262–266) and `enqueue` rejects STOPPED ("no turn will ever consume the
  message", :492–497). So re-spawning a dismissed name against an unchanged
  workspace resolves to the predecessor's corpse. The task rides `send`, not
  the chain — so this is the COMMON case for dismiss-and-rehire, not an edge.
- The registry's own doc comment cites the Services model, but Services
  FREE the key on exit (`delete(ss.running, key)`, core/services.go:1126;
  tombstones go to a capped side list) — the divergence is exactly the
  contested point. Services also mint `InstanceID: identity.NewID()` into
  the registry key for interactive services (services.go:733–739).
- Prior art is unanimous (Temporal workflowID/runID + reuse policies; Erlang
  name/Pid; Akka path/ref-with-UID "new incarnation… not the same actor";
  k8s name/UID + generateName; tailcall `create_agent` — server-minted
  stable AgentID, name as pure metadata, server-side uniqueness suffix so
  repeated `create_agent("reviewer")` never alias, its design §6.4 step 2):
  a reusable name is never the instance ID, and **uniqueness is minted where
  instances are born, never by callers**. In Dagger, instances are born in
  the runtime registry/schema layer — not in caller-supplied entropy.
- The design already solved instance-identity-with-honest-chains once, for
  messages (§9 re-exec pinning). Spawn extends that same pattern to Agent.
  Caller-side alternatives (nonce, `fork(label:)`, an `instance:` arg, an
  `onStopped:` policy, a `respawn` verb) all either keep caller entropy or
  add a per-spawn ritual; spawn dissolves the entire reuse-policy question:
  terminal stop becomes the honest semantics of an instance (nobody asks to
  restart a k8s UID), and ABA/stale-handle hazards become impossible.

### Mechanics (engine)

1. `core.Agent` gains an `InstanceID string` field; `Name` becomes optional
   display metadata (telemetry/TUI label only — no identity role).
2. `LLM.spawn(name: String)` resolver in core/schema/llm.go: `DoNotCache`,
   mint `identity.NewID()`, then pin exactly like `send`
   (core/schema/agent.go:188–235): re-exec Select through a new pure lookup
   `LLM.agent(id: String!): Agent!` on the same receiver, return
   `dagql.NewID[*core.Agent]` of the pinned result via
   `dagql.NewResultForCurrentCall` (agentSelfID pattern).
3. `LLM.agent(id:)`: pure, deliberately cached (same rationale as
   `message(id:)`, core/schema/agent.go:51): reconstructs the value
   `Agent{Seed: parent LLM, InstanceID: id, Name: …}`. Never touches the
   registry — a cold re-Select projects IDLE-from-absence as today
   (core/schema/agent.go:152–157). Name must ride the pin too (either as a
   second arg to `agent(id:, name:)` or fold display-name into the minted
   record) — pick whichever keeps the pinned chain self-contained.
4. **The registry does not change.** `agentKey` = digest of the pinned chain,
   which now contains a unique literal → every spawn gets a fresh key;
   GetOrCreate/Start/Send/stop/tombstones/LookupMessage all work verbatim.
5. `LLM.loop` stays a cached pure field; its resolver spawns internally.
   Identical `llm.loop` chains still dedupe at loop's own call ID (the one
   load-bearing dedupe), because the second evaluation cache-hits `loop`
   and never reaches the inner spawn.
6. Remove `asAgent`. CLI (internal/cmd/dagger/llm.go): the interactive
   session's `interactive-<hex>` entropy naming becomes a plain `spawn`
   (entropy no longer needed; pass a display name if wanted).
7. Codegen: `generate` with ["go-client:generate"], then ["docs:references"].

### Semantics ledger (for the design doc)

Renounced: attach-by-rederivation. Two evaluations of the same composition
are two agents; observing a running agent requires holding its ID — which
§3.3 already ratified as the addressing model ("to message an agent you must
hold its ID"). The §5 second-terminal flow goes through held IDs (the norm
since the ID-returning-verbs change). Kept verbatim: send-to-STOPPED errors,
seal ordering, signal-with-start, resume-retries-FAILED, message pinning,
tombstone readability (now per-instance and non-colliding), no namespace.
Strengthened: the capability story — IDs become unforgeable (composition
knowledge alone no longer derives a live handle).

Design-doc edits (hack/designs/async-agents.md):
- §3: constructor paragraph → spawn; note Agent joins AgentMessage in the
  minted+pinned family.
- §3.1: schema block (`spawn`, `agent(id:)`, name as display).
- §3.3: "name is a display label and identity discriminator" → display
  label only; drop the fork(label:) role for agents.
- §3.5: rewrite the dedupe bullet (LLM values dedupe; agents are spawned
  instances); correct the ExitedService citation (Services free the key,
  services.go:1116–1137; agents keep per-instance tombstones addressable —
  now unproblematic since keys never collide).
- §5: attach-by-held-ID.
- §9: add ratification bullet — agent identity is minted at spawn and
  pinned by re-exec, extending the message-identity pattern; `spawn` is
  ID-returning like every imperative verb.
- §11: status update.

### Test migration

- core/integration/agent_runtime_test.go: `agentHandle` helpers construct
  via `spawn` (two-step: force the ID, rehydrate). `TestDedupe` (:432–466)
  dies BY DESIGN — replace with its inversion: two spawns of an identical
  composition are distinct agents (distinct IDs, independent runtimes,
  both concurrently RUNNING under the same display name).
- agent_injection_test.go + testdata/modules/go/agent-poker: mechanical.
- New: spawn→stop→spawn (the staff repro) — second instance works; the
  first instance's tombstone stays readable via its held ID; old message
  IDs still await idempotently.

### modules/staff simplification (after the engine pivot)

- spawn tool: `.asAgent(name: name)` → `.spawn(name: name)` (or two-step if
  Dang needs the explicit force; `@expectedType` should make it seamless).
- Delete the nonce: `chiefLine(boss:, nonce:)` → `chiefLine(boss:)`,
  remove `ChiefLine.nonce` field and the spawn comment block about
  tombstone collision. Keep ALL `@cache(Never)` annotations (independent
  concern; they are load-bearing).
- `dismiss` docstring's "the name frees up" becomes trivially true.

## Task: staff E2E integration test (unchanged in essence)

New suite in core/integration (StaffSuite / staff_test.go), reusing the
agent_runtime_test.go machinery. Plan as before:

- `cannedReplayModel` recordings via the LLM API; the replayer excludes tool
  results from history matching (agent_injection_test.go:69–72; matcher at
  core/llm_replay.go:81–100). Worker recordings must match
  `Staff.workerPrompt` byte-for-byte (it is public for exactly this).
- Serve modules/staff by copying from repo root (sibling of
  `copyTestdataFixture`, module_helpers_test.go:167), then
  `ModuleSource(dir).AsModule().Serve(ctx)`.
- De-race askChief with a slow tool AFTER the chief's spawn call so the
  worker's question deterministically STEERS into the chief's open turn
  (slow-tool + cache-volume marker: agent_runtime_test.go:190–210).
- Cheap gates first: `dagger -m /src/modules/staff functions` (parse gate),
  direct `call status` ("(none)"), direct `call spawn` (negative check:
  requires calling agent).
- Update handles/recordings for spawn semantics where they touch identity.

## Open threads

1. **Module-call cache staleness recurrence (needs root-cause).** After a
   second `editor_reload` in the QA session, identical-arg staff calls
   (`status`, `read(name, last)`) replayed stale results DESPITE the
   `@cache(Never)` annotations — which had verifiably worked right after
   the first reload (status observed RUNNING→IDLE across identical calls).
   Fresh arg-tuples always read live. Suspect the module-function cache
   policy path (core/modfunc.go:130–134 `derivedCachePolicy`) or reload/
   re-serve interplay with cached function metadata. Until root-caused,
   repeated same-arg module reads in long sessions are untrustworthy.
2. **Worker workspace isolation.** Staff workers share only the message
   channel with the chief; their workspace edits stay in their own
   composition copies. Fine for research/QA staffing; code-writing workers
   need a delegateEdits-style changeset return — possible future staff tool.
3. **Prompt leak** (chief system prompt rides into workers via workspace
   compose): known, mitigated by workerPrompt's closing paragraph; observed
   effective live. Same class as delegate's documented leak.
4. **`Staff.read` counts SYSTEM entries toward `last`** — small `last`
   values return only boilerplate. Consider filtering SYSTEM role.

## Tooling notes

- `engineLab_start` boots the from-source engine (client container mounts
  the workspace at /src); `engineLab_engineTest pkg ./core/integration run
  TestAgentRuntime` for the suites. Keyless model:
  `replay/<base64-of-messages-JSON>`.
- Multi-statement stateful agent scenarios need ONE session (the runtime
  registry is per-session): `dagger shell -c` scripts work; separate
  `engineLab_query` calls do not share agents.
- The tailcall checkout referenced above: mount read-only with the gitClone
  tool (`https://gitlab.com/sipsma1/tailcall`); orchestration design at
  design/agent-orchestration/tailcall-v2-agent-orchestration.md.
- Commit style: scoped commits, one logical change each. delegateEdits has
  leaked empty `go/pkg/mod/` dirs before (`rm go` before committing); docs
  .mdx `sidebar_position` churn from docs:references is normal.
- Delete this HANDOFF.md in the pivot's wrap-up commit.
