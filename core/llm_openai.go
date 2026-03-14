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
	"go.opentelemetry.io/otel/log"
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
		opts = append(opts, option.WithHTTPClient(newLLMOTelHTTPClient("openai-azure")))
		c := openai.NewClient(opts...)
		return &OpenAIClient{client: c, endpoint: endpoint}
	}

	if endpoint.Key != "" {
		opts = append(opts, option.WithAPIKey(endpoint.Key))
	}
	if endpoint.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(endpoint.BaseURL))
	}

	// Inject OTel tracing HTTP client to capture LLM request/response bodies
	opts = append(opts, option.WithHTTPClient(newLLMOTelHTTPClient("openai")))

	c := openai.NewClient(opts...)
	return &OpenAIClient{client: c, endpoint: endpoint, disableStreaming: disableStreaming}
}

var _ LLMClient = (*OpenAIClient)(nil)

func (c *OpenAIClient) IsRetryable(err error) bool {
	// OpenAI client immplements retrying internally; nothing to do here.
	return false
}

func (c *OpenAIClient) SendQuery(ctx context.Context, history []*LLMMessage, tools []LLMTool) (_ *LLMResponse, rerr error) {
	ctx, span := Tracer(ctx).Start(ctx, "LLM response", telemetry.Reveal(), trace.WithAttributes(
		attribute.String(telemetry.UIActorEmojiAttr, "🤖"),
		attribute.String(telemetry.UIMessageAttr, telemetry.UIMessageReceived),
		attribute.String(telemetry.LLMRoleAttr, telemetry.LLMRoleAssistant),
	))
	defer telemetry.EndWithCause(span, &rerr)

	stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary,
		log.String(telemetry.ContentTypeAttr, "text/markdown"))
	defer stdio.Close()

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

	// Convert generic Message to OpenAI specific format
	var openAIMessages []openai.ChatCompletionMessageParamUnion

	for _, msg := range history {
		// Handle tool result messages
		if msg.IsToolResult() {
			text := msg.TextContent()
			if msg.ToolResultErrored() {
				text = "error: " + text
			}
			openAIMessages = append(openAIMessages, openai.ToolMessage(text, msg.ToolResultCallID()))
			continue
		}
		switch msg.Role {
		case LLMMessageRoleUser:
			var blocks []openai.ChatCompletionContentPartUnionParam
			blocks = append(blocks, openai.TextContentPart(msg.TextContent()))
			openAIMessages = append(openAIMessages, openai.UserMessage(blocks))
		case LLMMessageRoleAssistant:
			assistantMsg := openai.AssistantMessage(msg.TextContent())
			toolCalls := msg.ToolCalls()
			calls := make([]openai.ChatCompletionMessageToolCallUnionParam, len(toolCalls))
			for i, block := range toolCalls {
				calls[i] = openai.ChatCompletionMessageToolCallUnionParam{
					OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
						ID: block.CallID,
						Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
							Name:      block.ToolName,
							Arguments: block.Arguments.String(),
						},
					},
				}
			}
			if len(calls) > 0 {
				assistantMsg.OfAssistant.ToolCalls = calls
			}
			openAIMessages = append(openAIMessages, assistantMsg)
		case LLMMessageRoleSystem:
			openAIMessages = append(openAIMessages, openai.SystemMessage(msg.TextContent()))
		}
	}

	params := openai.ChatCompletionNewParams{
		Seed:     openai.Int(0),
		Model:    c.endpoint.Model,
		Messages: openAIMessages,
		// call tools one at a time, or else chaining breaks
	}

	if len(tools) > 0 {
		var toolParams []openai.ChatCompletionToolUnionParam
		for _, tool := range tools {
			toolParams = append(toolParams, openai.ChatCompletionToolUnionParam{
				OfFunction: &openai.ChatCompletionFunctionToolParam{
					Function: openai.FunctionDefinitionParam{
						Name:        tool.Name,
						Description: openai.Opt(tool.Description),
						Parameters:  openai.FunctionParameters(tool.Schema),
						Strict:      openai.Opt(tool.Strict),
					},
				},
			})
		}
		params.Tools = toolParams
	}

	var chatCompletion *openai.ChatCompletion

	if len(tools) > 0 && c.disableStreaming {
		chatCompletion, err = c.queryWithoutStreaming(ctx, params, outputTokens, inputTokens, attrs, stdio)
	} else {
		chatCompletion, err = c.queryWithStreaming(ctx, params, outputTokens, inputTokens, attrs, stdio)
	}
	if err != nil {
		return nil, err
	}

	if len(chatCompletion.Choices) == 0 {
		return nil, &ModelFinishedError{
			Reason: "no response from model",
		}
	}

	choice := chatCompletion.Choices[0]

	var contentBlocks []*LLMContentBlock
	if choice.Message.Content != "" {
		contentBlocks = append(contentBlocks, &LLMContentBlock{
			Kind: LLMContentText,
			Text: choice.Message.Content,
		})
	}
	for _, call := range choice.Message.ToolCalls {
		if call.Function.Name == "" {
			slog.Warn("skipping tool call with empty name", "toolCall", call)
			continue
		}
		contentBlocks = append(contentBlocks, &LLMContentBlock{
			Kind:      LLMContentToolCall,
			CallID:    call.ID,
			ToolName:  call.Function.Name,
			Arguments: JSON(call.Function.Arguments),
		})
	}

	if len(contentBlocks) == 0 {
		return nil, &ModelFinishedError{
			Reason: choice.FinishReason,
		}
	}

	return &LLMResponse{
		Content: contentBlocks,
		TokenUsage: LLMTokenUsage{
			InputTokens:      chatCompletion.Usage.PromptTokens,
			OutputTokens:     chatCompletion.Usage.CompletionTokens,
			CachedTokenReads: chatCompletion.Usage.PromptTokensDetails.CachedTokens,
			TotalTokens:      chatCompletion.Usage.TotalTokens,
		},
	}, nil
}

func (c *OpenAIClient) queryWithStreaming(
	ctx context.Context,
	params openai.ChatCompletionNewParams,
	outputTokens metric.Int64Gauge,
	inputTokens metric.Int64Gauge,
	attrs []attribute.KeyValue,
	stdio telemetry.SpanStreams,
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
				fmt.Fprint(stdio.Stdout, content)
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
	stdio telemetry.SpanStreams,
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
			fmt.Fprint(stdio.Stdout, content)
		}
	}

	return compl, nil
}
