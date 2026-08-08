package core

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
	"go.opentelemetry.io/otel/codes"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/internal/buildkit/identity"
)

// Agent is a conversation loop packaged as an addressable, long-lived entity
// within the session (hack/designs/async-agents.md §3).
//
// Like Service, Agent is a pure, content-addressed dagql value: the seed
// conversation plus a name. Starting it registers a runtime entry — loop
// goroutine on a detached context, computed state — in the session's
// AgentRuntimes table, one running instance per value digest. All runtime
// state lives in that table, never on the value.
type Agent struct {
	// Seed is the conversation the agent's evaluation loop starts from,
	// including its tools, workspace, and message history.
	Seed dagql.ObjectResult[*LLM]

	// Name is a display label and identity discriminator — not a
	// session-wide address. Two otherwise-identical agents with distinct
	// names are distinct values, and thus distinct running instances (the
	// fork(label:) role).
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
		"The loop failed; snapshot holds the completed prefix.")
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

// terminal reports whether the state is a tombstone: the runtime is released
// and no further transitions will occur (until a later phase adds resume).
func (state AgentState) terminal() bool {
	return state == AgentStateStopped || state == AgentStateFailed
}

// AgentMessage is the handle returned by Agent.send: it identifies one
// message record in an agent's runtime entry, and carries the delivery
// evidence computed at enqueue. Reply correlation rides this handle —
// await returns the final reply of whichever turn consumed the message
// (hack/designs/async-agents.md §3.2), which under multiple senders is the
// only non-racy way to pair a reply with a message.
type AgentMessage struct {
	// AgentKey is the registry key (the agent value's content digest) of
	// the runtime entry holding the message record.
	AgentKey digest.Digest
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
	// AgentMessageQueued is registered for schema completeness but is
	// unreachable until the pause phase lands: without pause, every
	// message is either consumed by the in-flight turn (STEERED) or opens
	// a new one (STARTED); only a paused agent accepts without draining.
	AgentMessageQueued = AgentMessageDeliveries.Register("QUEUED",
		"The message is queued behind the in-flight turn, awaiting a resume.")
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
// session, following the Services registry model (core/services.go): one
// running instance per agent value digest, entries persisting as tombstones
// after their loop ends so state and the last snapshot stay readable for the
// rest of the session (like ExitedService).
//
// The registry itself is session-scoped — created alongside Services in the
// session state (engine/server/session.go) — so keys are just the agent
// value's digest, with no session component.
type AgentRuntimes struct {
	entries map[digest.Digest]*AgentRuntime
	mu      sync.Mutex
}

// NewAgentRuntimes returns a new, empty AgentRuntimes registry.
func NewAgentRuntimes() *AgentRuntimes {
	return &AgentRuntimes{
		entries: map[digest.Digest]*AgentRuntime{},
	}
}

func agentKey(ctx context.Context, agent dagql.ObjectResult[*Agent]) (digest.Digest, error) {
	dig, err := agent.ContentPreferredDigest(ctx)
	if err != nil {
		return "", fmt.Errorf("agent digest: %w", err)
	}
	return dig, nil
}

// Get returns the runtime entry for the given agent value, if one exists.
// It never creates an entry: a never-started agent has no runtime, and its
// observable state (IDLE, snapshot == seed) is projected from that absence.
func (ars *AgentRuntimes) Get(ctx context.Context, agent dagql.ObjectResult[*Agent]) (*AgentRuntime, bool, error) {
	key, err := agentKey(ctx, agent)
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
func (ars *AgentRuntimes) GetOrCreate(ctx context.Context, agent dagql.ObjectResult[*Agent]) (*AgentRuntime, error) {
	key, err := agentKey(ctx, agent)
	if err != nil {
		return nil, err
	}
	ars.mu.Lock()
	defer ars.mu.Unlock()
	if rt, found := ars.entries[key]; found {
		return rt, nil
	}
	rt := &AgentRuntime{
		key:      key,
		name:     agent.Self().Name,
		last:     agent.Self().Seed,
		messages: map[string]*agentMessageRecord{},
		// wake has a single slot: it only needs to record "something changed,
		// re-check" (a stop request or a mailbox enqueue), not carry
		// payloads — the loop re-reads the facts after every wake.
		wake:         make(chan struct{}, 1),
		stateChanged: make(chan struct{}),
	}
	ars.entries[key] = rt
	return rt, nil
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
	rt.start(ctx)
	return rt, nil
}

// Send is the central enqueue path (design §3.3): it enqueues a message
// into the agent's mailbox and returns its handle, starting the agent's
// loop if it was never started (signal-with-start, Temporal's lesson — a
// message to an unstarted agent must start it rather than be lost).
//
// Sending to a STOPPED or FAILED tombstone fails: without resume (a later
// phase), no turn will ever consume the message, so accepting it would be a
// silent drop — the one thing send promises never to do.
func (ars *AgentRuntimes) Send(ctx context.Context, agent dagql.ObjectResult[*Agent], text string) (*AgentMessage, error) {
	rt, err := ars.GetOrCreate(ctx, agent)
	if err != nil {
		return nil, err
	}
	msgID, delivery, err := rt.enqueue(text)
	if err != nil {
		return nil, err
	}
	// No-op if the loop is already running. Enqueue-then-start (rather
	// than the reverse) guarantees the loop's very first mailbox check
	// sees the message; in the running case the enqueue's wake poke covers
	// delivery.
	rt.start(ctx)
	return &AgentMessage{
		AgentKey:  rt.key,
		AgentName: rt.name,
		MessageID: msgID,
		Delivery:  delivery,
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
func (ars *AgentRuntimes) KillAll(ctx context.Context, cause error) error {
	ars.mu.Lock()
	entries := make([]*AgentRuntime, 0, len(ars.entries))
	for _, rt := range ars.entries {
		entries = append(entries, rt)
	}
	ars.mu.Unlock()

	var errs error
	for _, rt := range entries {
		if err := rt.Stop(ctx, true, cause); err != nil {
			errs = errors.Join(errs, err)
		}
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
	key  digest.Digest
	name string

	mu sync.Mutex

	// Facts, guarded by mu. State() is a pure projection of these.
	started       bool                     // the loop goroutine was launched
	stepping      bool                     // a Step is in flight
	turnOpen      bool                     // a turn has consumed input and not yet resolved
	stopRequested bool                     // a graceful stop was requested
	done          bool                     // the loop has ended (tombstone)
	loopErr       error                    // why the loop failed, if it did
	last          dagql.ObjectResult[*LLM] // last committed conversation (initially the seed)
	cancel        context.CancelCauseFunc  // kills the loop context (set on start)

	// Mailbox, guarded by mu. mailbox is the FIFO of pending (not yet
	// consumed) message IDs; messages holds every record ever enqueued —
	// records are never deleted, keeping await idempotent for the rest of
	// the session; consumed is the set of records drained into the current
	// turn, awaiting its reply.
	mailbox  []string
	messages map[string]*agentMessageRecord
	consumed []*agentMessageRecord

	// wake unblocks the loop when it is idle. Stop and enqueue poke it;
	// the loop re-checks the facts (stop request, mailbox) after every
	// wake, so spurious pokes are harmless.
	wake chan struct{}

	// stateChanged is closed and replaced on every fact transition, so
	// WaitFor can block on transitions without polling.
	stateChanged chan struct{}
}

// Name returns the agent's display name.
func (rt *AgentRuntime) Name() string {
	return rt.name
}

// transitionLocked applies a fact mutation and broadcasts it to any WaitFor
// blocked on stateChanged. Must be called with rt.mu held.
func (rt *AgentRuntime) transitionLocked(mut func()) {
	mut()
	close(rt.stateChanged)
	rt.stateChanged = make(chan struct{})
}

// State projects the entry's lifecycle state from its facts.
func (rt *AgentRuntime) State() AgentState {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.stateLocked()
}

func (rt *AgentRuntime) stateLocked() AgentState {
	switch {
	case rt.done && rt.loopErr != nil:
		return AgentStateFailed
	case rt.done:
		return AgentStateStopped
	case rt.stepping, rt.turnOpen:
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
// It fails on tombstones: a released runtime will never consume the message.
func (rt *AgentRuntime) enqueue(text string) (string, AgentMessageDelivery, error) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.done {
		state := rt.stateLocked()
		if rt.loopErr != nil {
			return "", "", fmt.Errorf("agent %q is %s; resume arrives in a later phase (loop error: %w)", rt.name, state, rt.loopErr)
		}
		return "", "", fmt.Errorf("agent %q is %s; resume arrives in a later phase", rt.name, state)
	}
	msgID := identity.NewID()
	var delivery AgentMessageDelivery
	switch {
	case rt.turnOpen || rt.stepping:
		// A turn is in flight: the message WILL be absorbed into it at
		// the next step boundary — the loop drains the mailbox at every
		// boundary, and turn end re-checks the mailbox before resolving,
		// so a message enqueued while turnOpen always joins that turn.
		delivery = AgentMessageSteered
	default:
		// Idle or never started: the message opens a new turn. (A PAUSED
		// runtime would make this QUEUED; pause arrives in a later phase,
		// so that value is registered but unreachable.)
		delivery = AgentMessageStarted
	}
	rec := &agentMessageRecord{
		text:     text,
		delivery: delivery,
		done:     make(chan struct{}),
	}
	rt.transitionLocked(func() {
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
// awaiters share it. If the runtime reaches a terminal state while the
// record is still unresolved (a FAILED loop leaves unconsumed mail queued
// for a later resume phase), awaiting fails with that context instead of
// blocking forever.
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
		if rt.stopRequested || len(rt.mailbox) == 0 {
			rt.mu.Unlock()
			return nil
		}
		msgID := rt.mailbox[0]
		rt.mailbox = rt.mailbox[1:]
		rec := rt.messages[msgID]
		inst := rt.last
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
		rt.transitionLocked(func() {
			rt.last = next
			rec.consumed = true
			rt.consumed = append(rt.consumed, rec)
			rt.turnOpen = true
		})
		rt.mu.Unlock()
	}
}

// start launches the evaluation loop, once. Subsequent calls are no-ops,
// including on tombstones (a stopped agent stays stopped; a later phase adds
// resume).
func (rt *AgentRuntime) start(ctx context.Context) {
	rt.mu.Lock()
	if rt.started || rt.done {
		rt.mu.Unlock()
		return
	}
	// The loop must outlive this request: detach from the resolver's
	// cancellation but keep its values (query, dagql server, client
	// metadata), mirroring the detached service start (services.go).
	loopCtx, cancel := context.WithCancelCause(context.WithoutCancel(ctx))
	rt.cancel = cancel
	rt.transitionLocked(func() {
		rt.started = true
	})
	rt.mu.Unlock()

	go rt.loop(loopCtx)
}

// loop is the agent's evaluation loop, run on a goroutine under a detached
// context. It mirrors LLM.Loop (core/llm.go) with the mailbox spliced in:
// drain queued messages onto the conversation (each recorded as a
// withPrompt Select), step while anything is pending — draining again at
// every step boundary, so mid-turn messages steer the in-flight turn — and
// when the turn ends, resolve every message it consumed with the turn's
// reply, then block in receive (state: IDLE) until mail or a stop arrives.
func (rt *AgentRuntime) loop(ctx context.Context) {
	ctx, span := Tracer(ctx).Start(ctx, fmt.Sprintf("agent: %s", rt.name))

	var loopErr error
	defer func() {
		rt.mu.Lock()
		rt.transitionLocked(func() {
			// Settle the mailbox in the same transition that makes the
			// tombstone observable, so no awaiter sees a terminal state
			// with records still apparently in flight.
			if loopErr != nil {
				// FAILED: messages consumed by the failed turn resolve
				// with its error. Unconsumed mail stays queued for a
				// later resume phase to pick up; awaitMessage projects
				// the failure from the tombstone meanwhile.
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
			inst := rt.last
			if !inst.Self().HasPending() {
				rt.mu.Unlock()
				break
			}
			rt.transitionLocked(func() {
				rt.turnOpen = true
				rt.stepping = true
			})
			rt.mu.Unlock()

			// Step selects through the dagql server carried by the resolver's
			// context (preserved by WithoutCancel above), so every committed
			// snapshot is an honest ID chain — the same mechanism LLM.Loop
			// uses.
			next, err := inst.Self().Step(ctx, inst, 0)
			rt.mu.Lock()
			rt.transitionLocked(func() {
				rt.stepping = false
				// Step returns the last successfully recorded state even on
				// error, so committing unconditionally preserves the
				// completed prefix (mirrors LLM.Loop).
				rt.last = next
			})
			rt.mu.Unlock()
			if err != nil {
				if ctx.Err() != nil {
					// Canceled (stop --kill or session teardown): keep the
					// prefix, end as STOPPED rather than FAILED, mirroring
					// LLM.Loop's interrupt semantics.
					return
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
		rt.mu.Lock()
		if len(rt.mailbox) > 0 && !rt.stopRequested && ctx.Err() == nil {
			rt.mu.Unlock()
			continue
		}
		if rt.turnOpen {
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

		// Mailbox empty, turn complete: IDLE. Block in receive until an
		// enqueue or a stop request pokes wake.
		select {
		case <-ctx.Done():
			return
		case <-rt.wake:
			// Re-check stop and the mailbox at the top of the loop.
		}
	}
}

// Stop ends the agent's loop and waits for the tombstone. Graceful stop
// (kill=false) lets an in-flight step finish before the loop ends; kill
// cancels the loop context immediately (the Step cancellation path already
// preserves the completed prefix). Idempotent on tombstones.
func (rt *AgentRuntime) Stop(ctx context.Context, kill bool, cause error) error {
	rt.mu.Lock()
	if rt.done {
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
			rt.done = true
		})
		rt.mu.Unlock()
		return nil
	}
	rt.transitionLocked(func() {
		rt.stopRequested = true
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
// immediately if it already does. It errors when the requested state becomes
// unreachable: the entry reached a terminal state (STOPPED/FAILED) other
// than the requested one, after which no further transitions occur.
func (rt *AgentRuntime) WaitFor(ctx context.Context, want AgentState) error {
	for {
		rt.mu.Lock()
		cur := rt.stateLocked()
		loopErr := rt.loopErr
		ch := rt.stateChanged
		rt.mu.Unlock()

		if cur == want {
			return nil
		}
		if cur.terminal() {
			if loopErr != nil {
				return fmt.Errorf("agent %q is %s (%s unreachable): %w", rt.name, cur, want, loopErr)
			}
			return fmt.Errorf("agent %q is %s; state %s is unreachable", rt.name, cur, want)
		}

		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-ch:
		}
	}
}
