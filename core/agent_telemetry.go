package core

import (
	"context"
	"fmt"

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
// discovery plane instead. Loop spans carry immutable diagnostic identity;
// agent_control.go publishes the complete revisioned mutable projection.
// There is no separate state or snapshot log authority.

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

// agentRewindMessage is the text a rewind marker carries for renderers that
// know nothing of the marker attributes: an older client, the plain frontend,
// ReadLogs. The pretty frontend renders its own summary from the attributes.
const agentRewindMessage = "Conversation rewound: the messages above it are no longer part of the conversation."

// emitAgentRewind publishes a rewind marker beneath the agent's loop span: a
// conversation message recording that the committed conversation `from` was
// replaced by its ancestor `to`, so everything the transcript shows between
// the two is no longer in the model's history.
//
// It is a span rather than a state record because it is an EVENT with a place
// in the transcript — the row at which the conversation forked — and its facts
// are known at start and never change, which is all a span attribute can
// express. Emitted as an engine lifecycle event (EVENT origin) rather than an
// assistant message so a renderer that predates the marker collapses it to a
// one-liner instead of showing it as something the model said.
func emitAgentRewind(ctx context.Context, from, to string) {
	if from == "" || to == "" {
		return
	}
	attrs := []attribute.KeyValue{
		attribute.String(telemetry.UIActorEmojiAttr, "↶"),
		attribute.String(telemetry.UIMessageAttr, telemetry.UIMessageReceived),
		attribute.String(telemetry.LLMRoleAttr, telemetry.LLMRoleUser),
		attribute.String(telemetryattrs.LLMMessageOriginKindAttr, telemetryattrs.LLMMessageOriginKindEvent),
		attribute.String(telemetryattrs.AgentRewindFromDigestAttr, from),
		attribute.String(telemetryattrs.AgentRewindToDigestAttr, to),
	}
	attrs = append(attrs, genAIAgentAttrsFromContext(ctx)...)
	ctx, span := Tracer(ctx).Start(ctx, "conversation rewound", trace.WithAttributes(attrs...))
	stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary,
		log.String(telemetry.ContentTypeAttr, "text/plain"))
	fmt.Fprint(stdio.Stdout, agentRewindMessage)
	stdio.Close()
	span.End()
}

// rewindDigests reports whether replacing the conversation `from` with `next`
// is a REWIND — next is a strict ancestor on from's receiver chain — and
// returns the two recipe digests a rewind marker carries. Any other
// replacement (compaction, a rebind, a model change, or a conversation that
// cannot be inspected) is not a rewind: nothing the transcript shows was
// abandoned, so there is nothing to mark.
//
// The chain is walked on the rebuilt recipe ID, whose per-frame digests are
// the ones spans publish as LLMCallDigestAttr and dagger.io/dag.digest, so the
// digests returned here are exactly what a client can join against.
func rewindDigests(ctx context.Context, from, next dagql.ObjectResult[*LLM]) (string, string, bool) {
	if from.Self() == nil || next.Self() == nil {
		return "", "", false
	}
	toDigest, err := next.RecipeDigest(ctx)
	if err != nil || toDigest == "" {
		return "", "", false
	}
	fromID, err := from.RecipeID(ctx)
	if err != nil || fromID == nil {
		return "", "", false
	}
	fromDigest := fromID.Digest()
	if fromDigest == toDigest {
		return "", "", false
	}
	for cur := fromID.Receiver(); cur != nil; cur = cur.Receiver() {
		if cur.Digest() == toDigest {
			return fromDigest.String(), toDigest.String(), true
		}
	}
	return "", "", false
}
