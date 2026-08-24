# CLI-backed LLM harnesses

Design and implementation plan for running the official Codex and Claude Code
CLIs as the execution harness behind `LLM` and `Agent`, while preserving Dagger's
workspace, message, lifecycle, and telemetry model.

## Table of contents

- [Problem and constraints](#problem-and-constraints)
- [Solution](#solution)
- [Core API](#core-api)
- [Authentication and consent](#authentication-and-consent)
- [Agent integration and control plane](#agent-integration-and-control-plane)
- [Native child agents](#native-child-agents)
- [State model](#state-model)
- [Turn lifecycle](#turn-lifecycle)
- [Message compatibility](#message-compatibility)
- [Live telemetry](#live-telemetry)
- [HTTP MCP](#http-mcp)
- [Workspace synchronization](#workspace-synchronization)
- [Implementation](#implementation)
- [Failure and cancellation](#failure-and-cancellation)
- [Security and isolation](#security-and-isolation)
- [Status](#status)

## Problem and constraints

Dagger has two related LLM abstractions:

- `LLM` is an immutable DagQL value. Its messages, workspace, tool bindings, and
  every completed evaluation step are materialized as selectors, making a
  conversation branchable, replayable, and persistable.
- `Agent` is the session-scoped, addressable runtime around an `LLM`. As
  implemented in `core/agent.go` and `core/schema/agent.go`, it owns a FIFO
  mailbox, a detached evaluation loop, lifecycle facts, message records, and a
  last-committed `LLM` snapshot. `hack/designs/async-agents.md` defines the
  contract that messages may steer an active turn and that replies follow the
  message which caused them, rather than whichever turn happens to finish next.

Dagger currently owns the provider evaluation loop: it sends history, receives a
model response, evaluates tool calls through `MCP`, commits the resulting
selectors, and repeats. The official Codex and Claude Code programs instead own
that inner model/tool loop. Supporting them as alternate backends has these
constraints:

1. **A harness is a live protocol peer, not a one-shot command.** Codex must run
   `codex app-server`, a long-lived JSON-RPC server over JSONL stdio. Claude Code
   must run persistent `claude -p --input-format stream-json --output-format
   stream-json --verbose` with stdin kept open. `codex exec --json` and repeated
   one-shot Claude prompts cannot implement `Agent.send`, same-turn steering, or
   reliable interrupt and queue reconciliation.
2. **Dagger's mailbox remains canonical.** A Dagger message is assigned an ID
   and ordered before it crosses a vendor protocol. Experimental native queue
   APIs cannot become the source of truth because lifecycle, pause, restore, and
   `AgentMessage.await` must have one consistent meaning across harnesses.
3. **Delivery must be proved.** A local observation that a turn looked active
   is not enough to label a message `STEERED`. Every Dagger `AgentMessage` has a
   stable vendor correlation entry: Codex can carry its opaque Dagger ID as
   `clientUserMessageId`, while Claude requires a separate valid UUID. Command
   acknowledgement proves only native acceptance; correlated item/message
   lifecycle proves whether input joined the active turn, remained queued, was
   cancelled, or opened a later turn.
4. **The CLI owns opaque continuation state.** Native thread/session files and
   some accepted input may exist only inside the running program. Their location
   and format can change by CLI version. Dagger may retain them but must not
   interpret them as its canonical mailbox or transcript.
5. **The workspace has two representations.** Dagger owns an immutable
   `Workspace`; native CLI tools operate on a mutable directory inside the
   harness container. The two must be synchronized at every Dagger MCP boundary
   and at every checkpoint.
6. **Runtime and checkpoint have different lifetimes.** The CLI process, open
   pipes, active native turn, in-memory native queue, HTTP listener, and mutable
   worktree are session-scoped runtime state. Portable history is an immutable
   `LLM`; high-fidelity cold continuation additionally includes an opaque
   container/native-session checkpoint.
7. **Commits are atomic.** Projected messages, workspace/tool state, native
   session files, and the cursor describing what the native session has consumed
   may not advance independently. Native execution must be quiescent or
   explicitly finalized before Dagger captures a new cold checkpoint.
8. **Vendor interruption is not rollback.** Codex records an aborted-turn marker
   and can leave partial filesystem effects or background processes. A steer
   accepted by app-server can still be only in its in-memory pending queue.
   Claude can abort a partial assistant message and reports queued UUIDs through
   its control protocol. Dagger must reconcile those facts instead of assuming
   cancellation erased work.
9. **Existing `LLM` behavior remains useful.** `LLM.withHarness` configures cold
   execution and immutable snapshots remain valid for branching, provider
   switching, trace restore, and use without a live `Agent`.
10. **Session credentials are authority, not ordinary configuration.** The session
    may already hold an Anthropic API key, Claude Code subscription token, OpenAI
    API key, or Codex subscription token for provider-backed `LLM` calls. Passing
    that credential into a caller-supplied executable container gives the entire
    harness process the ability to use or exfiltrate it. Automatic forwarding must
    therefore be narrow, explicit, auditable, bound to the evaluated harness
    identity, and fail closed when consent cannot be obtained.
11. **Native child agents are observable before they are addressable.** Codex
    multiplexes parent and child threads over one app-server connection and
    streams each child's turns, text, tools, usage, and state. A child may still
    be controlled exclusively by its parent and reject direct user input.
    Dagger must preserve that capability boundary instead of advertising every
    observed native thread as an ordinary sendable Agent.

Trying to disable every built-in native tool is unnecessary and, for Codex, not
a stable supported configuration. The container sandbox, MCP capability set,
workspace boundary, and secret policy are the controls.

## Solution

`LLM.withHarness` attaches a vendor kind and a harness container to an immutable
conversation. Spawning that value creates an ordinary Dagger `Agent`. When the
agent starts, its runtime starts one live harness process and keeps the process,
its stdio protocol, HTTP MCP endpoint, and mutable workspace open while the
agent is active.

The driver translates the Dagger Agent protocol onto the native control plane:

- Codex uses `thread/start`, `thread/resume`, or `thread/fork`; `turn/start` for a
  new turn; `turn/steer` with `expectedTurnId` for same-turn input; and
  `turn/interrupt` for preemption.
- Claude uses one persistent stream-JSON process. User frames remain writable on
  stdin while work is running, and control frames interrupt or cancel queued
  input when supported.

The Agent mailbox is drained in FIFO order by exactly one dispatcher. The
adapter never infers delivery from timing. It records command acknowledgement as
pending transport state, then uses definitive vendor lifecycle evidence through
the per-record correlation mapping to resolve `AgentMessage.delivery` and, when
the consuming native turn terminates, `AgentMessage.await`.

The live process is deliberately absent from `LLM`. At checkpoint boundaries the
runtime projects observable vendor events into ordinary `LLMMessage` selectors,
pulls the mutable workspace, persists rebound tools, snapshots opaque native
files in the harness container, and advances a history cursor in one commit. The
result is a new immutable successor which can be branched or rehydrated without
the original OS process.

Synchronous `LLM.step` and `LLM.loop` use the same machinery through a temporary
anonymous agent: start a live harness, submit the pending suffix, await the
relevant turn, commit a checkpoint, and stop the runtime. They remain blocking
API sugar. A named `Agent` keeps the harness hot and exposes the full asynchronous
control plane.

A runtime-scoped workspace bridge preserves the original design's Streamable
HTTP MCP and bidirectional synchronization. Native tools use the mounted
working directory; Dagger tools call the live `MCP` through the local HTTP
server.

## Core API

The public harness configuration remains small:

```graphql
"""The official CLI used to execute an LLM conversation."""
enum LLMHarnessKind {
  """Anthropic's Claude Code CLI."""
  CLAUDE

  """OpenAI's Codex CLI."""
  CODEX
}

type LLM {
  """
  Run future evaluation through an official CLI in the given container.

  The container's configured working directory is the mutable workspace mount.
  The supplied container is the cold seed for a new harness lineage; existing
  messages are imported when they are not represented by a valid checkpoint.
  """
  withHarness(harness: Container!, kind: LLMHarnessKind!): LLM!
}
```

There is no public `LLMHarness` object or vendor session-file path. The caller
constructs the container and therefore controls the CLI version, configuration,
and any credentials explicitly mounted on it. Dagger controls invocation and
protocol handling. The next authentication phase may offer credentials already
held by the session, but only through the consented broker below; `withHarness`
itself never silently injects them.

`withHarness` deliberately accepts a `Container`, not a `Service`. The container
is the immutable cold seed which can be chained from prior Dagger state; Core
materializes it, mounts the workspace, and derives an internal interactive
service while retaining ownership of the exact command. The kind is also the
executable contract: `CODEX` requires `codex` on `PATH`, while `CLAUDE` requires
`claude`. The caller configures the image, installation, working directory, and
ordinary container policy, but not the app-server command or protocol flags.

The existing Agent API is the live interface; no second lifecycle API is added:

```graphql
type LLM {
  spawn(name: String): ID! @expectedType(name: "Agent")
}

"""How input may be delivered to an Agent."""
enum AgentInputMode {
  """Ordinary Agent.send is supported."""
  DIRECT

  """Another Agent owns input; direct input requires an explicit override."""
  CONTROLLED

  """The Agent can be observed but the native protocol exposes no input path."""
  OBSERVE_ONLY
}

type Agent {
  """The Agent which owns this Agent's input and lifecycle, if any."""
  controlledBy: Agent

  """The currently negotiated direct-input capability."""
  inputMode: AgentInputMode!

  state: AgentState!
  snapshot: LLM!
  start: ID! @expectedType(name: "Agent")
  send(message: String!, force: Boolean = false): ID!
    @expectedType(name: "AgentMessage")
  interrupt: ID! @expectedType(name: "Agent")
  pause: ID! @expectedType(name: "Agent")
  resume: ID! @expectedType(name: "Agent")
  waitFor(state: AgentState = IDLE): ID! @expectedType(name: "Agent")
  stop(kill: Boolean = false): ID! @expectedType(name: "Agent")
  reseed(conversation: LLMID!): ID! @expectedType(name: "Agent")
  rehydrate(state: AgentState = IDLE, error: String = ""): ID!
    @expectedType(name: "Agent")
}

type AgentMessage {
  delivery: AgentMessageDelivery!
  await: String!
}
```

One internal behavior must change for a CLI harness. Today `core/agent.go`
computes delivery from local facts in `enqueue`, copies it onto the handle, and
`core/schema/agent.go` treats the field as an immutable cached value. A native
turn can finish between enqueue and steer acceptance, so that prediction can be
wrong. The message record instead has a pending delivery state and a
`deliveryReady` notification. `send` still returns immediately after durable
FIFO enqueue. Reading `delivery` waits for conclusive native evidence and is
`DoNotCache`; once recorded, the result is immutable. A record definitively
cancelled before consumption resolves `delivery` with that cancellation error
rather than fabricating `STARTED` or `STEERED`. A pause or failure is itself
conclusive local `QUEUED` evidence because the dispatcher has not written the
record to native stdin. Provider-backed agents can confirm classification
synchronously at their existing step boundary. The handle carries only agent
identity and message ID; lookup reads the permanent runtime record rather than
freezing a predicted delivery into the handle.

Message handles remain pinned by the existing `Agent.message(id:)` lookup.
Dagger IDs are minted under the runtime-entry mutex with
`internal/buildkit/identity.NewID`; they are opaque 25-character base36 strings,
not UUIDs. Each permanent record also stores a stable vendor correlation ID.
Codex may use the Dagger string directly as `clientUserMessageId`. Claude gets a
valid UUID generated once or deterministically derived from a stable namespace
and the Dagger ID. No callback resolves a record by FIFO position or attempts to
parse one identifier out of the other.

The harness container must have a non-empty working directory other than `/`.
That directory becomes the workspace mount point. Rejecting `/` prevents the
mount from hiding the CLI, its configuration, or system files. New public types
and fields are gated with `AfterVersion("v1.0.0-0")`, including the enum.

## Authentication and consent

### Current behavior

`LLM.withHarness(harness:, kind:)` is the explicit trust boundary. The caller's
selection of `CODEX` or `CLAUDE` asserts that the supplied container implements
that official CLI protocol and authorizes Core to obtain only the matching
session credential for that protocol. Core never distinguishes an installable
module with a container environment marker: such a marker is forgeable, becomes
part of the portable recipe, and does not establish executable identity.

For Codex, the process starts without credentials and Core authenticates it over
app-server after initialization. ChatGPT subscription OAuth is delivered with
`account/login/start`'s `chatgptAuthTokens` form; an OpenAI API key fallback uses
the same method's `apiKey` form. Neither credential becomes a GraphQL `Secret`,
a module argument, a container environment variable, or part of the cold
harness seed. If Codex reports that an OAuth access token was rejected, its
`account/chatgptAuthTokens/refresh` request is forwarded to the CLI-side OAuth
refresher, which rotates and persists both tokens in the client's existing LLM
configuration before returning the new access token to app-server. Future
`dagger agent` invocations therefore pick up the rotated credential.

`codex login --with-access-token` is not an equivalent ingress. Upstream
classifies `at-*` values as personal access tokens and other JWTs as Agent
Identity credentials; neither is ChatGPT subscription OAuth. The command reads
a one-shot value from stdin and establishes Codex-owned auth state, with no
callback through which Dagger can refresh and persist its managed OAuth grant.
`chatgptAuthTokens` is the published app-server host-owned external-auth
contract and is the only refresh-capable ChatGPT ingress. It is currently
capability-gated and marked experimental/internal by Codex, so the adapter
negotiates that capability and keeps the dependency isolated behind its
versioned protocol implementation.

Claude Code has the analogous official `CLAUDE_CODE_OAUTH_TOKEN` environment
ingress, but its stream-JSON protocol has no host refresh request. Today the
installable Claude module therefore retains its explicit `ANTHROPIC_API_KEY`
secret. A future Claude OAuth path can use the same normalized Core auth offer,
but must inject only into the runtime process clone before launch and define how
client-side rotation is re-resolved; copying a refresh token or native auth file
into the harness is not acceptable.

A caller that supplies its own container and selects `CODEX` receives the same
Core-side protocol handling as the installable module; the kind selection, not
module identity, is the authorization declaration. Content-bound interactive
consent can strengthen that declaration as described below, but a magic module
marker is not an interim security boundary.

### Credential selection

At harness startup Dagger derives at most one credential offer from the session
configuration and explicit harness kind:

| Harness | Session credential class | Native delivery |
| --- | --- | --- |
| `CLAUDE` | Anthropic API key | Explicit module secret today |
| `CLAUDE` | Claude Code subscription OAuth | Future runtime-only `CLAUDE_CODE_OAUTH_TOKEN` injection |
| `CODEX` | OpenAI API key | app-server `account/login/start` with `apiKey` |
| `CODEX` | Codex/ChatGPT subscription OAuth | app-server `account/login/start` with `chatgptAuthTokens` |

OAuth is preferred over the API-key fallback when both valid Codex credentials
are configured. The adapter owns the exact environment variable, temporary
config, or protocol shape because those are versioned vendor behavior. Core
passes a normalized, runtime-only credential class rather than teaching `LLM`
or module SDKs about native auth files. A kind/provider mismatch never offers
an unrelated credential: Dagger does not offer Anthropic credentials to Codex,
OpenAI credentials to Claude, or arbitrary secrets from the session.

### Harness identity and consent

A future content-bound consent layer can strengthen the explicit `withHarness`
trust declaration. Before injecting a session credential, that broker would
evaluate the cold seed and compute its content-preferred container digest.
Consent would be bound to that evaluated digest, the harness kind, the
credential source/account identity (never its plaintext), and the adapter
protocol major version. A recipe digest alone is insufficient: a call such as
`npm install @anthropic-ai/claude-code@latest` can resolve to different bytes
without changing its call shape.

The prompt shows both identities:

```text
Allow this CLI harness to receive your Claude Code subscription credential?

Harness kind: CLAUDE
Container content: sha256:…
Credential: Claude Code subscription (value hidden)

Container recipe:
  Query.container
    .from(address: "node:22-bookworm-slim")
    .withExec(args: ["npm", "install", "--global",
                     "@anthropic-ai/claude-code@latest"])
    .withWorkdir(path: "/workspace")

[once] [remember this exact harness] [deny]
```

The full ID call chain is available from the same recipe data used by the ID
TUI; the confirmation UI may initially show a compact receiver spine with an
expand action. It must not evaluate or print secret values while rendering the
prompt.

`once` records a runtime grant for the current process. `remember` stores a
client-local approval for the exact tuple above. Approval state is not part of
the `LLM`, harness container, portable recipe, trace, or checkpoint and is never
shared merely because another client can reproduce the same LLM ID. Rebuilding
or changing the container, switching harness kind, changing account/source, or
crossing an adapter protocol major version requires new consent. Credential
rotation within the same approved account/source does not.

Headless execution fails closed when no remembered grant exists. A future
non-interactive policy may pre-approve explicit content digests, but a generic
`--yes`, module annotation, or `withHarness` argument must not authorize an
unknown executable to receive session credentials. The error includes the
harness digest and a command or UI route for reviewing it.

### Injection lifecycle

After approval, the future broker would:

1. resolve the matching session credential without exposing it through GraphQL;
2. clone the evaluated cold seed into runtime-only container state;
3. inject the credential as a Dagger secret environment variable, secret mount,
   or temporary native auth file selected by the adapter;
4. start the harness and keep the grant scoped to that process tree; and
5. remove the runtime auth material during stop or session teardown.

The broker leaves the caller-supplied cold seed unchanged; broker-provided auth
exists only on its runtime clone. Credential mounts generated by the broker,
temporary auth files, environment values, approval records, and any stable hash
of the secret are excluded from rootfs snapshots, workspace snapshots, messages,
protocol logs, telemetry, and call IDs. Explicit credentials already present on
the supplied seed keep their existing Dagger secret-mount semantics, but their
plaintext is likewise never checkpointed.

Once injected, the credential is available to the harness process and its
children; no container sandbox can prevent an already-authorized executable
from sending it over its permitted network. The consent UI must state that fact.
Where vendors eventually provide scoped or short-lived token exchange, the
broker should prefer it over forwarding the session's reusable bearer token.

## Agent integration and control plane

### Runtime ownership

`AgentRuntime` remains the owner of lifecycle facts, mailbox records, the last
committed `LLM`, and the detached client scope. A harness-backed entry adds one
session-scoped driver:

```go
type liveHarness struct {
    adapter    LLMHarnessAdapter
    process    *HarnessProcess
    bridge     *MCPWorkspaceBridge
    checkpoint *LLMHarnessCheckpoint

    activeTurn string
    dispatch   chan string // Dagger AgentMessage IDs; one consumer
    events     chan LLMHarnessEvent
}
```

The OS process is not embedded in a DagQL value, container ID, portable recipe,
or trace restore record. It belongs to the same session lifecycle as the Agent
runtime and is killed by session teardown. The runtime tombstone retains only
the immutable `LLM` checkpoint and lifecycle facts. `AgentState` remains a
computed projection of those facts, never a vendor status string stored as
truth.

The dispatcher and event reader are the only writers to native stdin and the
correlation ledger. Lock ordering is: copy Agent facts under `AgentRuntime.mu`,
release it, perform protocol I/O, then reacquire it to commit evidence. No
network or pipe operation occurs while holding the Agent mutex.

### Canonical FIFO mailbox

`Agent.send` performs these steps atomically under the runtime lock:

1. mint a unique opaque Dagger message ID;
2. create the permanent message record and its stable vendor correlation ID;
3. append the Dagger ID to the canonical FIFO mailbox; and
4. wake the dispatcher.

Signal-with-start remains the current core contract: sending to a never-started
entry starts its harness, and sending to a stopped tombstone relaunches the same
Agent instance from its last checkpoint. Sending while paused or failed commits
local `QUEUED` delivery and waits for `resume`; it never writes ahead into a
native queue.

The dispatcher processes one head record at a time. It writes the record's
vendor correlation ID and records the response only as accepted/pending. The
record stays at the Dagger mailbox head until a correlated user-message/item
lifecycle event definitively proves consumption or cancellation. Consumption
finalizes delivery and moves the record into the logical turn; cancellation
resolves it without `STARTED`/`STEERED`. Only then does the dispatcher advance
to the next record. Vendor notifications may arrive concurrently, but cannot
reorder Dagger records.

Codex's experimental `thread/queue/*` facilities may be used as a transport
optimization only if every entry carries the Dagger ID and startup/reconnect
performs a strict two-way reconciliation. If the adapter cannot prove that the
native queue exactly represents a prefix of Dagger's mailbox, it cancels or
abandons the native queue and replays from Dagger's last cold checkpoint. The
native queue is never restored as authoritative state.

### Same-turn steering and race safety

If Codex reports an active regular turn, the dispatcher sends `turn/steer` with
that turn's ID as `expectedTurnId` and the Dagger message ID as
`clientUserMessageId`. A successful RPC response proves only that Core accepted
the input into its pending queue. The record remains at the mailbox head with
pending delivery until a correlated `userMessage`/item lifecycle notification,
or equivalent definitive event, proves the input was consumed by that active
turn; only that evidence finalizes `STEERED`. An expected-turn mismatch means the
observed turn ended during the race, so the same head record is retried with
`turn/start` and can finalize as `STARTED` only after its correlated consumption
event. If interrupt wins after acceptance but before consumption, reconciliation
classifies the record as unconsumed/cancelled, never `STEERED`.

For Claude, an active command permits a user stream-JSON frame whose valid UUID
comes from the record's Dagger-ID-to-UUID correlation mapping. Supported
default/next-priority input may be folded into the current turn at the next
agent-loop or tool boundary; if that boundary has already passed, it runs
subsequently. It cannot alter a model response already streaming or a tool
already executing. Correlated `command_lifecycle` evidence, not write success or
an input acknowledgement, establishes whether the mapped UUID was started in
the active command or remains queued. An initial `queued` event is provisional:
the adapter waits for `started` in the active command or for that command to end
with the mapped UUID still queued before finalizing `STEERED` or `QUEUED`.

`STARTED`, `STEERED`, and `QUEUED` continue to describe how a Dagger message
actually landed:

- `STARTED`: definitive lifecycle proves it opened and was consumed by a new
  native turn/command;
- `STEERED`: definitive lifecycle proves it was consumed by the active regular
  turn, rather than merely accepted into a pending queue;
- `QUEUED`: pause/failure prevented dispatch, or native lifecycle confirmed it
  remained queued rather than joining the active turn.

A `QUEUED` message may later be consumed, but its delivery evidence remains the
fact recorded when it landed, matching the current Agent API.

### Dagger turns and native turns

`AgentRuntime.turnOpen` remains the logical correlation unit from
`core/agent.go`; a vendor turn ID is evidence attached to it, not a replacement
for it. They are normally one-to-one, and every message consumed by that logical
turn resolves to its final reply. Interruption is the exception: native
`turn/interrupt` or Claude control interrupt ends the vendor turn, while the
Dagger logical turn remains open with already-consumed message records
unresolved. As current core requires, unconsumed Dagger mailbox records are
discarded and resolved with the interrupt error. Resume starts a new native turn
from the interrupted checkpoint and completes the same Dagger logical turn.
Any synthetic continuation input needed by a vendor is represented in projected
history rather than hidden from the portable transcript.

### Pause, resume, interrupt, and stop

Neither native protocol exposes a safe freeze-anywhere operation equivalent to
suspending Dagger between provider steps. Lifecycle verbs therefore use the
strongest honest semantics each protocol can prove:

- `pause` immediately stops new mailbox dispatch. If a model response or native
  tool is executing, it is allowed to reach a native quiescent boundary; the
  Agent continues to project `RUNNING` until then, then `PAUSED`. Pause never
  claims to have frozen an already-streaming response or executing tool.
- `resume` clears the park and continues dispatch on the same hot process. If
  the process was finalized, it is recreated from the last immutable
  checkpoint before the mailbox drains.
- `interrupt` sets the Dagger pause fact first, prevents further dispatch, then
  invokes `turn/interrupt` for Codex or Claude's control interrupt. It waits for
  terminal interrupt evidence, reconciles queued IDs, pulls side effects, and
  attempts a partial checkpoint. The native active turn is ended, not suspended;
  a later Dagger `resume` opens a new native turn from the committed interrupted
  state for messages which remain pending.
- `stop(kill: false)` stops accepting work into native execution, lets the
  active command reach a terminal boundary, commits a final checkpoint, closes
  stdin, and terminates the process tree. The Agent tombstone remains.
- `stop(kill: true)` requests native interrupt, performs bounded reconciliation
  and checkpoint finalization, then kills the process tree even if finalization
  fails. The last previously atomic checkpoint always remains readable.

Codex `turn/interrupt` completes the turn with interrupted status and persists
an aborted-turn marker. Dagger records that observable marker/partial output and
never models interrupt as erasing the turn. Claude may abort a partial assistant
message; the accumulator closes it as aborted rather than presenting it as a
normal complete reply.

Core Codex has internal suspend/recover functionality, but it is not currently
an app-server API. This design does not invoke, emulate, or depend on it.

### Await, snapshot, rehydrate, and reseed

`AgentMessage.await` resolves only when lifecycle evidence identifies the
terminal native turn which consumed that exact message. Several messages folded
into one turn resolve to the same final reply. A queued message is not resolved
by an unrelated turn. Canceling the GraphQL await only detaches that waiter; the
runtime record and native correlation remain.

`Agent.snapshot` returns `AgentRuntime.last`, exactly as it does today: the last
atomic committed conversation. A harness-backed runtime may stage a mailbox
prompt after dispatch but materializes it only once definitive lifecycle proves
which turn consumed it; until then the permanent Agent message record, not the
snapshot, carries it. Streamed deltas and mutable filesystem state are staged
with the prompt until a terminal or interrupted checkpoint. Branching the
snapshot cannot affect the live process.

`rehydrate` restores an Agent runtime from its immutable `LLM` snapshot without
starting a process. On the next `start`, `send`, or `resume`, the driver verifies
the checkpoint cursor, recreates the container, and resumes the native
thread/session when possible. Otherwise it creates a fresh native session and
imports portable `LLM.Messages`. Rehydration never restores `RUNNING`; a process
and in-memory queue died with the old session.

`reseed` keeps Agent identity and the canonical mailbox but replaces the
conversation. As current core requires, it is refused while execution is in
flight. For a harness this includes an unacknowledged command, an active native
turn, an unresolved native queue, or an unquiesced workspace. The runtime first
parks, reconciles, and finalizes or discards the old hot process. The next resume
starts from the replacement conversation's checkpoint or imports its messages.
Native session state from the old conversation is never silently paired with the
new history.

## Native child agents

Codex app-server multiplexes spawned subagents as ordinary child threads. Their
notifications use the same `turn/started`, item lifecycle, text/reasoning delta,
tool, usage, status, and `turn/completed` shapes as the primary thread, keyed by
`threadId`. Dagger therefore has enough information to stream each child as its
own conversation and project a live lifecycle state. Child notifications never
enter the parent's canonical turn accumulator: a child completion must not
complete the parent's `AgentMessage` or advance its checkpoint.

Native children are represented by proxy Agent entries owned by the parent
harness runtime, not by independent `AgentRuntime` loops or harness processes.
The proxy identity is stable within the harness lineage and incorporates the
vendor thread ID. Its conversation spans sit beneath the parent Agent loop and
carry a correlation back to the primary-thread spawn item. The proxy ends when
the native thread closes or its owning harness is released. Until the complete
native thread tree participates in the atomic checkpoint, a proxy publishes no
restorable Agent checkpoint or snapshot digest.

Lineage and control are separate facts. `ParentAgentID` continues to describe
the roster hierarchy: an ordinary Dagger worker may have a parent while still
accepting direct input. A separate `controlledBy` identity says that another
Agent owns this Agent's input and lifecycle. Telemetry carries both that
identity and `inputMode`, allowing the TUI to show an owned/read-only marker,
focus the child for observation, and disable its prompt without requiring a
reconstructable send handle.

Codex's `Thread.canAcceptDirectInput` is the primary capability signal:

- `true` maps to `DIRECT`; the proxy supports the same `send`, delivery,
  streaming response, interrupt, and await behavior as the primary Agent;
- `false` with a controlling parent maps to `CONTROLLED`; `send` refuses by
  default with an error identifying the controller; and
- absence of any usable native input operation maps to `OBSERVE_ONLY`.

`send(force: true)` is the explicit break-glass path for a `CONTROLLED` Agent.
It submits directly to the child through the native turn control plane rather
than asking the parent model to relay the message. The harness dispatcher
serializes it with parent-driven collaboration and preserves the ordinary
correlation/delivery contract. If app-server enforces the direct-input
restriction, Dagger returns that refusal; it never reports the message as
queued or consumed. `force` cannot override `OBSERVE_ONLY`, bypass the outer
container boundary, or detach the child from its owner.

For Codex versions where controlled child input has not been proven safe, the
adapter reports `OBSERVE_ONLY` even when a lower-level request appears
available. Compatibility fixtures must verify direct start, active-turn steer,
interrupt, parent `sendInput`, parent close, and their races before enabling
`CONTROLLED` break-glass input for that version.

## State model

The design separates **hot runtime** from **immutable checkpoint**.

Hot, session-scoped state includes:

- the `codex app-server` or persistent Claude process and process tree;
- open JSONL/stream-JSON stdin and stdout;
- the current native thread/session and active turn/command IDs;
- accepted input still held only in a native in-memory pending queue;
- Dagger's canonical mailbox and unresolved `AgentMessage` records;
- a hot correlation ledger mapping each opaque Dagger ID to its Codex string or
  generated/derived Claude UUID, plus acknowledgement and lifecycle evidence;
- the HTTP MCP listener and bearer token;
- the live `*MCP`, workspace bridge, and mutable working directory; and
- staged events and filesystem changes not yet checkpointed.

Cold state is an immutable `LLM` containing portable messages, workspace, and
tools plus an opaque native checkpoint:

```go
type LLMHarnessKind string

const (
    LLMHarnessClaude LLMHarnessKind = "CLAUDE"
    LLMHarnessCodex  LLMHarnessKind = "CODEX"
)

type LLMHarnessMessageCorrelation struct {
    DaggerMessageID string
    VendorMessageID string // Codex string or valid Claude UUID
}

type LLMHarnessCheckpoint struct {
    Harness       dagql.ObjectResult[*Container]
    Kind          LLMHarnessKind
    MessageCount  int
    HistoryDigest digest.Digest
    NativeSession string // opaque adapter metadata, not a public path
    Protocol      string // adapter/version compatibility discriminator
    Correlations  []LLMHarnessMessageCorrelation
}
```

The hot ledger is authoritative while a process lives. Any mapping referenced by
opaque native checkpoint state, or needed to reconcile after recreating that
process, is included in `Correlations` in the same atomic checkpoint; a generated
Claude UUID is never regenerated differently after restart. Deterministic UUID
derivation may reduce stored data but does not remove the explicit mapping or
its validation.

The checkpoint's container contains opaque native session files and the
program/configuration needed to resume. The separately mounted workspace is not
part of its root filesystem; its immutable snapshot is recorded through
`LLM.withWorkspace`. Credentials remain secret mounts or secret environment
variables and never enter the checkpoint.

`LLM` gains harness configuration and an optional checkpoint. `withHarness`
stores the supplied cold container and kind, clears any previous cursor, and
does not claim the harness has seen existing messages. A valid cursor proves
that the first `MessageCount` projected messages hash to `HistoryDigest`. A
missing, incompatible, or invalid cursor triggers a portable history import.

The cursor deliberately permits a suffix:

```text
[ messages represented by cold native state ][ Dagger-only pending suffix ]
```

This suffix supports immutable API composition: a conversation may acquire
`withPrompt`, `withResponse`, or tool-result messages after its last native
checkpoint and import them on the next cold start. Hot Agent mailbox records are
not smuggled into that suffix before consumption; they remain canonical runtime
records. Likewise, input accepted only into Codex's in-memory pending queue is
not moved into either the committed message history or cold prefix until
notifications prove which turn consumed it and native execution reaches a
checkpoint boundary.

### Atomic checkpoint transaction

A checkpoint commit has four staged parts:

1. projected assistant/tool/lifecycle messages;
2. the final synchronized Workspace and rebound object tools;
3. an opaque snapshot of native session files/container rootfs; and
4. a cursor and correlation ledger describing exactly the native prefix.

Dagger materializes all selectors into one successor `LLM` and only then swaps
`AgentRuntime.last`. Failure preserves the previous committed snapshot. Runtime
telemetry may show staged progress, but trace snapshot records continue to point
at the last atomic commit.

The current implementation records the cursor and native metadata through a
cached internal `LLM.__withHarnessCheckpoint` selector. This is important even
before opaque rootfs capture is complete: the selector makes the committed
snapshot an attached DagQL result with an addressable ID, so `Agent.snapshot`,
cache reload, and subsequent selector chaining all operate on the same immutable
call chain. Correlations use a digested serialized argument to keep successive
checkpoint IDs compact. The portable recipe still reconstructs from the harness
seed and projected messages rather than persisting this transient session-native
checkpoint.

Before capturing native files, the runner must reach one of these conditions:

- the native regular turn/command emitted a terminal lifecycle event and the
  control process is idle;
- interrupt completed and queue reconciliation finished; or
- the process was explicitly finalized and its process tree stopped.

The runner then blocks new stdin, serializes the final workspace pull, and uses
an engine-level process-tree freeze or finalized process to snapshot the
writable rootfs and mount generations consistently. This is container execution
plumbing, not Codex suspend/recover. If background processes cannot be quiesced,
checkpointing fails or they are terminated according to stop policy; Dagger
must not snapshot a directory while known writers continue.

Immutable branches clone the checkpoint. Codex `thread/fork` is used when a
native branch is created from a resumed thread, so two Dagger LLM branches never
write one thread. Claude branches clone the opaque container/session state when
supported; otherwise they start a fresh session by importing portable history.

## Turn lifecycle

### Common lifecycle

A hot Agent performs the following sequence:

1. Verify the checkpoint cursor against `LLM.Messages`.
2. Resolve `MCP.workspace`, create the mutable working copy, and start the
   runtime-scoped workspace bridge and local Streamable HTTP MCP endpoint.
3. Start the native control process with stdin kept open.
4. Resume/fork native state when the checkpoint is valid, otherwise create a
   native session and import the portable history.
5. Drain Dagger mailbox records in FIFO order using the record's stable vendor
   correlation ID; keep each record at the head through command acceptance until
   definitive lifecycle proves consumption or cancellation.
6. Translate typed native notifications into live display events and staged
   `LLMMessage` values while native and MCP tools execute.
7. At each Dagger MCP call, pull native workspace edits, execute the tool, and
   push Dagger workspace edits back before replying.
8. On native turn termination, reconcile every correlated message and resolve
   awaits for messages consumed by that turn.
9. Quiesce, perform a final workspace pull, snapshot opaque native state, and
   atomically materialize the successor `LLM`.
10. Keep the process idle and live for the next `Agent.send` unless pause, stop,
    failure, or session teardown requires finalization.

There is no opaque one-shot “harness step” during which Agent input is
impossible. The useful unit is a native regular turn inside a longer-lived
control session. Dagger may commit only at safe boundaries, but it can enqueue,
steer, observe, interrupt, and reconcile while that turn is active.

### Codex app-server

The Codex adapter launches `codex app-server` and speaks JSON-RPC over JSONL
stdio for the life of the Agent runtime. It consumes typed turn, item, and delta
notifications rather than treating stdout as a final answer.

Startup chooses one thread operation:

- `thread/start` for a fresh lineage or portable import;
- `thread/resume` for a valid native checkpoint; or
- `thread/fork` before diverging from an existing checkpoint.

An idle message uses `turn/start`. While the same regular turn is active,
additional input uses `turn/steer` with `expectedTurnId`. In both cases the input
carries `clientUserMessageId = AgentMessage.MessageID`. A successful response
records the target turn and pending native acceptance only. Correlated
`userMessage`/item lifecycle, or an equivalent definitive consumption event,
finalizes `STARTED` or `STEERED` and moves the record out of Dagger's mailbox. A
stale expected ID leaves the head record untouched and retries it through
`turn/start`; interruption before consumption reconciles an accepted record as
unconsumed/cancelled.

`turn/interrupt` terminates the active turn with interrupted status. Its aborted
turn remains in native history. Completed item notifications, partial deltas,
workspace changes, and spawned background processes are reconciled before the
next checkpoint. An accepted steer is neither delivered nor cold-durable merely
because the RPC returned: until its correlated lifecycle proves consumption, it
may exist only in the app-server process's pending queue.

Experimental thread queue APIs do not change this model. They are optional
adapter internals subject to exact client-ID correlation and reconciliation.
The design does not depend on core suspend/recover because app-server does not
currently expose that interface.

### Claude Code stream-JSON

The Claude adapter launches exactly one persistent process per hot runtime:

```text
claude -p --input-format stream-json --output-format stream-json --verbose
```

Its stdin remains open. User frames may be written while Claude is running.
Each carries the valid UUID stored in the Dagger message record's vendor
correlation entry; the opaque 25-character Dagger ID is not a UUID and is not
placed in that field. Default/next-priority messages can be folded into the
current turn at the next agent-loop or tool boundary and otherwise execute
subsequently. They do not rewrite an assistant response already streaming and do
not preempt a tool already executing.

The adapter consumes `command_lifecycle` events (`queued`, `started`,
`completed`, `cancelled`, `discarded`, and `refused`) to correlate disposition
and completion. A successful stdin write alone is never delivery evidence.

The control protocol supports interrupt. When the negotiated
`interrupt_receipt_v1` capability is present, each receipt `still_queued` UUID is
resolved through the correlation ledger and the resulting Dagger record set is
compared exactly with Dagger's queue. With `interrupt_cancel_queued_v1`, Dagger
requests `cancel_queued`; individual queued input can be removed with
`cancel_async_message` by its mapped UUID. An unknown or duplicate UUID is a
correlation failure, and any disagreement keeps the Dagger record unresolved and
forces explicit recovery rather than guessing.
Partial assistant messages reported as aborted are preserved as partial output
but do not resolve an await as a successful completed reply.

If an older Claude CLI lacks a needed capability, the adapter may interrupt and
restart from the last checkpoint, but it cannot claim precise queue cancellation.
The unsupported operation fails loudly when correctness would otherwise depend
on that claim.

## Message compatibility

### Input and cold import

`LLM.Messages` remains the canonical portable projection. On native resume only
the suffix after a valid cursor is sent. On cold import the full history is
serialized into a fresh native session.

The suffix or imported history may contain:

- system messages;
- user prompts;
- synthetic assistant responses from `withResponse`;
- tool calls; and
- tool results from `withToolResult`.

Official CLI input protocols generally accept user input, not arbitrary
provider-level assistant/tool history. The common serializer therefore emits a
role-delimited continuation envelope, using native system-instruction controls
where available while preserving order in the envelope. This import is
necessarily lossy: provider-private continuation tokens, unexposed reasoning,
and signed thinking blocks cannot be recreated. Once imported, later turns use
native continuation and opaque state.

Agent mailbox messages are simpler: each is one native user input carrying the
record's vendor correlation ID (the Dagger string for Codex, a mapped UUID for
Claude). Dispatch stages the corresponding `withPrompt`; definitive native
lifecycle must prove that a turn consumed the correlated record before the
prompt is materialized in the atomic checkpoint. This is the harness form of
async-agents' influence-implies-append rule: queued or cancelled input is not
transcript history, while every message which influenced native execution is
recorded. The cursor and any required correlation mapping advance with the same
commit.

### Output

Codex typed notifications and Claude stream-JSON are decoded incrementally into
a common vocabulary:

```go
type LLMHarnessEvent interface { isLLMHarnessEvent() }

type LLMHarnessTurn struct {
    NativeTurnID string
    State        string
}

type LLMHarnessMessageLifecycle struct {
    DaggerMessageID string // resolved through the correlation ledger
    VendorMessageID string // Codex clientUserMessageId or Claude UUID
    NativeTurn      string
    State           string
}

type LLMHarnessTextDelta struct {
    Block int64
    Delta string
}

type LLMHarnessThinkingDelta struct {
    Block     int64
    Delta     string
    Signature string
}

type LLMHarnessToolCall struct {
    Block     int64
    CallID    string
    Name      string
    Arguments JSON
    Source    LLMHarnessToolSource
}

type LLMHarnessToolResult struct {
    CallID string
    Text   string
    Error  bool
}

type LLMHarnessUsage struct { Usage LLMTokenUsage }
type LLMHarnessCompleted struct { NativeTurnID string }
type LLMHarnessInterrupted struct { NativeTurnID string }
```

An ordered accumulator projects:

- assistant text to `LLMContentText`;
- observable reasoning to `LLMContentThinking`;
- native and MCP calls to `LLMContentToolCall`;
- results to user-role `LLMContentToolResult` messages; and
- reported usage to the corresponding assistant response, or the final response
  when the CLI reports only turn totals.

Observable intermediate calls and results remain in history. This keeps
`messages`, `transcript`, `lastReply`, replay, provider switching, and persisted
LLM IDs as close as possible to provider-backed evaluation.

Dagger MCP calls may appear in both the vendor stream and HTTP server. The HTTP
server is authoritative for execution and result. Correlation uses native call
IDs and request metadata; ordered name/arguments matching is only a presentation
fallback and never message-delivery evidence. Native tool calls come from the
vendor stream.

Materialization uses shared response/tool-result selector helpers, followed by
workspace, rebound-tool, and harness-checkpoint selectors. A malformed or
unmatched lifecycle event prevents checkpoint advancement.

## Live telemetry

Harness output must render while the process and native turn are running. The
runner exposes persistent bidirectional pipes; raw protocol records are verbose
logs under an encapsulated `Codex app-server` or `Claude Code` span, while parsed
events must drive the existing `displayPhases` implementation.

The harness seed is now materialized and converted through the ordinary
`Container.asService` selector with user-facing telemetry. Interactive service
startup preserves that selector's origin spans, so the TUI and trace show the
actual `codex app-server` or Claude service, its stdout/stderr logs, lifetime,
and exit status. This closes the earlier visibility gap where the process could
exit while only an internal container clone existed in the trace. Incremental
projection of every parsed native event into the richer LLM display remains in
progress.

The runtime now projects submitted prompts, streamed text and thinking, native
and MCP tool calls, and tool results through the existing `displayPhases`
implementation. It emits the same `LLMRole`, `LLMTool`, message, call-digest,
and Agent identity attributes as provider-backed evaluation, so those spans
participate in the existing reveal-independent conversation report and live
conversation promotion; no harness-specific TUI section is needed. Remaining
telemetry work is tracked exhaustively in the status checklist below.

```go
switch event := event.(type) {
case LLMHarnessTextDelta:
    phase := displays.StartText(event.Block)
    fmt.Fprint(phase.MarkdownW, event.Delta)
case LLMHarnessThinkingDelta:
    phase := displays.StartThinking(event.Block)
    fmt.Fprint(phase.Stdio.Stdout, event.Delta)
case LLMHarnessToolCall:
    displays.EmitToolCall(
        event.Block,
        event.CallID,
        event.Name,
        string(event.Arguments),
    )
}
```

This reuses response, thinking, and tool-call span names and the LLM call digest
used for TUI branching. Token events update existing LLM gauges. Turn and
message lifecycle events add correlation attributes for Dagger message ID,
native turn/command ID, delivery, and terminal status without exposing prompt
content.

Dagger MCP execution is parented beneath its tool-call span and includes bridge
pull, evaluation, and push. The existing per-exec OTLP setup remains active for
native subprocess telemetry. Structured CLI protocol JSON is intercepted before
ordinary stdio rendering so it is not dumped into user-facing output.

An MCP tool result is a content envelope, not itself display text. The live
tool-call span renders text blocks directly and uses bounded semantic summaries
for image, audio, and resource blocks. `structuredContent`, `_meta`, and the raw
vendor envelope remain available in the encapsulated app-server protocol logs,
but are not serialized into the user-facing stdout log. Error payloads render
only their message while retaining the tool span's error status.

Agent state and snapshot telemetry retain current semantics. `RUNNING` reflects
real native activity; `PAUSED` is emitted only after the process reaches the
park; `IDLE` follows terminal reconciliation and an atomic checkpoint. Snapshot
records identify the last committed LLM, not an accumulator containing partial
uncheckpointed deltas.

## HTTP MCP

A hot harness receives one generated Streamable HTTP MCP endpoint containing all
tools from the live `MCP.Tools` set: bound object tools, skills, builtins, and
external MCP proxies.

That sentence is the target contract, not current behavior. Core currently
registers the authenticated Streamable HTTP handler and makes a container-local
forwarding URL available in `LLMHarnessStart`, but neither adapter configures its
native CLI to use that URL/token. The harness startup path also constructs the
server directly from `llm.mcp` without applying the implicit workspace-module
binding performed by `LLM.MCP`. Consequently, the native model currently sees
none of the workspace's Dagger tools. Both tool resolution and vendor MCP
configuration must be completed before the endpoint is usable.

The existing `mark3labs/mcp-go` dependency provides the handler:

```go
handler := mcpserver.NewStreamableHTTPServer(
    server,
    mcpserver.WithStateful(true),
)
```

The listener is created on `127.0.0.1:0` inside the harness execution network
namespace, following the internal OTLP listener in
`engine/engineutil/executor_spec.go`. The handler runs in the engine and owns the
live `*MCP`; no nested `dagger mcp` process or LLM ID is required.

Core registers a runtime-scoped handler and passes an opaque token through
execution metadata. Engine execution creates the container-local listener and
forwards requests to the registered handler. Logical turn spans and correlation
change while the endpoint remains stable for the long-lived process.

```go
type ExecHTTPHandlerRegistry interface {
    Register(http.Handler) (token string, unregister func())
    ServeHTTP(token string, http.ResponseWriter, *http.Request)
}
```

The adapter will write the URL and random bearer token into temporary CLI
configuration. User/project MCP configuration is ignored where a strict-config
option exists, preventing accidental access to servers outside the Dagger tool
environment. Native CLI tools are not disabled.

Tool generation and dispatch become transport-neutral:

```go
type LLMToolServer struct {
    env    *MCP
    bridge *MCPWorkspaceBridge
}

func (s *LLMToolServer) StreamableHTTPHandler() http.Handler
func (s *LLMToolServer) ServeStdio(context.Context, io.ReadWriteCloser) error
```

`dagger mcp` continues to use stdio without a workspace bridge. CLI harnesses
use HTTP with the bridge.

## Workspace synchronization

The harness has a mutable working copy while `MCP.workspace` is the immutable
source recorded on a committed `LLM`. The boundary invariant is:

> Immediately before and after every Dagger MCP tool call, the harness
> filesystem and `MCP.workspace` represent the same logical tree.

A runtime-scoped bridge owns the mounted directory and serializes tool
boundaries:

```go
type MCPWorkspaceBridge struct {
    mcp     *MCP
    service *Service
    running *RunningService
    target  string
    synced  dagql.ObjectResult[*Directory]
    mu      sync.Mutex
}
```

Before a Dagger tool call, `pullLocked` snapshots the mutable directory, diffs it
against `synced`, applies the `Changeset` to `MCP.workspace`, and advances
`synced`. The tool observes every native edit completed before the call.

After the call, including a failed call, `pushLocked` resolves the current
workspace, detects changes, remounts a mutable copy, and advances `synced`. This
covers tools returning `Changeset` or `Workspace`, existing
`applyStateReturn` behavior, and future indirect workspace mutations.

```go
func (b *MCPWorkspaceBridge) Call(
    ctx context.Context,
    tool LLMTool,
    args any,
) (any, error) {
    b.mu.Lock()
    defer b.mu.Unlock()

    if err := b.pullLocked(ctx); err != nil {
        return nil, err
    }
    result, callErr := tool.Call(ctx, args)
    pushErr := b.pushLocked(ctx)
    return result, errors.Join(callErr, pushErr)
}
```

A final serialized pull occurs at every checkpoint because the native turn may
end with an edit and no later MCP call. The first implementation serializes all
MCP calls; even reads need a preceding pull.

The bridge generalizes `Service.runAndSnapshotChanges` and
`MCP.applyWorkspaceSnapshot`. Remounting while the foreground CLI waits for an
MCP response is a safe boundary. Background writers are different: Codex
interrupt may leave them running, and either CLI may launch a daemon. The runner
tracks the process tree, detects generation changes across finalization, and
must freeze or terminate writers before a checkpoint. A detected race is an
error, never last-writer-wins.

Only checkpoint commit advances the immutable `LLM.workspace`. Until then the
bridge's `MCP.workspace` and mutable directory are hot runtime state. If
finalization fails, the previous snapshot remains authoritative and diagnostics
identify that partial native side effects may remain in the live runtime.

## Implementation

### Harness adapters

Vendor behavior is isolated behind a stateful adapter rather than a one-shot
command builder:

```go
type LLMHarnessAdapter interface {
    Start(context.Context, LLMHarnessStart) (LLMHarnessSession, error)
    StartTurn(context.Context, LLMHarnessInput) error
    Steer(context.Context, nativeTurnID string, LLMHarnessInput) error
    Interrupt(context.Context, nativeTurnID string, cancelQueued bool) error
    CancelQueued(context.Context, messageID string) error
    Events() <-chan LLMHarnessEvent
    Quiesce(context.Context) (LLMHarnessNativeState, error)
    Close(context.Context) error
}

type LLMHarnessInput struct {
    DaggerMessageID string
    VendorMessageID string // Codex string or valid Claude UUID
    Content         []*LLMMessage
}

type LLMHarnessStart struct {
    History    []*LLMMessage
    Checkpoint *LLMHarnessCheckpoint
    Model      string
    MaxTokens  int
    MCPURL     string
    MCPToken   string
    CallDigest string
}
```

Codex implements this with app-server JSON-RPC request IDs distinct from its
`clientUserMessageId`, which may be the Dagger ID. Claude implements it with
persistent stream-JSON user/control frames and a valid UUID allocated through
the correlation ledger. Exact vendor schemas are versioned adapter code and
fixture-tested because they can change independently of Dagger.

### LLM and Agent evaluation

`LLMClient` remains the provider API interface. A live harness does not implement
`LLMClient`; it is owned by `AgentRuntime`. The Agent loop selects a provider or
harness driver from the committed `LLM`.

Provider-backed execution keeps the current `Step` loop. Harness-backed
execution replaces the single blocking `Step` call with driver operations which
can accept mailbox wakeups and lifecycle events concurrently. The outer Agent
state machine still owns `turnOpen`, consumed records, pause/stop facts, snapshot
commit, and await resolution.

Synchronous `LLM.Step`/`Loop` use an internal temporary Agent runtime rather than
calling a one-shot adapter. This preserves one implementation of native message
correlation and checkpointing. `maxSteps` counts completed native regular turns,
not private model/tool iterations inside a CLI. `maxTokens` is forwarded only
where the selected CLI exposes an equivalent option.

Existing before/after workspace and bound-tool persistence is factored for both
provider and harness commits:

```go
type llmMutableStateSnapshot struct {
    Workspace *call.ID
    Tools     []boundToolBinding
}

func (llm *LLM) snapshotMutableState() (llmMutableStateSnapshot, error)
func (llm *LLM) mutableStateSelectors(
    before llmMutableStateSnapshot,
) ([]dagql.Selector, error)
```

Message materialization similarly accepts a sequence of assistant and tool
result messages. The final Select includes the harness checkpoint selector in
the same transaction.

### Container execution

The runner needs an internal container operation with these properties:

1. start a process with persistent writable stdin and streaming stdout/stderr;
2. expose a runtime-scoped HTTP handler inside the exec network namespace;
3. retain the mutable workspace mount across native turns;
4. signal and wait for the full process tree;
5. freeze or finalize execution at a verified protocol boundary; and
6. return consistent rootfs and workspace mount snapshots without including
   secrets.

This is engine plumbing, not public `Container` API. A normal `withExec` result
is insufficient because it returns only after process exit and cannot carry
same-turn input. Mounted workspace writes stay outside the rootfs checkpoint;
opaque CLI session files elsewhere become part of the returned harness
container.

The implemented runner materializes the cold seed, attaches the terminal clone
to the session cache, selects `asService(args:)`, and starts a unique interactive
service from that attached result. Persistent stdin is bridged through an OS
pipe so process exit is observable even when the protocol input source remains
open; service wait no longer hangs on `os/exec`'s stdin-copy goroutine. Rootfs
and mounted-workspace capture at a verified quiescent boundary remain the next
container-lifecycle step.

For Codex, Dagger's harness container is the execution security boundary.
`turn/start` selects app-server's `externalSandbox` policy with network access
delegated to the container and disables interactive approvals. This avoids a
second bubblewrap namespace, which the normal nested service environment cannot
create, while preserving the container's filesystem, network, mount, and secret
policy as the authority boundary.

The runner separates protocol process stdout from subprocess/user stdout. It
bounds JSON records, rejects malformed framing, and applies backpressure without
blocking lifecycle/control messages behind verbose tool output.

## Failure and cancellation

### Protocol and process failure

Malformed JSON, unknown mandatory lifecycle states, correlation conflicts,
unexpected EOF, failed workspace synchronization, or inability to snapshot the
native state prevents cursor advancement. The driver transitions the Agent to
`FAILED` with the previous atomic snapshot. Pre-existing immutable history after
the cursor remains a valid import suffix, but staged Agent mailbox prompts and
partial native output are not published unless bounded finalization can commit
them with matching workspace and native state.

Recovery starts from the last cold checkpoint and Dagger's canonical mailbox.
A message proved consumed by the committed native prefix is not replayed. A
message merely written to stdin or accepted into an in-memory queue is replayed
only from a checkpoint which predates that acceptance. If Dagger cannot prove
which side of the checkpoint a message occupies, it fails rather than guessing.

This gives transcript and checkpoint consistency, not exactly-once external side
effects. An interrupted native tool may have contacted a remote service or left
a process running before Dagger observed completion. The trace and error surface
that ambiguity; retry policy belongs to the tool/caller.

### Interrupt and queue reconciliation

Codex interruption waits for the interrupted terminal turn event, records its
aborted marker, and queries/reconciles any available queue state. A steer which
was acknowledged but lacks definitive correlated consumption remains at the
Dagger mailbox head and is reconciled as unconsumed/cancelled; it never acquires
`STEERED` delivery from the acknowledgement. Partial workspace effects are
pulled only after remaining writers are quiesced.

Claude interruption uses the control protocol. With
`interrupt_receipt_v1`, every `still_queued` UUID must reverse-map to exactly one
known Dagger record and the resulting record set must match Dagger's queue. With
`interrupt_cancel_queued_v1`, `cancel_queued` requests cancellation and lifecycle
must report `cancelled`/`discarded`; `cancel_async_message` targets the record's
mapped UUID. A `refused` command resolves that mapped Dagger message with an
error. Missing, duplicate, or extra UUID mappings are a reconciliation failure.

Unconsumed mailbox records discarded by Dagger `interrupt` are cancelled in the
native queue by correlated ID before their Agent records resolve with the
interrupt error. If native cancellation cannot be confirmed, the hot process is
abandoned and recovery uses the prior checkpoint where those records were not
present.

### Pause and stop

Pause is cooperative and does not abort work. If native work does not reach a
boundary, pause remains `RUNNING`; callers can choose `interrupt` for preemption.
Messages sent after the pause request stay in Dagger's mailbox with `QUEUED`
delivery and are not written to native stdin.

Graceful stop finishes and atomically checkpoints the active turn before process
release. Kill stop makes a bounded best effort to interrupt, close partial
content, reconcile queues, pull the workspace, and checkpoint. If that complete
transaction fails, it returns the previous LLM snapshot and a stop/finalization
error; it never returns a cursor paired with mismatched messages or filesystem
state.

Canceling `AgentMessage.await`, `waitFor`, or another GraphQL request does not
cancel the detached Agent. Explicit `interrupt` and `stop` are the lifecycle
controls. Session teardown invokes kill-stop semantics for every surviving
runtime and releases listeners, secret mounts, bridges, and process trees.

Tool execution errors remain ordinary tool-result messages. Workspace push runs
on tool failure because the tool may have mutated state before returning the
error.

## Security and isolation

- The harness runs under the container's normal network, filesystem, secret, and
  service policy. Native tools gain no authority beyond that container.
- The MCP endpoint listens on loopback inside the harness network namespace and
  uses a random runtime-scoped bearer token.
- Generated MCP configuration exposes only the Dagger server. Strict MCP config
  is enabled when supported by the vendor CLI.
- Session LLM credentials are never forwarded implicitly. The credential broker
  requires an exact evaluated harness-content grant and offers only the
  credential class matching the harness kind and resolved provider.
- Remembered grants are client-local capability policy, not portable LLM state;
  an unrecognized harness in headless execution fails closed.
- Credentials are Dagger secrets and are never copied into opaque container
  snapshots, protocol logs, message-correlation telemetry, or call IDs.
- The mutable workspace mount contains only the bound workspace directory, not
  the engine filesystem or caller host checkout.
- Host writes still require explicit `Workspace.export`; bridge capture advances
  only the in-memory workspace overlay.
- Protocol input is bounded and decoded as data. Vendor event fields never
  become DagQL selectors without validation.

## Status

This is the exhaustive implementation checklist for this design. Every item is
atomic: `[x]` is implemented at the current milestone and `[ ]` remains. There
are deliberately no partially checked items; incomplete work is split into its
completed foundation and remaining behavior.

### Public API and immutable state

- [x] Add the version-gated `LLMHarnessKind` enum and
  `LLM.withHarness(harness:, kind:)` API.
- [x] Keep the public input as a cold `Container` seed while Core owns the exact
  `codex app-server` or persistent Claude invocation.
- [x] Validate the harness kind and require a non-root, non-empty working
  directory for the workspace mount.
- [x] Store harness kind, container, native session, protocol, history cursor,
  digest, and message correlations in the immutable LLM checkpoint model.
- [x] Validate checkpoint prefixes while allowing a Dagger-only message suffix.
- [x] Materialize checkpoints through an attached, cache-addressable internal
  selector so snapshots have IDs and remain chainable across turns.
- [x] Route harness-backed `Agent` evaluation through the live runtime.
- [x] Route synchronous `LLM.step` and `LLM.loop` through a temporary live
  harness runtime.
- [ ] Add version-gated `Agent.inputMode`, `Agent.controlledBy`, and explicit
  break-glass input to the public Agent API.
- [ ] Import arbitrary portable `LLM.Messages`, including synthetic assistant
  and tool-result history, when no valid native checkpoint exists.
- [ ] Send only the suffix after a valid cursor when native state resumes.
- [ ] Fork native state before evaluating two immutable branches from one
  checkpoint.
- [ ] Include messages, workspace, rebound tools, native rootfs, cursor, and
  correlations in one atomic checkpoint transaction.

### Persistent process and service lifecycle

- [x] Run one persistent `codex app-server` JSONL process per hot Codex runtime.
- [x] Run one persistent Claude stream-JSON process with stdin kept open.
- [x] Materialize the caller's container and derive the harness with the normal
  internal `Container.asService` selector.
- [x] Preserve the service selector's origin, command, stdout/stderr, lifetime,
  exit status, and startup failure in the TUI and trace.
- [x] Provide separate persistent protocol stdout and diagnostic stderr pipes.
- [x] Apply bounded JSONL framing, malformed-record rejection, and output
  backpressure.
- [x] Signal, stop, wait for, and release the live service without blocking on
  the persistent stdin-copy goroutine.
- [x] Propagate natural process exit and its real error through the adapter
  instead of reducing it to a generic EOF or hanging the turn.
- [x] Run Codex with `approvalPolicy: never` and app-server
  `externalSandbox`, delegating filesystem and network enforcement to the
  Dagger harness container.
- [ ] Track and quiesce the entire native process tree at checkpoint boundaries.
- [ ] Freeze or terminate background writers before capturing workspace or
  rootfs state.
- [ ] Capture a mutually consistent writable rootfs and mounted-workspace
  generation without including secret mounts.

### Canonical mailbox, lifecycle, and commits

- [x] Mint one opaque Dagger message ID and one stable vendor correlation ID per
  permanent Agent message record.
- [x] Use the Dagger ID directly for Codex `clientUserMessageId` and a distinct,
  persisted UUID for Claude.
- [x] Serialize all native submission through one FIFO dispatcher.
- [x] Keep an accepted record at the mailbox head until correlated native
  lifecycle proves consumption or cancellation.
- [x] Make `AgentMessage.delivery` wait for definitive vendor evidence instead
  of freezing an enqueue-time prediction.
- [x] Ignore Codex's paired started/completed notification only after the same
  message has already been correlated to the same native turn.
- [x] Retry a stale Codex `turn/steer` through `turn/start` without changing the
  FIFO head or preserving the stale `STEERED` classification.
- [x] Accumulate typed text, thinking, tool-call, tool-result, usage, and terminal
  events into ordinary LLM messages at turn completion.
- [x] Commit message correlations, native metadata, history digest, and cursor
  together in an attached checkpoint selector.
- [x] Keep one adapter hot across consecutive turns and resolve every consumed
  message against its terminal native turn.
- [ ] Finish exact pause semantics: stop new dispatch immediately, remain
  `RUNNING` until native quiescence, then publish `PAUSED`.
- [ ] Finish graceful-stop and kill-stop reconciliation under races with send,
  lifecycle notification, and process exit.
- [ ] Reconcile every native queued input by correlation ID before resolving an
  interrupt or abandoning a process.
- [ ] Preserve interrupted/aborted partial assistant content without presenting
  it as a successful reply.
- [ ] Surface native terminal failure details, including the failed turn and
  affected message IDs, through `AgentMessage.await` and Agent state.

### Codex adapter

- [x] Implement JSON-RPC request correlation independently of Agent message IDs.
- [x] Implement app-server initialization and capability negotiation.
- [x] Implement `thread/start` and valid-checkpoint `thread/resume`.
- [x] Recover a missing Codex rollout by resuming from the exact portable-history
  prefix represented by the checkpoint, preserving system prompts as developer
  instructions and never duplicating an uncommitted suffix.
- [x] Implement `turn/start`, expected-turn `turn/steer`, and
  `turn/interrupt`.
- [x] Decode turn, user-message, text, thinking, native-command, MCP-call,
  result, usage, and terminal notifications into the common event vocabulary.
- [x] Scope turn, item, delta, and usage notifications to the adapter's primary
  thread so spawned Codex agents cannot replace or terminate Dagger's canonical
  turn.
- [x] Decode primary-thread `subAgentActivity` and `collabAgentToolCall` items as
  native tool calls/results while leaving child-thread output owned by Codex.
- [ ] Track Codex child threads as native Agent proxies and route their turn,
  item, delta, tool, usage, and status notifications into per-child streams
  without affecting the primary turn accumulator.
- [ ] Negotiate `canAcceptDirectInput` for each child and compatibility-test
  direct start, active-turn steer, interrupt, parent input, and close before
  enabling controlled break-glass input.
- [x] Normalize `openai-codex/<model>` to the unqualified app-server model name.
- [x] Authenticate API keys through `account/login/start` without placing them
  in the harness recipe.
- [x] Authenticate ChatGPT subscription OAuth through
  `chatgptAuthTokens` external auth without persisting tokens in the harness.
- [x] Handle app-server token refresh requests through the CLI-side refresher
  and persist rotated client credentials.
- [x] Disable Codex's nested bubblewrap sandbox through the app-server external
  sandbox policy while retaining the outer Dagger container boundary.
- [ ] Implement `thread/fork` before diverging from a shared native checkpoint.
- [ ] Implement optional native queue inspection/cancellation with exact
  two-way reconciliation against Dagger's mailbox.
- [ ] Add compatibility fixtures for every supported app-server protocol
  version and fail clearly when required lifecycle/auth features are absent.

### Claude adapter

- [x] Implement persistent bidirectional stream-JSON input and output.
- [x] Persist and validate the Dagger-ID-to-UUID correlation ledger.
- [x] Decode `command_lifecycle`, streaming content, usage, completion, and
  interruption into the common event vocabulary.
- [x] Implement interrupt control requests and negotiated interrupt receipts.
- [x] Implement `cancel_queued` and per-message `cancel_async_message` requests.
- [x] Reject unknown or duplicate UUIDs in queue receipts.
- [x] Resume a supported native Claude session with its persisted session ID.
- [ ] Project aborted partial assistant messages distinctly from complete
  replies.
- [ ] Define and implement the fallback when an older CLI lacks precise queue
  lifecycle or cancellation capabilities.
- [ ] Add compatibility fixtures for all supported Claude stream-JSON versions.

### Dagger tools, MCP, and workspace synchronization

- [x] Refactor `LLMToolServer` so the same tool set can be served over stdio or
  stateful Streamable HTTP.
- [x] Add a session-owned exec HTTP registry with opaque, unique handler tokens.
- [x] Forward the registered handler through the harness exec's loopback
  listener.
- [x] Start the loopback listener for an exec-HTTP-only service even when the
  execution has no nested-client metadata.
- [x] Inject the exec-HTTP capability into the live process as a runtime-only,
  output-scrubbed secret environment variable which does not affect cache
  identity.
- [x] Require that runtime capability as the MCP bearer token before dispatching
  a request.
- [x] Construct a live MCP handler from the harness LLM's current MCP state.
- [x] Apply the same implicit workspace-module main-object binding used by
  `LLM.MCP` before constructing the harness tool server.
- [x] Configure Codex app-server with one complete process-level Dagger MCP
  table containing the generated URL, bearer-token environment variable,
  required-server policy, and tool approval policy.
- [ ] Configure Claude Code to use the generated MCP URL and bearer token.
- [ ] Ignore unrelated user/project MCP configuration, using strict native MCP
  configuration where the vendor supports it.
- [ ] Verify that bound object tools, workspace-module tools, skills, builtins,
  and external MCP proxies are all visible to the native model.
- [x] Preserve the Dagger client/query context across Streamable HTTP tool calls
  while retaining request cancellation and protocol values.
- [x] Correlate the vendor MCP item with the authoritative HTTP request by native
  call ID without executing or materializing it twice.
- [x] Parent Dagger MCP evaluation beneath the corresponding live tool-call span.
- [ ] Pull native workspace changes into `MCP.workspace` immediately before
  every Dagger tool call.
- [ ] Push Dagger workspace/tool changes into the mutable harness mount after
  every tool call, including failures.
- [ ] Perform a final serialized pull when a native turn ends without a trailing
  Dagger MCP call.
- [ ] Persist the final workspace and rebound object tools in the same atomic
  commit as messages and native state.

### Live telemetry and TUI

- [x] Expose the derived harness service, raw protocol/stderr logs, lifetime, and
  exit status through normal service telemetry.
- [x] Decode vendor output into typed prompt lifecycle, text/thinking delta,
  tool-call, tool-result, usage, and terminal events.
- [x] Keep structured protocol JSON inside the verbose harness service logs
  instead of rendering it as the user-facing conversation.
- [x] Emit the submitted user message immediately as an `LLM prompt` span with
  `LLMRole`, message, call-digest, and Agent identity attributes.
- [x] Feed every text delta into a live `displayPhases.StartText` span so the
  model response streams instead of appearing only at terminal commit.
- [x] Feed observable reasoning deltas into live thinking spans.
- [ ] Distinguish completed from aborted thinking spans at every terminal
  lifecycle boundary.
- [x] Emit each native tool call as a live tool-call span with its name,
  arguments, call ID, and native source.
- [x] Render Codex sub-agent creation and collaboration controls as native tool
  spans without promoting the child agent's private response as the parent's
  model response.
- [x] Emit each Dagger MCP tool call as the same live tool-call shape and nest
  its actual execution span beneath it.
- [x] Attach native and MCP tool results, error status, output, and result-token
  metadata to their corresponding tool-call spans.
- [x] Render MCP tool-result `content` blocks in user-facing logs instead of the
  raw `{content, structuredContent, _meta}` response envelope, using bounded
  semantic summaries for binary and resource content while retaining the full
  envelope in encapsulated app-server protocol logs.
- [ ] Emit a child Agent identity, controlling-Agent identity, input mode, and
  live state while streaming each Codex child conversation beneath its own
  roster entry.
- [ ] Let the TUI focus an observe-only or controlled Agent for inspection
  without requiring a sendable call digest, mark who controls it, and disable
  or explicitly gate its prompt according to `inputMode`.
- [ ] Update existing LLM token gauges from harness usage notifications before
  the turn completes.
- [x] Attach Dagger message ID, vendor message ID, native turn ID, tool-call ID,
  and native source as non-content correlation attributes.
- [ ] Attach final delivery classification and terminal status as non-content
  correlation attributes.
- [x] Close all live display spans exactly once on normal completion and runtime
  close or failure.
- [ ] Prove exact-once display closure across interruption, process exit, and
  caller cancellation races.
- [ ] Preserve the same surfaced-conversation tree in both the live promoted TUI
  and the reveal-independent final report.
- [x] Unit-test prompt/text/tool/result log emission and exact Dagger MCP
  parenting, including the race where HTTP arrives before app-server lifecycle.
- [ ] Add TUI tests proving the prompt, streaming response, native tools, MCP
  tools, results, and errors appear before terminal checkpoint commit.
- [x] Exercise a real Codex turn with `tui-qa`, capturing the submitted prompt,
  response while `RUNNING`, live MCP tool call, successful result, and final
  answer before checkpoint completion.
- [ ] Add equivalent `tui-qa` coverage for Claude, including timing assertions
  that distinguish streaming from a terminal-only redraw.

### Authentication, consent, and isolation

- [x] Treat explicit harness kind selection as the current protocol trust
  declaration and never offer a credential from an unrelated provider.
- [x] Normalize matching Codex OAuth and API-key credentials into a runtime-only
  internal auth offer.
- [x] Keep broker-provided Codex credentials out of GraphQL arguments, the cold
  container recipe, checkpoint messages, and protocol logs.
- [x] Keep the Claude module's current `ANTHROPIC_API_KEY` ingress explicit.
- [x] Make the harness container, rather than Codex's nested sandbox, the
  filesystem/network/secret authority boundary.
- [ ] Compute a content-preferred digest for the evaluated cold harness seed.
- [ ] Render the harness recipe, kind, credential class, and account/source in an
  interactive consent prompt without resolving secret plaintext.
- [ ] Add once-only and client-local remembered grants bound to harness content,
  kind, credential source/account, and adapter protocol major version.
- [ ] Add explicit digest pre-approval plus grant listing and revocation.
- [ ] Fail closed with an actionable digest when headless execution has no
  matching remembered grant.
- [ ] Inject Claude Code subscription OAuth only into the runtime process clone
  and define client-side rotation behavior.
- [ ] Exclude all broker-created auth files, mounts, environment values, approval
  records, and secret-derived hashes from rootfs capture and telemetry.
- [ ] Prove that approval records cannot be recovered from an LLM ID, trace,
  checkpoint, portable recipe, or another client's session.

### Cold continuation, recovery, and branching

- [x] Validate native checkpoint compatibility before attempting resume.
- [x] Resume Codex threads and Claude sessions from compatible native IDs.
- [x] Fall back from Codex's exact `no rollout found` error to a new native
  thread reconstructed from the checkpointed portable Responses API history.
- [x] Preserve the last committed snapshot when protocol framing, correlation,
  service startup, or process execution fails.
- [x] Refuse reseed while the current Agent reports active execution.
- [ ] Snapshot and restore opaque CLI session files from the harness rootfs.
- [ ] Recreate a stopped runtime from the full atomic container/workspace/tool
  checkpoint rather than only native session metadata.
- [ ] Make `rehydrate` lazy, start no process until start/send/resume, and import
  portable history when opaque native state is unavailable or invalid.
- [ ] Complete reseed guards for unacknowledged commands, unresolved native
  queues, active background writers, and unquiesced workspace state.
- [ ] Recover from a crash by replaying only mailbox records not proved consumed
  by the last committed native prefix.
- [ ] Fork independent Codex threads/containers for immutable LLM branches.
- [ ] Clone or portably import Claude continuation state for immutable branches.
- [ ] Preserve portable messages, workspace, and tools when switching from a
  harness-backed LLM to a provider-backed LLM.

### Installable modules

- [x] Provide `modules/claude` and `modules/codex` `@agent` middlewares.
- [x] Require `claude` or `codex` on `PATH` according to the selected harness
  kind while Core owns all invocation flags.
- [x] Build the Codex harness on Wolfi with Node/npm, `ripgrep`, and the system CA
  bundle needed by its native TLS client.
- [x] Verify a fresh `dagger-dev --env dev agent` Codex session authenticates,
  executes a native shell command under the external sandbox contract, and
  completes a turn.
- [x] Verify a fresh `dagger-dev --env dev agent` Codex session discovers and
  successfully calls the workspace's `review_status` tool through authenticated
  Streamable HTTP MCP.
- [ ] Add equivalent end-to-end Claude module coverage for the persistent
  stream-JSON path.

### Required regression coverage

- [x] Unit-test exact Codex and Claude persistent command specifications.
- [x] Unit-test bounded JSONL framing, malformed records, and protocol EOF.
- [x] Unit-test one adapter serving consecutive turns.
- [x] Unit-test Codex command acknowledgement remaining pending until correlated
  consumption.
- [x] Unit-test expected-turn retry winning a lifecycle/response race.
- [x] Unit-test interruption cancelling an accepted but unconsumed steer without
  recording `STEERED`.
- [x] Unit-test Codex's duplicate started/completed user-message lifecycle.
- [x] Unit-test out-of-order JSON-RPC responses by request ID.
- [x] Unit-test Claude UUID checkpoint restoration, control receipts, queued
  cancellation, and unknown receipt rejection.
- [x] Unit-test natural process exit, startup output, stderr visibility, and real
  exit-error propagation with persistent stdin.
- [x] Unit-test checkpoint cursor validation and attached checkpoint
  materialization.
- [x] Fixture-test Codex OAuth, refresh, API-key login, model selection, external
  sandbox policy, typed text/tool events, steering, interruption, and resume.
- [x] Fixture-test interleaved parent/child Codex threads, collaboration tool
  telemetry, and missing-rollout recovery from only the committed history
  prefix.
- [x] Manually verify with `tui-qa` that the Wolfi Codex module completes a real
  native shell tool call without bubblewrap or approval failure.
- [x] Unit-test runtime-only exec-HTTP capability injection and output-secret
  registration.
- [x] Unit-test Streamable HTTP preservation of the MCP server's Dagger context.
- [x] Unit-test live harness prompt/text/tool/result telemetry and exact native
  call-ID span parenting across event/HTTP ordering races.
- [x] Capture raw OTLP and `tui-qa` evidence for a real Codex workspace MCP call:
  the prompt and token deltas stream as logs, `Review.status` is a direct child
  of `review_status`, and the tool result completes successfully.
- [x] Capture raw OTLP and `tui-qa` evidence for a real Codex delegation: child
  lifecycle/output stays filtered, `subAgentActivity` and `wait` are sibling
  native tool spans beneath the parent response, the parent completes, and a
  follow-up prompt succeeds on the same hot runtime.
- [ ] Integration-test concurrent `Agent.send` calls for unique IDs, exact FIFO
  native submission, correlated delivery, and await resolution.
- [ ] Integration-test cancel-and-re-await plus concurrent awaiters.
- [ ] Integration-test pause, cooperative quiescence, queued sends, and ordered
  resume.
- [ ] Integration-test Codex interrupted-turn markers, completed partial output,
  queued steer reconciliation, and filesystem side effects.
- [ ] Integration-test Claude folded-versus-queued lifecycle, interruption
  receipts, cancellation, and aborted partial output.
- [ ] Integration-test stop races with send, terminal notification, and process
  exit.
- [ ] Integration-test arbitrary history import, valid-cursor suffix import,
  cold rehydrate, and reseed.
- [ ] Integration-test atomic snapshots under workspace edits, tool mutations,
  background writers, malformed protocol, process crash, and sync failure.
- [ ] Integration-test independent immutable forks and switching back to a
  provider-backed LLM.
- [ ] Integration-test the complete workspace tool set through each native MCP
  client and bidirectional workspace synchronization around every call.
- [ ] Integration-test live and final conversation telemetry for prompts,
  streamed text/thinking, native tools, MCP tools, tool results, usage, failure,
  interruption, and replay.
- [ ] Security-test credential non-disclosure in containers, messages,
  telemetry, call IDs, and checkpoints.
- [ ] Security-test content-bound interactive consent, remembered-grant
  invalidation, headless refusal, rotation, and client-local approval isolation.
