package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"dagger.io/dagger/telemetry"
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/dagger/dagger/core/bbi"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/trace"
)

type AnthropicClient struct {
	client              *anthropic.Client
	endpoint            *LlmEndpoint
	defaultSystemPrompt string
}

func newAnthropicClient(endpoint *LlmEndpoint, defaultSystemPrompt string) *AnthropicClient {
	opts := []option.RequestOption{option.WithAPIKey(endpoint.Key)}
	if endpoint.Key != "" {
		opts = append(opts, option.WithAPIKey(endpoint.Key))
	}
	if endpoint.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(endpoint.BaseURL))
	}
	client := anthropic.NewClient(opts...)
	return &AnthropicClient{
		client:              client,
		endpoint:            endpoint,
		defaultSystemPrompt: defaultSystemPrompt,
	}
}

func (c *AnthropicClient) SendQuery(ctx context.Context, history []ModelMessage, tools []bbi.Tool) (*LLMResponse, error) {
	// Convert generic messages to Anthropic-specific message parameters.
	var messages []anthropic.MessageParam
	var systemPrompts []anthropic.TextBlockParam
	for _, msg := range history {
		var blocks []anthropic.ContentBlockParamUnion

		// Anthropic's API sometimes returns an empty content whilst not accepting it:
		// anthropic.BadRequestError: Error code: 400 - {'type': 'error', 'error': {'type': 'invalid_request_error', 'message': 'messages: text content blocks must be non-empty'}}
		// This workaround overwrites the empty content to space character
		// As soon as this issue is resolved, we can remove this hack
		// https://github.com/anthropics/anthropic-sdk-python/issues/461#issuecomment-2141882744
		content := msg.Content.(string)
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
		for _, call := range msg.ToolCalls {
			blocks = append(blocks, anthropic.NewToolUseBlockParam(
				call.ID,
				call.Function.Name,
				call.Function.Arguments,
			))
		}
		switch msg.Role {
		case "user":
			messages = append(messages, anthropic.NewUserMessage(blocks...))
		case "assistant":
			messages = append(messages, anthropic.NewAssistantMessage(blocks...))
		case "system":
			// Collect all system prompt messages.
			systemPrompts = append(systemPrompts, anthropic.NewTextBlock(msg.Content.(string)))
		}
	}

	// If no system messages were found, use the default system prompt.
	if len(systemPrompts) == 0 {
		systemPrompts = []anthropic.TextBlockParam{anthropic.NewTextBlock(c.defaultSystemPrompt)}
	}

	// Convert tools to Anthropic tool format.
	var toolsConfig []anthropic.ToolParam
	for _, tool := range tools {
		toolsConfig = append(toolsConfig, anthropic.ToolParam{
			Name:        anthropic.F(tool.Name),
			Description: anthropic.F(tool.Description),
			InputSchema: anthropic.F(interface{}(tool.Schema)),
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

	// Start a streaming request.
	stream := c.client.Messages.NewStreaming(ctx, params)
	defer stream.Close()

	var logsW io.Writer
	acc := new(anthropic.Message)
	// Loop over the streamed events.
	for stream.Next() {
		if err := stream.Err(); err != nil {
			return nil, err
		}

		event := stream.Current()
		acc.Accumulate(event)

		// Check if the event delta contains text and trace it.
		switch delta := event.Delta.(type) {
		case anthropic.ContentBlockDeltaEventDelta:
			if delta.Text != "" {
				// Lazily initialize telemetry/logging on first text response.
				if logsW == nil {
					ctx, span := Tracer(ctx).Start(ctx, "LLM response", telemetry.Reveal(), trace.WithAttributes(
						attribute.String(telemetry.UIActorEmojiAttr, "🤖"),
						attribute.String(telemetry.UIMessageAttr, "received"),
					))
					defer telemetry.End(span, func() error { return nil })

					stdio := telemetry.SpanStdio(ctx, "", log.String(telemetry.ContentTypeAttr, "text/markdown"))
					logsW = stdio.Stdout
				}
				fmt.Fprint(logsW, delta.Text)
			}
		}
	}

	if err := stream.Err(); err != nil {
		return nil, err
	}

	// Check that we have some accumulated content.
	if len(acc.Content) == 0 {
		return nil, fmt.Errorf("no response from model")
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
	}, nil
}
