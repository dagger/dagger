package core

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/vektah/gqlparser/v2/ast"
	"go.opentelemetry.io/otel/codes"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/internal/buildkit/identity"
)

// Agent is a conversation loop packaged as an addressable, long-lived entity
// within the session (hack/designs/async-agents.md §3).
//
// An Agent is a spawned instance, not a derivable value: LLM.spawn mints a
// unique InstanceID per call and pins it into the handle's ID chain (via the
// pure LLM.agent(id:) lookup — the same re-exec pinning AgentMessage uses),
// so two spawns of an identical composition are two distinct agents. The
// value itself stays pure and content-addressed — seed conversation, minted
// instance ID, display name — and starting it registers a runtime entry
// (loop goroutine on a detached context, computed state) in the session's
// AgentRuntimes table, keyed by the InstanceID. All runtime state lives in
// that table, never on the value.
type Agent struct {
	// Seed is the conversation the agent's evaluation loop starts from,
	// including its tools, workspace, and message history.
	Seed dagql.ObjectResult[*LLM]

	// InstanceID is the unique identity minted by the spawn that created
	// this agent, and the agent's runtime registry key: every spawn yields
	// a fresh entry, and a stopped instance's tombstone can never be
	// collided with by a later spawn of the same composition.
	//
	// Identity is the ID and nothing else — deliberately NOT the value's
	// content digest, which would make addressing depend on the seed
	// re-deriving byte-identically. It does not: a handle rebuilt from
	// telemetry is the RECIPE form, so using it re-executes the chain, and
	// a per-session or non-replayable leaf anywhere in the composition
	// (Query.currentWorkspace being the one every real agent carries)
	// mints a fresh value on every evaluation. Keyed on the digest, such a
	// handle missed the registry and — since Get never creates and
	// IDLE-with-seed-snapshot is the honest projection of a never-started
	// agent — looked healthy while addressing nothing, then spawned a
	// second loop from the seed on the first send.
	InstanceID string

	// Name is a display label — telemetry and error messages — with no
	// identity role: uniqueness comes from InstanceID, never from the
	// caller's choice of name.
	Name string
}

func (*Agent) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Agent",
		NonNull:   true,
	}
}

func (*Agent) TypeDescription() string {
	return "A conversation loop running as an addressable, long-lived entity within the session. " +
		"The conversation itself remains observable at any time as an immutable LLM value."
}

func (a *Agent) Clone() *Agent {
	cp := *a
	return &cp
}

// AgentState is the computed lifecycle state of an agent's runtime entry.
// It is always a projection of the entry's facts, never stored
// (hack/designs/async-agents.md §3.4).
type AgentState string

var AgentStates = dagql.NewEnum[AgentState]()

var (
	AgentStateIdle = AgentStates.Register("IDLE",
		"Mailbox empty, turn complete; blocked in receive.")
	AgentStateRunning = AgentStates.Register("RUNNING",
		"A model request or tool evaluation is in flight.")
	AgentStateWaitingInput = AgentStates.Register("WAITING_INPUT",
		"Blocked on input from the user (derived; see waitingOn).")
	AgentStatePaused = AgentStates.Register("PAUSED",
		"Mailbox accepting but not draining, until resume.")
	AgentStateStopped = AgentStates.Register("STOPPED",
		"Runtime released; snapshot remains readable.")
	AgentStateFailed = AgentStates.Register("FAILED",
		"The loop failed; snapshot holds the completed prefix. Resume retries.")
)

func (state AgentState) Type() *ast.Type {
	return &ast.Type{
		NamedType: "AgentState",
		NonNull:   true,
	}
}

func (state AgentState) TypeDescription() string {
	return "Computed lifecycle state of an agent."
}

func (state AgentState) Decoder() dagql.InputDecoder {
	return AgentStates
}

func (state AgentState) ToLiteral() call.Literal {
	return AgentStates.Literal(state)
}

// AgentStopReason distinguishes a stop somebody asked for from the stop the
// session's own teardown performs. It is a fact on the runtime entry, read by
// the state publisher and carried on the terminal record — never part of the
// projection, which knows only STOPPED.
//
// It exists because those two stops are indistinguishable otherwise, and a
// client restoring a trace has to tell them apart: session close stops every
// agent it holds (KillAll), so without a reason a cleanly closed session
// publishes a trace in which every agent looks deliberately dismissed —
// restore them all and every dismissal is reversed, restore none and a normal
// exit restores nothing at all.
type AgentStopReason string

const (
	// AgentStopExplicit is a stop somebody asked for: the stop verb, whoever
	// called it. A client restores such an agent in STOPPED state; a later
	// message may restart it from its preserved snapshot.
	AgentStopExplicit AgentStopReason = "EXPLICIT"

	// AgentStopSession is the stop session teardown performs on every
	// surviving entry. It says nothing about what the user wanted, so a
	// client restores the state the agent held BEFORE it.
	AgentStopSession AgentStopReason = "SESSION"
)

// AgentMessage is the handle returned by Agent.send: it identifies one
// message record in an agent's runtime entry, and carries the delivery
// evidence computed at enqueue. Reply correlation rides this handle —
// await returns the final reply of whichever turn consumed the message
// (hack/designs/async-agents.md §3.2), which under multiple senders is the
// only non-racy way to pair a reply with a message.
type AgentMessage struct {
	// AgentKey is the registry key (the agent's instance ID) of the runtime
	// entry holding the message record.
	AgentKey string
	// AgentName is the agent's display name, carried for error messages.
	AgentName string
	// MessageID uniquely identifies the message record within the entry.
	MessageID string
	// Delivery is how the message landed, computed at enqueue time.
	Delivery AgentMessageDelivery
}

func (*AgentMessage) Type() *ast.Type {
	return &ast.Type{
		NamedType: "AgentMessage",
		NonNull:   true,
	}
}

func (*AgentMessage) TypeDescription() string {
	return "A message delivered to an agent's mailbox."
}

func (m *AgentMessage) Clone() *AgentMessage {
	cp := *m
	return &cp
}

// AgentMessageDelivery is the delivery evidence of a message: how it landed
// in the agent's evaluation, computed once at enqueue and immutable after.
type AgentMessageDelivery string

var AgentMessageDeliveries = dagql.NewEnum[AgentMessageDelivery]()

var (
	AgentMessageStarted = AgentMessageDeliveries.Register("STARTED",
		"The message opened a new turn: the agent was idle or newly started.")
	AgentMessageSteered = AgentMessageDeliveries.Register("STEERED",
		"The message was absorbed into the in-flight turn at a step boundary, steering it.")
	// AgentMessageQueued is the evidence of a mailbox that accepts without
	// draining: the agent is paused (or a FAILED tombstone awaiting a
	// retry), so nothing consumes the message until a resume.
	AgentMessageQueued = AgentMessageDeliveries.Register("QUEUED",
		"The message is queued: the agent is paused or failed, and a resume will drain it.")
)

func (delivery AgentMessageDelivery) Type() *ast.Type {
	return &ast.Type{
		NamedType: "AgentMessageDelivery",
		NonNull:   true,
	}
}

func (delivery AgentMessageDelivery) TypeDescription() string {
	return "How a message landed in an agent's evaluation."
}

func (delivery AgentMessageDelivery) Decoder() dagql.InputDecoder {
	return AgentMessageDeliveries
}

func (delivery AgentMessageDelivery) ToLiteral() call.Literal {
	return AgentMessageDeliveries.Literal(delivery)
}

// agentMessageRecord is the runtime side of an AgentMessage handle: one
// entry in the runtime entry's message table, guarded by the entry mutex.
// Records are never deleted, so await stays idempotent for the rest of the
// session: a canceled awaiter can re-await and read the same result, and
// concurrent awaiters share it.
type agentMessageRecord struct {
	// text is the message body, recorded as a withPrompt selector when a
	// turn consumes it.
	text string
	// delivery is the evidence computed at enqueue.
	delivery AgentMessageDelivery
	// consumed marks the record as drained into the current in-flight
	// turn: recorded in the history, reply pending.
	consumed bool
	// resolved marks reply/err as final. Set under the entry mutex just
	// before done is closed; awaiters that observed the close read them
	// freely.
	resolved bool
	reply    string
	err      error
	// done is closed when the record resolves, unblocking awaiters.
	done chan struct{}
}

// AgentRuntimes manages the lifecycle of agent runtime entries for a single
// session: one entry per spawned agent instance, keyed by the spawn-minted
// InstanceID, which is unique by construction so keys never collide across
// spawns. Entries persist as tombstones after their loop ends (state and the
// last snapshot stay readable for the rest of the session, like
// ExitedService); unlike Services — which free a running entry's key on exit
// precisely because their keys are reusable composition digests — an agent's
// key is born unique, so the tombstone can keep the keyed slot harmlessly
// forever.
//
// Keying on the ID rather than on the agent value's content digest is what
// makes a handle rebuilt from telemetry address the LIVE runtime (design
// §10.2 mode B): the ID is a literal on the pinned chain, so it survives
// re-execution of a composition whose leaves do not. The capability model
// (§3.3) is unaffected — the same trace that publishes the ID publishes the
// call payloads a client rebuilds the whole composition from, so the digest
// was never the stronger secret — and the ID itself is engine-minted
// entropy, scoped to this session's registry.
//
// The registry is session-scoped — created alongside Services in the session
// state (engine/server/session.go) — so keys carry no session component.
type AgentRuntimes struct {
	entries map[string]*AgentRuntime
	mu      sync.Mutex
}

// NewAgentRuntimes returns a new, empty AgentRuntimes registry.
func NewAgentRuntimes() *AgentRuntimes {
	return &AgentRuntimes{
		entries: map[string]*AgentRuntime{},
	}
}

// agentKey is the registry key of an agent value: its instance ID, minted by
// the spawn that created it. An agent with no instance ID was never minted by
// a spawn (LLM.agent(id: "") is the only way to build one), and has no
// identity to address a runtime by, so it is rejected rather than sharing one
// key with every other such value.
func agentKey(agent dagql.ObjectResult[*Agent]) (string, error) {
	self := agent.Self()
	if self == nil || self.InstanceID == "" {
		return "", fmt.Errorf("agent has no instance ID: only an agent minted by spawn can be addressed")
	}
	return self.InstanceID, nil
}

// Get returns the runtime entry for the given agent value, if one exists.
// It never creates an entry: an instance nothing in this session ever minted
// or re-hydrated has no runtime, and its observable state (IDLE, snapshot ==
// the value's own conversation) is projected from that absence.
func (ars *AgentRuntimes) Get(ctx context.Context, agent dagql.ObjectResult[*Agent]) (*AgentRuntime, bool, error) {
	key, err := agentKey(agent)
	if err != nil {
		return nil, false, err
	}
	ars.mu.Lock()
	defer ars.mu.Unlock()
	rt, found := ars.entries[key]
	return rt, found, nil
}

// GetOrCreate returns the runtime entry for the given agent value, creating
// an inert one (loop not started, snapshot == seed) if none exists yet.
//
// The seed is read off the value only when the entry is CREATED. A later
// handle for the same instance addresses the entry as it stands — its own
// seed is not consulted, and cannot displace the conversation the loop has
// been building. That is what makes a rebuilt handle safe to use: it names an
// instance, it does not redefine one.
func (ars *AgentRuntimes) GetOrCreate(ctx context.Context, agent dagql.ObjectResult[*Agent]) (*AgentRuntime, error) {
	key, err := agentKey(agent)
	if err != nil {
		return nil, err
	}
	ars.mu.Lock()
	defer ars.mu.Unlock()
	if rt, found := ars.entries[key]; found {
		return rt, nil
	}
	rt := newAgentRuntime(key, agent)
	ars.entries[key] = rt
	return rt, nil
}

// Rehydrate creates the runtime entry for an instance from a conversation it
// did not seed, without starting its loop: the receiver's snapshot becomes the
// entry's committed history, so prompting it continues where the previous
// session left off (hack/designs/resume-from-trace.md §4.1).
//
// It works for the same reason GetOrCreate is safe to call with a rebuilt
// handle: the seed is read only when the entry is created. spawn is
// mint-create-pin; this is adopt-create-pin, and the whole difference is which
// conversation the entry begins life holding.
//
// state sets FACTS, never a stored state — the projection stays a projection
// (async-agents §3.4) — and only the states a conversation can actually be
// restored into are accepted: nothing restores as RUNNING, because the loop
// died with the session that published it, and a roster redisplaying it as
// running would be lying. An instance that already has an entry is refused
// rather than re-seeded: by then it may have stepped, and a late restore that
// silently discarded that would be worse than a loud one.
func (ars *AgentRuntimes) Rehydrate(ctx context.Context, agent dagql.ObjectResult[*Agent], state AgentState, loopErr string) (*AgentRuntime, error) {
	key, err := agentKey(agent)
	if err != nil {
		return nil, err
	}
	name := agent.Self().Name

	switch state {
	case AgentStateIdle, AgentStatePaused, AgentStateFailed, AgentStateStopped:
	default:
		return nil, fmt.Errorf("agent %q cannot be re-hydrated as %s: a restored agent holds a conversation, not a running loop — restore it as IDLE, and its still-pending input re-steps when it is next prompted", name, state)
	}
	if loopErr != "" && state != AgentStateFailed {
		return nil, fmt.Errorf("agent %q: an error can only be restored with state FAILED, not %s", name, state)
	}

	ars.mu.Lock()
	if _, found := ars.entries[key]; found {
		ars.mu.Unlock()
		return nil, fmt.Errorf("agent %q already has a runtime entry in this session: re-hydration must happen before anything else addresses the instance", name)
	}
	rt := newAgentRuntime(key, agent)
	ars.entries[key] = rt
	ars.mu.Unlock()

	if err := rt.rehydrate(ctx, state, loopErr); err != nil {
		ars.mu.Lock()
		delete(ars.entries, key)
		ars.mu.Unlock()
		return nil, err
	}
	return rt, nil
}

// Reseed replaces an existing entry's committed conversation with the given
// one, keeping the entry: identity, mailbox, and lifecycle facts are all
// untouched. It is the continuity verb — the client-facing sibling of the
// continuation adoption a tool performs mid-turn (MCP.adoptLLM): compaction,
// a workspace rebind, or a model change produce a new conversation value for
// the SAME agent, and swapping it in place is what keeps one agent one
// roster entry, where a stop-and-respawn would mint a successor instance
// under the same display name.
//
// It routes through Get, never GetOrCreate: a registry miss is an error, not
// a constructor (resume-from-trace §4.2) — an instance nothing here spawned
// or re-hydrated has no conversation to replace. rehydrate is adopt-create;
// this is the swap on an entry that exists, and the create-vs-swap guards
// point in opposite directions deliberately: a restore that finds an entry
// must be loud (the instance may have stepped), and a reseed that finds none
// must be too (the caller's bookkeeping is wrong).
func (ars *AgentRuntimes) Reseed(ctx context.Context, agent dagql.ObjectResult[*Agent], conversation dagql.ObjectResult[*LLM]) error {
	rt, found, err := ars.Get(ctx, agent)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("agent %q has no runtime in this session: only a spawned or re-hydrated instance holds a conversation to replace", agent.Self().Name)
	}
	return rt.Reseed(ctx, conversation)
}

// newAgentRuntime builds an inert entry for an agent value: loop not started,
// snapshot == the value's conversation.
func newAgentRuntime(key string, agent dagql.ObjectResult[*Agent]) *AgentRuntime {
	return &AgentRuntime{
		key:      key,
		name:     agent.Self().Name,
		self:     agent,
		last:     agent.Self().Seed,
		messages: map[string]*agentMessageRecord{},
		// wake has a single slot: it only needs to record "something changed,
		// re-check" (a stop request or a mailbox enqueue), not carry
		// payloads — the loop re-reads the facts after every wake.
		wake:         make(chan struct{}, 1),
		stateChanged: make(chan struct{}),
	}
}

// Start returns the running (or tombstoned) runtime entry for the given
// agent value, launching its evaluation loop if it isn't running yet. A
// second start of the same agent value in the same session is a no-op
// returning the existing entry; starting a stopped agent leaves the
// tombstone in place.
func (ars *AgentRuntimes) Start(ctx context.Context, agent dagql.ObjectResult[*Agent]) (*AgentRuntime, error) {
	rt, err := ars.GetOrCreate(ctx, agent)
	if err != nil {
		return nil, err
	}
	if err := rt.start(ctx); err != nil {
		return nil, err
	}
	return rt, nil
}

// Send is the central enqueue path (design §3.3): it enqueues a message
// into the agent's mailbox and returns its handle, starting the agent's
// loop if it was never started (signal-with-start, Temporal's lesson — a
// message to an unstarted agent must start it rather than be lost).
//
// Sending to an instance this session holds no entry for is an ERROR, not a
// creation. A registry miss used to route through GetOrCreate, which made a
// miss a constructor: a handle rebuilt from a trace whose agent this session
// never spawned booted a second loop from the handle's own seed, answered
// with no history, and published the same instance ID as the original — one
// roster entry, two runtimes (async-agents §10.2). Restore makes that case
// routine rather than exotic, since importing a trace rosters every agent of
// the old session including any this one failed to re-hydrate, so the miss
// has to say so. Signal-with-start survives: it starts an entry that exists.
//
// Sending to a STOPPED tombstone reopens the same runtime entry from its last
// committed snapshot and starts a fresh loop, so held and trace-restored
// handles keep their identity and history. Sending to a paused agent or a
// FAILED tombstone enqueues with QUEUED delivery: an explicit resume drains it
// (retrying the loop in the FAILED case), and until then awaiting the message
// projects the tombstone's failure rather than blocking forever.
func (ars *AgentRuntimes) Send(ctx context.Context, agent dagql.ObjectResult[*Agent], text string) (*AgentMessage, error) {
	rt, found, err := ars.Get(ctx, agent)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("agent %q has no runtime in this session: it was not spawned here, and an instance restored from a trace must be re-hydrated before anything can address it", agent.Self().Name)
	}
	msgID, delivery, err := rt.enqueue(text)
	if err != nil {
		return nil, err
	}
	// No-op if the loop is already running. Enqueue-then-start (rather
	// than the reverse) guarantees the loop's very first mailbox check
	// sees the message; in the running case the enqueue's wake poke covers
	// delivery.
	if err := rt.start(ctx); err != nil {
		return nil, err
	}
	return &AgentMessage{
		AgentKey:  rt.key,
		AgentName: rt.name,
		MessageID: msgID,
		Delivery:  delivery,
	}, nil
}

// LookupMessage returns the handle for a message already enqueued into the
// given agent's runtime entry, populated from the entry's message record.
// This is the runtime side of Agent.message — the lookup field send re-execs
// through to pin its result's identity (design §9): the record is immutable
// after enqueue (delivery evidence is computed once), so the same (agent,
// message ID) pair always denotes the same handle. An agent with no runtime
// entry, or an entry with no record of the ID, is a clear error: message
// never creates anything.
func (ars *AgentRuntimes) LookupMessage(ctx context.Context, agent dagql.ObjectResult[*Agent], msgID string) (*AgentMessage, error) {
	rt, found, err := ars.Get(ctx, agent)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("agent %q has no runtime entry in this session", agent.Self().Name)
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rec, found := rt.messages[msgID]
	if !found {
		return nil, fmt.Errorf("agent %q has no record of message %q", rt.name, msgID)
	}
	return &AgentMessage{
		AgentKey:  rt.key,
		AgentName: rt.name,
		MessageID: msgID,
		Delivery:  rec.delivery,
	}, nil
}

// AwaitMessage blocks until the turn that consumed the given message ends,
// returning that turn's reply (or the error the message resolved with).
func (ars *AgentRuntimes) AwaitMessage(ctx context.Context, msg *AgentMessage) (string, error) {
	ars.mu.Lock()
	rt, found := ars.entries[msg.AgentKey]
	ars.mu.Unlock()
	if !found {
		return "", fmt.Errorf("agent %q has no runtime entry in this session", msg.AgentName)
	}
	return rt.awaitMessage(ctx, msg.MessageID)
}

// KillAll cancels every running loop and waits (bounded by ctx) for them to
// wind down. Called at session teardown, the agent analog of
// Services.StopSessionServices.
//
// Every entry is stopped with AgentStopSession, so the terminal record says
// the session ended it rather than a caller: a client restoring the trace
// restores such an agent in the state it held before teardown, instead of
// reading a clean exit as a session-wide dismissal.
func (ars *AgentRuntimes) KillAll(ctx context.Context, cause error) error {
	ars.mu.Lock()
	entries := make([]*AgentRuntime, 0, len(ars.entries))
	for _, rt := range ars.entries {
		entries = append(entries, rt)
	}
	ars.mu.Unlock()

	var errs error
	for _, rt := range entries {
		if err := rt.Stop(ctx, true, cause, AgentStopSession); err != nil {
			errs = errors.Join(errs, err)
			continue
		}
		rt.releaseClientScope()
	}
	return errs
}

// AgentRuntime is the per-(agent value, session) runtime entry: the loop
// goroutine's shared state. Its lifecycle state is never stored — only the
// facts below are, and State projects them (design §3.4).
//
// After the loop ends the entry persists as a tombstone: State keeps
// reporting STOPPED/FAILED and Snapshot the last committed conversation for
// the rest of the session, like ExitedService (core/services.go).
type AgentRuntime struct {
	key  string
	name string

	// self is the agent handle the entry was created from: an honest dagql
	// instance of the agent value. Immutable after creation (any handle
	// carrying the same instance ID denotes the same entry, whatever its
	// seed re-derived to). The loop binds it into its context
	// (AgentToContext) so tools dispatched by a step can reach the calling
	// agent — the Agent! argument injection.
	self dagql.ObjectResult[*Agent]

	mu sync.Mutex

	// Facts, guarded by mu. State() is a pure projection of these.
	started       bool                         // the loop goroutine was launched
	stepping      bool                         // a Step is in flight
	turnOpen      bool                         // a turn has consumed input and not yet resolved
	paused        bool                         // pause requested: park without draining or stepping
	stopRequested bool                         // a graceful stop was requested
	done          bool                         // the loop has ended (tombstone)
	sealed        bool                         // stop sealed the tombstone: projects STOPPED over FAILED
	stopReason    AgentStopReason              // who ended it: a caller's stop, or session teardown
	loopErr       error                        // why the loop failed, if it did
	last          dagql.ObjectResult[*LLM]     // last committed conversation (initially the seed)
	cancel        context.CancelCauseFunc      // kills the loop context (set on start)
	stepCancel    context.CancelCauseFunc      // cancels the in-flight step's context (set while stepping)
	scopeLease    *engine.ClientLifecycleLease // launching scope; retained by tombstones until relaunch/teardown

	// Mailbox, guarded by mu. mailbox is the FIFO of pending (not yet
	// consumed) message IDs; messages holds every record ever enqueued —
	// records are never deleted, keeping await idempotent for the rest of
	// the session; consumed is the set of records drained into the current
	// turn, awaiting its reply. interruptSeq increments whenever Interrupt
	// discards the mailbox, letting a drain that already popped a record detect
	// the race before committing it.
	mailbox      []string
	messages     map[string]*agentMessageRecord
	consumed     []*agentMessageRecord
	interruptSeq uint64

	// wake unblocks the loop when it is idle. Stop and enqueue poke it;
	// the loop re-checks the facts (stop request, mailbox) after every
	// wake, so spurious pokes are harmless.
	wake chan struct{}

	// stateChanged is closed and replaced on every fact transition, so
	// WaitFor can block on transitions without polling.
	stateChanged chan struct{}

	// Telemetry directory plumbing (design §3.3), guarded by mu.
	//
	// spanCtx carries the loop span, set when the loop starts, and is what
	// state records are attributed to. It deliberately outlives the span
	// itself: a record emitted after the span ended still carries its span
	// ID, which is how the tombstone-sealing transition (Stop on a FAILED
	// agent, after the loop returned) still reaches a client's roster.
	//
	// emittedState is the last state published on that channel, so a
	// transition that does not change the PROJECTION emits nothing —
	// transitionLocked fires on every fact change, of which only a fraction
	// are state changes. emittedSnapshot is the same guard for the snapshot
	// channel: a relaunched loop re-publishes the conversation it resumes
	// from, and a client gains nothing from the duplicate.
	spanCtx         context.Context
	emittedState    AgentState
	emittedSnapshot string
}

func (rt *AgentRuntime) releaseClientScope() {
	rt.mu.Lock()
	lease := rt.scopeLease
	rt.scopeLease = nil
	rt.mu.Unlock()
	lease.Release()
}

// Name returns the agent's display name.
func (rt *AgentRuntime) Name() string {
	return rt.name
}

// transitionLocked applies a fact mutation and broadcasts it to any WaitFor
// blocked on stateChanged. Must be called with rt.mu held.
//
// It is also the single choke point for state telemetry: every fact change
// in the runtime goes through here, so publishing the projection here is
// what guarantees a client's roster can never miss a transition.
func (rt *AgentRuntime) transitionLocked(mut func()) {
	mut()
	close(rt.stateChanged)
	rt.stateChanged = make(chan struct{})
	rt.publishStateLocked()
}

// publishStateLocked emits a state record when the PROJECTED state has
// changed since the last one, attributed to the agent's loop span. Must be
// called with rt.mu held.
//
// Emitting under the lock is safe and deliberate: the OTel log pipeline is a
// non-blocking batch processor, and serializing emission with the transition
// that caused it is what keeps the published sequence faithful to the
// runtime's actual history (two racing transitions can never publish out of
// order).
func (rt *AgentRuntime) publishStateLocked() {
	if rt.spanCtx == nil {
		// The loop has not started, so there is no span to attribute state
		// to. Nothing is lost: start() publishes the initial state, and an
		// inert entry's state is IDLE — exactly what a client projects for
		// an agent it has never seen a record for.
		return
	}
	state := rt.stateLocked()
	if state == rt.emittedState {
		return
	}
	rt.emittedState = state
	stopReason := rt.stopReason
	if state != AgentStateStopped {
		// The reason belongs to the terminal record and nowhere else: a
		// FAILED tombstone that a stop later seals publishes its reason on
		// the sealing record, not on the failure that preceded it.
		stopReason = ""
	}
	EmitAgentState(rt.spanCtx, state, "", stopReason)
}

// commitLast commits next as the entry's last committed conversation and
// publishes its portable recipe digest — the resume anchor of §4.3 in
// hack/designs/resume-from-trace.md. Every advance of rt.last goes through
// here, which is what makes the published digest a fact about the runtime
// rather than an inference from whichever call spans happened to be emitted.
//
// Must be called with rt.mu held, from inside a transitionLocked mutation:
// the commit and the facts that go with it (a step landing, a message joining
// the turn) are one transition, and splitting them would let a reader observe
// a state that no longer matches the conversation it is reading.
func (rt *AgentRuntime) commitLast(ctx context.Context, next dagql.ObjectResult[*LLM]) {
	rt.last = next
	rt.publishSnapshotLocked(ctx)
}

// publishSnapshotLocked emits the portable recipe digest of the last committed
// conversation, skipping a digest already published. Must be called with
// rt.mu held.
//
// The PORTABLE recipe digest, deliberately: a post-evaluation result handle is
// an engine-local reference that dies with its session, while the runtime's raw
// recipe retains every superseded workspace and tool binding on its receiver
// chain. Replaying one of those stale bindings can re-run operations against
// today's live workspace (for example an old Workspace.withCommit after that
// commit was exported) even though the conversation no longer uses it.
// PortableRecipe flattens the conversation to its effective bindings and data,
// which is the same durability boundary used by saved sessions.
//
// Derivation is best-effort — an agent whose digest cannot be derived is still
// observable and addressable, it just cannot be resumed from, which is exactly
// how agentSpanAttrs treats the same failure.
func (rt *AgentRuntime) publishSnapshotLocked(ctx context.Context) {
	if rt.spanCtx == nil {
		// No span to attribute the record to yet. The loop publishes the
		// seed's digest as soon as it has one, so nothing is lost.
		return
	}
	if rt.last.Self() == nil {
		return
	}
	recipe, err := rt.last.Self().PortableRecipe(ctx)
	if err != nil {
		return
	}
	dig, err := recipe.RecipeDigest(ctx)
	if err != nil {
		return
	}
	if dig.String() == rt.emittedSnapshot {
		return
	}
	rt.emittedSnapshot = dig.String()
	EmitAgentSnapshot(rt.spanCtx, dig.String())
}

// State projects the entry's lifecycle state from its facts.
func (rt *AgentRuntime) State() AgentState {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.stateLocked()
}

func (rt *AgentRuntime) stateLocked() AgentState {
	switch {
	case rt.done && rt.loopErr != nil && !rt.sealed:
		// The loop failed and no stop sealed the tombstone: resume may
		// still retry it.
		return AgentStateFailed
	case rt.done:
		return AgentStateStopped
	case rt.stepping:
		// A pause or interrupt requested mid-step projects RUNNING until
		// the step actually lands: stepping is a live fact, and the state
		// never claims a park that hasn't happened.
		return AgentStateRunning
	case rt.paused:
		// Paused parks the loop even with a suspended turn or queued mail:
		// pause takes priority over pending work.
		return AgentStatePaused
	case rt.turnOpen:
		// A turn is suspended between steps (or awaiting its resolution):
		// the loop is about to continue it.
		return AgentStateRunning
	case len(rt.mailbox) > 0:
		// Mail has arrived but the loop hasn't opened a turn on it yet
		// (it is between the enqueue's wake and the drain): the agent is
		// about to run, and IDLE means "mailbox empty, turn complete" —
		// so this transient is RUNNING.
		return AgentStateRunning
	default:
		// Started with nothing in flight (blocked in receive), or created
		// but never started: both are IDLE — mailbox empty, no turn open.
		return AgentStateIdle
	}
}

// Snapshot returns the last committed conversation: the seed if the loop
// never stepped, otherwise the result of the last completed Step — always an
// honest dagql ID chain, since only instances produced by real Selects are
// ever stored.
func (rt *AgentRuntime) Snapshot() dagql.ObjectResult[*LLM] {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.last
}

// enqueue appends a message to the mailbox, computing its delivery evidence
// from the entry's facts at this instant, and wakes the loop if it is idle.
// A STOPPED tombstone is reopened from its preserved snapshot, so the new
// message restarts the loop instead of being rejected. A FAILED tombstone
// accepts with QUEUED delivery, since an explicit resume decides when to retry
// the failed step.
func (rt *AgentRuntime) enqueue(text string) (string, AgentMessageDelivery, error) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	stopped := rt.stateLocked() == AgentStateStopped
	msgID := identity.NewID()
	var delivery AgentMessageDelivery
	switch {
	case stopped:
		// A stopped loop has fully wound down before STOPPED is observable.
		// Reopen the entry before recording the message; Send starts the fresh
		// loop after enqueue, guaranteeing its first mailbox drain sees it.
		delivery = AgentMessageStarted
	case rt.done:
		// FAILED tombstone: the loop is gone until a resume retries it.
		// Accept rather than error — never silently drop — with QUEUED
		// evidence; awaitMessage projects the tombstone's failure until a
		// resume consumes the message.
		delivery = AgentMessageQueued
	case rt.paused:
		// Accepting but not draining: the message waits behind the pause
		// for a resume.
		delivery = AgentMessageQueued
	case rt.turnOpen || rt.stepping:
		// A turn is in flight: the message WILL be absorbed into it at
		// the next step boundary — the loop drains the mailbox at every
		// boundary, and turn end re-checks the mailbox before resolving,
		// so a message enqueued while turnOpen always joins that turn.
		delivery = AgentMessageSteered
	default:
		// Idle or never started: the message opens a new turn.
		delivery = AgentMessageStarted
	}
	rec := &agentMessageRecord{
		text:     text,
		delivery: delivery,
		done:     make(chan struct{}),
	}
	rt.transitionLocked(func() {
		if stopped {
			rt.resetForRelaunchLocked()
		}
		rt.messages[msgID] = rec
		rt.mailbox = append(rt.mailbox, msgID)
	})
	// Poke the idle receive; drop the poke if one is already pending (the
	// loop drains the whole mailbox per wake).
	select {
	case rt.wake <- struct{}{}:
	default:
	}
	return msgID, delivery, nil
}

// resolveLocked finalizes a message record: reply/err become readable and
// every awaiter blocked on done unblocks. Idempotent — the first resolution
// wins. Must be called with rt.mu held; close never blocks, so holding the
// mutex here is safe.
func (rt *AgentRuntime) resolveLocked(rec *agentMessageRecord, reply string, err error) {
	if rec.resolved {
		return
	}
	rec.resolved = true
	rec.reply = reply
	rec.err = err
	close(rec.done)
}

func (rt *AgentRuntime) interruptedMessageError() error {
	return fmt.Errorf("agent %q interrupted before consuming this message: %w", rt.name, errAgentInterrupted)
}

// failMessage resolves a single record with an error, outside any held
// mutex — used when consuming a message fails before it joins a turn.
func (rt *AgentRuntime) failMessage(rec *agentMessageRecord, err error) {
	rt.mu.Lock()
	rt.transitionLocked(func() {
		rt.resolveLocked(rec, "", err)
	})
	rt.mu.Unlock()
}

// awaitMessage blocks until the given message record resolves, returning
// its reply or error. Idempotent: records persist for the session, so a
// canceled request can re-await and read the same result, and concurrent
// awaiters share it. If the runtime reaches a tombstone while the record is
// still unresolved (a FAILED loop leaves unconsumed mail queued for a
// resume to drain), awaiting projects that context instead of blocking
// forever — without resolving the record, so a later resume can still
// consume the message and a re-await then reads its real reply.
func (rt *AgentRuntime) awaitMessage(ctx context.Context, msgID string) (string, error) {
	for {
		rt.mu.Lock()
		rec, found := rt.messages[msgID]
		if !found {
			rt.mu.Unlock()
			return "", fmt.Errorf("agent %q has no record of this message", rt.name)
		}
		if rec.resolved {
			reply, err := rec.reply, rec.err
			rt.mu.Unlock()
			return reply, err
		}
		if rt.done {
			loopErr := rt.loopErr
			rt.mu.Unlock()
			if loopErr != nil {
				return "", fmt.Errorf("agent %q failed before consuming this message: %w", rt.name, loopErr)
			}
			return "", fmt.Errorf("agent %q stopped before consuming this message", rt.name)
		}
		done := rec.done
		ch := rt.stateChanged
		rt.mu.Unlock()

		select {
		case <-ctx.Done():
			return "", context.Cause(ctx)
		case <-done:
			// Resolved: re-read under the lock.
		case <-ch:
			// Fact transition (possibly to a terminal state): re-check.
		}
	}
}

// drainMailbox consumes every queued message into the current turn: each is
// recorded as a real withPrompt Select on the last-committed conversation —
// the honest-chain discipline: a message that influences the agent appears
// in its history exactly as the model saw it (design §3.2, influence ⇔
// append) — committed as the new snapshot, and marked consumed by the
// in-flight turn. The entry mutex is never held across a Select.
func (rt *AgentRuntime) drainMailbox(ctx context.Context) error {
	for {
		rt.mu.Lock()
		if err := ctx.Err(); err != nil {
			// Canceled: surface the context error; the loop's caller treats
			// it as a stop, not a failure.
			rt.mu.Unlock()
			return err
		}
		if rt.stopRequested || rt.paused || len(rt.mailbox) == 0 {
			// Stop settles the queue itself; pause parks without draining
			// (pause takes priority over pending work).
			rt.mu.Unlock()
			return nil
		}
		msgID := rt.mailbox[0]
		rt.mailbox = rt.mailbox[1:]
		rec := rt.messages[msgID]
		inst := rt.last
		interruptSeq := rt.interruptSeq
		rt.mu.Unlock()

		srv, err := CurrentDagqlServer(ctx)
		if err != nil {
			rt.failMessage(rec, err)
			return err
		}
		var next dagql.ObjectResult[*LLM]
		if err := srv.Select(ctx, inst, &next, dagql.Selector{
			Field: "withPrompt",
			Args: []dagql.NamedInput{
				{
					Name:  "prompt",
					Value: dagql.NewString(rec.text),
				},
			},
		}); err != nil {
			// The message was popped but never joined the turn: resolve
			// it with the failure rather than leaving awaiters to hang on
			// a record nothing will ever touch again.
			err = fmt.Errorf("record message as prompt: %w", err)
			rt.failMessage(rec, err)
			return err
		}

		rt.mu.Lock()
		if rt.interruptSeq != interruptSeq {
			// Interrupt raced with the pure withPrompt Select after this record
			// left the mailbox. It never influenced the model and must follow the
			// rest of the discarded queue, not survive invisibly on the snapshot.
			rt.transitionLocked(func() {
				rt.resolveLocked(rec, "", rt.interruptedMessageError())
			})
			rt.mu.Unlock()
			continue
		}
		rt.transitionLocked(func() {
			rt.commitLast(ctx, next)
			rec.consumed = true
			rt.consumed = append(rt.consumed, rec)
			rt.turnOpen = true
		})
		rt.mu.Unlock()
	}
}

// rehydrate publishes the restored instance's identity and sets the facts
// its restored state projects from. The entry is fresh (Rehydrate is the only
// caller, holding the only reference), and its loop is deliberately NOT
// started: a restored agent spends no tokens until somebody prompts it.
//
// The identity span is the reason this is not just a field assignment.
// Telemetry is the directory (async-agents §3.3), so an agent with no span in
// the CURRENT trace is invisible to every client watching it — and a restored
// agent that is never started would publish none. It therefore opens and
// immediately ends a span carrying the same attributes a loop span does, and
// keeps its context: AgentRuntime.spanCtx already outlives its span
// deliberately, which is what lets the state and snapshot records below —
// and every later transition — reach a roster. A later start opens the real
// loop span; both carry the same dagger.io/agent.id, and a client unions
// them into one entry by construction.
func (rt *AgentRuntime) rehydrate(ctx context.Context, state AgentState, loopErr string) error {
	// A restored snapshot is not yet demonstrably independent of its DagQL
	// runtime, so retain a visible durable agent lease even without a loop.
	scopeCtx, scopeLease, err := engine.DetachClientScope(ctx, engine.ClientLeaseAgent, rt.key)
	if err != nil {
		return fmt.Errorf("retain rehydrated agent scope: %w", err)
	}
	// Detached from the request: the context is retained past this call, and
	// records emitted on a canceled one would be publishing into a corpse.
	spanCtx, span := Tracer(scopeCtx).Start(scopeCtx,
		fmt.Sprintf("agent: %s", rt.name),
		agentSpanAttrs(ctx, rt.name, rt.self)...)
	span.End()

	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.scopeLease = scopeLease
	rt.spanCtx = spanCtx
	switch state {
	case AgentStatePaused:
		rt.paused = true
	case AgentStateFailed:
		// FAILED projects from done + an error, so the error is a fact, not
		// a label: a resume retries from the snapshot, and mail queued
		// meanwhile projects this error rather than blocking. A restore with
		// no error text still has to hold one, or the projection would read
		// STOPPED and foreclose the retry.
		if loopErr == "" {
			loopErr = "failed in a previous session"
		}
		rt.done = true
		rt.loopErr = errors.New(loopErr)
	case AgentStateStopped:
		// A dormant tombstone. Only an EXPLICIT stop restores this way — a
		// session-teardown stop says nothing about what the user wanted, so a
		// client restores the state held before it (§3.1) and never asks for
		// STOPPED here. A later send or resume relaunches from rt.last.
		rt.done = true
		rt.sealed = true
		rt.stopRequested = true
		rt.stopReason = AgentStopExplicit
	}
	rt.publishStateLocked()
	rt.publishSnapshotLocked(scopeCtx)
	return nil
}

// start launches an evaluation loop for a live entry, once. Subsequent calls
// are no-ops, as are calls on tombstones; Resume is the explicit relaunch path
// for failed and stopped entries.
func (rt *AgentRuntime) start(ctx context.Context) error {
	rt.mu.Lock()
	if rt.started || rt.done {
		rt.mu.Unlock()
		return nil
	}
	// The loop must outlive this request, but it must retain the exact client
	// scope that launched it. A tombstone's previous scope is deliberately
	// retained until this replacement has been acquired successfully.
	loopBase, scopeLease, err := engine.DetachClientScope(ctx, engine.ClientLeaseAgent, rt.key)
	if err != nil {
		rt.mu.Unlock()
		return fmt.Errorf("acquire agent client scope: %w", err)
	}
	loopCtx, cancel := context.WithCancelCause(loopBase)
	previousLease := rt.scopeLease
	rt.scopeLease = scopeLease
	rt.cancel = cancel
	rt.transitionLocked(func() {
		rt.started = true
	})
	rt.mu.Unlock()
	previousLease.Release()

	go rt.loop(loopCtx)
	return nil
}

// errAgentInterrupted is the cancellation cause Interrupt uses on the
// in-flight step's context. The loop uses it to distinguish "step preempted
// by interrupt" — park PAUSED with the turn suspended and its consumed
// messages still pending — from "loop context canceled by stop or session
// teardown", which tombstones as STOPPED.
var errAgentInterrupted = errors.New("agent interrupted")

// loop is the agent's evaluation loop, run on a goroutine under a detached
// context. It mirrors LLM.Loop (core/llm.go) with the mailbox spliced in:
// drain queued messages onto the conversation (each recorded as a
// withPrompt Select), step while anything is pending — draining again at
// every step boundary, so mid-turn messages steer the in-flight turn — and
// when the turn ends, resolve every message it consumed with the turn's
// reply, then block in receive (state: IDLE) until mail or a stop arrives.
//
// A pause parks the loop in that same receive without draining or stepping,
// even mid-turn: the suspended turn (and any queued mail) waits for a
// resume, which wakes the loop to continue exactly where it left off.
func (rt *AgentRuntime) loop(ctx context.Context) {
	// Bind the agent's own handle into the loop context: every Step below —
	// and thus every tool dispatched within one — descends from it, so a
	// module tool declaring an `Agent!` argument is auto-injected with THIS
	// agent (AgentToContext -> ModuleFunction.loadAgentArg), the
	// child->parent channel of design §3.1. Covers the Resume-retry
	// relaunch too, which re-enters here on a fresh detached context.
	ctx = AgentToContext(ctx, rt.self)
	ctx, span := Tracer(ctx).Start(ctx, fmt.Sprintf("agent: %s", rt.name),
		agentSpanAttrs(ctx, rt.name, rt.self)...)

	// Publish the loop span as the agent's state channel, and seed it with
	// the state the loop is starting in, plus the digest of the conversation
	// it is starting FROM — so an agent that never steps still carries a
	// resume anchor, and a consumer needs no special case for its absence.
	// Everything after this point flows through transitionLocked, which
	// publishes on every change of the projection (design §3.3: telemetry is
	// the directory) and, via commitLast, on every commit.
	rt.mu.Lock()
	rt.spanCtx = ctx
	rt.emittedState = ""
	rt.publishStateLocked()
	rt.publishSnapshotLocked(ctx)
	rt.mu.Unlock()

	var loopErr error
	defer func() {
		// Make a final failure part of the permanent conversation before waking
		// message awaiters. This gives the focused-agent TUI a durable error line
		// to render instead of racing the await's transient prompt error.
		emitAgentFailure(ctx, loopErr)

		rt.mu.Lock()
		rt.transitionLocked(func() {
			// Settle the mailbox in the same transition that makes the
			// tombstone observable, so no awaiter sees a terminal state
			// with records still apparently in flight.
			if loopErr != nil {
				// FAILED: messages consumed by the failed turn resolve
				// with its error. Unconsumed mail stays queued for a
				// resume to pick up; awaitMessage projects the failure
				// from the tombstone meanwhile.
				for _, rec := range rt.consumed {
					rt.resolveLocked(rec, "", fmt.Errorf("agent %q failed during the turn that consumed this message: %w", rt.name, loopErr))
				}
				rt.consumed = nil
			} else {
				// STOPPED: no turn will ever consume anything again, so
				// consumed and queued messages alike resolve with a stop
				// error.
				for _, rec := range rt.consumed {
					rt.resolveLocked(rec, "", fmt.Errorf("agent %q stopped before completing the turn that consumed this message", rt.name))
				}
				rt.consumed = nil
				for _, msgID := range rt.mailbox {
					if rec := rt.messages[msgID]; rec != nil {
						rt.resolveLocked(rec, "", fmt.Errorf("agent %q stopped before consuming this message", rt.name))
					}
				}
				rt.mailbox = nil
			}
			rt.stepping = false
			rt.stepCancel = nil
			rt.turnOpen = false
			rt.done = true
			rt.loopErr = loopErr
		})
		rt.mu.Unlock()
		if loopErr != nil {
			span.SetStatus(codes.Error, loopErr.Error())
		}
		span.End()
	}()

	for {
		// Run the turn: drain queued mail into the conversation, then step
		// while anything is pending, draining again at every step boundary
		// — a message arriving mid-turn joins the in-flight turn (its
		// STEERED delivery evidence) rather than waiting behind it.
		for {
			rt.mu.Lock()
			if rt.stopRequested || ctx.Err() != nil {
				rt.mu.Unlock()
				return
			}
			if rt.paused {
				// Pause takes priority over pending work: park without
				// draining or stepping, even mid-turn — a suspended turn
				// (turnOpen, pending input on the snapshot) waits for
				// resume to continue it.
				rt.mu.Unlock()
				break
			}
			rt.mu.Unlock()

			if err := rt.drainMailbox(ctx); err != nil {
				if ctx.Err() != nil {
					return
				}
				loopErr = err
				return
			}

			rt.mu.Lock()
			if rt.stopRequested || ctx.Err() != nil {
				rt.mu.Unlock()
				return
			}
			if rt.paused {
				// A pause raced in during the drain: whatever was drained
				// already joined the turn; park before stepping.
				rt.mu.Unlock()
				break
			}
			inst := rt.last
			if !inst.Self().HasPending() {
				rt.mu.Unlock()
				break
			}
			// Each step gets its own cancellable context so Interrupt can
			// preempt just the step: the loop context stays alive, which is
			// exactly what distinguishes an interrupt from a stop below.
			stepCtx, stepCancel := context.WithCancelCause(ctx)
			rt.transitionLocked(func() {
				rt.turnOpen = true
				rt.stepping = true
				rt.stepCancel = stepCancel
			})
			rt.mu.Unlock()

			// Step selects through the dagql server carried by the resolver's
			// context (preserved by WithoutCancel above), so every committed
			// snapshot is an honest ID chain — the same mechanism LLM.Loop
			// uses.
			next, err := inst.Self().Step(stepCtx, inst, 0)
			// Read the cause before the cleanup cancel below overwrites it:
			// errAgentInterrupted means Interrupt preempted this step while
			// the loop context stayed alive.
			interrupted := errors.Is(context.Cause(stepCtx), errAgentInterrupted)
			stepCancel(nil)
			rt.mu.Lock()
			rt.transitionLocked(func() {
				rt.stepping = false
				rt.stepCancel = nil
				// Step returns the last successfully recorded state even on
				// error, so committing unconditionally preserves the
				// completed prefix (mirrors LLM.Loop).
				rt.commitLast(ctx, next)
			})
			rt.mu.Unlock()
			if err != nil {
				if ctx.Err() != nil {
					// Loop context canceled (stop --kill or session
					// teardown): keep the prefix, end as STOPPED rather
					// than FAILED, mirroring LLM.Loop's interrupt
					// semantics.
					return
				}
				if interrupted {
					// Interrupt preempted the step: the completed prefix is
					// committed, the turn stays open with its consumed
					// messages pending (their input is still pending on the
					// snapshot), and Interrupt set paused — loop back to the
					// pause check and park. Resume re-steps the pending
					// input, continuing the turn.
					continue
				}
				loopErr = err
				return
			}
		}

		// Turn end — but a message that raced in since the final drain was
		// enqueued while the turn was still open (its delivery evidence
		// says STEERED), so it belongs to this turn: re-check the mailbox
		// under the same lock that would resolve it, and keep stepping
		// instead of resolving if anything is queued. This is what makes
		// STEERED truthful: enqueue and this check are serialized on
		// rt.mu, so a STEERED message is either already consumed or still
		// in the mailbox here, never silently deferred to the next turn.
		//
		// A paused park skips both: no turn resolution (a suspended turn
		// resolves only after resume completes it — pause never cuts a
		// turn short) and no mailbox continuation (queued mail waits for
		// resume too).
		rt.mu.Lock()
		if !rt.paused && len(rt.mailbox) > 0 && !rt.stopRequested && ctx.Err() == nil {
			rt.mu.Unlock()
			continue
		}
		if rt.turnOpen && !rt.paused {
			// Resolve every message the turn consumed with its final
			// reply: reply correlation follows the message to whichever
			// turn consumed it (design §3.2), not the clock.
			reply, _ := rt.last.Self().LastReply()
			rt.transitionLocked(func() {
				for _, rec := range rt.consumed {
					rt.resolveLocked(rec, reply, nil)
				}
				rt.consumed = nil
				rt.turnOpen = false
			})
		}
		rt.mu.Unlock()

		// Mailbox empty and turn complete (IDLE), or paused (PAUSED):
		// block in receive until an enqueue, a resume, or a stop request
		// pokes wake.
		select {
		case <-ctx.Done():
			return
		case <-rt.wake:
			// Re-check stop, pause, and the mailbox at the top of the loop.
		}
	}
}

// Pause marks the runtime paused. The in-flight step (if any) completes;
// after that the loop parks without draining the mailbox or stepping, even
// if input is still pending — pause takes priority over pending work, so a
// mid-turn pause suspends the turn for resume to continue. Messages sent
// while paused enqueue with QUEUED delivery.
//
// Pausing a never-started agent leaves its (lazily created) entry paused,
// so a later start or send parks immediately. Pausing a FAILED tombstone is
// allowed — it only sets the flag; resume decides the retry. Pausing a
// STOPPED tombstone fails: a released runtime has nothing to pause.
func (rt *AgentRuntime) Pause() error {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.stateLocked() == AgentStateStopped {
		return fmt.Errorf("agent %q is stopped; a released runtime cannot be paused", rt.name)
	}
	if rt.paused {
		return nil
	}
	rt.transitionLocked(func() {
		rt.paused = true
	})
	return nil
}

// Interrupt preempts the in-flight step and pauses: it cancels the step's
// context (the completed prefix stays committed — Step returns the last
// recorded state on cancellation) and sets the paused flag. Messages already
// consumed into the interrupted turn stay pending, but unconsumed mailbox
// entries are discarded: they never influenced the model and Ctrl-C abandons
// queued follow-up input. Resume continues from the completed prefix and turn
// end resolves the consumed messages normally. On an idle, never-started, or
// FAILED agent there is no step to preempt, so interrupt degenerates to pause.
// Interrupting a STOPPED tombstone fails.
func (rt *AgentRuntime) Interrupt() error {
	rt.mu.Lock()
	if rt.stateLocked() == AgentStateStopped {
		rt.mu.Unlock()
		return fmt.Errorf("agent %q is stopped; a released runtime cannot be interrupted", rt.name)
	}
	rt.transitionLocked(func() {
		rt.paused = true
		rt.interruptSeq++
		for _, msgID := range rt.mailbox {
			rec := rt.messages[msgID]
			rt.resolveLocked(rec, "", rt.interruptedMessageError())
		}
		rt.mailbox = nil
	})
	// Read the step cancel AFTER committing paused and discarding the queue,
	// under the same lock hold: once paused is set the loop starts no new step,
	// so this either targets the one step in flight or is nil (nothing to
	// preempt).
	stepCancel := rt.stepCancel
	rt.mu.Unlock()

	if stepCancel != nil {
		stepCancel(errAgentInterrupted)
	}
	return nil
}

// resetForRelaunchLocked clears the facts left by a completed loop while
// preserving its committed conversation and message history. The caller must
// launch a fresh loop after releasing rt.mu.
func (rt *AgentRuntime) resetForRelaunchLocked() {
	rt.started = false
	rt.stepping = false
	rt.turnOpen = false
	rt.paused = false
	rt.stopRequested = false
	rt.done = false
	rt.sealed = false
	rt.stopReason = ""
	rt.loopErr = nil
	rt.cancel = nil
	rt.stepCancel = nil
}

// Resume clears the paused flag and wakes the loop: the suspended turn (if
// any) continues from the last committed step, and queued mail drains.
//
// On a FAILED tombstone, resume retries (design §3.5, supervision-lite):
// the loop relaunches from the last committed snapshot — the failed step's
// input is still pending on it, so the loop naturally retries the step, and
// QUEUED mail drains into the turn. A STOPPED tombstone relaunches from the
// same preserved snapshot, keeping the instance and runtime entry. On a
// running or idle non-paused agent resume is a no-op.
func (rt *AgentRuntime) Resume(ctx context.Context) error {
	rt.mu.Lock()
	switch rt.stateLocked() {
	case AgentStateStopped, AgentStateFailed:
		// Relaunch from the last committed snapshot under the NEW caller's
		// scope. The old tombstone lease remains held until acquisition wins.
		loopBase, scopeLease, err := engine.DetachClientScope(ctx, engine.ClientLeaseAgent, rt.key)
		if err != nil {
			rt.mu.Unlock()
			return fmt.Errorf("acquire agent relaunch scope: %w", err)
		}
		loopCtx, cancel := context.WithCancelCause(loopBase)
		previousLease := rt.scopeLease
		rt.transitionLocked(func() {
			rt.resetForRelaunchLocked()
			rt.scopeLease = scopeLease
			rt.cancel = cancel
			rt.started = true
		})
		rt.mu.Unlock()
		previousLease.Release()
		go rt.loop(loopCtx)
		return nil
	}
	if !rt.paused {
		rt.mu.Unlock()
		return nil
	}
	rt.transitionLocked(func() {
		rt.paused = false
	})
	rt.mu.Unlock()

	// Wake the parked loop; it re-checks the facts (suspended turn, queued
	// mail) and continues where the pause left off.
	select {
	case rt.wake <- struct{}{}:
	default:
	}
	return nil
}

// Reseed commits the given conversation as the entry's committed history.
// Queued mail stays queued (and drains onto the NEW conversation when a turn
// next opens — a message is addressed to the agent, not to a particular
// history), and a FAILED tombstone keeps its error, so resume retries from the
// new conversation.
//
// A paused suspended turn is the one deliberate exception to "touching nothing
// else": reseed abandons that turn, resolving the messages it consumed and
// clearing turnOpen before committing next. This is the rewind primitive used
// by inline prompt editing — Interrupt first parks the step and discards its
// unconsumed mailbox, then reseed atomically cuts the consumed prompt and all
// later history from the active conversation. The pause itself remains, so the
// edited prompt's send queues and Resume starts it from next.
//
// The guards exist because the swap races the loop's own commits otherwise: a
// step in flight (or a drain about to open a turn) commits a conversation
// derived from the one it read, and a swap between the read and the commit
// would be silently overwritten. So reseed requires the loop parked — idle,
// paused, failed, or never started. A non-paused suspended turn remains refused,
// and a stopped agent is refused until send or resume relaunches its loop.
func (rt *AgentRuntime) Reseed(ctx context.Context, next dagql.ObjectResult[*LLM]) error {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	switch rt.stateLocked() {
	case AgentStateStopped:
		return fmt.Errorf("agent %q is stopped; a released runtime's conversation cannot be replaced", rt.name)
	case AgentStateRunning:
		return fmt.Errorf("agent %q is mid-turn; wait for the turn to finish (or interrupt it) before reseeding", rt.name)
	}
	if rt.turnOpen && !rt.paused {
		return fmt.Errorf("agent %q has a suspended turn; pause or interrupt it before reseeding", rt.name)
	}
	rt.transitionLocked(func() {
		if rt.turnOpen {
			err := fmt.Errorf("agent %q rewound before completing the turn that consumed this message", rt.name)
			for _, rec := range rt.consumed {
				rt.resolveLocked(rec, "", err)
			}
			rt.consumed = nil
			rt.turnOpen = false
		}
		rt.commitLast(ctx, next)
	})
	return nil
}

// Stop ends the agent's loop and waits for the tombstone. Graceful stop
// (kill=false) lets an in-flight step finish before the loop ends; kill
// cancels the loop context immediately (the Step cancellation path already
// preserves the completed prefix). Idempotent on tombstones — though
// stopping a FAILED tombstone seals it: no resume will retry it anymore, so
// its queued mail settles with stop errors and its state projects STOPPED
// from then on.
//
// reason records who ended it (a caller, or session teardown) and rides the
// terminal state record: the projection is STOPPED either way, and only the
// reason lets a client restoring the trace tell a dismissal from a teardown.
func (rt *AgentRuntime) Stop(ctx context.Context, kill bool, cause error, reason AgentStopReason) error {
	rt.mu.Lock()
	if rt.done {
		if rt.stateLocked() == AgentStateFailed {
			// A FAILED tombstone still admits a resume-retry and may hold
			// QUEUED mail waiting on one. Stop forecloses the retry:
			// settle the queue in the same transition that seals the
			// tombstone, so no awaiter sees STOPPED with mail apparently
			// still in flight.
			rt.transitionLocked(func() {
				for _, msgID := range rt.mailbox {
					if rec := rt.messages[msgID]; rec != nil {
						rt.resolveLocked(rec, "", fmt.Errorf("agent %q stopped before consuming this message", rt.name))
					}
				}
				rt.mailbox = nil
				rt.sealed = true
				rt.stopReason = reason
			})
		}
		rt.mu.Unlock()
		return nil
	}
	if !rt.started {
		// Never started: there is no loop to wind down, so stopping is a
		// pure transition to the tombstone. Settle any mail that raced in
		// through Send's enqueue-then-start window, so awaiters aren't
		// left projecting the terminal state themselves.
		rt.transitionLocked(func() {
			for _, msgID := range rt.mailbox {
				if rec := rt.messages[msgID]; rec != nil {
					rt.resolveLocked(rec, "", fmt.Errorf("agent %q stopped before consuming this message", rt.name))
				}
			}
			rt.mailbox = nil
			rt.stopRequested = true
			rt.stopReason = reason
			rt.done = true
		})
		rt.mu.Unlock()
		return nil
	}
	rt.transitionLocked(func() {
		rt.stopRequested = true
		// Recorded before the loop winds down, so the tombstone transition
		// the loop's own defer performs already carries the reason.
		rt.stopReason = reason
	})
	cancel := rt.cancel
	rt.mu.Unlock()

	if kill {
		if cause == nil {
			cause = errors.New("agent killed")
		}
		cancel(cause)
	} else {
		// Wake the loop if it is idle; if it is mid-step, it re-checks
		// stopRequested at the next step boundary.
		select {
		case rt.wake <- struct{}{}:
		default:
		}
	}

	// Block until the loop has actually ended, so a stop followed by a state
	// read deterministically observes the tombstone.
	for {
		rt.mu.Lock()
		done := rt.done
		ch := rt.stateChanged
		rt.mu.Unlock()
		if done {
			return nil
		}
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-ch:
		}
	}
}

// WaitFor blocks until the entry's projected state equals want, returning
// immediately if it already does. STOPPED and FAILED are both dormant rather
// than terminal: resume or send can relaunch the same entry, so waiting for a
// later state remains valid until the caller cancels.
func (rt *AgentRuntime) WaitFor(ctx context.Context, want AgentState) error {
	for {
		rt.mu.Lock()
		cur := rt.stateLocked()
		ch := rt.stateChanged
		rt.mu.Unlock()

		if cur == want {
			return nil
		}

		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-ch:
		}
	}
}
