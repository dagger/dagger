package core

import (
	"context"
	"io"
	"sync"

	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/trace"
)

// displayPhase tracks a single display span for streaming LLM content
// (text response, thinking, or tool call arguments).
type displayPhase struct {
	ctx  context.Context
	span trace.Span

	// Stdio wraps the span's log output.
	Stdio telemetry.SpanStreams

	// MarkdownW is set for text/thinking phases that stream markdown.
	MarkdownW io.Writer

	// callID is set for tool call phases.
	callID string
}

// displayPhases manages the lifecycle of display spans during LLM streaming.
// It is used by all providers to create, track, and close display phases for
// text responses, thinking blocks, and tool call arguments.
type displayPhases struct {
	parentCtx context.Context

	mu     sync.Mutex
	phases map[int64]*displayPhase

	// displaySpans collects closed phases' spans in order.
	displaySpans []trace.Span

	// toolCalls maps call IDs to their display context and span,
	// so that tool execution can be parented beneath them and
	// spans can be ended individually.
	toolCalls map[string]toolCallDisplay
}

// toolCallDisplay bundles the context and span for a tool call's display phase.
type toolCallDisplay struct {
	Ctx  context.Context
	Span trace.Span
}

func newDisplayPhases(parentCtx context.Context) *displayPhases {
	return &displayPhases{
		parentCtx: parentCtx,
		phases:    map[int64]*displayPhase{},
		toolCalls: map[string]toolCallDisplay{},
	}
}

// StartText creates or returns the text response display phase at the given
// index. Text phases get markdown content type.
func (dp *displayPhases) StartText(idx int64) *displayPhase {
	dp.mu.Lock()
	defer dp.mu.Unlock()
	if p, ok := dp.phases[idx]; ok {
		return p
	}
	phaseCtx, span := Tracer(dp.parentCtx).Start(dp.parentCtx, "LLM response",
		telemetry.Reveal(),
		trace.WithAttributes(
			attribute.String(telemetry.UIActorEmojiAttr, "🤖"),
			attribute.String(telemetry.UIMessageAttr, telemetry.UIMessageReceived),
			attribute.String(telemetry.LLMRoleAttr, telemetry.LLMRoleAssistant),
		),
	)
	p := &displayPhase{
		ctx:  phaseCtx,
		span: span,
	}
	p.Stdio = telemetry.SpanStdio(phaseCtx, InstrumentationLibrary)
	p.MarkdownW = telemetry.NewWriter(phaseCtx, InstrumentationLibrary,
		log.String(telemetry.ContentTypeAttr, "text/markdown"))
	dp.phases[idx] = p
	return p
}

// StartThinking creates or returns a thinking display phase at the given index.
func (dp *displayPhases) StartThinking(idx int64) *displayPhase {
	dp.mu.Lock()
	defer dp.mu.Unlock()
	if p, ok := dp.phases[idx]; ok {
		return p
	}
	phaseCtx, span := Tracer(dp.parentCtx).Start(dp.parentCtx, "thinking",
		telemetry.Reveal(),
		trace.WithAttributes(
			attribute.String(telemetry.UIActorEmojiAttr, "💭"),
			attribute.String(telemetry.UIMessageAttr, telemetry.UIMessageReceived),
			attribute.String(telemetry.LLMRoleAttr, telemetry.LLMRoleAssistant),
			attribute.Bool("llm.thinking", true),
		),
	)
	p := &displayPhase{
		ctx:  phaseCtx,
		span: span,
	}
	p.Stdio = telemetry.SpanStdio(phaseCtx, InstrumentationLibrary,
		log.String(telemetry.ContentTypeAttr, "text/markdown"),
		log.Bool("llm.thinking", true),
	)
	dp.phases[idx] = p
	return p
}

// StartToolCall creates or returns a tool call display phase at the given
// index. Tool call phases stream JSON arguments.
func (dp *displayPhases) StartToolCall(idx int64, callID, toolName string) *displayPhase {
	dp.mu.Lock()
	defer dp.mu.Unlock()
	if p, ok := dp.phases[idx]; ok {
		return p
	}
	phaseCtx, span := Tracer(dp.parentCtx).Start(dp.parentCtx, toolName,
		telemetry.Reveal(),
		trace.WithAttributes(
			attribute.String(telemetry.UIActorEmojiAttr, "🤖"),
			attribute.String(telemetry.LLMRoleAttr, telemetry.LLMRoleAssistant),
			attribute.String(telemetry.LLMToolAttr, toolName),
			attribute.Bool(telemetry.UIRollUpSpansAttr, true),
			attribute.Bool(telemetry.UIRollUpLogsAttr, true),
		),
	)
	p := &displayPhase{
		ctx:    phaseCtx,
		span:   span,
		callID: callID,
	}
	p.Stdio = telemetry.SpanStdio(phaseCtx, InstrumentationLibrary,
		log.String(telemetry.ContentTypeAttr, "application/json"),
		log.Bool(telemetry.LogsVerboseAttr, true))
	dp.phases[idx] = p
	return p
}

// Phase returns the phase at the given index, or nil if none exists.
func (dp *displayPhases) Phase(idx int64) *displayPhase {
	dp.mu.Lock()
	defer dp.mu.Unlock()
	return dp.phases[idx]
}

// Close closes the phase at the given index, ending its stdio and
// collecting its span. Safe to call multiple times for the same index.
func (dp *displayPhases) Close(idx int64) {
	dp.mu.Lock()
	defer dp.mu.Unlock()
	p, ok := dp.phases[idx]
	if !ok {
		return
	}
	p.Stdio.Close()
	dp.displaySpans = append(dp.displaySpans, p.span)
	if p.callID != "" {
		dp.toolCalls[p.callID] = toolCallDisplay{
			Ctx:  p.ctx,
			Span: p.span,
		}
	}
	delete(dp.phases, idx)
}

// CloseAll closes any remaining open phases.
func (dp *displayPhases) CloseAll() {
	dp.mu.Lock()
	idxs := make([]int64, 0, len(dp.phases))
	for idx := range dp.phases {
		idxs = append(idxs, idx)
	}
	dp.mu.Unlock()
	for _, idx := range idxs {
		dp.Close(idx)
	}
}

// Abort records an error on all display spans and ends them. Called when
// SendQuery fails and the spans won't be returned to the caller.
func (dp *displayPhases) Abort(err error) {
	dp.CloseAll()
	for _, s := range dp.displaySpans {
		s.RecordError(err)
		s.End()
	}
	dp.displaySpans = nil
	dp.toolCalls = nil
}

// Response builds the display-related fields of an LLMResponse.
func (dp *displayPhases) Response() (displaySpans []trace.Span, toolCalls map[string]toolCallDisplay) {
	return dp.displaySpans, dp.toolCalls
}
