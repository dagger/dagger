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

func (c *GenaiClient) SendQuery(ctx context.Context, history []ModelMessage, tools []LLMTool) (_ *LLMResponse, rerr error) {
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

	inputTokensCacheReads, err := m.Int64Gauge(telemetry.LLMInputTokensCacheReads)
	if err != nil {
		return nil, err
	}

	outputTokens, err := m.Int64Gauge(telemetry.LLMOutputTokens)
	if err != nil {
		return nil, err
	}

	// set model
	model := c.client.GenerativeModel(c.endpoint.Model)

	// set system prompt
	model.SystemInstruction = &genai.Content{
		Role: "system",
	}

	// convert tools to Genai tool format.
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
	model.Tools = []*genai.Tool{
		&genai.Tool{
			FunctionDeclarations: fns,
		},
	}

	// convert history to genai.Content
	genaiHistory := []*genai.Content{}
	if len(history) == 0 {
		return nil, fmt.Errorf("genai history cannot be empty")
	}

	model.SystemInstruction = &genai.Content{
		Parts: []genai.Part{},
		Role:  "system",
	}

	for _, msg := range history {
		var content *genai.Content
		switch msg.Role {
		case "system":
			content = model.SystemInstruction
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
			return nil, fmt.Errorf("unexpected role %s", msg.Role)
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
	if len(model.SystemInstruction.Parts) == 0 {
		model.SystemInstruction.Parts = []genai.Part{genai.Text(defaultSystemPrompt)}
	}

	dbgEnc.Encode("---------------------------------------------")
	dbgEnc.Encode(model)
	dbgEnc.Encode(genaiHistory)

	// Pop last message from history for SendMessage
	userMessage := genaiHistory[len(genaiHistory)-1]
	genaiHistory = genaiHistory[:len(genaiHistory)-1]

	chat := model.StartChat()
	chat.History = genaiHistory

	stream := chat.SendMessageStream(ctx, userMessage.Parts...)

	var content string
	var toolCalls []ToolCall
	for {
		res, err := stream.Next()
		if err != nil {
			if errors.Is(err, iterator.Done) {
				break
			}
			if apiErr, ok := err.(*apierror.APIError); ok {
				// unwrap the APIError
				return nil, fmt.Errorf("google API error occurred: %w", apiErr.Unwrap())
			}
			return nil, err
		}

		dbgEnc.Encode(res)

		// Keep track of the token usage
		if res.UsageMetadata != nil {
			if res.UsageMetadata.CandidatesTokenCount > 0 {
				outputTokens.Record(ctx, int64(res.UsageMetadata.CandidatesTokenCount), metric.WithAttributes(attrs...))
			}
			if res.UsageMetadata.PromptTokenCount > 0 {
				inputTokens.Record(ctx, int64(res.UsageMetadata.PromptTokenCount), metric.WithAttributes(attrs...))
			}
			if res.UsageMetadata.CachedContentTokenCount > 0 {
				inputTokensCacheReads.Record(ctx, int64(res.UsageMetadata.CachedContentTokenCount), metric.WithAttributes(attrs...))
			}
		}

		if len(res.Candidates) == 0 {
			return nil, fmt.Errorf("no response from model")
		}
		candidate := res.Candidates[0]
		if candidate.Content == nil {
			return nil, fmt.Errorf("no content?")
		}

		for _, part := range candidate.Content.Parts {
			switch x := part.(type) {
			case genai.Text:
				fmt.Fprint(stdio.Stdout, x)
				content += string(part.(genai.Text))
			case genai.FunctionCall:
				toolCalls = append(toolCalls, ToolCall{
					ID: x.Name,
					Function: FuncCall{
						Name:      x.Name,
						Arguments: x.Args,
					},
					Type: "function",
				})
			default:
				return nil, fmt.Errorf("unexpected genai part type %T", part)
			}
		}
	}

	return &LLMResponse{
		Content:   content,
		ToolCalls: toolCalls,
	}, nil
}

// TODO: this definitely needs a unit test
func bbiSchemaToGenaiSchema(bbi map[string]any) *genai.Schema {
	schema := &genai.Schema{}
	for key, param := range bbi {
		switch key {
		case "description":
			schema.Description = param.(string)
		case "properties":
			schema.Properties = map[string]*genai.Schema{}
			for propKey, propParam := range param.(map[string]any) {
				schema.Properties[propKey] = bbiSchemaToGenaiSchema(propParam.(map[string]any))
			}
		case "default": // just setting Nullable=true. Genai Schema does not have Default
			schema.Nullable = true
		case "type":
			switch x := param.(type) {
			case string:
				gtype := bbiTypeToGenaiType(x)
				schema.Type = gtype
			case []string:
				if len(x) == 2 {
					gtype := bbiTypeToGenaiType(x[0])
					schema.Type = gtype
					if x[1] == "null" {
						schema.Nullable = true
					}
				} else {
					panic(fmt.Sprintf("unexpected bbi schema array: %+v (%T)", x, x))
				}
			}
		// case "format": // ignoring format. Genai is very picky about format values
		// 	schema.Format = param.(string)
		case "items":
			schema.Items = bbiSchemaToGenaiSchema(param.(map[string]any))
		case "required":
			schema.Required = bbi["required"].([]string)
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
