package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"
	telemetry "github.com/dagger/otel-go"
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
	var opts []option.RequestOption

	if endpoint.IsOAuth {
		// Claude Code OAuth: use bearer token auth with Claude Code headers
		opts = append(opts,
			option.WithAuthToken(endpoint.AuthToken),
			option.WithHeader("anthropic-beta", "claude-code-20250219,oauth-2025-04-20"),
			option.WithHeader("user-agent", "claude-cli/2.1.2 (external, cli)"),
			option.WithHeader("x-app", "cli"),
		)
	} else if endpoint.Key != "" {
		opts = append(opts, option.WithAPIKey(endpoint.Key))
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
func (c *AnthropicClient) SendQuery(ctx context.Context, history []*LLMMessage, tools []LLMTool) (res *LLMResponse, rerr error) {
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
	var cachedBlocks int
	for _, msg := range history {
		if msg.Role == LLMMessageRoleSystem {
			systemPrompts = append(systemPrompts, anthropic.TextBlockParam{Text: msg.TextContent()})
			continue
		}

		var blocks []anthropic.ContentBlockParamUnion
		for _, block := range msg.Content {
			switch block.Kind {
			case LLMContentText:
				text := block.Text
				// Anthropic's API sometimes returns empty content but rejects it on input.
				if text == "" {
					text = " "
				}
				blocks = append(blocks, anthropic.NewTextBlock(text))
			case LLMContentThinking:
				blocks = append(blocks, anthropic.NewThinkingBlock(block.Signature, block.Text))
			case LLMContentToolCall:
				var args map[string]any
				if err := json.Unmarshal(block.Arguments.Bytes(), &args); err != nil {
					return nil, err
				}
				blocks = append(blocks, anthropic.NewToolUseBlock(block.CallID, args, block.ToolName))
			case LLMContentToolResult:
				text := block.Text
				if text == "" {
					text = " "
				}
				blocks = append(blocks, anthropic.NewToolResultBlock(block.CallID, text, block.Errored))
			}
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
				// ThinkingBlockParam doesn't support CacheControl
			}
		}

		switch msg.Role {
		case LLMMessageRoleUser:
			messages = append(messages, anthropic.NewUserMessage(blocks...))
		case LLMMessageRoleAssistant:
			messages = append(messages, anthropic.NewAssistantMessage(blocks...))
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

	// Configure thinking/reasoning if requested
	var thinkingConfig anthropic.ThinkingConfigParamUnion
	switch c.endpoint.ThinkingMode {
	case "adaptive":
		thinkingConfig = anthropic.ThinkingConfigParamUnion{
			OfAdaptive: &anthropic.ThinkingConfigAdaptiveParam{},
		}
		// Anthropic requires MaxTokens >= 1024+BudgetTokens for thinking
		if maxTokens < 16384 {
			maxTokens = 16384
		}
	case "enabled":
		budget := c.endpoint.ThinkingBudget
		if budget < 1024 {
			budget = 10000 // reasonable default
		}
		thinkingConfig = anthropic.ThinkingConfigParamOfEnabled(budget)
		if maxTokens < budget+1024 {
			maxTokens = budget + 1024
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

	// Phase-based span management: thinking and response are sibling spans
	// created from parentCtx. Each content block maps to a phase; phases
	// open/close spans lazily as streaming content arrives. This supports
	// interleaved thinking/response blocks (thinking → text → thinking → text).
	type phase struct {
		span       trace.Span
		stdio      telemetry.SpanStreams
		markdownW  io.Writer
		isThinking bool
	}
	phases := map[int64]*phase{}

	startPhase := func(idx int64, thinking bool) *phase {
		if p, ok := phases[idx]; ok {
			return p
		}
		var p phase
		p.isThinking = thinking
		if thinking {
			phaseCtx, span := Tracer(parentCtx).Start(parentCtx, "thinking",
				telemetry.Reveal(),
				trace.WithAttributes(
					attribute.String(telemetry.UIActorEmojiAttr, "💭"),
					attribute.String(telemetry.UIMessageAttr, telemetry.UIMessageReceived),
					attribute.String(telemetry.LLMRoleAttr, telemetry.LLMRoleAssistant),
					attribute.Bool("llm.thinking", true),
				),
			)
			p.span = span
			p.stdio = telemetry.SpanStdio(phaseCtx, InstrumentationLibrary,
				log.String(telemetry.ContentTypeAttr, "text/markdown"),
				log.Bool("llm.thinking", true),
			)
		} else {
			phaseCtx, span := Tracer(parentCtx).Start(parentCtx, "LLM response",
				telemetry.Reveal(),
				trace.WithAttributes(
					attribute.String(telemetry.UIActorEmojiAttr, "🤖"),
					attribute.String(telemetry.UIMessageAttr, telemetry.UIMessageReceived),
					attribute.String(telemetry.LLMRoleAttr, telemetry.LLMRoleAssistant),
				),
			)
			p.span = span
			p.stdio = telemetry.SpanStdio(phaseCtx, InstrumentationLibrary)
			p.markdownW = telemetry.NewWriter(phaseCtx, InstrumentationLibrary,
				log.String(telemetry.ContentTypeAttr, "text/markdown"))
		}
		phases[idx] = &p
		return &p
	}

	endPhase := func(idx int64) {
		if p, ok := phases[idx]; ok {
			p.stdio.Close()
			telemetry.EndWithCause(p.span, &rerr)
			delete(phases, idx)
		}
	}

	defer func() {
		for idx := range phases {
			endPhase(idx)
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
				startPhase(ev.Index, true)
			case "text":
				startPhase(ev.Index, false)
			}

		case anthropic.ContentBlockDeltaEvent:
			switch ev.Delta.Type {
			case "thinking_delta":
				if p := phases[ev.Index]; p != nil {
					fmt.Fprint(p.stdio.Stdout, ev.Delta.Thinking)
				}
			case "text_delta":
				p := phases[ev.Index]
				if p == nil {
					// Lazily create a response phase if we get text without a start event
					p = startPhase(ev.Index, false)
				}
				if p.markdownW != nil {
					fmt.Fprint(p.markdownW, ev.Delta.Text)
				}
			}

		case anthropic.ContentBlockStopEvent:
			endPhase(ev.Index)
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

	return &LLMResponse{
		Content: contentBlocks,
		TokenUsage: LLMTokenUsage{
			InputTokens:       acc.Usage.InputTokens,
			OutputTokens:      acc.Usage.OutputTokens,
			CachedTokenReads:  acc.Usage.CacheReadInputTokens,
			CachedTokenWrites: acc.Usage.CacheCreationInputTokens,
			TotalTokens:       acc.Usage.InputTokens + acc.Usage.OutputTokens,
		},
	}, nil
}
