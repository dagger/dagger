package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"dagger.io/dagger/telemetry"
	"github.com/googleapis/gax-go/v2/apierror"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"

	genai "github.com/google/generative-ai-go/genai"
)

type GenaiClient struct {
	client   *genai.Client
	endpoint *LLMEndpoint
}

func newGenaiClient(endpoint *LLMEndpoint) (*GenaiClient, error) {
	opts := []option.ClientOption{option.WithAPIKey(endpoint.Key)}
	if endpoint.Key != "" {
		opts = append(opts, option.WithAPIKey(endpoint.Key))
	}
	if endpoint.BaseURL != "" {
		opts = append(opts, option.WithEndpoint(endpoint.BaseURL))
	}
	ctx := context.Background() // FIXME: should we wire this through from somewhere else?
	client, err := genai.NewClient(ctx, opts...)
	if err != nil {
		return nil, err
	}
	return &GenaiClient{
		client:   client,
		endpoint: endpoint,
	}, err
}

var dbgEnc *json.Encoder

func init() {
	os.Setenv("LLM_LOG", "/tmp/llm_log.json")
	if fn := os.Getenv("LLM_LOG"); fn != "" {
		debugFile, err := os.Create(fn)
		if err != nil {
			panic(err)
		}
		dbgEnc = json.NewEncoder(debugFile)
	} else {
		dbgEnc = json.NewEncoder(io.Discard)
	}
	dbgEnc.SetIndent("", "  ")
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

func (c *GenaiClient) prepareGenaiHistory(history []ModelMessage) (genaiHistory []*genai.Content, systemInstruction *genai.Content, err error) {
	systemInstruction = &genai.Content{
		Parts: []genai.Part{},
		Role:  "system",
	}
	for _, msg := range history {
		var content *genai.Content
		switch msg.Role {
		case "system":
			content = systemInstruction
		case "user", "function":
			content = &genai.Content{
				Parts: []genai.Part{},
				Role:  "user",
			}
		case "model", "assistant":
			content = &genai.Content{
				Parts: []genai.Part{},
				Role:  "model",
			}
		default:
			return nil, nil, fmt.Errorf("unexpected role %s", msg.Role)
		}

		// message was a tool call
		if msg.ToolCallID != "" {
			// find the function name
			content.Parts = append(content.Parts, genai.FunctionResponse{
				Name: msg.ToolCallID,
				// Genai expects a json format response
				Response: map[string]any{
					"response": msg.Content,
					"error":    msg.ToolErrored,
				},
			})
		} else { // just content
			c := msg.Content
			if c == "" {
				c = " "
			}
			content.Parts = append(content.Parts, genai.Text(c))
		}

		// add tool calls
		for _, call := range msg.ToolCalls {
			content.Parts = append(content.Parts, genai.FunctionCall{
				Name: call.ID,
				Args: call.Function.Arguments,
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
	stream *genai.GenerateContentResponseIterator,
	stdout io.Writer,
	onTokenUsage func(*genai.UsageMetadata) LLMTokenUsage,
) (content string, toolCalls []ToolCall, tokenUsage LLMTokenUsage, err error) {
	for {
		res, err := stream.Next()
		if err != nil {
			if errors.Is(err, iterator.Done) {
				break
			}
			if apiErr, ok := err.(*apierror.APIError); ok {
				err = fmt.Errorf("google API error occurred: %w", apiErr.Unwrap())
				return content, toolCalls, tokenUsage, err
			}

			return content, toolCalls, tokenUsage, err
		}

		dbgEnc.Encode(res)

		// Keep track of the token usage
		if res.UsageMetadata != nil {
			tokenUsage = onTokenUsage(res.UsageMetadata)
		}

		if len(res.Candidates) == 0 {
			err = &ModelFinishedError{Reason: "no response from model"}
			return content, toolCalls, tokenUsage, err
		}
		candidate := res.Candidates[0]
		if candidate.Content == nil {
			err = &ModelFinishedError{Reason: candidate.FinishReason.String()}
			return content, toolCalls, tokenUsage, err
		}

		for _, part := range candidate.Content.Parts {
			switch x := part.(type) {
			case genai.Text:
				fmt.Fprint(stdout, x)
				content += string(x)
			case genai.FunctionCall:
				toolCalls = append(toolCalls, ToolCall{
					ID:       x.Name,
					Function: FuncCall{Name: x.Name, Arguments: x.Args},
					Type:     "function",
				})
			default:
				err = fmt.Errorf("unexpected genai part type %T", part)
				return content, toolCalls, tokenUsage, err
			}
		}
	}

	return content, toolCalls, tokenUsage, nil
}

func (c *GenaiClient) SendQuery(ctx context.Context, history []ModelMessage, tools []LLMTool) (_ *LLMResponse, rerr error) {
	// setup tracing & telemetry
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

	// setup model
	model := c.client.GenerativeModel(c.endpoint.Model)

	// convert tools
	model.Tools = c.convertToolsToGenai(tools)

	// convert history
	if len(history) == 0 {
		return nil, fmt.Errorf("genai history cannot be empty")
	}

	genaiHistory, systemInstruction, err := c.prepareGenaiHistory(history)
	if err != nil {
		return nil, fmt.Errorf("failed to convert history: %w", err)
	}
	model.SystemInstruction = systemInstruction

	dbgEnc.Encode("---------------------------------------------")
	dbgEnc.Encode(model)
	dbgEnc.Encode(genaiHistory)

	// Pop last message from history for SendMessage
	if len(genaiHistory) == 0 {
		return nil, fmt.Errorf("no user prompt")
	}

	userMessage := genaiHistory[len(genaiHistory)-1]
	chatHistoryForGenai := genaiHistory[:len(genaiHistory)-1]

	chat := model.StartChat()
	chat.History = chatHistoryForGenai

	stream := chat.SendMessageStream(ctx, userMessage.Parts...)

	// records token usage metrics and updates the final summary struct based on metadata from the stream.
	tokenHandler := func(usageMeta *genai.UsageMetadata) (usageSummary LLMTokenUsage) {
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
			schema.Nullable = true
		case "type":
			gtype := bbiTypeToGenaiType(param.(string))
			schema.Type = gtype
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
