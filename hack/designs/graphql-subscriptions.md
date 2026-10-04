# GraphQL subscriptions in dagql: code waits on events

Proposal, with a spike on this branch (§9). Companion to
hack/designs/agent-messaging.md (which settled that *agents* wait by ending
their turn, with lifecycle arriving as mailbox messages, §3/§4.3) and to
hack/designs/notes/busybees-as-modules.md (§2.1, §3.1: the orchestrator loop
that *code* — not a model — has to run). It answers agent-messaging §9's
"combinators for code" item with a transport rather than a combinator.

The one-sentence version: **an engine-owned, ordered, cursor-addressed event
log is what every concurrency proposal converged on, and GraphQL already has
a name for reading one — the `Subscription` root; dagql implements it as a
push transport (graphql-sse) over a pull-addressable log whose every event
is a Node with an honest ID.**

## 1. Why subscriptions

The busybees factory (busybees §2.1) must, in one loop: poll GitHub every 5
minutes, wake at once when any of N `Agent`s settles, wake when a human or an
agent sends it a message, and schedule follow-ups ("re-check CI in 10m").
Today a module can do none of that without polling `Agent.state` on a sleep
(busybees §3.1: "nothing wakes on a clock").

Five competing Dang-side proposals were written against this problem. They
differ in surface — a `select` expression, `Stream[T]` combinators, engine
awaitables, actors — but every one of them ended up putting the same thing
in the engine:

- **an append-only log** with a per-log `seq` (`Mailbox` in the streams and
  engine-side proposals, the actor proposal's typed `Message` union);
- **read in order through a cursor** — `events(after:)`, `next(after:)` —
  idempotent, so a canceled or raced read loses nothing;
- **edge-triggered agent facts** feeding it, reusing `queueEventsLocked`'s
  per-subscriber bookkeeping (core/agent.go:950) and the subscribe-time
  level check (`installSubscriptionLocked`, core/agent.go:918-939);
- **merging done server-side**, because only the engine knows the order in
  which a settle, a send and a timer happened.

The streams proposal had to invent a schema hint, `@stream(cursor:)`,
meaning "call me repeatedly, passing the previous result's cursor back",
because dagql rejects subscriptions (dagql/server.go:1129, `// TODO return
"subscriptions not supported"`). That hint *is* a subscription, spelled as a
convention every SDK must learn separately. This design replaces it with the
real thing:

- **The Subscription root is the event-log reader.** A subscription
  operation names one root field, gets a stream of values of that field's
  type, each with the operation's sub-selection applied.
- **The spec's one-root-field-per-subscription rule** (GraphQL §5.2.3.1,
  "Single root field") is not a limitation to work around; it is the design
  advice "aggregate server-side". A program that waits on many sources
  subscribes to *one* log that the sources append to — the mailbox (§4.2).
  Fan-in across N subscriptions on the client is possible but is the
  ephemeral, unordered case (§8).
- **Push transport over a pull-addressable log.** Every pushed event is a
  `Node` whose ID is an honest chain — `…agent(handle:"…").event(seq: 7)`,
  later `…mailbox(…).event(seq: 41)` — so dagql's ID model holds: an event
  can be re-loaded, passed as an argument, cached like any other value. The
  subscription field takes `after: Int` so a reconnecting or restored
  reader resumes exactly where it left off. The push stream is an
  optimization of the pull read, never a different source of truth.
- **The pull twin stays.** Each subscription field has a blocking long-poll
  sibling on the log's owner (`Agent.events(after:)`, later
  `Mailbox.events(after:)`), for SDKs without subscription codegen (§6), for
  callers whose transport cannot stream, and as the durability story: a
  cursor is just an `Int` you can persist.

## 2. Wire protocol

**Fixed** (a Dang client is being built against it in parallel): the
[graphql-sse](https://github.com/enisdenjo/graphql-sse/blob/master/PROTOCOL.md)
**distinct connections mode**.

- The client POSTs the ordinary GraphQL request JSON
  (`{"query", "variables", "operationName"}`) to the **same `/query`
  endpoint** every client already uses, with `Accept: text/event-stream`
  and `Content-Type: application/json`.
- The server responds `200` with `Content-Type: text/event-stream` and,
  for each value, one SSE event:

  ```
  event: next
  data: {"data":{"agentEvents":{"seq":3,"state":"IDLE"}}}

  ```

  `data` is a GraphQL `ExecutionResult` (`{"data":…,"errors":…}`) on one
  line. When the stream ends:

  ```
  event: complete
  data:

  ```

  The empty `data:` line is deliberate: a browser `EventSource` does not
  dispatch an event whose data buffer is empty (HTML §9.2.6 "dispatch the
  event", step 2), and graphql-sse's own server prints it. Clients must
  accept `complete` with or without it.
- **Errors**: a validation error, a resolver error, or an error resolving
  one event's sub-selection is sent as `event: next` with `errors` set (and
  `data` null), followed by `event: complete`. An error terminates the
  stream.
- **Keep-alive**: SSE comment lines (`: ping`) every 15s of silence, so
  proxies and idle-connection reapers do not cut a quiet subscription. Every
  SSE parser ignores lines starting with `:` (HTML §9.2.6); clients must too.
- **Cancellation**: the client aborting the request (closing the
  connection) cancels the subscription's context, which unwinds the
  resolver. There is no "stop" message in distinct-connections mode.
- **A subscription without `Accept: text/event-stream` is refused** with a
  GraphQL error naming the header (plain JSON response, the normal
  transport), rather than returning the first event and silently dropping
  the rest.
- **Non-subscription operations are unaffected**: without the `Accept`
  header nothing changes. A query or a mutation *with* the header is
  answered graphql-sse-style as one `next` and a `complete`, which is what
  the protocol specifies for single results.

The engine already speaks SSE to clients for telemetry
(engine/server/telemetry.go:1254 on the server, engine/client/client.go:1190
on the client), and the session HTTP stack already streams those responses
through every hop, including the nested-client listeners (§5). Nothing below
the GraphQL handler changes.

## 3. dagql

### 3.1 Declaring a subscription field

Subscription fields are not methods on an object: the `Subscription` root has
no receiver value and its fields return a sequence, not a result. They are
installed on the `Server` beside the classes:

```go
// SubscriptionFunc runs one subscription: it calls emit once per value, in
// order, and returns when the stream ends (nil), fails (error), or ctx is
// canceled. emit returns an error when the subscriber is gone; the
// resolver must then return.
type SubscriptionFunc func(ctx context.Context, args map[string]Input, view call.View,
	emit func(Typed) error) error

type SubscriptionField struct {
	Spec *FieldSpec
	Func SubscriptionFunc
}

// Subscribe derives the spec from A (args, as Func does) and R (the event
// type, as Func derives a return type).
func Subscribe[A any, R Typed](name string,
	fn func(ctx context.Context, args A, emit func(R) error) error) SubscriptionField

dagql.Subscriptions{
	dagql.Subscribe("agentEvents", s.agentEvents).Doc(…).View(…),
}.Install(srv)
```

- **A callback, not a channel or `iter.Seq`.** Emitting synchronously gives
  backpressure for free — the resolver cannot run ahead of the transport,
  and the log, not a Go buffer, holds what has not been read (§8) — and
  makes the "subscriber went away" path a plain returned error.
- **No cache, by construction.** A subscription field never goes through
  `GetOrInitCall`; there is no `DoNotCache` to forget. Caching applies
  inside, to the per-event values, which are ordinary results.
- **Schema**: `SchemaForView` emits a `Subscription` object definition from
  the installed fields (respecting each spec's `ViewFilter`) and sets
  `schema.Subscription`, so validation (gqlparser's
  `SingleFieldSubscriptionsRule` applies once the root exists) and
  introspection (`__schema.subscriptionType`, already plumbed in
  dagql/introspection/types.go:98) work unchanged. `Fork` carries the
  fields over like classes.

### 3.2 Executing a subscription

`Server.Exec` (dagql/server.go:1049) is gqlgen's `ResponseHandler` factory:
gqlgen calls the returned function repeatedly and stops at `nil`. Today it
re-executes the query on every call, which only works because the POST
transport calls it once; the spike makes the query path one-shot and adds
a streaming path for `ast.Subscription`:

1. Refuse unless the transport marked the context as streaming (§3.4).
2. Parse the single root field against the installed spec — arguments decode
   exactly as `Class.ParseField` + `preselect` do (defaults, required
   checks) — and parse its sub-selection with `parseASTSelections` against
   the field's type, so fragments, `__typename` and interfaces behave as in
   queries.
3. Start the resolver on a goroutine under a cancelable child of the request
   context, holding one operation lease for the subscription's lifetime.
4. `emit(v)` resolves the sub-selection on `v` **with the normal selection
   path** — `toSelectable` + `Resolve` for objects, a plain value for
   leaves — and hands the resulting `{"field": …}` map to the transport,
   blocking until it has been taken. Each event is an `ObjectResult`, so
   its fields are dagql calls with telemetry and caching like any query.
5. When the resolver returns, the handler returns its error (if any) as a
   final `errors` response, then `nil`; the transport writes `complete`.

### 3.3 Honest per-event IDs

The resolver does not mint event values out of thin air: it *selects* them.
For `agentEvents`, each log entry is emitted as the result of
`srv.Select(ctx, agent, &ev, Selector{Field: "event", Args: seq})`, the same
re-exec pinning `Agent.send` uses for messages (core/schema/agent.go:292-325).
The event's ID is therefore `…llm(…).agent(handle:"…").event(seq: 3)`:
loadable with `node(id:)` from any request in the session, usable as an
argument, and what the pull twin returns too. `Agent.event(seq:)` is
`DoNotCache` and reads the live log, so a stale ID in a new incarnation
(§7) fails loudly instead of replaying a cached fact from another runtime.

### 3.4 Transport

dagql grows its own gqlgen transport, `dagql.SSE`, added ahead of `POST` in
`NewDefaultHandler` (dagql/server.go:333). gqlgen ships a `transport.SSE`,
but it writes `event: complete` with no `data:` line (so `EventSource`
never dispatches it) and loops `responses(ctx)` until `nil`, which
re-executed queries forever against the old `Exec`. Ours: `Supports` = POST
+ JSON content type + `Accept` containing `text/event-stream`; it marks the
context as streaming (how `Exec` tells a refused plain-POST subscription
from an accepted one), writes headers (`Content-Type: text/event-stream`,
`Cache-Control: no-cache`, `X-Accel-Buffering: no`) and flushes, then
writes and flushes one `next` per response, keep-alive comments while idle,
and `complete`.

Cancellation: the request context is the subscription context — the engine
already layers session-close (`withClosingCancel`) and engine-shutdown
cancellation onto it in `serveHTTPToClient`/`serveQuery`
(engine/server/session.go:2315, :2558) — so closing the connection, closing
the session, or stopping the engine all end the resolver. The session's
in-flight accounting (`dagqlInFlight`, session.go:2542) counts a live
subscription as one in-flight request, which is right: teardown cancels it,
then waits for it.

## 4. Schema

### 4.1 Agent lifecycle (first)

```graphql
type Subscription {
  """
  The agent's lifecycle transitions, in order. Without `after`, starts with
  the transition into the agent's current state (the level check), then
  every later edge. With `after`, replays the retained log from seq > after.
  """
  agentEvents(agent: AgentID!, after: Int): AgentEvent!
}

"One projected lifecycle transition of an agent's runtime entry."
type AgentEvent implements Node {
  id: AgentEventID!
  agent: Agent!
  "1-based position in this runtime entry's transition log."
  seq: Int!
  state: AgentState!
  """
  An IDLE edge that completed a turn with newly committed work: exactly the
  IDLE transitions Agent.notify announces (idleEventDue).
  """
  turnCompleted: Boolean!
  "The completed turn's final reply (IDLE), else empty."
  reply: String!
  "The loop error (FAILED), else empty."
  error: String!
  "The event text Agent.notify delivers to a subscribing agent."
  text: String!
}

extend type Agent {
  "Look up one transition by seq: the target of every AgentEvent ID."
  event(seq: Int!): AgentEvent!
  "Pull twin: block until a transition after `after` exists, then return every retained one."
  events(after: Int! = 0): [AgentEvent!]!
}
```

**No new runtime concept.** This is `notify` with a subscription as the sink.
The single choke point, `transitionLocked` (core/agent.go:1283), already
detects every projection edge (`state != rt.lastEventState`) before handing
it to `queueEventsLocked`. The spike appends the same edge to a small
per-entry log (`seq`, state, the `idleEventDue` verdict, reply/error
snapshot) in the same critical section, and seeds it at `create`
(core/agent.go:1766, where `lastEventState` is initialized). A subscriber
reads entries past its cursor under `rt.mu`, then blocks on the
`stateChanged` channel transitionLocked already closes on every change —
exactly `WaitSettled`'s loop (core/agent.go:2457) — so no transition can fall
between "read" and "wait".

Edge semantics match notify's where they should and are rawer where code
needs it:

- **Level check at subscribe**: no `after` ⇒ start at the latest entry, i.e.
  the transition into the current state. A fast agent that settled before
  the subscription landed is not missed — the `installSubscriptionLocked`
  rule (core/agent.go:936).
- **Every edge is logged**, including IDLE edges notify suppresses as
  not-news; `turnCompleted` carries notify's verdict so code that wants
  "a turn finished" filters on it, and code that wants the raw projection
  has it.
- **Restored, never-activated entries** (core/agent.go:913) log their
  restored state as seq 1 of a new incarnation; a reader that wants "next
  transition only" passes `after:` the current seq.

**Capability-based**: you need the Agent ID (async-agents §3.3). There is no
`Query.agents` firehose, and no subscription that enumerates agents — the
TUI's roster comes from telemetry (§7), not from here.

**Refused inside an agent turn.** A subscription is a blocking read; an
agent's turn must never block on another agent's progress (agent-messaging
§3). `agentEvents` and `Agent.events` called with `core.CallerAgent(ctx)`
set (an agent's loop, or a module function called from one) are refused:

> agent "chief" cannot subscribe to agent "w" from within its own turn: a
> turn never blocks on another agent — an agent waits by ending its turn.
> Use w.notify(subscriber: chief) and the events arrive as messages.

### 4.2 Mailbox (second)

The factory's real subscription is to *one* log that everything appends to.
Sketch (not in the spike):

```graphql
extend type Query {
  "Mint a session-scoped event log. The handle is the capability, as with spawn."
  mailbox(name: String! = ""): ID! @expectedType(name: "Mailbox")
}
type Mailbox implements Node {
  id: MailboxID!
  name: String!
  send(message: String!, replyTo: String): ID! @expectedType(name: "MailboxEvent")
  schedule(tag: String!, after: String = "0s", every: String): ID! @expectedType(name: "Schedule")
  event(seq: Int!): MailboxEvent!
  events(after: Int! = 0): [MailboxEvent!]!          # pull twin, blocking
}
interface MailboxEvent implements Node { id: ID!  seq: Int!  at: String! }
type MessageEvent  implements MailboxEvent & Node { …  text: String!  origin: LLMMessageOrigin!  ref: String! }
type AgentEvent    implements MailboxEvent & Node { … as §4.1 … }
type ScheduleEvent implements MailboxEvent & Node { …  tag: String!  firing: Int!  missed: Int! }
extend type Agent {
  notify(subscriber: AgentID, mailbox: MailboxID, on: [AgentState!] = [IDLE, FAILED]): ID!
}
extend type Subscription {
  mailboxEvents(mailbox: MailboxID!, after: Int): MailboxEvent!
}
```

- The log is the actor proposal's typed `Message` union and the streams
  proposal's `Mailbox`, minus `@stream`: Dang reads `mailboxEvents` as
  `Stream[MailboxEvent!]` because it is a subscription field, not because of
  a hint. `case (ev) { a: AgentEvent => … }` narrows on `__typename`.
- `notify(mailbox:)` is a second sink in `deliverEvent` (core/agent.go:1049)
  that appends an `AgentEvent` (with the *agent's* seq as payload and the
  mailbox's seq as position) instead of enqueueing text.
- `schedule` is an engine timer that appends `ScheduleEvent`s; ticks that
  fire while nothing reads accumulate in the log, so `missed` is only about
  coalescing a backlog the reader asks to skip.
- `send` resolves its origin exactly as `Agent.send` does
  (`resolveMessageOrigin`, core/agent.go:471), so a foreman agent or a human
  writing to the factory's box is attributed.
- Consumer cursors (the streams proposal's `consumer:`/`ack`) are optional
  sugar; the minimum is the `after:` the client holds.

The factory loop in Dang (against the sibling client work) becomes:

```dang
let box = mailbox(name: "factory")
f.workers.values.each { w => w.notify(mailbox: box) }
box.schedule(tag: "poll", every: "5m")
mailboxEvents(mailbox: box, after: f.cursor).each { ev =>
  f = f.handle(ev).withCursor(ev.seq)     # persist the cursor with the state
}
```

## 5. Nested clients

Module code reaches the engine through a **nested client**: an HTTP listener
the engine serves for the duration of one function call.

- **Container SDKs** (Go, Python, TS modules): `setupNestedClient`
  (engine/engineutil/executor_spec.go:1125-1157) listens *inside the
  container's network namespace* and serves each request in-process with
  `SessionHandler.ServeHTTPToNestedClient` (engine/server/session.go:2185) →
  `serveHTTPToClient` → `serveQuery`. The SDK talks to
  `127.0.0.1:$DAGGER_SESSION_PORT/query`.
- **In-engine Dang**: `WithNestedClientServer`
  (core/sdk/dang/shared/shared.go:40-69) does the same on a loopback
  listener and points its client at `http://<addr>/query` (:112).

Neither path proxies bytes: both are a `net/http.Server` whose handler calls
straight into the same `serveQuery` that main clients reach, with the real
`http.ResponseWriter` (a `Flusher`; the container listener also speaks h2c).
So **subscriptions work over nested clients with no proxying change** — the
same reason telemetry SSE already works there (session.go:2207-2210 mentions
SSE clients on that path). The spike's integration test subscribes from
inside a container through `DAGGER_SESSION_PORT` to prove it.

What a nested caller must do itself:

- Send `Accept: text/event-stream` and parse SSE (the generated clients do
  not; §6). Dang's runtime uses genqlient's `graphql.Client` for queries;
  the subscription path needs a raw `http.Client` POST to the same URL — the
  sibling Dang work.
- **Abort the request when done.** `WithNestedClientServer`'s shutdown waits
  up to 10s for in-flight requests (shared.go:98); a leaked subscription
  would hold the function call's teardown for that long and then be cut by
  the listener closing. Container listeners close at exec cleanup.

## 6. SDKs

Go/TS/Python codegen does not emit subscriptions; they keep working
unchanged, and **the pull twin is their API**:

```go
for after := 0; ; {
	evs, err := worker.Events(ctx, dagger.AgentEventsOpts{After: after}) // blocks
	if err != nil { return err }
	for _, ev := range evs {
		after, _ = ev.Seq(ctx)
		// handle ev
	}
}
```

Codegen consumes introspection, which now reports a `Subscription` object
type; `cmd/codegen` must skip `__schema.subscriptionType` (it already parses
it, cmd/codegen/introspection/introspection.go:46) or it would generate a
query-shaped `Subscription` struct whose methods cannot work.

Later, Go codegen could emit, per subscription field, a channel-returning
function on `*Client`, using the same query builder for the sub-selection
and a small SSE reader (engine/client already parses SSE with
`github.com/vito/go-sse`, `sse.NewReadCloser`):

```go
// AgentEvents subscribes to the agent's lifecycle transitions.
func (c *Client) AgentEvents(ctx context.Context, agent *Agent, opts ...AgentEventsOpts) (<-chan *AgentEvent, <-chan error)
```

Each received event would be returned as a lazy handle loaded from its `id`
(the event is a Node), so selecting more fields later is an ordinary query.
TS would return an `AsyncIterable<AgentEvent>`, Python an `async for`
generator. None of this is needed for the spike.

## 7. Telemetry, the roster, and notify

One transition, now three sinks — all fed from `transitionLocked`:

| Sink | Consumer | Delivery |
|---|---|---|
| `publishStateLocked` → agentcontrol records (core/agent.go:1296) | TUI roster, restore | Complete projections, asynchronous, coalescing — "not a delta" (engine/agentcontrol/control.go). A lagging UI may see IDLE→RUNNING→IDLE as no change; fine for display. |
| `queueEventsLocked` → `deliverEvent` (core/agent.go:950, 1049) | a subscribing **agent** (`notify`) | Text messages into its mailbox; IDLE suppressed when not news; never relaunches. Unchanged. |
| transition log → `agentEvents` / `Agent.events` | **code** (Dang, SDKs, CLI) | Every edge, typed, in seq order, replayable from a cursor. |

The roster stays telemetry-driven (async-agents §3.3, "telemetry is the
directory"): subscriptions are capability-scoped and cannot enumerate agents
you do not hold, which is exactly why they are not a roster source. Conversely
the roster is not a waiting primitive, because coalescing makes it miss edges.

`Agent.notify(subscriber: Agent)` is unchanged and remains the *agent*
answer: an agent's turn must not hold a subscription open (§4.1). A
supervisor that is code subscribes; a supervisor that is a model gets
messages.

A subscription request is one `POST /query` span for its whole lifetime
(session.go:2598); each pushed event's field resolutions are child spans of
it. The `Agent.event(seq:)` select that pins each event shows as a call per
event. A `dagger.io/await` attribute to render the wait as idle rather than
busy (engine-side proposal §3.8) is a follow-up.

## 8. Sizing and open questions

| Piece | Status | Size |
|---|---|---|
| dagql: `Subscribe`/`SubscriptionField`, schema emission, `Exec` streaming, SSE transport, unit tests | spike | ~450 LOC |
| core: transition log, `Subscription.agentEvents`, `AgentEvent`, `Agent.event/events`, turn refusal | spike | ~350 LOC |
| integration test through a nested client | spike | ~150 LOC |
| codegen skips `Subscription`; docs schema regen | follow-up | small |
| `Mailbox`, `notify(mailbox:)`, `schedule`, `mailboxEvents` | design only | ~1 week (the proposals' estimate) |
| Go/TS/Python subscription codegen | design only | ~3 d each |

Open:

1. **Durability across sessions.** Logs live in the session's runtime table
   and die with it. Restore (trace-native-agent-resume) re-creates entries
   as a new incarnation with seq restarting at 1. Should the cursor be
   `"<incarnation>:<seq>"` (the streams proposal's `CursorResetEvent`) so a
   reader with a stale cursor gets an explicit "resync by level" event
   rather than silently re-reading seq 1..n of a different log? The spike
   uses a bare `Int` and documents the hazard; the Mailbox should not ship
   without an answer, because the factory persists its cursor.
2. **Retention.** The agent log is tiny (one entry per state edge) and
   unbounded in the spike. The Mailbox needs a floor: trim below the
   minimum acknowledged consumer cursor, or a cap, with `after:` below the
   floor answered by a reset event.
3. **Backpressure.** Emission is synchronous, so a slow reader slows only
   its own resolver; nothing buffers per subscriber in Go. The log is the
   buffer. A reader that never reads holds an operation lease and a
   goroutine for as long as its connection lives — same as a hung query.
   Should a subscription hold its lease per event instead of for its
   lifetime? (The spike holds it for the lifetime.)
4. **Dang `{{ }}` cannot fan out at the subscription root.** A subscription
   operation has exactly one root field, so Dang's parallel-field batching
   cannot put two subscriptions in one request; each is its own connection.
   That is the "aggregate server-side" advice again: fan in with the
   Mailbox, not with N connections.
5. **Errors as data vs. terminal.** The spike ends the stream on any error.
   graphql-sse allows `next` with partial errors and a continuing stream; a
   per-event field error (e.g. a reply too large) arguably should not kill
   a factory's only wake-up channel.
6. **HTTP/1.1 connection limits.** Each subscription holds a connection.
   Fine for engine clients (h2c to the nested listener; one per session
   otherwise); a browser client would want the single-connection mode.
7. **CLI.** `dagger query` does not send the `Accept` header, so it gets
   the refusal. A `dagger query --subscribe` (stream `next` payloads as
   JSON lines) is cheap and would make subscriptions scriptable.
