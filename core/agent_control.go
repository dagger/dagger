package core

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/agentcontrol"
	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/trace"
)

// agentCapture owns the immutable committed value until recipe derivation has
// finished. The runtime's tombstone lease is held until its publisher drains.
// State-only revisions share this capture; a new conversation never does.
type agentCapture struct {
	ctx    context.Context
	value  dagql.ObjectResult[*LLM]
	once   sync.Once
	digest string
	err    error
}

func (capture *agentCapture) resolve() (string, error) {
	capture.once.Do(func() {
		defer func() { capture.ctx = nil; capture.value = dagql.ObjectResult[*LLM]{} }()
		if capture.value.Self() == nil {
			capture.err = errors.New("no committed conversation")
			return
		}
		// Retain internal flattening until all raw binding paths have proven
		// snapshot-at-capture semantics. This never invokes a public portableID.
		recipe, err := capture.value.Self().PortableRecipe(capture.ctx)
		if err != nil {
			capture.err = err
			return
		}
		digest, err := recipe.RecipeDigest(capture.ctx)
		if err != nil {
			capture.err = err
			return
		}
		capture.digest = digest.String()
	})
	return capture.digest, capture.err
}

type agentControlJob struct {
	projection agentcontrol.Agent
	capture    *agentCapture
}

// agentControlPublisher bounds pending work to the latest agent revision and
// the latest revision of each edge. Coalescing does not lose finality: the
// independently obtained producer expectation witnesses the required revision.
// No recipe evaluation or logger processor runs under the runtime mutex.
type agentControlPublisher struct {
	ctx     context.Context
	mu      sync.Mutex
	pending *agentControlJob
	edges   map[string]agentcontrol.Subscription
	wake    chan struct{}
	stop    chan chan struct{}
	flush   chan chan struct{}
	done    chan struct{}
}

func newAgentControlPublisher(ctx context.Context) *agentControlPublisher {
	p := &agentControlPublisher{ctx: agentTelemetryContext(ctx), edges: map[string]agentcontrol.Subscription{}, wake: make(chan struct{}, 1), stop: make(chan chan struct{}), flush: make(chan chan struct{}), done: make(chan struct{})}
	go p.run()
	return p
}

// Preserve only telemetry routing and providers, not resolver/query values.
func agentTelemetryContext(ctx context.Context) context.Context {
	out := trace.ContextWithSpanContext(context.Background(), trace.SpanContextFromContext(ctx))
	out = telemetry.WithLoggerProvider(out, telemetry.LoggerProvider(ctx))
	if md, err := engine.ClientMetadataFromContext(ctx); err == nil {
		out = engine.ContextWithClientMetadata(out, md)
	}
	return out
}

func (p *agentControlPublisher) enqueue(job agentControlJob) {
	p.mu.Lock()
	p.pending = &job
	p.mu.Unlock()
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

func (p *agentControlPublisher) subscription(edge agentcontrol.Subscription) {
	p.mu.Lock()
	p.edges[edge.Subscriber] = edge
	p.mu.Unlock()
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

func (p *agentControlPublisher) drain() {
	for {
		p.mu.Lock()
		job, edges := p.pending, p.edges
		p.pending, p.edges = nil, map[string]agentcontrol.Subscription{}
		p.mu.Unlock()
		if job == nil && len(edges) == 0 {
			return
		}
		if job != nil {
			a := job.projection
			if digest, err := job.capture.resolve(); err != nil {
				a.CaptureError = err.Error()
			} else {
				a.Digest = digest
			}
			telemetry.Logger(p.ctx, AgentInstrumentationScope).Emit(p.ctx, a.Record())
		}
		for _, edge := range edges {
			telemetry.Logger(p.ctx, AgentInstrumentationScope).Emit(p.ctx, edge.Record())
		}
	}
}

func (p *agentControlPublisher) run() {
	defer close(p.done)
	for {
		select {
		case <-p.wake:
			p.drain()
		case ack := <-p.flush:
			p.drain()
			close(ack)
		case ack := <-p.stop:
			p.drain()
			close(ack)
			return
		}
	}
}

func (p *agentControlPublisher) close(ctx context.Context) error {
	if p == nil {
		return nil
	}
	ack := make(chan struct{})
	select {
	case p.stop <- ack:
	case <-p.done:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
	select {
	case <-ack:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// publishControlLocked captures a coherent projection only after the mutation
// completed. Revision assignment, snapshot association and teardown facts are
// atomic; derivation and emission are deferred outside the critical section.
func (rt *AgentRuntime) publishControlLocked() {
	if rt.control == nil {
		return
	}
	state := rt.stateLocked()
	failure := ""
	if rt.loopErr != nil {
		failure = rt.loopErr.Error()
	}
	stopReason := ""
	if state == AgentStateStopped {
		stopReason = string(rt.stopReason)
	}
	if rt.controlCapture == nil {
		return
	}
	projection := agentcontrol.Agent{Key: agentcontrol.Key{Namespace: rt.controlNamespace, Handle: rt.key}, Name: rt.name, Removed: rt.removed.Load(), Parent: rt.parentHandle, CallDigest: rt.controlCallDigest,
		State: string(state), StopReason: stopReason, PreTeardownState: string(rt.preTeardownState), Failure: failure, Activity: rt.controlActivity}
	if projection == rt.controlLast && rt.controlCapture == rt.controlLastCapture {
		return
	}
	rt.controlLast, rt.controlLastCapture = projection, rt.controlCapture
	rt.controlRevision++
	projection.Revision = rt.controlRevision
	rt.control.enqueue(agentControlJob{projection: projection, capture: rt.controlCapture})
}

func (rt *AgentRuntime) captureConversationLocked(ctx context.Context) {
	rt.controlCapture = &agentCapture{ctx: context.WithoutCancel(ctx), value: rt.last}
	rt.controlActivity = time.Now().UTC()
}

// RestoreNotify installs a recorded edge without a level check or a model turn.
// Both capabilities must refer to restored entries that have never been activated.
func (ars *AgentRuntimes) RestoreNotify(ctx context.Context, target, subscriber dagql.ObjectResult[*Agent], states []AgentState) error {
	rt, err := ars.Require(ctx, target)
	if err != nil {
		return err
	}
	sub, err := ars.Require(ctx, subscriber)
	if err != nil {
		return err
	}
	if rt == sub {
		return errors.New("agent cannot subscribe to itself")
	}
	first, second := rt, sub
	if first.key > second.key {
		first, second = second, first
	}
	first.mu.Lock()
	defer first.mu.Unlock()
	second.mu.Lock()
	defer second.mu.Unlock()
	for _, endpoint := range []*AgentRuntime{rt, sub} {
		if !endpoint.restored || endpoint.activated || endpoint.closing {
			return fmt.Errorf("agent %q is not an inactive restored endpoint", endpoint.name)
		}
	}
	set := map[AgentState]bool{}
	for _, state := range states {
		set[state] = true
	}
	// A restored snapshot is the baseline, not newly committed work. Merely
	// relaunching through IDLE must not announce its old final reply.
	rt.idleEventDue = false
	rt.installSubscriptionLocked(sub.key, set, false)
	return nil
}

// DiscardRestore rolls back only an unactivated restored entry. Keep a removal
// tombstone for the final producer witness, while making all held handles fail
// registry lookup. It cannot delete an unrelated or already-used live agent.
func (ars *AgentRuntimes) DiscardRestore(ctx context.Context, agent dagql.ObjectResult[*Agent]) error {
	rt, err := ars.Require(ctx, agent)
	if err != nil {
		return err
	}
	rt.mu.Lock()
	if !rt.restored || rt.activated || rt.closing {
		rt.mu.Unlock()
		return errors.New("only an unactivated restored agent can be discarded")
	}
	rt.closing = true
	rt.transitionLocked(func() {
		rt.removed.Store(true)
		rt.done, rt.sealed, rt.stopRequested = true, true, true
		rt.stopReason = AgentStopExplicit
		for subscriber := range rt.subs {
			rt.installSubscriptionLocked(subscriber, nil, false)
		}
	})
	rt.controlClosed = true
	rt.mu.Unlock()
	ars.mu.Lock()
	entries := make([]*AgentRuntime, 0, len(ars.entries))
	for _, other := range ars.entries {
		if other != rt {
			entries = append(entries, other)
		}
	}
	ars.mu.Unlock()
	for _, other := range entries {
		other.mu.Lock()
		if _, ok := other.subs[rt.key]; ok && !other.controlClosed {
			other.installSubscriptionLocked(rt.key, nil, false)
		}
		other.mu.Unlock()
	}
	if err := rt.control.close(ctx); err != nil {
		return err
	}
	rt.clientScopeLease.Release()
	return nil
}

// CloseControl is the producer-side finality barrier. It closes admission,
// captures teardown projections, drains capture/publication, and returns final
// revisions grouped by producing client. The server applies its normal
// telemetry visibility routes to these independent expectations before sealing.
// Success is not a persistence acknowledgment: providers must still drain and
// archives must verify the selected recipe closure.
func (ars *AgentRuntimes) CloseControl(ctx context.Context) (map[string]agentcontrol.Expectation, error) {
	ars.closeMu.Lock()
	defer ars.closeMu.Unlock()
	ars.mu.Lock()
	ars.closing = true
	entries := make([]*AgentRuntime, 0, len(ars.entries))
	for _, rt := range ars.entries {
		entries = append(entries, rt)
	}
	ars.mu.Unlock()
	var errs error
	// Freeze every endpoint before stopping any: teardown notifications must
	// not enqueue new work and change another agent's pre-teardown state.
	for _, rt := range entries {
		rt.mu.Lock()
		if rt.preTeardownState == "" { rt.preTeardownState = rt.stateLocked() }
		rt.closing = true
		rt.mu.Unlock()
	}
	for _, rt := range entries {
		if err := rt.Stop(ctx, true, nil, AgentStopSession); err != nil {
			errs = errors.Join(errs, err)
		}
	}
	if errs != nil {
		// A producer still winding down may advance its committed tip. Keep
		// capture workers and leases alive; no fixed cut exists yet.
		return nil, errs
	}
	out := map[string]agentcontrol.Expectation{}
	for _, rt := range entries {
		rt.mu.Lock()
		rt.controlClosed = true
		expect := out[rt.controlOrigin]
		if expect.Agents == nil {
			expect.Agents = map[agentcontrol.Key]int64{}
			expect.Subscriptions = map[agentcontrol.EdgeKey]int64{}
		}
		expect.Agents[agentcontrol.Key{Namespace: rt.controlNamespace, Handle: rt.key}] = rt.controlRevision
		for subscriber, revision := range rt.subscriptionRevisions {
			expect.Subscriptions[agentcontrol.EdgeKey{Namespace: rt.controlNamespace, Watched: rt.key, Subscriber: subscriber}] = revision
		}
		out[rt.controlOrigin] = expect
		rt.mu.Unlock()
		if err := rt.control.close(ctx); err != nil {
			errs = errors.Join(errs, fmt.Errorf("finalize agent %q: %w", rt.name, err))
		} else {
			rt.clientScopeLease.Release()
		}
	}
	return out, errs
}
