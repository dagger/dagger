package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"

	"dagger.io/dagger/telemetry"
	"github.com/googleapis/gax-go/v2/apierror"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"google.golang.org/genai"
)

type GenaiClient struct {
	client   *genai.Client
	endpoint *LLMEndpoint
}

func newGenaiClient(endpoint *LLMEndpoint) (*GenaiClient, error) {
	ctx := context.Background() // FIXME: should we wire this through from somewhere else?
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey: endpoint.Key,
	})
	if err != nil {
		return nil, err
	}
	return &GenaiClient{
		client:   client,
		endpoint: endpoint,
	}, err
}

func (c *GenaiClient) convertToolsToGenai(tools []LLMTool) []*genai.Tool {
	// Gemini expects non empty FunctionDeclarations when tools are provided
	if len(tools) == 0 {
		return nil
	}

	fns := []*genai.FunctionDeclaration{}
	for _, tool := range tools {
		fd := &genai.FunctionDeclaration{
			Name:        tool.Name,
			Description: tool.Description,
		}
		schema := bbiSchemaToGenaiSchema(tool.Schema)
		// only add Parameters if there are any
		if len(schema.Properties) > 0 {
			fd.Parameters = schema
		}
		fns = append(fns, fd)
	}

	return []*genai.Tool{
		{FunctionDeclarations: fns},
	}
}

func (c *GenaiClient) prepareGenaiHistory(history []*ModelMessage) (genaiHistory []*genai.Content, systemInstruction *genai.Content, err error) {
	systemInstruction = &genai.Content{
		Parts: []*genai.Part{},
		Role:  "system",
	}
	for _, msg := range history {
		var content *genai.Content
		switch msg.Role {
		case "system":
			content = systemInstruction
		case "user", "function":
			content = &genai.Content{
				Parts: []*genai.Part{},
				Role:  "user",
			}
		case "model", "assistant":
			content = &genai.Content{
				Parts: []*genai.Part{},
				Role:  "model",
			}
		default:
			return nil, nil, fmt.Errorf("unexpected role %s", msg.Role)
		}

		// message was a tool call
		if msg.ToolCallID != "" {
			// find the function name
			content.Parts = append(content.Parts, &genai.Part{
				FunctionResponse: &genai.FunctionResponse{
					Name: msg.ToolCallID,
					// Genai expects a json format response
					Response: map[string]any{
						"response": msg.Content,
						"error":    msg.ToolErrored,
					},
				},
			})
		} else { // just content
			c := msg.Content
			if c == "" {
				c = " "
			}
			content.Parts = append(content.Parts, &genai.Part{Text: c})
		}

		// add tool calls
		for _, call := range msg.ToolCalls {
			content.Parts = append(content.Parts, &genai.Part{
				FunctionCall: &genai.FunctionCall{
					Name: call.ID,
					Args: call.Function.Arguments,
				},
			})
		}

		if content.Role != "system" {
			genaiHistory = append(genaiHistory, content)
		}
	}

	if len(systemInstruction.Parts) == 0 {
		systemInstruction = nil
	}

	return genaiHistory, systemInstruction, nil
}

func (c *GenaiClient) processStreamResponse(
	stream iter.Seq2[*genai.GenerateContentResponse, error],
	stdout io.Writer,
	onTokenUsage func(*genai.GenerateContentResponseUsageMetadata) LLMTokenUsage,
) (content string, toolCalls []LLMToolCall, tokenUsage LLMTokenUsage, err error) {
	for res, err := range stream {
		if err != nil {
			if apiErr, ok := err.(*apierror.APIError); ok {
				err = fmt.Errorf("google API error occurred: %w", apiErr.Unwrap())
				return content, toolCalls, tokenUsage, err
			}

			return content, toolCalls, tokenUsage, err
		}

		if res.UsageMetadata != nil {
			tokenUsage = onTokenUsage(res.UsageMetadata)
		}

		if len(res.Candidates) == 0 {
			err = &ModelFinishedError{Reason: "no response from model"}
			return content, toolCalls, tokenUsage, err
		}
		candidate := res.Candidates[0]
		if candidate.Content == nil {
			err = &ModelFinishedError{Reason: string(candidate.FinishReason)}
			return content, toolCalls, tokenUsage, err
		}

		for _, part := range candidate.Content.Parts {
			if x := part.Text; x != "" {
				fmt.Fprint(stdout, x)
				content += x
			} else if x := part.FunctionCall; x != nil {
				toolCalls = append(toolCalls, LLMToolCall{
					ID:       x.Name,
					Function: FuncCall{Name: x.Name, Arguments: x.Args},
					Type:     "function",
				})
			} else {
				err = fmt.Errorf("unexpected genai part: %+v", part)
				return content, toolCalls, tokenUsage, err
			}
		}
	}
	return content, toolCalls, tokenUsage, nil
}

var _ LLMClient = (*GenaiClient)(nil)

func (c *GenaiClient) IsRetryable(err error) bool {
	var apiErr genai.APIError
	ok := errors.As(err, &apiErr)
	if !ok {
		return false
	}
	switch apiErr.Code {
	case http.StatusServiceUnavailable, http.StatusTooManyRequests:
		return true
	default:
		return false
	}
}

func (c *GenaiClient) SendQuery(ctx context.Context, history []*ModelMessage, tools []LLMTool) (_ *LLMResponse, rerr error) {
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
		return nil, fmt.Errorf("failed to get inputTokens gauge: %w", err)
	}

	inputTokensCacheReads, err := m.Int64Gauge(telemetry.LLMInputTokensCacheReads)
	if err != nil {
		return nil, fmt.Errorf("failed to get inputTokensCacheReads gauge: %w", err)
	}

	outputTokens, err := m.Int64Gauge(telemetry.LLMOutputTokens)
	if err != nil {
		return nil, fmt.Errorf("failed to get outputTokens gauge: %w", err)
	}

	// convert history
	if len(history) == 0 {
		return nil, fmt.Errorf("genai history cannot be empty")
	}

	genaiHistory, systemInstruction, err := c.prepareGenaiHistory(history)
	if err != nil {
		return nil, fmt.Errorf("failed to convert history: %w", err)
	}

	if len(genaiHistory) == 0 {
		return nil, fmt.Errorf("no user prompt")
	}

	userMessage := genaiHistory[len(genaiHistory)-1]
	chatHistoryForGenai := genaiHistory[:len(genaiHistory)-1]

	// setup model
	chat, err := c.client.Chats.Create(ctx, c.endpoint.Model, &genai.GenerateContentConfig{
		SystemInstruction: systemInstruction,
		Tools:             c.convertToolsToGenai(tools),
	}, chatHistoryForGenai)
	if err != nil {
		return nil, fmt.Errorf("failed to create chat: %w", err)
	}

	// some unfortunate clunkiness here
	parts := []genai.Part{}
	for _, part := range userMessage.Parts {
		parts = append(parts, *part)
	}
	stream := chat.SendMessageStream(ctx, parts...)

	// records token usage metrics and updates the final summary struct based on metadata from the stream.
	tokenHandler := func(usageMeta *genai.GenerateContentResponseUsageMetadata) (usageSummary LLMTokenUsage) {
		candidatesTokens := int64(usageMeta.CandidatesTokenCount)
		promptTokens := int64(usageMeta.PromptTokenCount)
		cachedTokens := int64(usageMeta.CachedContentTokenCount)

		if candidatesTokens > 0 {
			outputTokens.Record(ctx, candidatesTokens, metric.WithAttributes(attrs...))
		}
		if promptTokens > 0 {
			inputTokens.Record(ctx, promptTokens, metric.WithAttributes(attrs...))
		}
		if cachedTokens > 0 {
			inputTokensCacheReads.Record(ctx, cachedTokens, metric.WithAttributes(attrs...))
		}

		usageSummary.OutputTokens += candidatesTokens
		usageSummary.InputTokens += promptTokens
		usageSummary.CachedTokenReads += cachedTokens
		usageSummary.TotalTokens += candidatesTokens + promptTokens

		return usageSummary
	}

	content, toolCalls, tokenUsage, err := c.processStreamResponse(
		stream,
		stdio.Stdout,
		tokenHandler,
	)
	if err != nil {
		return nil, err
	}

	return &LLMResponse{
		Content:    content,
		ToolCalls:  toolCalls,
		TokenUsage: tokenUsage,
	}, nil
}

// TODO: this definitely needs a unit test
func bbiSchemaToGenaiSchema(bbi map[string]any) *genai.Schema {
	schema := &genai.Schema{}
	for key, param := range bbi {
		switch key {
		case "description":
			if schema.Description != "" {
				schema.Description += " "
			}
			schema.Description += param.(string)
		case "properties":
			schema.Properties = map[string]*genai.Schema{}
			for propKey, propParam := range param.(map[string]any) {
				schema.Properties[propKey] = bbiSchemaToGenaiSchema(propParam.(map[string]any))
			}
		case "default": // just setting Nullable=true. Genai Schema does not have Default
			yes := true
			schema.Nullable = &yes
		case "type":
			switch x := param.(type) {
			case string:
				gtype := bbiTypeToGenaiType(x)
				schema.Type = gtype
			case []string:
				gtype := bbiTypeToGenaiType(x[0])
				schema.Type = gtype
				if x[1] == "null" {
					yes := true
					schema.Nullable = &yes
				}
			}
		case "format":
			switch param {
			case "enum", "date-time":
				// Genai only supports these format values. :(
				schema.Format = param.(string)
			case "uri":
				if pattern, ok := bbi["pattern"].(string); ok {
					schema.Description = fmt.Sprintf("URI format, %s.", pattern)
				} else {
					schema.Description = "URI format."
				}
			}
		case "items":
			schema.Items = bbiSchemaToGenaiSchema(param.(map[string]any))
		case "required":
			schema.Required = bbi["required"].([]string)
		case "enum":
			schema.Enum = param.([]string)
			schema.Format = "enum"
		}
	}

	return schema
}

func bbiTypeToGenaiType(bbi string) genai.Type {
	switch bbi {
	case "integer":
		return genai.TypeInteger
	case "number":
		return genai.TypeNumber
	case "string":
		return genai.TypeString
	case "boolean":
		return genai.TypeBoolean
	case "array":
		return genai.TypeArray
	case "object":
		return genai.TypeObject
	default: // should not happen
		return genai.TypeUnspecified
	}
}
