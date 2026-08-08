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
		key:  key,
		name: agent.Self().Name,
		last: agent.Self().Seed,
		// wake has a single slot: it only needs to record "something changed,
		// re-check" (a stop request now; mail in a later phase), not carry
		// payloads.
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
	stopRequested bool                     // a graceful stop was requested
	done          bool                     // the loop has ended (tombstone)
	loopErr       error                    // why the loop failed, if it did
	last          dagql.ObjectResult[*LLM] // last committed conversation (initially the seed)
	cancel        context.CancelCauseFunc  // kills the loop context (set on start)

	// wake unblocks the loop when it is idle. For now only Stop pokes it; a
	// later phase will also poke it on mailbox enqueue, which is why the loop
	// blocks in receive instead of returning when a turn ends.
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
	case rt.stepping:
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
// context. It mirrors LLM.Loop (core/llm.go): while the conversation has
// pending input, Step it, committing each step's result to rt.last. When the
// turn ends, instead of returning it marks the entry idle and blocks on the
// wake channel — the receive that a later phase will use for mailbox
// delivery; for now only stop/cancel wake it.
func (rt *AgentRuntime) loop(ctx context.Context) {
	ctx, span := Tracer(ctx).Start(ctx, fmt.Sprintf("agent: %s", rt.name))

	var loopErr error
	defer func() {
		rt.mu.Lock()
		rt.transitionLocked(func() {
			rt.stepping = false
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
		// Drain the turn: step while anything is pending.
		for {
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

		// Turn complete: IDLE (stepping is false, nothing pending). Block in
		// receive until something changes — a stop request now; mailbox
		// delivery in a later phase.
		select {
		case <-ctx.Done():
			return
		case <-rt.wake:
			// Re-check stop and pending input at the top of the loop.
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
		// pure transition to the tombstone.
		rt.transitionLocked(func() {
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
