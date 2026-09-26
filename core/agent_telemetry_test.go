package core

// The agent state channel is edge-triggered on the PROJECTION, which is the
// part most likely to regress silently: transitionLocked fires on every fact
// change, and most fact changes do not move the state, so a publication hung
// off the wrong hook would flood every client with duplicates — or, worse,
// coalesce two real transitions into one and leave a roster lying.

import (
	"context"
	"errors"
	"sync"
	"testing"

	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/agentcontrol"
	"github.com/dagger/dagger/engine/telemetryattrs"
)

// recordedState is one emitted log record, reduced to the lifecycle fields,
// body and content type the tests assert on.
type recordedState struct {
	state       string
	stopReason  string
	body        string
	contentType string
}

// stateRecorder captures log records emitted through a context, keeping
// agent control records whole.
type stateRecorder struct {
	mu      sync.Mutex
	records []recordedState
	control []sdklog.Record
}

func (r *stateRecorder) OnEmit(ctx context.Context, rec *sdklog.Record) error {
	got := recordedState{body: rec.Body().AsString()}
	rec.WalkAttributes(func(kv otellog.KeyValue) bool {
		switch kv.Key {
		case telemetryattrs.AgentStateAttr:
			got.state = kv.Value.AsString()
		case telemetryattrs.AgentStopReasonAttr:
			got.stopReason = kv.Value.AsString()
		case telemetry.ContentTypeAttr:
			got.contentType = kv.Value.AsString()
		}
		return true
	})
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, got)
	if agentcontrol.IsRecord(*rec) {
		r.control = append(r.control, rec.Clone())
	}
	return nil
}

func (r *stateRecorder) Shutdown(context.Context) error   { return nil }
func (r *stateRecorder) ForceFlush(context.Context) error { return nil }

func (r *stateRecorder) Enabled(context.Context, sdklog.EnabledParameters) bool { return true }

// states extracts lifecycle projections, ignoring subscription/payload records.
func (r *stateRecorder) states() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var states []string
	for _, rec := range r.records {
		if rec.state == "" {
			continue
		}
		states = append(states, rec.state)
	}
	return states
}

// stateRecorderCtx returns a context whose logger provider records every
// log record emitted through it.
func stateRecorderCtx(t *testing.T) (*stateRecorder, context.Context) {
	t.Helper()
	rec := &stateRecorder{}
	provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(rec))
	return rec, telemetry.WithLoggerProvider(context.Background(), provider)
}

// testRuntime builds a bare runtime entry wired to a recording context — the
// facts and the publication plumbing only, since that is all the state
// projection reads.
func testRuntime(t *testing.T, ctx context.Context) *AgentRuntime {
	t.Helper()
	p := newAgentControlPublisher(ctx)
	t.Cleanup(func() { require.NoError(t, p.close(context.Background())) })
	return &AgentRuntime{
		key: "test", name: "test", stateChanged: make(chan struct{}), spanCtx: ctx,
		control: p, controlDigest: "xxh3:committed",
		controlNamespace: agentcontrol.Namespace{Session: "session", Trace: "trace", Incarnation: "registry"},
	}
}

func (rt *AgentRuntime) flushControl() {
	ack := make(chan struct{})
	select {
	case rt.control.flush <- ack:
		<-ack
	case <-rt.control.done:
	}
}

func testAgentContext(t *testing.T, ctx context.Context, id, name string) context.Context {
	t.Helper()
	srv, err := dagql.NewServer(ctx, &Query{})
	require.NoError(t, err)
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*Agent]{Typed: &Agent{}}))
	agent := &Agent{Handle: id, Name: name}
	res, err := dagql.NewObjectResultForCall(agent, srv, &dagql.ResultCall{
		Kind:        dagql.ResultCallKindSynthetic,
		SyntheticOp: "test_agent",
		Type:        dagql.NewResultCallType(agent.Type()),
	})
	require.NoError(t, err)
	return AgentToContext(ctx, res)
}

func (rt *AgentRuntime) testTransition(mut func()) {
	rt.mu.Lock()
	rt.transitionLocked(mut)
	rt.mu.Unlock()
	rt.flushControl()
}

// TestPublishStateEdgeTriggered covers the core contract: one record per
// change of the projected state, and none at all for fact changes that leave
// the projection where it was.
func TestPublishStateEdgeTriggered(t *testing.T) {
	rec, ctx := stateRecorderCtx(t)
	rt := testRuntime(t, ctx)

	// Seed, as loop() does on start.
	rt.mu.Lock()
	rt.publishStateLocked()
	rt.mu.Unlock()
	rt.flushControl()
	require.Equal(t, []string{string(AgentStateIdle)}, rec.states())

	// Mail arrives: IDLE -> RUNNING.
	rt.testTransition(func() { rt.mailbox = append(rt.mailbox, "msg-1") })
	require.Equal(t, []string{"IDLE", "RUNNING"}, rec.states())

	// The turn opens and a step starts, draining the mailbox. Three more
	// fact changes, all of which project RUNNING: nothing new is published.
	rt.testTransition(func() { rt.turnOpen = true })
	rt.testTransition(func() { rt.mailbox = nil })
	rt.testTransition(func() { rt.stepping = true })
	require.Equal(t, []string{"IDLE", "RUNNING"}, rec.states(),
		"fact changes that leave the projection alone must not publish")

	// The step lands and the turn resolves: back to IDLE.
	rt.testTransition(func() { rt.stepping = false; rt.turnOpen = false })
	require.Equal(t, []string{"IDLE", "RUNNING", "IDLE"}, rec.states())

	// A pause mid-idle is a real transition, and so is the resume.
	rt.testTransition(func() { rt.paused = true })
	rt.testTransition(func() { rt.paused = false })
	require.Equal(t, []string{"IDLE", "RUNNING", "IDLE", "PAUSED", "IDLE"}, rec.states())
}

// A dormant entry publishes without waiting for a loop/identity span.
func TestPublishStateBeforeStart(t *testing.T) {
	rec, ctx := stateRecorderCtx(t)
	rt := testRuntime(t, ctx)
	rt.spanCtx = nil

	rt.testTransition(func() { rt.mailbox = append(rt.mailbox, "msg-1") })
	require.Equal(t, []string{"RUNNING"}, rec.states(), "publication does not depend on a loop span")

	// Once the loop starts, the first publication reports the state as it
	// stands — including the mail that arrived while it was inert.
	rt.mu.Lock()
	rt.spanCtx = ctx
	rt.publishStateLocked()
	rt.mu.Unlock()
	rt.flushControl()
	require.Equal(t, []string{"RUNNING"}, rec.states())
}

// TestPublishStateSealedTombstone covers the transition that outlives the
// loop span: stop sealing a FAILED tombstone runs after loop() returned, and
// must still reach the roster — otherwise a failed agent looks retryable
// forever.
func TestPublishStateSealedTombstone(t *testing.T) {
	rec, ctx := stateRecorderCtx(t)
	rt := testRuntime(t, ctx)

	rt.testTransition(func() {
		rt.done = true
		rt.loopErr = context.DeadlineExceeded
	})
	require.Equal(t, []string{"FAILED"}, rec.states())

	rt.testTransition(func() { rt.sealed = true })
	require.Equal(t, []string{"FAILED", "STOPPED"}, rec.states())
}

// TestEmitAgentFailureMessage locks the durable failure surface: the loop's
// actual error becomes a failed assistant message beneath the loop, with the
// same text on stdio for the focused conversation to retain in scrollback.
func TestEmitAgentFailureMessage(t *testing.T) {
	logs, ctx := stateRecorderCtx(t)
	ctx = testAgentContext(t, ctx, "agent-123", "reviewer")
	spans := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(spans),
	)
	ctx, loop := tp.Tracer("agent-failure-test").Start(ctx, "agent loop")
	defer loop.End()

	// A clean stop is silent.
	emitAgentFailure(ctx, nil)
	require.Empty(t, spans.Ended())

	loopErr := errors.New("context limit reached")
	emitAgentFailure(ctx, loopErr)

	require.Len(t, spans.Ended(), 1)
	failure := spans.Ended()[0]
	require.Equal(t, "agent failure", failure.Name())
	require.Equal(t, loop.SpanContext().SpanID(), failure.Parent().SpanID())
	require.Equal(t, codes.Error, failure.Status().Code)
	require.Equal(t, loopErr.Error(), failure.Status().Description)

	attrs := map[string]string{}
	for _, attr := range failure.Attributes() {
		attrs[string(attr.Key)] = attr.Value.AsString()
	}
	require.Equal(t, telemetry.LLMRoleAssistant, attrs[telemetry.LLMRoleAttr])
	require.Equal(t, telemetry.UIMessageReceived, attrs[telemetry.UIMessageAttr])
	require.Equal(t, "agent-123", attrs[string(semconv.GenAIAgentIDKey)])
	require.Equal(t, "reviewer", attrs[string(semconv.GenAIAgentNameKey)])

	logs.mu.Lock()
	defer logs.mu.Unlock()
	var bodies []string
	for _, rec := range logs.records {
		if rec.body != "" {
			bodies = append(bodies, rec.body)
		}
	}
	require.Equal(t, []string{loopErr.Error()}, bodies)
}

// llmChainResult builds an LLM result whose recipe is the given inline frame
// chain, so recipe digests and IDs can be derived without a live cache.
func llmChainResult(t *testing.T, srv *dagql.Server, frame *dagql.ResultCall) dagql.ObjectResult[*LLM] {
	t.Helper()
	res, err := dagql.NewObjectResultForCall(&LLM{}, srv, frame)
	require.NoError(t, err)
	return res
}

// TestRewindDigestsRequiresAncestor pins what makes a reseed a REWIND: the
// adopted conversation sits on the abandoned one's receiver chain. Every other
// replacement — the same conversation, a descendant (a model change), an
// unrelated chain (compaction) — is not one, because nothing the transcript
// shows was abandoned.
func TestRewindDigestsRequiresAncestor(t *testing.T) {
	ctx := dagql.ContextWithCache(context.Background(), nil)
	srv := newCoreDagqlServerForTest(t, &Query{})
	srv.InstallObject(dagql.NewClass[*LLM](srv))

	llmFrame := testResultCall("llm", &LLM{}, nil)
	promptFrame := testResultCall("withPrompt", &LLM{}, llmFrame)
	responseFrame := testResultCall("withResponse", &LLM{}, promptFrame)
	modelFrame := testResultCall("withModel", &LLM{}, responseFrame)
	otherFrame := testResultCall("withPrompt", &LLM{}, testResultCall("llm", &LLM{}, testResultCall("other", &LLM{}, nil)))

	base := llmChainResult(t, srv, llmFrame)
	tip := llmChainResult(t, srv, responseFrame)

	from, to, ok := rewindDigests(ctx, tip, base)
	require.True(t, ok, "the seed is an ancestor of the tip")
	tipDigest, err := tip.RecipeDigest(ctx)
	require.NoError(t, err)
	baseDigest, err := base.RecipeDigest(ctx)
	require.NoError(t, err)
	require.Equal(t, tipDigest.String(), from,
		"from must be the digest spans publish for the abandoned tip")
	require.Equal(t, baseDigest.String(), to)

	_, _, ok = rewindDigests(ctx, tip, tip)
	require.False(t, ok, "replacing a conversation with itself abandons nothing")

	_, _, ok = rewindDigests(ctx, tip, llmChainResult(t, srv, modelFrame))
	require.False(t, ok, "a descendant keeps every message: not a rewind")

	_, _, ok = rewindDigests(ctx, tip, llmChainResult(t, srv, otherFrame))
	require.False(t, ok, "an unrelated chain is a replacement, not a rewind")
}

// TestReseedPublishesRewindMarker locks the marker's wire contract and the
// one place it is emitted: a reseed that rewinds publishes exactly one
// conversation message beneath the loop span, carrying the abandoned and
// adopted recipe digests, and a reseed that merely replaces publishes none.
func TestReseedPublishesRewindMarker(t *testing.T) {
	logs, ctx := stateRecorderCtx(t)
	ctx = dagql.ContextWithCache(ctx, nil)
	ctx = testAgentContext(t, ctx, "agent-123", "reviewer")
	spans := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(spans),
	)
	loopCtx, loop := tp.Tracer("agent-rewind-test").Start(ctx, "agent loop")
	defer loop.End()

	srv := newCoreDagqlServerForTest(t, &Query{})
	srv.InstallObject(dagql.NewClass[*LLM](srv))
	llmFrame := testResultCall("llm", &LLM{}, nil)
	responseFrame := testResultCall("withResponse", &LLM{}, testResultCall("withPrompt", &LLM{}, llmFrame))
	base := llmChainResult(t, srv, llmFrame)
	tip := llmChainResult(t, srv, responseFrame)

	rt := testRuntime(t, loopCtx)
	rt.last = tip

	// A replacement that is not a rewind leaves no marker.
	require.NoError(t, rt.Reseed(ctx, llmChainResult(t, srv, testResultCall("withModel", &LLM{}, responseFrame))))
	require.Empty(t, spans.Ended())

	rt.last = tip
	require.NoError(t, rt.Reseed(ctx, base))
	require.Same(t, base.Self(), rt.Snapshot().Self())

	require.Len(t, spans.Ended(), 1)
	marker := spans.Ended()[0]
	require.Equal(t, "conversation rewound", marker.Name())
	require.Equal(t, loop.SpanContext().SpanID(), marker.Parent().SpanID(),
		"the marker belongs to the transcript beneath the loop span")

	attrs := map[string]string{}
	for _, attr := range marker.Attributes() {
		attrs[string(attr.Key)] = attr.Value.AsString()
	}
	tipDigest, err := tip.RecipeDigest(ctx)
	require.NoError(t, err)
	baseDigest, err := base.RecipeDigest(ctx)
	require.NoError(t, err)
	require.Equal(t, tipDigest.String(), attrs[telemetryattrs.AgentRewindFromDigestAttr])
	require.Equal(t, baseDigest.String(), attrs[telemetryattrs.AgentRewindToDigestAttr])
	require.Equal(t, telemetry.LLMRoleUser, attrs[telemetry.LLMRoleAttr])
	require.Equal(t, telemetryattrs.LLMMessageOriginKindEvent, attrs[telemetryattrs.LLMMessageOriginKindAttr],
		"a client predating the marker must render it as an engine event, not as words the model said")
	require.Equal(t, "agent-123", attrs[string(semconv.GenAIAgentIDKey)])

	logs.mu.Lock()
	defer logs.mu.Unlock()
	var bodies []string
	for _, rec := range logs.records {
		if rec.body != "" {
			bodies = append(bodies, rec.body)
		}
	}
	require.Equal(t, []string{agentRewindMessage}, bodies)
}

// TestStopReasonRidesTerminalRecord covers the fact that makes a trace
// restorable at all: a stop somebody asked for and a stop the session's
// teardown performed are the same STOPPED projection, and only the reason
// tells them apart. Restoring the first as live reverses a dismissal;
// refusing to restore the second loses a cleanly closed session entirely.
func TestStopReasonRidesTerminalRecord(t *testing.T) {
	rec, ctx := stateRecorderCtx(t)
	rt := testRuntime(t, ctx)

	// A non-terminal transition carries no reason, so a consumer folding
	// records latest-wins never attributes a stale reason to a later stop.
	rt.testTransition(func() { rt.paused = true })

	require.NoError(t, rt.Stop(context.Background(), false, nil, AgentStopExplicit))
	rt.flushControl()

	rec.mu.Lock()
	defer rec.mu.Unlock()
	require.Len(t, rec.records, 2)
	require.Equal(t, "PAUSED", rec.records[0].state)
	require.Empty(t, rec.records[0].stopReason)
	require.Equal(t, "STOPPED", rec.records[1].state)
	require.Equal(t, string(AgentStopExplicit), rec.records[1].stopReason)
}

// TestKillAllStopsWithSessionReason is the other half: session teardown stops
// every entry it holds, and each says so — otherwise a normal exit publishes a
// trace in which every agent looks deliberately dismissed.
func TestKillAllStopsWithSessionReason(t *testing.T) {
	rec, ctx := stateRecorderCtx(t)
	rt := testRuntime(t, ctx)
	rt.key = "instance-1"

	ars := NewAgentRuntimes()
	ars.entries[rt.key] = rt
	require.NoError(t, ars.KillAll(context.Background(), errors.New("session closed")))

	require.Equal(t, []string{"STOPPED"}, rec.states())
	rec.mu.Lock()
	defer rec.mu.Unlock()
	require.Equal(t, string(AgentStopSession), rec.records[0].stopReason)
}
