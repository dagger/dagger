package core

import (
	"context"
	"time"

	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
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
// span: the marker, the spawn-minted instance ID, the display name, and the
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
		attrs = append(attrs, attribute.String(telemetryattrs.AgentIDAttr, self.Self().InstanceID))
	}
	if dig, err := self.RecipeDigest(ctx); err == nil {
		attrs = append(attrs, attribute.String(telemetryattrs.AgentCallDigestAttr, dig.String()))
	}
	return []trace.SpanStartOption{trace.WithAttributes(attrs...)}
}

// EmitAgentState publishes one agent-state record, attributed to the loop
// span carried by ctx. Callers emit only on a real change of the projected
// state (AgentRuntime.publishStateLocked), so the record stream is the
// agent's transition history rather than a sampling of it.
//
// waitingOn is the question a WAITING_INPUT agent is parked on, and is
// emitted as an explicit empty string for every other state so a consumer
// folding records latest-wins clears a stale one instead of showing a
// question that has already been answered.
func EmitAgentState(ctx context.Context, state AgentState, waitingOn string) {
	rec := log.Record{}
	rec.SetTimestamp(time.Now())
	// Explicit empty body: an unset body does not survive the OTLP
	// round-trip, and consumers skip empty-bodied records as text — this
	// record is state, not output. (Same contract as EmitProgress.)
	rec.SetBody(log.StringValue(""))
	rec.AddAttributes(
		log.String(telemetryattrs.AgentStateAttr, string(state)),
		log.String(telemetryattrs.AgentWaitingOnAttr, waitingOn),
	)
	telemetry.Logger(ctx, AgentInstrumentationScope).Emit(ctx, rec)
}
