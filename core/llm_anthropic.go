package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"dagger.io/dagger/telemetry"
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"
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
		client:   &client,
		endpoint: endpoint,
	}
}

var ephemeral = anthropic.CacheControlEphemeralParam{Type: constant.Ephemeral("").Default()}

// Anthropic's API only allows 4 cache breakpoints.
const maxAnthropicCacheBlocks = 4

// Set a reasonable threshold for when we should start caching.
//
// Sonnet's minimum is 1024, Haiku's is 2048. Better to err on the higher side
// so we don't waste cache breakpoints.
const anthropicCacheThreshold = 2048

var _ LLMClient = (*AnthropicClient)(nil)

var anthropicRetryable = []string{
	// there's gotta be a better way to do this...
	string(constant.RateLimitError("").Default()),
	string(constant.OverloadedError("").Default()),
	"Internal server error",
}

func (c *AnthropicClient) IsRetryable(err error) bool {
	msg := err.Error()
	for _, retryable := range anthropicRetryable {
		if strings.Contains(msg, retryable) {
			return true
		}
	}
	return false
}

//nolint:gocyclo
func (c *AnthropicClient) SendQuery(ctx context.Context, history []*ModelMessage, tools []LLMTool) (res *LLMResponse, rerr error) {
	stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary)
	defer stdio.Close()

	markdownW := telemetry.NewWriter(ctx, InstrumentationLibrary,
		log.String(telemetry.ContentTypeAttr, "text/markdown"))

	m := telemetry.Meter(ctx, InstrumentationLibrary)
	spanCtx := trace.SpanContextFromContext(ctx)
	attrs := []attribute.KeyValue{
		attribute.String(telemetry.MetricsTraceIDAttr, spanCtx.TraceID().String()),
		attribute.String(telemetry.MetricsSpanIDAttr, spanCtx.SpanID().String()),
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
			blocks = append(blocks, anthropic.NewToolUseBlock(call.ID, call.Function.Arguments, call.Function.Name))
		}

		// enable caching based on simple token usage heuristic
		var cacheControl anthropic.CacheControlEphemeralParam
		if msg.TokenUsage.TotalTokens > anthropicCacheThreshold && cachedBlocks < maxAnthropicCacheBlocks {
			cacheControl = ephemeral
			cachedBlocks++
		}

		if len(blocks) > 0 {
			lastBlock := &blocks[len(blocks)-1]
			switch {
			case lastBlock.OfText != nil:
				lastBlock.OfText.CacheControl = cacheControl
			case lastBlock.OfToolUse != nil:
				lastBlock.OfToolUse.CacheControl = cacheControl
			case lastBlock.OfToolResult != nil:
				lastBlock.OfToolResult.CacheControl = cacheControl
			}
		}

		switch msg.Role {
		case "user":
			messages = append(messages, anthropic.NewUserMessage(blocks...))
		case "assistant":
			messages = append(messages, anthropic.NewAssistantMessage(blocks...))
		case "system":
			// Collect all system prompt messages.
			systemPrompts = append(systemPrompts, anthropic.TextBlockParam{Text: msg.Content})
		}
	}

	// Convert tools to Anthropic tool format.
	var toolsConfig []anthropic.ToolUnionParam
	for _, tool := range tools {
		// TODO: figure out cache control. do we want a checkpoint at the end?
		var inputSchema anthropic.ToolInputSchemaParam
		for k, v := range tool.Schema {
			switch k {
			case "properties":
				inputSchema.Properties = v
			case "type":
				if v != "object" {
					return nil, fmt.Errorf("tool must accept object, got %q", v)
				}
				inputSchema.Type = "object"
			default:
				if inputSchema.ExtraFields == nil {
					inputSchema.ExtraFields = make(map[string]any)
				}
				inputSchema.ExtraFields[k] = v
			}
		}
		toolsConfig = append(toolsConfig, anthropic.ToolUnionParam{
			OfTool: &anthropic.ToolParam{
				Name:        tool.Name,
				Description: anthropic.Opt(tool.Description),
				InputSchema: inputSchema,
				// CacheControl: ephemeral,
			},
		})
	}

	// Prepare parameters for the streaming call.
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(c.endpoint.Model),
		MaxTokens: int64(8192),
		Messages:  messages,
		Tools:     toolsConfig,
		System:    systemPrompts,
	}

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
		if delta, ok := event.AsAny().(anthropic.ContentBlockDeltaEvent); ok {
			if delta.Delta.Text != "" {
				// Lazily initialize telemetry/logging on first text response.
				fmt.Fprint(markdownW, delta.Delta.Text)
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
	var toolCalls []LLMToolCall
	for _, block := range acc.Content {
		switch b := block.AsAny().(type) {
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
			toolCalls = append(toolCalls, LLMToolCall{
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
			InputTokens:       acc.Usage.InputTokens,
			OutputTokens:      acc.Usage.OutputTokens,
			CachedTokenReads:  acc.Usage.CacheReadInputTokens,
			CachedTokenWrites: acc.Usage.CacheCreationInputTokens,
			TotalTokens:       acc.Usage.InputTokens + acc.Usage.OutputTokens,
		},
	}, nil
}
