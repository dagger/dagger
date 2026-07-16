package core

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/engine/slog"
	telemetry "github.com/dagger/otel-go"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/azure"
	"github.com/openai/openai-go/option"
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
func convertHistoryToOpenAI(history []*LLMMessage) []openai.ChatCompletionMessageParamUnion {
	var openAIMessages []openai.ChatCompletionMessageParamUnion
	for _, msg := range history {
		switch msg.Role {
		case LLMMessageRoleSystem:
			openAIMessages = append(openAIMessages, openai.SystemMessage(msg.TextContent()))
		case LLMMessageRoleUser:
			// A user message may carry tool results and/or text content.
			var textParts []openai.ChatCompletionContentPartUnionParam
			for _, block := range msg.Content {
				switch block.Kind {
				case LLMContentToolResult:
					content := block.Text
					if block.Errored {
						content = "error: " + content
					}
					openAIMessages = append(openAIMessages, openai.ToolMessage(content, block.CallID))
				case LLMContentText:
					textParts = append(textParts, openai.TextContentPart(block.Text))
				}
			}
			if len(textParts) > 0 {
				openAIMessages = append(openAIMessages, openai.UserMessage(textParts))
			}
		case LLMMessageRoleAssistant:
			assistantMsg := openai.AssistantMessage(msg.TextContent())
			var calls []openai.ChatCompletionMessageToolCallParam
			for _, block := range msg.Content {
				if block.Kind != LLMContentToolCall {
					continue
				}
				args := string(block.Arguments)
				if args == "" {
					args = "{}"
				}
				calls = append(calls, openai.ChatCompletionMessageToolCallParam{
					ID: block.CallID,
					Function: openai.ChatCompletionMessageToolCallFunctionParam{
						Name:      block.ToolName,
						Arguments: args,
					},
				})
			}
			if len(calls) > 0 {
				assistantMsg.OfAssistant.ToolCalls = calls
			}
			openAIMessages = append(openAIMessages, assistantMsg)
		}
	}
	return openAIMessages
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

	outputTokens, err := m.Int64Gauge(telemetry.LLMOutputTokens)
	if err != nil {
		return nil, err
	}

	openAIMessages := convertHistoryToOpenAI(history)

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
		var toolParams []openai.ChatCompletionToolParam
		for _, tool := range tools {
			toolParams = append(toolParams, openai.ChatCompletionToolParam{
				Function: openai.FunctionDefinitionParam{
					Name:        tool.Name,
					Description: openai.Opt(tool.Description),
					Parameters:  openai.FunctionParameters(tool.Schema),
					Strict:      openai.Opt(tool.Strict),
				},
			})
		}
		params.Tools = toolParams
	}

	var chatCompletion *openai.ChatCompletion

	if len(tools) > 0 && c.disableStreaming {
		chatCompletion, err = c.queryWithoutStreaming(ctx, params, outputTokens, inputTokens, attrs, dp)
	} else {
		chatCompletion, err = c.queryWithStreaming(ctx, params, outputTokens, inputTokens, attrs, dp)
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
		Content: contentBlocks,
		TokenUsage: LLMTokenUsage{
			InputTokens:      chatCompletion.Usage.PromptTokens,
			OutputTokens:     chatCompletion.Usage.CompletionTokens,
			CachedTokenReads: chatCompletion.Usage.PromptTokensDetails.CachedTokens,
			TotalTokens:      chatCompletion.Usage.TotalTokens,
		},
		DisplaySpans:     displaySpans,
		ToolCallDisplays: toolCallDisplays,
	}, nil
}

func (c *OpenAIClient) queryWithStreaming(
	ctx context.Context,
	params openai.ChatCompletionNewParams,
	outputTokens metric.Int64Gauge,
	inputTokens metric.Int64Gauge,
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

		// Keep track of the token usage
		//
		// NOTE: so far I'm only seeing 0 back from OpenAI - is this not actually supported?
		if res.Usage.CompletionTokens > 0 {
			outputTokens.Record(ctx, acc.Usage.CompletionTokens, metric.WithAttributes(attrs...))
		}
		if res.Usage.PromptTokens > 0 {
			inputTokens.Record(ctx, acc.Usage.PromptTokens, metric.WithAttributes(attrs...))
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
	attrs []attribute.KeyValue,
	dp *displayPhases,
) (*openai.ChatCompletion, error) {
	compl, err := c.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return nil, err
	}

	if compl.Usage.CompletionTokens > 0 {
		outputTokens.Record(ctx, compl.Usage.CompletionTokens, metric.WithAttributes(attrs...))
	}
	if compl.Usage.PromptTokens > 0 {
		inputTokens.Record(ctx, compl.Usage.PromptTokens, metric.WithAttributes(attrs...))
	}

	if len(compl.Choices) > 0 {
		if content := compl.Choices[0].Message.Content; content != "" {
			fmt.Fprint(dp.StartText(0).MarkdownW, content)
		}
	}

	return compl, nil
}
