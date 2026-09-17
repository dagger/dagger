package core

import (
	"context"
	"strings"
	"testing"

	telemetry "github.com/dagger/otel-go"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

type harnessDisplayTestKey struct{}

func TestLLMHarnessDisplayStreamsAndParentsExactMCPCall(t *testing.T) {
	spans, ctx := replayTestRecorder(t)
	logs := &stateRecorder{}
	ctx = telemetry.WithLoggerProvider(ctx, sdklog.NewLoggerProvider(sdklog.WithProcessor(logs)))
	display := newLLMHarnessDisplay(ctx, "sha256:call")

	display.prompt(LLMHarnessInput{
		DaggerMessageID: "dagger-message",
		VendorMessageID: "vendor-message",
		Content:         harnessPrompt("hello live harness"),
	})
	display.startTurn("turn-1")
	display.text("turn-1", LLMHarnessTextDelta{Block: 1, Delta: "streamed reply"})
	display.toolCall("turn-1", LLMHarnessToolCall{
		Block:     2,
		CallID:    "native-call-1",
		Name:      "workspace_read",
		Arguments: JSON(`{"path":"README.md"}`),
		Source:    LLMHarnessToolSourceMCP,
	})

	request := mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name: "workspace_read",
		Meta: &mcp.Meta{AdditionalFields: map[string]any{
			llmHarnessMCPCallIDMetaKey: "native-call-1",
		}},
	}}
	toolCtx := display.mcpCallContext(context.WithValue(ctx, harnessDisplayTestKey{}, "preserved"), request)
	_, evalSpan := Tracer(toolCtx).Start(toolCtx, "Dagger MCP evaluation")
	evalSpan.End()
	display.toolResult("turn-1", LLMHarnessToolResult{CallID: "native-call-1", Text: "README contents"})
	display.finishTurn("turn-1", nil)

	ended := spans.Ended()
	prompt := otelprofSpanByName(t, ended, "LLM prompt")
	response := otelprofSpanByName(t, ended, "LLM response")
	tool := otelprofSpanByName(t, ended, "workspace_read")
	eval := otelprofSpanByName(t, ended, "Dagger MCP evaluation")
	require.Equal(t, prompt.SpanContext().TraceID(), response.SpanContext().TraceID())
	require.Equal(t, tool.SpanContext().SpanID(), eval.Parent().SpanID())
	require.Equal(t, "dagger-message", otelprofAttrStr(prompt, llmHarnessDaggerMessageIDAttr))
	require.Equal(t, "vendor-message", otelprofAttrStr(prompt, llmHarnessVendorMessageIDAttr))
	require.Equal(t, "native-call-1", otelprofAttrStr(tool, llmHarnessToolCallIDAttr))
	require.Equal(t, string(LLMHarnessToolSourceMCP), otelprofAttrStr(tool, llmHarnessToolSourceAttr))

	logs.mu.Lock()
	defer logs.mu.Unlock()
	var output strings.Builder
	for _, record := range logs.records {
		output.WriteString(record.body)
	}
	require.Contains(t, output.String(), "hello live harness")
	require.Contains(t, output.String(), "streamed reply")
	require.Contains(t, output.String(), `{"path":"README.md"}`)
	require.Contains(t, output.String(), "README contents")
}

func TestLLMHarnessDisplayWaitsForCodexCallLifecycle(t *testing.T) {
	spans, ctx := replayTestRecorder(t)
	display := newLLMHarnessDisplay(ctx, "")
	display.startTurn("turn-1")

	request := mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name: "workspace_read",
		Meta: &mcp.Meta{AdditionalFields: map[string]any{
			llmHarnessMCPCallIDMetaKey: "racing-call",
		}},
	}}
	parentReady := make(chan context.Context, 1)
	go func() { parentReady <- display.mcpCallContext(ctx, request) }()
	display.toolCall("turn-1", LLMHarnessToolCall{
		Block: 2, CallID: "racing-call", Name: "workspace_read",
		Arguments: JSON(`{}`), Source: LLMHarnessToolSourceMCP,
	})

	toolCtx := <-parentReady
	_, evalSpan := Tracer(toolCtx).Start(toolCtx, "racing evaluation")
	evalSpan.End()
	display.toolResult("turn-1", LLMHarnessToolResult{CallID: "racing-call", Text: "ok"})
	display.finishTurn("turn-1", nil)

	tool := otelprofSpanByName(t, spans.Ended(), "workspace_read")
	eval := otelprofSpanByName(t, spans.Ended(), "racing evaluation")
	require.Equal(t, tool.SpanContext().SpanID(), eval.Parent().SpanID())
}
