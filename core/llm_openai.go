package core

import (
	"context"
	"encoding/json"
	"fmt"

	"dagger.io/dagger/telemetry"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/azure"
	"github.com/openai/openai-go/option"
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
		c := openai.NewClient(opts...)
		return &OpenAIClient{client: c, endpoint: endpoint}
	}

	if endpoint.Key != "" {
		opts = append(opts, option.WithAPIKey(endpoint.Key))
	}
	if endpoint.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(endpoint.BaseURL))
	}

	c := openai.NewClient(opts...)
	return &OpenAIClient{client: c, endpoint: endpoint, disableStreaming: disableStreaming}
}

func (c *OpenAIClient) SendQuery(ctx context.Context, history []ModelMessage, tools []LLMTool) (_ *LLMResponse, rerr error) {
	ctx, span := Tracer(ctx).Start(ctx, "LLM query", telemetry.Reveal(), trace.WithAttributes(
		attribute.String(telemetry.UIActorEmojiAttr, "🤖"),
		attribute.String(telemetry.UIMessageAttr, "received"),
	))
	defer telemetry.End(span, func() error { return rerr })

	stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary,
		log.String(telemetry.ContentTypeAttr, "text/markdown"))
	defer stdio.Close()

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

	outputTokens, err := m.Int64Gauge(telemetry.LLMOutputTokens)
	if err != nil {
		return nil, err
	}

	// Convert generic Message to OpenAI specific format
	var openAIMessages []openai.ChatCompletionMessageParamUnion

	for _, msg := range history {
		if msg.ToolCallID != "" {
			content := msg.Content
			if msg.ToolErrored {
				content = "error: " + content
			}
			openAIMessages = append(openAIMessages, openai.ToolMessage(content, msg.ToolCallID))
			continue
		}
		var blocks []openai.ChatCompletionContentPartUnionParam
		switch msg.Role {
		case "user":
			blocks = append(blocks, openai.TextContentPart(msg.Content))
			openAIMessages = append(openAIMessages, openai.UserMessage(blocks))
		case "assistant":
			assistantMsg := openai.AssistantMessage(msg.Content)
			calls := make([]openai.ChatCompletionMessageToolCallParam, len(msg.ToolCalls))
			for i, call := range msg.ToolCalls {
				args, err := json.Marshal(call.Function.Arguments)
				if err != nil {
					return nil, fmt.Errorf("failed to marshal tool call arguments: %w", err)
				}
				calls[i] = openai.ChatCompletionMessageToolCallParam{
					ID: call.ID,
					Function: openai.ChatCompletionMessageToolCallFunctionParam{
						Name:      call.Function.Name,
						Arguments: string(args),
					},
				}
			}
			if len(calls) > 0 {
				assistantMsg.OfAssistant.ToolCalls = calls
			}
			openAIMessages = append(openAIMessages, assistantMsg)
		case "system":
			openAIMessages = append(openAIMessages, openai.SystemMessage(msg.Content))
		}
	}

	params := openai.ChatCompletionNewParams{
		Seed:     openai.Int(0),
		Model:    c.endpoint.Model,
		Messages: openAIMessages,
		// call tools one at a time, or else chaining breaks
	}

	if len(tools) > 0 {
		// OpenAI is picky about this being set if no tools are specified
		params.ParallelToolCalls = openai.Opt(false)

		var toolParams []openai.ChatCompletionToolParam
		for _, tool := range tools {
			toolParams = append(toolParams, openai.ChatCompletionToolParam{
				Function: openai.FunctionDefinitionParam{
					Name:        tool.Name,
					Description: openai.Opt(tool.Description),
					Parameters:  openai.FunctionParameters(tool.Schema),
				},
			})
		}
		params.Tools = toolParams
	}
	dbgEnc.Encode("---------------------------------------------")
	dbgEnc.Encode(params)

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

	toolCalls, err := convertOpenAIToolCalls(choice.Message.ToolCalls)
	if err != nil {
		return nil, fmt.Errorf("failed to convert tool calls: %w", err)
	}

	if choice.Message.Content == "" && len(toolCalls) == 0 {
		return nil, &ModelFinishedError{
			Reason: choice.FinishReason,
		}
	}

	// Convert OpenAI response to generic LLMResponse
	return &LLMResponse{
		Content:   choice.Message.Content,
		ToolCalls: toolCalls,
		TokenUsage: LLMTokenUsage{
			InputTokens:  chatCompletion.Usage.PromptTokens,
			OutputTokens: chatCompletion.Usage.CompletionTokens,
			TotalTokens:  chatCompletion.Usage.TotalTokens,
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
	dbgEnc.Encode("---------------------------------------------")
	dbgEnc.Encode(params)

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

		dbgEnc.Encode(res)

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

func convertOpenAIToolCalls(calls []openai.ChatCompletionMessageToolCall) ([]ToolCall, error) {
	var toolCalls []ToolCall
	for _, call := range calls {
		var args map[string]any
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			return nil, fmt.Errorf("failed to unmarshal tool call arguments: %w", err)
		}
		toolCalls = append(toolCalls, ToolCall{
			ID: call.ID,
			Function: FuncCall{
				Name:      call.Function.Name,
				Arguments: args,
			},
			Type: string(call.Type),
		})
	}
	return toolCalls, nil
}
