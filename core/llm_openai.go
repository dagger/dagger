package core

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/engine/slog"
	telemetry "github.com/dagger/otel-go"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
	"github.com/openai/openai-go/v3/option"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

type OpenAIClient struct {
	client           openai.Client
	endpoint         *LLMEndpoint
	disableStreaming bool
}

func newOpenAIClient(endpoint *LLMEndpoint, azureVersion string, disableStreaming bool) *OpenAIClient {
	var opts []option.RequestOption
	opts = append(opts, option.WithHeader("Content-Type", "application/json"))
	if azureVersion != "" {
		opts = append(opts, azure.WithEndpoint(endpoint.BaseURL, azureVersion))
		if endpoint.Key != "" {
			opts = append(opts, azure.WithAPIKey(endpoint.Key))
		}
		opts = append(opts, option.WithHTTPClient(endpoint.otelHTTPClient("openai-azure")))
		c := openai.NewClient(opts...)
		return &OpenAIClient{client: c, endpoint: endpoint}
	}

	if endpoint.Key != "" {
		opts = append(opts, option.WithAPIKey(endpoint.Key))
	}
	if endpoint.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(endpoint.BaseURL))
	}

	opts = append(opts, option.WithHTTPClient(endpoint.otelHTTPClient("openai")))
	c := openai.NewClient(opts...)
	return &OpenAIClient{client: c, endpoint: endpoint, disableStreaming: disableStreaming}
}

var _ LLMClient = (*OpenAIClient)(nil)

func (c *OpenAIClient) IsRetryable(err error) bool {
	// OpenAI client immplements retrying internally; nothing to do here.
	return false
}

// convertHistoryToOpenAI converts content-block messages to the OpenAI
// chat-completions message format.
func convertHistoryToOpenAI(history []*LLMMessage) ([]openai.ChatCompletionMessageParamUnion, error) {
	var openAIMessages []openai.ChatCompletionMessageParamUnion
	// Chat tool messages accept only text. Keep native media in a labeled user
	// message after the entire run of tool results (including results stored in
	// separate history messages), so parallel calls all receive their outputs
	// before any user message interrupts the protocol.
	var toolMedia []openai.ChatCompletionContentPartUnionParam
	flushToolMedia := func() {
		if len(toolMedia) > 0 {
			openAIMessages = append(openAIMessages, openai.UserMessage(toolMedia))
			toolMedia = nil
		}
	}
	for i, msg := range history {
		if err := validateOpenAIMessage(msg); err != nil {
			return nil, fmt.Errorf("OpenAI message %d: %w", i, err)
		}
		if msg.Role != LLMMessageRoleUser {
			flushToolMedia()
		}
		switch msg.Role {
		case LLMMessageRoleSystem:
			openAIMessages = append(openAIMessages, openai.SystemMessage(msg.TextContent()))
		case LLMMessageRoleUser:
			var parts []openai.ChatCompletionContentPartUnionParam
			flushParts := func() {
				if len(parts) > 0 {
					openAIMessages = append(openAIMessages, openai.UserMessage(parts))
					parts = nil
				}
			}
			for _, block := range msg.Content {
				if block.Kind == LLMContentToolResult {
					flushParts()
					content := block.ContentText()
					if block.Errored {
						content = "error: " + content
					}
					openAIMessages = append(openAIMessages, openai.ToolMessage(content, block.CallID))
					hasMedia := false
					for _, child := range block.Content {
						hasMedia = hasMedia || child.Kind != LLMContentText
					}
					if hasMedia {
						toolMedia = append(toolMedia, openai.TextContentPart(fmt.Sprintf("Content from tool result %q:", block.CallID)))
						if block.Text != "" {
							toolMedia = append(toolMedia, openai.TextContentPart(block.Text))
						}
						for _, child := range block.Content {
							part, err := openAIContentPart(child)
							if err != nil {
								return nil, fmt.Errorf("OpenAI tool result %q: %w", block.CallID, err)
							}
							toolMedia = append(toolMedia, part)
						}
					}
					continue
				}
				flushToolMedia()
				part, err := openAIContentPart(block)
				if err != nil {
					return nil, fmt.Errorf("OpenAI message %d: %w", i, err)
				}
				parts = append(parts, part)
			}
			flushParts()
		case LLMMessageRoleAssistant:
			assistantMsg := openai.AssistantMessage(msg.TextContent())
			var calls []openai.ChatCompletionMessageToolCallUnionParam
			for _, block := range msg.Content {
				if block.Kind != LLMContentToolCall {
					continue
				}
				args := string(block.Arguments)
				if args == "" {
					args = "{}"
				}
				calls = append(calls, openai.ChatCompletionMessageToolCallUnionParam{
					OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
						ID: block.CallID,
						Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
							Name:      block.ToolName,
							Arguments: args,
						},
					},
				})
			}
			if len(calls) > 0 {
				assistantMsg.OfAssistant.ToolCalls = calls
			}
			openAIMessages = append(openAIMessages, assistantMsg)
		}
	}
	flushToolMedia()
	return openAIMessages, nil
}

// Both OpenAI protocols accept media only as user input or tool results, not
// system instructions or historical assistant output. Thinking remains
// provider-specific: chat omits it, while Responses replays signed reasoning.
func validateOpenAIMessage(msg *LLMMessage) error {
	if msg == nil {
		return fmt.Errorf("nil message")
	}
	if err := ValidateLLMContent(msg.Content); err != nil {
		return err
	}
	for _, block := range msg.Content {
		allowed := false
		switch msg.Role {
		case LLMMessageRoleSystem:
			allowed = block.Kind == LLMContentText
		case LLMMessageRoleUser:
			switch block.Kind {
			case LLMContentText, LLMContentImage, LLMContentAudio, LLMContentDocument, LLMContentToolResult:
				allowed = true
			}
		case LLMMessageRoleAssistant:
			switch block.Kind {
			case LLMContentText, LLMContentThinking, LLMContentToolCall:
				allowed = true
			}
		default:
			return fmt.Errorf("unsupported role %q", msg.Role)
		}
		if !allowed {
			return fmt.Errorf("%s content is unsupported in %s messages", block.Kind, msg.Role)
		}
	}
	switch msg.Role {
	case LLMMessageRoleSystem, LLMMessageRoleUser, LLMMessageRoleAssistant:
		return nil
	default:
		return fmt.Errorf("unsupported role %q", msg.Role)
	}
}

func openAIContentPart(block *LLMContentBlock) (openai.ChatCompletionContentPartUnionParam, error) {
	switch block.Kind {
	case LLMContentText:
		return openai.TextContentPart(block.Text), nil
	case LLMContentImage:
		if err := validateOpenAIImage(block); err != nil {
			return openai.ChatCompletionContentPartUnionParam{}, err
		}
		return openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{
			URL: openAIMediaDataURL(block),
		}), nil
	case LLMContentAudio:
		var format string
		switch block.MIMEType {
		case "audio/wav", "audio/wave", "audio/x-wav":
			format = "wav"
		case "audio/mpeg", "audio/mp3":
			format = "mp3"
		default:
			return openai.ChatCompletionContentPartUnionParam{}, fmt.Errorf("unsupported OpenAI audio MIME type %q (expected WAV or MP3)", block.MIMEType)
		}
		return openai.InputAudioContentPart(openai.ChatCompletionContentPartInputAudioInputAudioParam{
			Data: block.Data, Format: format,
		}), nil
	case LLMContentDocument:
		if block.MIMEType != "application/pdf" {
			return openai.ChatCompletionContentPartUnionParam{}, fmt.Errorf("unsupported OpenAI document MIME type %q (expected PDF)", block.MIMEType)
		}
		return openai.FileContentPart(openai.ChatCompletionContentPartFileFileParam{
			FileData: openai.String(openAIMediaDataURL(block)), Filename: openai.String("document.pdf"),
		}), nil
	default:
		return openai.ChatCompletionContentPartUnionParam{}, fmt.Errorf("unsupported OpenAI content kind %q", block.Kind)
	}
}

func validateOpenAIImage(block *LLMContentBlock) error {
	switch block.MIMEType {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return nil
	default:
		return fmt.Errorf("unsupported OpenAI image MIME type %q", block.MIMEType)
	}
}

func openAIMediaDataURL(block *LLMContentBlock) string {
	return "data:" + block.MIMEType + ";base64," + block.Data
}

func (c *OpenAIClient) SendQuery(ctx context.Context, history []*LLMMessage, tools []LLMTool, opts *LLMCallOpts) (_ *LLMResponse, rerr error) {
	// Stream this turn's content into per-block display spans.
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

	outputTokens, err := m.Int64Gauge(telemetry.LLMOutputTokens)
	if err != nil {
		return nil, err
	}

	openAIMessages, err := convertHistoryToOpenAI(history)
	if err != nil {
		return nil, err
	}

	params := openai.ChatCompletionNewParams{
		Seed:     openai.Int(0),
		Model:    c.endpoint.Model,
		Messages: openAIMessages,
		// call tools one at a time, or else chaining breaks
	}

	// Apply an explicit maxTokens cap. The parameter is optional for
	// OpenAI-style APIs — left unset, the provider allows up to the model's
	// maximum — so no default is invented. OpenAI itself needs the modern
	// max_completion_tokens field (reasoning models reject max_tokens), while
	// OpenAI-compatible endpoints (local, other) more universally support the
	// classic max_tokens.
	if opts != nil && opts.MaxTokens > 0 {
		if c.endpoint.Provider == OpenAI {
			params.MaxCompletionTokens = openai.Int(int64(opts.MaxTokens))
		} else {
			params.MaxTokens = openai.Int(int64(opts.MaxTokens))
		}
	}

	if len(tools) > 0 {
		var toolParams []openai.ChatCompletionToolUnionParam
		for _, tool := range tools {
			toolParams = append(toolParams, openai.ChatCompletionFunctionTool(openai.FunctionDefinitionParam{
				Name:        tool.Name,
				Description: openai.Opt(tool.Description),
				Parameters:  openai.FunctionParameters(tool.Schema),
			}))
		}
		params.Tools = toolParams
	}

	var chatCompletion *openai.ChatCompletion

	if len(tools) > 0 && c.disableStreaming {
		chatCompletion, err = c.queryWithoutStreaming(ctx, params, outputTokens, inputTokens, inputTokensCacheReads, attrs, dp)
	} else {
		chatCompletion, err = c.queryWithStreaming(ctx, params, outputTokens, inputTokens, inputTokensCacheReads, attrs, dp)
	}
	// Close the streamed text response phase (if any) before the tool-call
	// phases, so spans close in the order the model produced them.
	dp.Close(0)
	if err != nil {
		return nil, err
	}

	if len(chatCompletion.Choices) == 0 {
		return nil, &ModelFinishedError{
			Reason: "no response from model",
		}
	}

	choice := chatCompletion.Choices[0]

	// A filtered or truncated choice can still carry partial content, so the
	// finish reason is checked on its own rather than only when no blocks come
	// out of it below. See anthropicStoppedCleanly.
	if !openAIFinishedCleanly(choice.FinishReason) {
		return nil, &ModelFinishedError{Reason: choice.FinishReason}
	}

	// Convert the OpenAI response into content blocks.
	var contentBlocks []*LLMContentBlock
	if choice.Message.Content != "" {
		contentBlocks = append(contentBlocks, &LLMContentBlock{
			Kind: LLMContentText,
			Text: choice.Message.Content,
		})
	}
	for i, call := range choice.Message.ToolCalls {
		if call.Function.Name == "" {
			slog.Warn("skipping tool call with empty name", "toolCall", call)
			continue
		}
		args := call.Function.Arguments
		if args == "" {
			args = "{}"
		}
		contentBlocks = append(contentBlocks, &LLMContentBlock{
			Kind:      LLMContentToolCall,
			CallID:    call.ID,
			ToolName:  call.Function.Name,
			Arguments: JSON(args),
		})
		dp.EmitToolCall(int64(i+1), call.ID, call.Function.Name, args)
	}

	if len(contentBlocks) == 0 {
		return nil, &ModelFinishedError{
			Reason: choice.FinishReason,
		}
	}

	// Convert OpenAI response to generic LLMResponse
	displaySpans, toolCallDisplays := dp.Response()
	return &LLMResponse{
		Content:          contentBlocks,
		TokenUsage:       openAICompletionUsage(chatCompletion.Usage),
		DisplaySpans:     displaySpans,
		ToolCallDisplays: toolCallDisplays,
	}, nil
}

// openAIFinishedCleanly reports whether a chat completion finish reason means
// the model finished its turn normally. Like the Anthropic check, it rejects
// only the reasons known to leave the turn unusable.
func openAIFinishedCleanly(reason string) bool {
	switch reason {
	case "length", "content_filter":
		return false
	default:
		return true
	}
}

func openAICompletionUsage(usage openai.CompletionUsage) LLMTokenUsage {
	cachedTokens := usage.PromptTokensDetails.CachedTokens
	inputTokens := uncachedInputTokens(usage.PromptTokens, cachedTokens)
	totalTokens := inputTokens + usage.CompletionTokens + cachedTokens
	if usage.TotalTokens > totalTokens {
		totalTokens = usage.TotalTokens
	}
	return LLMTokenUsage{
		InputTokens:      inputTokens,
		OutputTokens:     usage.CompletionTokens,
		CachedTokenReads: cachedTokens,
		TotalTokens:      totalTokens,
	}
}

func recordOpenAICompletionUsage(
	ctx context.Context,
	usage openai.CompletionUsage,
	outputTokens metric.Int64Gauge,
	inputTokens metric.Int64Gauge,
	inputTokensCacheReads metric.Int64Gauge,
	attrs []attribute.KeyValue,
) {
	normalized := openAICompletionUsage(usage)
	if normalized.OutputTokens > 0 {
		outputTokens.Record(ctx, normalized.OutputTokens, metric.WithAttributes(attrs...))
	}
	if normalized.InputTokens > 0 {
		inputTokens.Record(ctx, normalized.InputTokens, metric.WithAttributes(attrs...))
	}
	if normalized.CachedTokenReads > 0 {
		inputTokensCacheReads.Record(ctx, normalized.CachedTokenReads, metric.WithAttributes(attrs...))
	}
}

func (c *OpenAIClient) queryWithStreaming(
	ctx context.Context,
	params openai.ChatCompletionNewParams,
	outputTokens metric.Int64Gauge,
	inputTokens metric.Int64Gauge,
	inputTokensCacheReads metric.Int64Gauge,
	attrs []attribute.KeyValue,
	dp *displayPhases,
) (*openai.ChatCompletion, error) {
	params.StreamOptions = openai.ChatCompletionStreamOptionsParam{
		IncludeUsage: openai.Opt(true),
	}

	stream := c.client.Chat.Completions.NewStreaming(ctx, params)
	if stream.Err() != nil {
		// errored establishing connection; bail so stream.Close doesn't panic
		return nil, stream.Err()
	}
	defer stream.Close()

	if stream.Err() != nil {
		return nil, stream.Err()
	}

	acc := new(openai.ChatCompletionAccumulator)
	for stream.Next() {
		res := stream.Current()
		acc.AddChunk(res)

		// Keep track of token usage. The stream accumulator holds cumulative
		// usage, and the UI's gauge aggregation keeps the last value per stream.
		if res.Usage.CompletionTokens > 0 || res.Usage.PromptTokens > 0 {
			recordOpenAICompletionUsage(ctx, acc.Usage, outputTokens, inputTokens, inputTokensCacheReads, attrs)
		}

		if len(res.Choices) > 0 {
			if content := res.Choices[0].Delta.Content; content != "" {
				fmt.Fprint(dp.StartText(0).MarkdownW, content)
			}
		}
	}

	if stream.Err() != nil {
		return nil, stream.Err()
	}

	return &acc.ChatCompletion, nil
}

func (c *OpenAIClient) queryWithoutStreaming(
	ctx context.Context,
	params openai.ChatCompletionNewParams,
	outputTokens metric.Int64Gauge,
	inputTokens metric.Int64Gauge,
	inputTokensCacheReads metric.Int64Gauge,
	attrs []attribute.KeyValue,
	dp *displayPhases,
) (*openai.ChatCompletion, error) {
	compl, err := c.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return nil, err
	}

	recordOpenAICompletionUsage(ctx, compl.Usage, outputTokens, inputTokens, inputTokensCacheReads, attrs)

	if len(compl.Choices) > 0 {
		if content := compl.Choices[0].Message.Content; content != "" {
			fmt.Fprint(dp.StartText(0).MarkdownW, content)
		}
	}

	return compl, nil
}
