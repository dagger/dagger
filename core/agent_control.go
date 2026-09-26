package core

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/agentcontrol"
	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/trace"
)

// agentControlPublisher bounds pending work to the latest agent revision and
// the latest revision of each edge. Coalescing does not lose finality: the
// independently obtained producer expectation witnesses the required revision.
// No logger processor runs under the runtime mutex.
type agentControlPublisher struct {
	ctx     context.Context
	mu      sync.Mutex
	pending *agentcontrol.Agent
	edges   map[string]agentcontrol.Subscription
	wake    chan struct{}
	// quit is closed exactly once to stop the publisher. Signaling never
	// depends on the caller's context, so an expired teardown context still
	// stops the goroutine; only waiting for its final drain is bounded.
	quit     chan struct{}
	quitOnce sync.Once
	flush    chan chan struct{}
	done     chan struct{}
}

func newAgentControlPublisher(ctx context.Context) *agentControlPublisher {
	p := &agentControlPublisher{ctx: agentTelemetryContext(ctx), edges: map[string]agentcontrol.Subscription{}, wake: make(chan struct{}, 1), quit: make(chan struct{}), flush: make(chan chan struct{}), done: make(chan struct{})}
	go p.run()
	return p
}

// Preserve only telemetry routing and providers, not resolver/query values.
// The span itself (not just its SpanContext) is kept so Tracer(ctx) still
// reaches the real TracerProvider, and the agent identity so emitted message
// spans keep their gen_ai.agent.* attributes; the runtime retains both anyway.
func agentTelemetryContext(ctx context.Context) context.Context {
	out := trace.ContextWithSpan(context.Background(), trace.SpanFromContext(ctx))
	out = telemetry.WithLoggerProvider(out, telemetry.LoggerProvider(ctx))
	if agent, ok := AgentFromContext(ctx); ok {
		out = AgentToContext(out, agent)
	}
	if md, err := engine.ClientMetadataFromContext(ctx); err == nil {
		out = engine.ContextWithClientMetadata(out, md)
	}
	return out
}

func (p *agentControlPublisher) enqueue(projection agentcontrol.Agent) {
	p.mu.Lock()
	p.pending = &projection
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
		projection, edges := p.pending, p.edges
		p.pending, p.edges = nil, map[string]agentcontrol.Subscription{}
		p.mu.Unlock()
		if projection == nil && len(edges) == 0 {
			return
		}
		if projection != nil {
			telemetry.Logger(p.ctx, AgentInstrumentationScope).Emit(p.ctx, projection.Record())
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
		case <-p.quit:
			p.drain()
			return
		}
	}
}

// close stops the publisher after a final drain and waits, bounded by ctx, for
// it to exit. The stop is signaled even when ctx is already done. Work
// enqueued afterwards is dropped: nothing blocks on a closed publisher.
func (p *agentControlPublisher) close(ctx context.Context) error {
	if p == nil {
		return nil
	}
	p.quitOnce.Do(func() { close(p.quit) })
	select {
	case <-p.done:
		return nil
	default:
	}
	select {
	case <-p.done:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// publishControlLocked captures a coherent projection only after the mutation
// completed. Revision assignment, snapshot association and teardown facts are
// atomic; emission is deferred outside the critical section.
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
	if rt.controlDigest == "" && rt.controlCaptureError == "" {
		return
	}
	projection := agentcontrol.Agent{Key: agentcontrol.Key{Namespace: rt.controlNamespace, Handle: rt.key}, Name: rt.name, Parent: rt.parentHandle, CallDigest: rt.controlCallDigest,
		Digest: rt.controlDigest, CaptureError: rt.controlCaptureError, State: string(state), StopReason: stopReason, PreTeardownState: string(rt.preTeardownState), Failure: failure, Activity: rt.controlActivity}
	if projection == rt.controlLast {
		return
	}
	rt.controlLast = projection
	rt.controlRevision++
	projection.Revision = rt.controlRevision
	rt.control.enqueue(projection)
}

// associateConversationLocked records only the committed LLM call's leaf digest.
// Ordinary call telemetry owns frame delivery; consumers rebuild the graph and
// archive finalization verifies its closure. Never select or serialize a recipe
// here, nor infer the committed value from descendant spans.
func (rt *AgentRuntime) associateConversationLocked(ctx context.Context) {
	rt.controlDigest, rt.controlCaptureError = "", ""
	if rt.last.Self() == nil {
		rt.controlCaptureError = "no committed conversation"
	} else if digest, err := rt.last.RecipeDigest(ctx); err != nil {
		rt.controlCaptureError = err.Error()
	} else {
		rt.controlDigest = digest.String()
	}
	rt.controlActivity = time.Now().UTC()
}

// CloseControl is the producer-side finality barrier. It closes admission,
// captures teardown projections, drains publication, and returns final
// revisions grouped by producing client. The server applies its normal
// telemetry visibility routes to these independent expectations before sealing.
// Success is not a persistence acknowledgment: providers must still drain and
// archives must verify the selected recipe closure.
func (ars *AgentRuntimes) CloseControl(ctx context.Context) (map[string]agentcontrol.Expectation, error) {
	out, err := ars.closeControl(ctx, nil)
	if err == nil && out == nil {
		// An earlier teardown closed the publishers before producers were
		// quiescent: its revisions were never fixed, so none can be witnessed.
		err = fmt.Errorf("agent control closed without a fixed cut: %w", ars.controlTorn)
	}
	return out, err
}

// closeControl stops every producer and then tears down every publisher and
// lease, whatever happened on the way. It returns the expectation witness only
// when every producer reached quiescence, now or in an earlier call; a nil map
// means there is no fixed cut. The error reports this call's own failures.
//
// Failures never leave teardown half-done: a held tombstone lease blocks the
// session's client-scope drain indefinitely, and a live publisher goroutine
// outlives the session. A producer still winding down after its publisher
// closed only drops its late records; the missing witness already marks the
// archive incomplete.
func (ars *AgentRuntimes) closeControl(ctx context.Context, cause error) (map[string]agentcontrol.Expectation, error) {
	ars.closeMu.Lock()
	defer ars.closeMu.Unlock()
	ars.mu.Lock()
	ars.closing = true
	entries := make([]*AgentRuntime, 0, len(ars.entries))
	for _, rt := range ars.entries {
		entries = append(entries, rt)
	}
	ars.mu.Unlock()
	var stopErrs error
	// Freeze every endpoint before stopping any: teardown notifications must
	// not enqueue new work and change another agent's pre-teardown state.
	for _, rt := range entries {
		rt.mu.Lock()
		if rt.preTeardownState == "" {
			rt.preTeardownState = rt.stateLocked()
		}
		rt.closing = true
		rt.mu.Unlock()
	}
	for _, rt := range entries {
		if err := rt.Stop(ctx, true, cause, AgentStopSession); err != nil {
			stopErrs = errors.Join(stopErrs, err)
		}
	}
	var out map[string]agentcontrol.Expectation
	switch {
	case stopErrs != nil:
		// A producer still winding down may advance its committed tip: no
		// fixed cut exists, now or later, once its publisher closes below.
		if ars.controlTorn == nil {
			ars.controlTorn = stopErrs
		}
	case ars.controlTorn == nil:
		out = map[string]agentcontrol.Expectation{}
		for _, rt := range entries {
			rt.mu.Lock()
			if !rt.controlClosed {
				rt.publishControlLocked()
			}
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
		}
	}
	errs := stopErrs
	for _, rt := range entries {
		if err := rt.control.close(ctx); err != nil {
			errs = errors.Join(errs, fmt.Errorf("finalize agent %q: %w", rt.name, err))
		}
		rt.clientScopeLease.Release()
	}
	return out, errs
}
