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

//nolint:gocyclo
func (c *OpenAIClient) SendQuery(ctx context.Context, history []*LLMMessage, tools []LLMTool, opts *LLMCallOpts) (_ *LLMResponse, rerr error) {
	// parentCtx is the context we create sibling spans from (response, tool calls).
	parentCtx := ctx

	m := telemetry.Meter(parentCtx, InstrumentationLibrary)
	spanCtx := trace.SpanContextFromContext(parentCtx)
	metricAttrs := []attribute.KeyValue{
		attribute.String(telemetry.MetricsTraceIDAttr, spanCtx.TraceID().String()),
		attribute.String(telemetry.MetricsSpanIDAttr, spanCtx.SpanID().String()),
		attribute.String("model", c.endpoint.Model),
		attribute.String("provider", string(c.endpoint.Provider)),
	}

	inputTokensGauge, err := m.Int64Gauge(telemetry.LLMInputTokens)
	if err != nil {
		return nil, err
	}

	outputTokensGauge, err := m.Int64Gauge(telemetry.LLMOutputTokens)
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

	if opts != nil && opts.MaxTokens > 0 {
		params.MaxCompletionTokens = openai.Int(int64(opts.MaxTokens))
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

	dp := newDisplayPhases(parentCtx)
	defer func() {
		dp.CloseAll()
		if rerr != nil {
			dp.Abort(rerr)
		}
	}()

	var chatCompletion *openai.ChatCompletion

	if len(tools) > 0 && c.disableStreaming {
		// Non-streaming: create a single text phase for the response
		p := dp.StartText(-1)
		chatCompletion, err = c.queryWithoutStreaming(ctx, params, outputTokensGauge, inputTokensGauge, metricAttrs, p.Stdio)
	} else {
		chatCompletion, err = c.queryWithStreaming(ctx, params, outputTokensGauge, inputTokensGauge, metricAttrs, dp)
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

	displaySpans, toolCalls := dp.Response()
	return &LLMResponse{
		Content: contentBlocks,
		TokenUsage: LLMTokenUsage{
			InputTokens:      chatCompletion.Usage.PromptTokens,
			OutputTokens:     chatCompletion.Usage.CompletionTokens,
			CachedTokenReads: chatCompletion.Usage.PromptTokensDetails.CachedTokens,
			TotalTokens:      chatCompletion.Usage.TotalTokens,
		},
		DisplaySpans:     displaySpans,
		ToolCallDisplays: toolCalls,
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

	// Track which tool call indices we've already seen a name/ID for
	toolCallNames := map[int64]string{}
	toolCallIDs := map[int64]string{}

	acc := new(openai.ChatCompletionAccumulator)
	for stream.Next() {
		res := stream.Current()
		acc.AddChunk(res)

		// Keep track of the token usage
		if res.Usage.CompletionTokens > 0 {
			outputTokens.Record(ctx, acc.Usage.CompletionTokens, metric.WithAttributes(attrs...))
		}
		if res.Usage.PromptTokens > 0 {
			inputTokens.Record(ctx, acc.Usage.PromptTokens, metric.WithAttributes(attrs...))
		}

		if len(res.Choices) > 0 {
			delta := res.Choices[0].Delta

			// Stream text content
			if delta.Content != "" {
				p := dp.StartText(-1)
				if p.MarkdownW != nil {
					fmt.Fprint(p.MarkdownW, delta.Content)
				}
			}

			// Stream tool call arguments
			for _, tc := range delta.ToolCalls {
				idx := tc.Index
				if tc.ID != "" {
					toolCallIDs[idx] = tc.ID
				}
				if tc.Function.Name != "" {
					toolCallNames[idx] = tc.Function.Name
				}
				name := toolCallNames[idx]
				if name == "" {
					name = fmt.Sprintf("tool_call_%d", idx)
				}
				p := dp.StartToolCall(idx, toolCallIDs[idx], name)
				if tc.Function.Arguments != "" {
					fmt.Fprint(p.Stdio.Stdout, tc.Function.Arguments)
				}
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
