package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"
	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

type AnthropicClient struct {
	client   *anthropic.Client
	endpoint *LLMEndpoint
}

func newAnthropicClient(endpoint *LLMEndpoint) *AnthropicClient {
	var opts []option.RequestOption
	switch {
	case endpoint.IsOAuth:
		// Claude Code subscription OAuth: bearer token + Claude Code identity
		// headers. The endpoint rejects requests that don't look like Claude
		// Code (see also the system-prompt injection in SendQuery).
		opts = append(opts,
			option.WithAuthToken(endpoint.AuthToken),
			option.WithHeader("anthropic-beta", "claude-code-20250219,oauth-2025-04-20"),
			option.WithHeader("user-agent", "claude-cli/2.1.2 (external, cli)"),
			option.WithHeader("x-app", "cli"),
		)
	case endpoint.Key != "":
		opts = append(opts, option.WithAPIKey(endpoint.Key))
	}
	if endpoint.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(endpoint.BaseURL))
	}
	opts = append(opts, option.WithHTTPClient(endpoint.otelHTTPClient("anthropic")))
	client := anthropic.NewClient(opts...)
	return &AnthropicClient{
		client:   &client,
		endpoint: endpoint,
	}
}

var ephemeral = anthropic.CacheControlEphemeralParam{Type: constant.Ephemeral("").Default()}

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
func (c *AnthropicClient) SendQuery(ctx context.Context, history []*LLMMessage, tools []LLMTool, opts *LLMCallOpts) (res *LLMResponse, rerr error) {
	// Stream this turn's content into per-block display spans (thinking, text
	// response, tool-call arguments) as it arrives.
	dp := newDisplayPhases(ctx, opts.CallDigest)
	defer func() {
		dp.CloseAll()
		if rerr != nil {
			dp.Abort(rerr)
		}
	}()

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

	// Reasoning (extended thinking) is enabled for this request only when an
	// effort is configured. This single condition gates both OutputConfig.Effort
	// below and whether prior thinking blocks may be replayed: Anthropic rejects
	// a request that carries thinking blocks when thinking isn't enabled for it
	// ("thinking blocks without thinking enabled"). If a prior turn produced
	// thinking but this turn has reasoning off, the stored blocks must be dropped.
	reasoningEnabled := c.endpoint.ReasoningEffort != "" && c.endpoint.ReasoningEffort != "none"

	// Convert content-block messages to Anthropic-specific message parameters.
	var messages []anthropic.MessageParam
	var systemPrompts []anthropic.TextBlockParam
	for _, msg := range history {
		if msg.Role == LLMMessageRoleSystem {
			text := msg.TextContent()
			if text != "" {
				systemPrompts = append(systemPrompts, anthropic.TextBlockParam{Text: text})
			}
			continue
		}

		var blocks []anthropic.ContentBlockParamUnion
		for _, block := range msg.Content {
			switch block.Kind {
			case LLMContentText:
				// Anthropic's API rejects empty text content blocks:
				// "messages: text content blocks must be non-empty". Substitute
				// a space to work around it.
				text := block.Text
				if text == "" {
					text = " "
				}
				blocks = append(blocks, anthropic.NewTextBlock(text))
			case LLMContentToolCall:
				var args map[string]any
				if len(block.Arguments) > 0 {
					if err := json.Unmarshal(block.Arguments.Bytes(), &args); err != nil {
						return nil, fmt.Errorf("failed to unmarshal tool arguments: %w", err)
					}
				}
				blocks = append(blocks, anthropic.NewToolUseBlock(block.CallID, args, block.ToolName))
			case LLMContentToolResult:
				content := block.Text
				if content == "" {
					content = " "
				}
				blocks = append(blocks, anthropic.NewToolResultBlock(block.CallID, content, block.Errored))
			case LLMContentThinking:
				// Round-trip extended thinking. When thinking is enabled and the
				// assistant made tool calls, Anthropic requires the original
				// thinking blocks to be replayed unmodified with their signature,
				// or it rejects the request. A block with no text but a signature
				// is a redacted thinking block (opaque data stored in Signature).
				//
				// Only replay thinking (and redacted-thinking) blocks when
				// reasoning is enabled for this request. If it isn't (effort
				// cleared, or a model that doesn't support it), Anthropic rejects
				// a request that carries thinking blocks, so drop them instead.
				if !reasoningEnabled {
					continue
				}
				switch {
				case block.Text != "":
					blocks = append(blocks, anthropic.NewThinkingBlock(block.Signature, block.Text))
				case block.Signature != "":
					blocks = append(blocks, anthropic.NewRedactedThinkingBlock(block.Signature))
				}
			}
		}

		switch msg.Role {
		case LLMMessageRoleUser:
			messages = appendOrMerge(messages, anthropic.MessageParamRoleUser, blocks)
		case LLMMessageRoleAssistant:
			messages = appendOrMerge(messages, anthropic.MessageParamRoleAssistant, blocks)
		}
	}

	// Add cache_control breakpoints. Anthropic allows at most 4 per request.
	// Place them on the last system prompt block and the last block of the
	// last user message, so the next turn gets a cache hit on all preceding
	// content.
	if len(systemPrompts) > 0 {
		systemPrompts[len(systemPrompts)-1].CacheControl = ephemeral
	}
	if len(messages) > 0 {
		for i := len(messages) - 1; i >= 0; i-- {
			if messages[i].Role != anthropic.MessageParamRoleUser {
				continue
			}
			blocks := messages[i].Content
			if len(blocks) > 0 {
				lastBlock := &blocks[len(blocks)-1]
				switch {
				case lastBlock.OfText != nil:
					lastBlock.OfText.CacheControl = ephemeral
				case lastBlock.OfToolUse != nil:
					lastBlock.OfToolUse.CacheControl = ephemeral
				case lastBlock.OfToolResult != nil:
					lastBlock.OfToolResult.CacheControl = ephemeral
				}
			}
			break
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

	// Claude Code subscription OAuth requires the Claude Code identity system
	// prompt to be present, or the endpoint rejects the request.
	if c.endpoint.IsOAuth {
		claudeCodePrompt := anthropic.TextBlockParam{
			Text:         "You are Claude Code, Anthropic's official CLI for Claude.",
			CacheControl: ephemeral,
		}
		systemPrompts = append([]anthropic.TextBlockParam{claudeCodePrompt}, systemPrompts...)
	}

	// Prepare parameters for the streaming call. The API requires max_tokens;
	// default to the model's own output cap (from the catwalk catalog) so
	// replies are never artificially truncated, keeping a conservative
	// fallback for models the catalog doesn't know (e.g. Anthropic-compatible
	// local endpoints).
	userSetMaxTokens := opts != nil && opts.MaxTokens > 0
	maxTokens := c.endpoint.DefaultMaxTokens
	if maxTokens <= 0 {
		maxTokens = 8192
	}
	if userSetMaxTokens {
		maxTokens = int64(opts.MaxTokens)
	}

	// Configure reasoning effort, if requested. Anthropic takes the level
	// straight through as output_config.effort; when on the conservative
	// fallback default, leave room for reasoning tokens on top of the reply
	// so the answer isn't truncated. An explicit maxTokens cap is respected
	// as-is.
	var outputConfig anthropic.OutputConfigParam
	if reasoningEnabled {
		outputConfig.Effort = anthropic.OutputConfigEffort(c.endpoint.ReasoningEffort)
		if !userSetMaxTokens && maxTokens < 16384 {
			maxTokens = 16384
		}
	}

	// Cap max_tokens to the context window's remaining space; the API rejects
	// requests whose input tokens + max_tokens exceed it.
	maxTokens = clampMaxTokensToContext(maxTokens, c.endpoint.ContextWindow, history, tools)

	params := anthropic.MessageNewParams{
		Model:        c.endpoint.Model,
		MaxTokens:    maxTokens,
		Messages:     messages,
		Tools:        toolsConfig,
		System:       systemPrompts,
		OutputConfig: outputConfig,
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

		// Route each content block's stream into its own display span: thinking
		// and tool-call arguments (which main otherwise only shows post-hoc) as
		// well as the text response.
		switch ev := event.AsAny().(type) {
		case anthropic.ContentBlockStartEvent:
			switch ev.ContentBlock.Type {
			case "thinking":
				dp.StartThinking(ev.Index)
			case "text":
				dp.StartText(ev.Index)
			case "tool_use":
				dp.StartToolCall(ev.Index, ev.ContentBlock.ID, ev.ContentBlock.Name)
			}
		case anthropic.ContentBlockDeltaEvent:
			switch ev.Delta.Type {
			case "thinking_delta":
				if p := dp.Phase(ev.Index); p != nil {
					fmt.Fprint(p.Stdio.Stdout, ev.Delta.Thinking)
				}
			case "text_delta":
				p := dp.Phase(ev.Index)
				if p == nil {
					// Text without a start event: lazily open a response phase.
					p = dp.StartText(ev.Index)
				}
				if p.MarkdownW != nil {
					fmt.Fprint(p.MarkdownW, ev.Delta.Text)
				}
			case "input_json_delta":
				if p := dp.Phase(ev.Index); p != nil {
					fmt.Fprint(p.Stdio.Stdout, ev.Delta.PartialJSON)
				}
			}
		case anthropic.ContentBlockStopEvent:
			dp.Close(ev.Index)
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

	// Process the accumulated content into content blocks.
	var contentBlocks []*LLMContentBlock
	for _, block := range acc.Content {
		switch b := block.AsAny().(type) {
		case anthropic.TextBlock:
			contentBlocks = append(contentBlocks, &LLMContentBlock{
				Kind: LLMContentText,
				Text: b.Text,
			})
		case anthropic.ToolUseBlock:
			contentBlocks = append(contentBlocks, &LLMContentBlock{
				Kind:      LLMContentToolCall,
				CallID:    b.ID,
				ToolName:  b.Name,
				Arguments: JSON(b.Input),
			})
		case anthropic.ThinkingBlock:
			contentBlocks = append(contentBlocks, &LLMContentBlock{
				Kind:      LLMContentThinking,
				Text:      b.Thinking,
				Signature: b.Signature,
			})
		case anthropic.RedactedThinkingBlock:
			// Redacted thinking carries no readable text, only opaque data that
			// must be replayed verbatim. Stash it in Signature so the send path
			// can reconstruct the block.
			contentBlocks = append(contentBlocks, &LLMContentBlock{
				Kind:      LLMContentThinking,
				Signature: b.Data,
			})
		}
	}

	displaySpans, toolCallDisplays := dp.Response()
	return &LLMResponse{
		Content: contentBlocks,
		TokenUsage: LLMTokenUsage{
			InputTokens:       acc.Usage.InputTokens,
			OutputTokens:      acc.Usage.OutputTokens,
			CachedTokenReads:  acc.Usage.CacheReadInputTokens,
			CachedTokenWrites: acc.Usage.CacheCreationInputTokens,
			TotalTokens:       acc.Usage.InputTokens + acc.Usage.OutputTokens + acc.Usage.CacheReadInputTokens + acc.Usage.CacheCreationInputTokens,
		},
		DisplaySpans:     displaySpans,
		ToolCallDisplays: toolCallDisplays,
	}, nil
}

// appendOrMerge appends content blocks to the messages slice. If the last
// message has the same role, the blocks are merged into it rather than
// creating a new message. This is required by the Anthropic API, which
// mandates alternating user/assistant roles — consecutive tool_result blocks
// from parallel tool calls must be in a single user message.
func appendOrMerge(messages []anthropic.MessageParam, role anthropic.MessageParamRole, blocks []anthropic.ContentBlockParamUnion) []anthropic.MessageParam {
	if len(messages) > 0 && messages[len(messages)-1].Role == role {
		messages[len(messages)-1].Content = append(messages[len(messages)-1].Content, blocks...)
		return messages
	}
	return append(messages, anthropic.MessageParam{
		Role:    role,
		Content: blocks,
	})
}
