package core

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	"github.com/mark3labs/mcp-go/mcp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/trace"
)

const (
	llmHarnessDaggerMessageIDAttr = "dagger.io/llm.harness.message.id"
	llmHarnessVendorMessageIDAttr = "dagger.io/llm.harness.vendor_message.id"
	llmHarnessNativeTurnIDAttr    = "dagger.io/llm.harness.turn.id"
	llmHarnessToolCallIDAttr      = "dagger.io/llm.harness.tool.call_id"
	llmHarnessToolSourceAttr      = "dagger.io/llm.harness.tool.source"
	llmHarnessMCPCallIDMetaKey    = "callId"
)

type llmHarnessDisplayTurn struct {
	phases    *displayPhases
	callIDs   map[string]struct{}
	toolSpans map[trace.Span]struct{}
}

// llmHarnessDisplay projects the adapter's vendor-neutral events into the same
// live span vocabulary as provider-backed LLM evaluation. It also joins an MCP
// request's exact Codex `_meta.callId` to its open tool-call span so the real
// Dagger evaluation nests beneath the conversation row.
type llmHarnessDisplay struct {
	parentCtx  context.Context
	callDigest string

	mu      sync.Mutex
	turns   map[string]*llmHarnessDisplayTurn
	calls   map[string]toolCallDisplay
	waiters map[string]chan struct{}
	closed  bool
}

func newLLMHarnessDisplay(parentCtx context.Context, callDigest string) *llmHarnessDisplay {
	return &llmHarnessDisplay{
		parentCtx:  parentCtx,
		callDigest: callDigest,
		turns:      map[string]*llmHarnessDisplayTurn{},
		calls:      map[string]toolCallDisplay{},
		waiters:    map[string]chan struct{}{},
	}
}

func (display *llmHarnessDisplay) prompt(input LLMHarnessInput) {
	for _, message := range input.Content {
		if message.Role != LLMMessageRoleUser || message.TextContent() == "" {
			continue
		}
		attrs := []attribute.KeyValue{
			attribute.String(telemetry.UIActorEmojiAttr, "🧑"),
			attribute.String(telemetry.UIMessageAttr, telemetry.UIMessageSent),
			attribute.String(telemetry.LLMRoleAttr, telemetry.LLMRoleUser),
			attribute.String(llmHarnessDaggerMessageIDAttr, input.DaggerMessageID),
			attribute.String(llmHarnessVendorMessageIDAttr, input.VendorMessageID),
		}
		attrs = append(attrs, genAIAgentAttrsFromContext(display.parentCtx)...)
		if display.callDigest != "" {
			attrs = append(attrs, attribute.String(telemetryattrs.LLMCallDigestAttr, display.callDigest))
		}
		promptCtx, span := Tracer(display.parentCtx).Start(display.parentCtx, "LLM prompt", trace.WithAttributes(attrs...))
		stdio := telemetry.SpanStdio(promptCtx, InstrumentationLibrary,
			log.String(telemetry.ContentTypeAttr, "text/markdown"))
		fmt.Fprint(stdio.Stdout, message.TextContent())
		_ = stdio.Close()
		span.End()
	}
}

func (display *llmHarnessDisplay) startTurn(turnID string) {
	if turnID == "" {
		return
	}
	display.mu.Lock()
	defer display.mu.Unlock()
	if display.closed {
		return
	}
	if _, ok := display.turns[turnID]; ok {
		return
	}
	display.turns[turnID] = &llmHarnessDisplayTurn{
		phases:    newDisplayPhases(display.parentCtx, display.callDigest),
		callIDs:   map[string]struct{}{},
		toolSpans: map[trace.Span]struct{}{},
	}
}

func (display *llmHarnessDisplay) text(turnID string, event LLMHarnessTextDelta) {
	if turn := display.turn(turnID); turn != nil {
		fmt.Fprint(turn.phases.StartText(event.Block).MarkdownW, event.Delta)
	}
}

func (display *llmHarnessDisplay) thinking(turnID string, event LLMHarnessThinkingDelta) {
	if turn := display.turn(turnID); turn != nil {
		fmt.Fprint(turn.phases.StartThinking(event.Block).Stdio.Stdout, event.Delta)
	}
}

func (display *llmHarnessDisplay) toolCall(turnID string, event LLMHarnessToolCall) {
	turn := display.turn(turnID)
	if turn == nil || event.CallID == "" {
		return
	}
	turn.phases.EmitToolCall(event.Block, event.CallID, event.Name, string(event.Arguments))
	toolDisplay, ok := turn.phases.ToolCall(event.CallID)
	if !ok {
		return
	}
	toolDisplay.Span.SetAttributes(
		attribute.String(llmHarnessNativeTurnIDAttr, turnID),
		attribute.String(llmHarnessToolCallIDAttr, event.CallID),
		attribute.String(llmHarnessToolSourceAttr, string(event.Source)),
	)

	display.mu.Lock()
	defer display.mu.Unlock()
	if display.closed {
		return
	}
	turn.callIDs[event.CallID] = struct{}{}
	turn.toolSpans[toolDisplay.Span] = struct{}{}
	display.calls[event.CallID] = toolDisplay
	if waiter := display.waiters[event.CallID]; waiter != nil {
		close(waiter)
		delete(display.waiters, event.CallID)
	}
}

func (display *llmHarnessDisplay) toolResult(turnID string, event LLMHarnessToolResult) {
	turn := display.turn(turnID)
	if turn == nil {
		return
	}
	turn.phases.FinishToolCall(event.CallID, event.Text, event.Error)
	display.mu.Lock()
	delete(display.calls, event.CallID)
	display.mu.Unlock()
}

func (display *llmHarnessDisplay) turn(turnID string) *llmHarnessDisplayTurn {
	display.mu.Lock()
	defer display.mu.Unlock()
	return display.turns[turnID]
}

// mcpCallContext preserves the HTTP request's Dagger/session values while
// replacing only its OTel parent with the exact native tool-call span.
func (display *llmHarnessDisplay) mcpCallContext(ctx context.Context, request mcp.CallToolRequest) context.Context {
	callID := ""
	if request.Params.Meta != nil {
		callID, _ = request.Params.Meta.AdditionalFields[llmHarnessMCPCallIDMetaKey].(string)
	}
	if callID == "" {
		return ctx
	}

	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for {
		display.mu.Lock()
		if toolDisplay, ok := display.calls[callID]; ok {
			display.mu.Unlock()
			return trace.ContextWithSpan(ctx, toolDisplay.Span)
		}
		if display.closed {
			display.mu.Unlock()
			return ctx
		}
		waiter := display.waiters[callID]
		if waiter == nil {
			waiter = make(chan struct{})
			display.waiters[callID] = waiter
		}
		display.mu.Unlock()

		select {
		case <-ctx.Done():
			display.removeWaiter(callID, waiter)
			return ctx
		case <-timer.C:
			display.removeWaiter(callID, waiter)
			return ctx
		case <-waiter:
		}
	}
}

func (display *llmHarnessDisplay) removeWaiter(callID string, waiter chan struct{}) {
	display.mu.Lock()
	if display.waiters[callID] == waiter {
		delete(display.waiters, callID)
	}
	display.mu.Unlock()
}

func (display *llmHarnessDisplay) finishTurn(turnID string, terminalErr error) {
	display.mu.Lock()
	turn := display.turns[turnID]
	delete(display.turns, turnID)
	if turn != nil {
		for callID := range turn.callIDs {
			delete(display.calls, callID)
		}
	}
	display.mu.Unlock()
	if turn == nil {
		return
	}

	turn.phases.CloseAll()
	spans, toolCalls := turn.phases.Response()
	for _, toolCall := range toolCalls {
		if terminalErr != nil {
			toolCall.Span.RecordError(terminalErr)
			toolCall.Span.SetStatus(codes.Error, terminalErr.Error())
		}
		toolCall.Span.End()
	}
	for _, span := range spans {
		if _, isTool := turn.toolSpans[span]; isTool {
			continue
		}
		span.End()
	}
}

func (display *llmHarnessDisplay) close(err error) {
	display.mu.Lock()
	if display.closed {
		display.mu.Unlock()
		return
	}
	display.closed = true
	turnIDs := make([]string, 0, len(display.turns))
	for turnID := range display.turns {
		turnIDs = append(turnIDs, turnID)
	}
	for callID, waiter := range display.waiters {
		close(waiter)
		delete(display.waiters, callID)
	}
	display.mu.Unlock()
	for _, turnID := range turnIDs {
		display.finishTurn(turnID, err)
	}
}
