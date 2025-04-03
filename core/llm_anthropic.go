package core

import (
	"context"
	"encoding/json"
	"fmt"

	"dagger.io/dagger/telemetry"
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

type AnthropicClient struct {
	client   *anthropic.Client
	endpoint *LLMEndpoint
}

func newAnthropicClient(endpoint *LLMEndpoint) *AnthropicClient {
	opts := []option.RequestOption{option.WithAPIKey(endpoint.Key)}
	if endpoint.Key != "" {
		opts = append(opts, option.WithAPIKey(endpoint.Key))
	}
	if endpoint.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(endpoint.BaseURL))
	}
	client := anthropic.NewClient(opts...)
	return &AnthropicClient{
		client:   client,
		endpoint: endpoint,
	}
}

var ephemeral = anthropic.F(anthropic.CacheControlEphemeralParam{
	Type: anthropic.F(anthropic.CacheControlEphemeralTypeEphemeral),
})

// Anthropic's API only allows 4 cache breakpoints.
const maxAnthropicCacheBlocks = 4

// Set a reasonable threshold for when we should start caching.
//
// Sonnet's minimum is 1024, Haiku's is 2048. Better to err on the higher side
// so we don't waste cache breakpoints.
const anthropicCacheThreshold = 2048

//nolint:gocyclo
func (c *AnthropicClient) SendQuery(ctx context.Context, history []ModelMessage, tools []LLMTool) (res *LLMResponse, rerr error) {
	ctx, span := Tracer(ctx).Start(ctx, "LLM query", telemetry.Reveal(), trace.WithAttributes(
		attribute.String(telemetry.UIActorEmojiAttr, "🤖"),
		attribute.String(telemetry.UIMessageAttr, "received"),
	))
	defer telemetry.End(span, func() error { return rerr })

	stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary)
	defer stdio.Close()

	markdownW := telemetry.NewWriter(ctx, InstrumentationLibrary,
		log.String(telemetry.ContentTypeAttr, "text/markdown"))

	m := telemetry.Meter(ctx, InstrumentationLibrary)
	attrs := []attribute.KeyValue{
		attribute.String(telemetry.MetricsTraceIDAttr, span.SpanContext().TraceID().String()),
		attribute.String(telemetry.MetricsSpanIDAttr, span.SpanContext().SpanID().String()),
		attribute.String("model", c.endpoint.Model),
		attribute.String("provider", string(c.endpoint.Provider)),
	}

	inputTokens, err := m.Int64Gauge(telemetry.LLMInputTokens)
	if err != nil {
		return nil, err
	}

	inputTokensCacheReads, err := m.Int64Gauge(telemetry.LLMInputTokensCacheReads)
	if err != nil {
		return nil, err
	}

	inputTokensCacheWrites, err := m.Int64Gauge(telemetry.LLMInputTokensCacheWrites)
	if err != nil {
		return nil, err
	}

	outputTokens, err := m.Int64Gauge(telemetry.LLMOutputTokens)
	if err != nil {
		return nil, err
	}

	// Convert generic messages to Anthropic-specific message parameters.
	var messages []anthropic.MessageParam
	var systemPrompts []anthropic.TextBlockParam
	var cachedBlocks int
	for _, msg := range history {
		var blocks []anthropic.ContentBlockParamUnion

		// Anthropic's API sometimes returns an empty content whilst not accepting it:
		// anthropic.BadRequestError: Error code: 400 - {'type': 'error', 'error': {'type': 'invalid_request_error', 'message': 'messages: text content blocks must be non-empty'}}
		// This workaround overwrites the empty content to space character
		// As soon as this issue is resolved, we can remove this hack
		// https://github.com/anthropics/anthropic-sdk-python/issues/461#issuecomment-2141882744
		content := msg.Content
		if content == "" {
			content = " "
		}

		if msg.ToolCallID != "" {
			blocks = append(blocks, anthropic.NewToolResultBlock(
				msg.ToolCallID,
				content,
				msg.ToolErrored,
			))
		} else {
			blocks = append(blocks, anthropic.NewTextBlock(content))
		}

		// add tool usage blocks first so they get cached when setting
		// CacheControl below
		for _, call := range msg.ToolCalls {
			blocks = append(blocks, anthropic.NewToolUseBlockParam(
				call.ID,
				call.Function.Name,
				call.Function.Arguments,
			))
		}

		cacheControl := anthropic.Null[anthropic.CacheControlEphemeralParam]()

		// enable caching based on simple token usage heuristic
		if msg.TokenUsage.TotalTokens > anthropicCacheThreshold && cachedBlocks < maxAnthropicCacheBlocks {
			cacheControl = ephemeral
			cachedBlocks++
		}

		if len(blocks) > 0 {
			lastBlock := blocks[len(blocks)-1]
			switch x := lastBlock.(type) {
			case anthropic.TextBlockParam:
				x.CacheControl = cacheControl
				blocks[len(blocks)-1] = x
			case anthropic.ToolUseBlockParam:
				x.CacheControl = cacheControl
				blocks[len(blocks)-1] = x
			case anthropic.ToolResultBlockParam:
				x.CacheControl = cacheControl
				blocks[len(blocks)-1] = x
			}
		}

		switch msg.Role {
		case "user":
			messages = append(messages, anthropic.NewUserMessage(blocks...))
		case "assistant":
			messages = append(messages, anthropic.NewAssistantMessage(blocks...))
		case "system":
			// Collect all system prompt messages.
			systemPrompts = append(systemPrompts, anthropic.NewTextBlock(msg.Content))
		}
	}

	// Convert tools to Anthropic tool format.
	var toolsConfig []anthropic.ToolUnionUnionParam
	for _, tool := range tools {
		// TODO: figure out cache control. do we want a checkpoint at the end?
		toolsConfig = append(toolsConfig, anthropic.ToolParam{
			Name:        anthropic.F(tool.Name),
			Description: anthropic.F(tool.Description),
			InputSchema: anthropic.F(any(tool.Schema)),
			// CacheControl: ephemeral,
		})
	}

	// Prepare parameters for the streaming call.
	params := anthropic.MessageNewParams{
		Model:     anthropic.F(c.endpoint.Model),
		MaxTokens: anthropic.F(int64(8192)),
		Messages:  anthropic.F(messages),
		Tools:     anthropic.F(toolsConfig),
		System:    anthropic.F(systemPrompts),
	}

	dbgEnc.Encode("---------------------------------------------")
	dbgEnc.Encode(params)
	defer func() {
		if rerr != nil {
			dbgEnc.Encode(fmt.Sprintf("error: %s %T %+v", rerr.Error(), rerr, rerr))
		}
	}()

	// Start a streaming request.
	stream := c.client.Messages.NewStreaming(ctx, params)
	defer stream.Close()

	if err := stream.Err(); err != nil {
		return nil, err
	}

	acc := new(anthropic.Message)
	for stream.Next() {
		event := stream.Current()
		acc.Accumulate(event)

		dbgEnc.Encode(event)

		// Keep track of the token usage
		if acc.Usage.OutputTokens > 0 {
			outputTokens.Record(ctx, acc.Usage.OutputTokens, metric.WithAttributes(attrs...))
		}
		if acc.Usage.InputTokens > 0 {
			inputTokens.Record(ctx, acc.Usage.InputTokens, metric.WithAttributes(attrs...))
		}
		if acc.Usage.CacheReadInputTokens > 0 {
			inputTokensCacheReads.Record(ctx, acc.Usage.CacheReadInputTokens, metric.WithAttributes(attrs...))
		}
		if acc.Usage.CacheCreationInputTokens > 0 {
			inputTokensCacheWrites.Record(ctx, acc.Usage.CacheCreationInputTokens, metric.WithAttributes(attrs...))
		}

		// Check if the event delta contains text and trace it.
		if delta, ok := event.Delta.(anthropic.ContentBlockDeltaEventDelta); ok {
			if delta.Text != "" {
				// Lazily initialize telemetry/logging on first text response.
				fmt.Fprint(markdownW, delta.Text)
			}
		}
	}

	if err := stream.Err(); err != nil {
		return nil, err
	}

	// Check that we have some accumulated content.
	if len(acc.Content) == 0 {
		return nil, &ModelFinishedError{
			Reason: string(acc.StopReason),
		}
	}

	// Process the accumulated content into a generic LLMResponse.
	var content string
	var toolCalls []ToolCall
	for _, block := range acc.Content {
		switch b := block.AsUnion().(type) {
		case anthropic.TextBlock:
			// Append text from text blocks.
			content += b.Text
		case anthropic.ToolUseBlock:
			var args map[string]any
			if len(b.Input) > 0 {
				if err := json.Unmarshal([]byte(b.Input), &args); err != nil {
					return nil, fmt.Errorf("failed to unmarshal tool input: %w", err)
				}
			}
			// Map tool-use blocks to our generic tool call structure.
			toolCalls = append(toolCalls, ToolCall{
				ID: b.ID,
				Function: FuncCall{
					Name:      b.Name,
					Arguments: args,
				},
				Type: "function",
			})
		}
	}

	return &LLMResponse{
		Content:   content,
		ToolCalls: toolCalls,
		TokenUsage: LLMTokenUsage{
			InputTokens:  acc.Usage.InputTokens,
			OutputTokens: acc.Usage.OutputTokens,
			TotalTokens:  acc.Usage.InputTokens + acc.Usage.OutputTokens,
		},
	}, nil
}
