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

	if endpoint.IsOAuth {
		// Claude Code OAuth: use bearer token auth with Claude Code headers
		opts = append(opts,
			option.WithAuthToken(endpoint.AuthToken),
			option.WithHeader("anthropic-beta", "claude-code-20250219,oauth-2025-04-20,fine-grained-tool-streaming-2025-05-14"),
			option.WithHeader("user-agent", "claude-cli/2.1.2 (external, cli)"),
			option.WithHeader("x-app", "cli"),
		)
	} else if endpoint.Key != "" {
		opts = append(opts, option.WithAPIKey(endpoint.Key))
		opts = append(opts, option.WithHeader("anthropic-beta", "fine-grained-tool-streaming-2025-05-14"))
	}

	if endpoint.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(endpoint.BaseURL))
	}

	// Inject OTel tracing HTTP client to capture LLM request/response bodies
	opts = append(opts, option.WithHTTPClient(newLLMOTelHTTPClient("anthropic")))

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
	// parentCtx is the context we create sibling spans from (thinking, response).
	// Each phase of the streaming response gets its own span.
	parentCtx := ctx

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
				blocks = append(blocks, anthropic.NewTextBlock(block.Text))
			case LLMContentThinking:
				blocks = append(blocks, anthropic.NewThinkingBlock(block.Signature, block.Text))
			case LLMContentToolCall:
				var args map[string]any
				if err := json.Unmarshal(block.Arguments.Bytes(), &args); err != nil {
					return nil, err
				}
				blocks = append(blocks, anthropic.NewToolUseBlock(block.CallID, args, block.ToolName))
			case LLMContentToolResult:
				blocks = append(blocks, anthropic.NewToolResultBlock(block.CallID, block.Text, block.Errored))
			}
		}

		switch msg.Role {
		case LLMMessageRoleUser:
			messages = appendOrMerge(messages, anthropic.MessageParamRoleUser, blocks)
		case LLMMessageRoleAssistant:
			messages = appendOrMerge(messages, anthropic.MessageParamRoleAssistant, blocks)
		}
	}

	// Add cache_control breakpoints. Anthropic allows at most 4 cache
	// breakpoints per request. We place them on:
	//   1. The last system prompt block (stable across turns)
	//   2. The last block of the last user message (so the next turn
	//      gets a cache hit on all preceding content)
	// This mirrors what Claude Code / pi do and stays well within the
	// 4-breakpoint limit, even when OAuth adds its own cached system
	// prompt.
	if len(systemPrompts) > 0 {
		systemPrompts[len(systemPrompts)-1].CacheControl = ephemeral
	}
	if len(messages) > 0 {
		// Walk backwards to find the last user message.
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

	// When using OAuth (Claude Code subscription), prepend the Claude Code
	// identity system prompt. This is required for the OAuth endpoint to
	// accept the request.
	if c.endpoint.IsOAuth {
		claudeCodePrompt := anthropic.TextBlockParam{
			Text:         "You are Claude Code, Anthropic's official CLI for Claude.",
			CacheControl: ephemeral,
		}
		systemPrompts = append([]anthropic.TextBlockParam{claudeCodePrompt}, systemPrompts...)
	}

	// Prepare parameters for the streaming call.
	maxTokens := int64(8192)
	if opts != nil && opts.MaxTokens > 0 {
		maxTokens = int64(opts.MaxTokens)
	}

	// Configure thinking/reasoning if requested.
	// Named levels from catwalk map to token budgets.
	var thinkingConfig anthropic.ThinkingConfigParamUnion
	switch c.endpoint.ThinkingMode {
	case "adaptive":
		thinkingConfig = anthropic.ThinkingConfigParamUnion{
			OfAdaptive: &anthropic.ThinkingConfigAdaptiveParam{},
		}
		if maxTokens < 16384 {
			maxTokens = 16384
		}
	case "low":
		thinkingConfig = anthropic.ThinkingConfigParamOfEnabled(2048)
		if maxTokens < 2048+1024 {
			maxTokens = 2048 + 1024
		}
	case "medium":
		thinkingConfig = anthropic.ThinkingConfigParamOfEnabled(10000)
		if maxTokens < 10000+1024 {
			maxTokens = 10000 + 1024
		}
	case "high":
		thinkingConfig = anthropic.ThinkingConfigParamOfEnabled(32000)
		if maxTokens < 32000+1024 {
			maxTokens = 32000 + 1024
		}
	case "max":
		thinkingConfig = anthropic.ThinkingConfigParamOfEnabled(128000)
		if maxTokens < 128000+1024 {
			maxTokens = 128000 + 1024
		}
	}

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(c.endpoint.Model),
		MaxTokens: maxTokens,
		Messages:  messages,
		Tools:     toolsConfig,
		System:    systemPrompts,
		Thinking:  thinkingConfig,
	}

	// Start a streaming request.
	stream := c.client.Messages.NewStreaming(ctx, params)
	defer stream.Close()

	if err := stream.Err(); err != nil {
		return nil, err
	}

	acc := new(anthropic.Message)

	dp := newDisplayPhases(parentCtx)
	defer func() {
		dp.CloseAll()
		if rerr != nil {
			dp.Abort(rerr)
		}
	}()

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
					// Lazily create a response phase if we get text without a start event
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
		case anthropic.ThinkingBlock:
			contentBlocks = append(contentBlocks, &LLMContentBlock{
				Kind:      LLMContentThinking,
				Text:      b.Thinking,
				Signature: b.Signature,
			})
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
		}
	}

	displaySpans, toolCalls := dp.Response()
	return &LLMResponse{
		Content: contentBlocks,
		TokenUsage: LLMTokenUsage{
			InputTokens:       acc.Usage.InputTokens,
			OutputTokens:      acc.Usage.OutputTokens,
			CachedTokenReads:  acc.Usage.CacheReadInputTokens,
			CachedTokenWrites: acc.Usage.CacheCreationInputTokens,
			TotalTokens:       acc.Usage.InputTokens + acc.Usage.OutputTokens,
		},
		DisplaySpans:     displaySpans,
		ToolCallDisplays: toolCalls,
	}, nil
}

// appendOrMerge appends content blocks to the messages slice. If the last
// message has the same role, the blocks are merged into it rather than
// creating a new message. This is required by the Anthropic API spec
// (and strictly enforced by compatible servers like LM Studio) which
// mandates alternating user/assistant roles — consecutive tool_result
// blocks from parallel tool calls must be in a single user message.
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
