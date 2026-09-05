package core

import (
	"context"
	"fmt"
	"time"

	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/log"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/telemetryattrs"
)

// Agent telemetry: the directory a client builds its roster from.
//
// hack/designs/async-agents.md §3.3 renounces a session-wide agent namespace
// in favour of capability-based addressing, and nominates telemetry as the
// discovery plane instead. This file is that plane's producer half: the loop
// span carries who the agent is, and log records carry what it is doing.
//
// The split between the two is forced, not stylistic — see the
// dagger.io/agent.* block in engine/telemetryattrs for why mutable state
// cannot ride span attributes.

// AgentInstrumentationScope names the logger emitting agent state records.
const AgentInstrumentationScope = "dagger.io/agent"

// agentSpanAttrs builds the identity attributes stamped on an agent's loop
// span: the marker, the spawn-minted runtime handle, the display name, and the
// digest of the call that produced the agent value.
//
// The call digest is what lets a client turn a rendered roster entry back
// into a sendable handle, the same way LLMCallDigestAttr lets it branch from
// a message. It is best-effort by design: if the digest cannot be derived the
// remaining attributes still describe the agent, and a client degrades to a
// read-only roster entry rather than losing the agent entirely.
func agentSpanAttrs(ctx context.Context, name string, self dagql.ObjectResult[*Agent]) []trace.SpanStartOption {
	attrs := []attribute.KeyValue{
		attribute.Bool(telemetryattrs.AgentAttr, true),
		attribute.String(telemetryattrs.AgentNameAttr, name),
	}
	if self.Self() != nil {
		attrs = append(attrs, attribute.String(telemetryattrs.AgentIDAttr, self.Self().Handle))
		attrs = append(attrs, genAIAgentAttrs(self.Self().Handle, name)...)
	}
	if dig, err := self.RecipeDigest(ctx); err == nil {
		attrs = append(attrs, attribute.String(telemetryattrs.AgentCallDigestAttr, dig.String()))
	}
	return []trace.SpanStartOption{trace.WithAttributes(attrs...)}
}

// genAIAgentAttrs returns the standard GenAI identity attributes for an
// agent. These ride both the long-lived agent span and each conversation
// message span beneath it, allowing trace consumers to group and discover an
// agent's conversation without depending on Dagger-specific attributes.
func genAIAgentAttrs(id, name string) []attribute.KeyValue {
	attrs := make([]attribute.KeyValue, 0, 2)
	if id != "" {
		attrs = append(attrs, semconv.GenAIAgentID(id))
	}
	if name != "" {
		attrs = append(attrs, semconv.GenAIAgentName(name))
	}
	return attrs
}

// genAIAgentAttrsFromContext identifies conversation message spans emitted by
// an asynchronous agent. Synchronous LLM conversations have no agent identity
// and intentionally receive no agent attributes.
func genAIAgentAttrsFromContext(ctx context.Context) []attribute.KeyValue {
	agent, ok := AgentFromContext(ctx)
	if !ok {
		return nil
	}
	self := agent.Self()
	return genAIAgentAttrs(self.Handle, self.Name)
}

// EmitAgentState publishes one agent-state record, attributed to the loop
// span carried by ctx. Callers emit only on a real change of the projected
// state (AgentRuntime.publishStateLocked), so the record stream is the
// agent's transition history rather than a sampling of it.
//
// waitingOn is the question a WAITING_INPUT agent is parked on, and stopReason
// is what ended a STOPPED one. Both are emitted as an explicit empty string
// for every other state, so a consumer folding records latest-wins clears a
// stale value instead of showing a question that has already been answered —
// or attributing an earlier stop's reason to a later transition.
func EmitAgentState(ctx context.Context, state AgentState, waitingOn string, stopReason AgentStopReason) {
	rec := log.Record{}
	rec.SetTimestamp(time.Now())
	// Explicit empty body: an unset body does not survive the OTLP
	// round-trip, and consumers skip empty-bodied records as text — this
	// record is state, not output. (Same contract as EmitProgress.)
	rec.SetBody(log.StringValue(""))
	rec.AddAttributes(
		log.String(telemetryattrs.AgentStateAttr, string(state)),
		log.String(telemetryattrs.AgentWaitingOnAttr, waitingOn),
		log.String(telemetryattrs.AgentStopReasonAttr, string(stopReason)),
	)
	telemetry.Logger(ctx, AgentInstrumentationScope).Emit(ctx, rec)
}

// EmitAgentSnapshot publishes the portable recipe digest of the agent's last
// conversation, attributed to the loop span carried by ctx. Latest record
// wins: this is the resume anchor a client re-hydrates the instance from.
//
// It is a record of its own rather than a field on the state record because
// the two are triggered by different things — state records are edge-triggered
// on the projected state, and most commits leave the state exactly where it
// was while every commit moves the snapshot.
func EmitAgentSnapshot(ctx context.Context, digest string) {
	rec := log.Record{}
	rec.SetTimestamp(time.Now())
	// Explicit empty body, for the same reason as EmitAgentState: this is
	// data about the agent, never text from it.
	rec.SetBody(log.StringValue(""))
	rec.AddAttributes(
		log.String(telemetryattrs.AgentSnapshotDigestAttr, digest),
	)
	telemetry.Logger(ctx, AgentInstrumentationScope).Emit(ctx, rec)
}

// emitAgentFailure publishes a failed loop's terminal error as a permanent
// conversation message beneath that loop. Its status description and stdio are
// both the loop's actual error: the former keeps failure semantics in the trace,
// while the latter gives conversation renderers content they can retain in
// scrollback and scope with agent focus.
func emitAgentFailure(ctx context.Context, loopErr error) {
	if loopErr == nil {
		return
	}
	attrs := []attribute.KeyValue{
		attribute.String(telemetry.UIActorEmojiAttr, "✘"),
		attribute.String(telemetry.UIMessageAttr, telemetry.UIMessageReceived),
		attribute.String(telemetry.LLMRoleAttr, telemetry.LLMRoleAssistant),
	}
	attrs = append(attrs, genAIAgentAttrsFromContext(ctx)...)
	ctx, span := Tracer(ctx).Start(ctx, "agent failure", trace.WithAttributes(attrs...))
	span.SetStatus(codes.Error, loopErr.Error())
	stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary,
		log.String(telemetry.ContentTypeAttr, "text/plain"))
	fmt.Fprint(stdio.Stderr, loopErr.Error())
	stdio.Close()
	span.End()
}
