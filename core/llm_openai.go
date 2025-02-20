package core

import (
	"context"
	"fmt"
	"io"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/core/bbi"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/trace"
)

type OpenAIClient struct {
	client   *openai.Client
	endpoint *LlmEndpoint
}

func newOpenAIClient(endpoint *LlmEndpoint) *OpenAIClient {
	var opts []option.RequestOption
	opts = append(opts, option.WithHeader("Content-Type", "application/json"))
	if endpoint.Key != "" {
		opts = append(opts, option.WithAPIKey(endpoint.Key))
	}
	if endpoint.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(endpoint.BaseURL))
	}
	c := openai.NewClient(opts...)
	return &OpenAIClient{client: c, endpoint: endpoint}
}

func (c *OpenAIClient) SendQuery(ctx context.Context, history []ModelMessage, tools []bbi.Tool) (_ *LLMResponse, rerr error) {
	// Convert generic Message to OpenAI specific format
	var openAIMessages []openai.ChatCompletionMessageParamUnion
	for _, msg := range history {
		switch msg.Role {
		case "user":
			openAIMessages = append(openAIMessages, openai.UserMessage(msg.Content.(string)))
		case "assistant":
			openAIMessages = append(openAIMessages, openai.AssistantMessage(msg.Content.(string)))
		case "system":
			openAIMessages = append(openAIMessages, openai.SystemMessage(msg.Content.(string)))
		}
	}

	params := openai.ChatCompletionNewParams{
		Seed:     openai.Int(0),
		Model:    openai.F(openai.ChatModel(c.endpoint.Model)),
		Messages: openai.F(openAIMessages),
	}

	if len(tools) > 0 {
		var toolParams []openai.ChatCompletionToolParam
		for _, tool := range tools {
			toolParams = append(toolParams, openai.ChatCompletionToolParam{
				Type: openai.F(openai.ChatCompletionToolTypeFunction),
				Function: openai.F(openai.FunctionDefinitionParam{
					Name:        openai.String(tool.Name),
					Description: openai.String(tool.Description),
					Parameters:  openai.F(openai.FunctionParameters(tool.Schema)),
				}),
			})
		}
		params.Tools = openai.F(toolParams)
	}

	stream := c.client.Chat.Completions.NewStreaming(ctx, params)
	defer stream.Close()

	var logsW io.Writer
	acc := new(openai.ChatCompletionAccumulator)
	for stream.Next() {
		if stream.Err() != nil {
			return nil, stream.Err()
		}

		res := stream.Current()
		acc.AddChunk(res)

		if len(res.Choices) > 0 {
			if content := res.Choices[0].Delta.Content; content != "" {
				if logsW == nil {
					// only show a message if we actually get a text response back
					// (as opposed to tool calls)
					ctx, span := Tracer(ctx).Start(ctx, "LLM response", telemetry.Reveal(), trace.WithAttributes(
						attribute.String("dagger.io/ui.actor", "🤖"),
						attribute.String("dagger.io/ui.message", "received"),
					))
					defer telemetry.End(span, func() error { return rerr })

					stdio := telemetry.SpanStdio(ctx, "",
						log.String("dagger.io/content.type", "text/markdown"))

					logsW = stdio.Stdout
				}

				fmt.Fprint(logsW, content)
			}
		}
	}

	if stream.Err() != nil {
		return nil, stream.Err()
	}

	if len(acc.ChatCompletion.Choices) == 0 {
		return nil, fmt.Errorf("no response from model")
	}

	// Convert OpenAI response to generic LLMResponse
	return &LLMResponse{
		Content:   acc.Choices[0].Message.Content,
		ToolCalls: convertOpenAIToolCalls(acc.Choices[0].Message.ToolCalls),
	}, nil
}

func convertOpenAIToolCalls(calls []openai.ChatCompletionMessageToolCall) []ToolCall {
	var toolCalls []ToolCall
	for _, call := range calls {
		toolCalls = append(toolCalls, ToolCall{
			ID: call.ID,
			Function: FuncCall{
				Name:      call.Function.Name,
				Arguments: call.Function.Arguments,
			},
			Type: string(call.Type),
		})
	}
	return toolCalls
}
