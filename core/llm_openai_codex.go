package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	telemetry "github.com/dagger/otel-go"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// OpenAICodexClient uses the OpenAI Responses API against the ChatGPT
// backend (chatgpt.com/backend-api/codex/responses) with a ChatGPT
// subscription OAuth token.
type OpenAICodexClient struct {
	svc      responses.ResponseService
	endpoint *LLMEndpoint
}

func newOpenAICodexClient(endpoint *LLMEndpoint) *OpenAICodexClient {
	var opts []option.RequestOption

	// The base URL for the Codex API; the SDK appends "responses" to it.
	opts = append(opts, option.WithBaseURL(endpoint.BaseURL+"/codex"))

	// Use the OAuth access token as the API key (sets Authorization: Bearer <token>)
	opts = append(opts, option.WithAPIKey(endpoint.AuthToken))

	// Extract chatgpt_account_id from JWT for required header
	if accountID := extractChatGPTAccountID(endpoint.AuthToken); accountID != "" {
		opts = append(opts, option.WithHeader("chatgpt-account-id", accountID))
	}

	opts = append(opts, option.WithHeader("OpenAI-Beta", "responses=experimental"))
	opts = append(opts, option.WithHeader("originator", "dagger"))
	opts = append(opts, option.WithHeader("User-Agent", "dagger"))

	// Inject OTel tracing HTTP client to capture LLM request/response bodies
	opts = append(opts, option.WithHTTPClient(newLLMOTelHTTPClient("openai-codex")))

	svc := responses.NewResponseService(opts...)
	return &OpenAICodexClient{svc: svc, endpoint: endpoint}
}

var _ LLMClient = (*OpenAICodexClient)(nil)

func (c *OpenAICodexClient) IsRetryable(err error) bool {
	return false
}

func (c *OpenAICodexClient) SendQuery(ctx context.Context, history []*LLMMessage, tools []LLMTool) (_ *LLMResponse, rerr error) {
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

	// Build system prompt and input messages
	systemPrompt, inputItems := convertToCodexResponsesFormat(history)

	// Build tools
	var toolParams []responses.ToolUnionParam
	for _, tool := range tools {
		toolParams = append(toolParams, responses.ToolUnionParam{
			OfFunction: &responses.FunctionToolParam{
				Name:        tool.Name,
				Description: param.NewOpt(tool.Description),
				Parameters:  tool.Schema,
			},
		})
	}

	params := responses.ResponseNewParams{
		Model:        c.endpoint.Model,
		Instructions: param.NewOpt(systemPrompt),
		Input: responses.ResponseNewParamsInputUnion{
			OfInputItemList: inputItems,
		},
		Store: param.NewOpt(false),
	}
	if len(toolParams) > 0 {
		params.Tools = toolParams
		params.ToolChoice = responses.ResponseNewParamsToolChoiceUnion{
			OfToolChoiceMode: param.NewOpt(responses.ToolChoiceOptionsAuto),
		}
		params.ParallelToolCalls = param.NewOpt(true)
	}

	// Configure reasoning effort if specified
	if c.endpoint.ThinkingMode != "" {
		params.Reasoning = shared.ReasoningParam{
			Effort:  shared.ReasoningEffort(c.endpoint.ThinkingMode),
			Summary: shared.ReasoningSummaryConcise,
		}
	}

	// Use streaming
	stream := c.svc.NewStreaming(ctx, params)
	defer stream.Close()

	var content strings.Builder
	var toolCalls []*LLMToolCall
	var usage LLMTokenUsage

	for stream.Next() {
		event := stream.Current()

		switch event.Type {
		case "response.output_text.delta":
			e := event.AsResponseOutputTextDelta()
			fmt.Fprint(stdio.Stdout, e.Delta)
			content.WriteString(e.Delta)

		case "response.completed":
			e := event.AsResponseCompleted()
			resp := e.Response
			if resp.Usage.InputTokens > 0 {
				usage.InputTokens = int64(resp.Usage.InputTokens)
				inputTokens.Record(ctx, usage.InputTokens, metric.WithAttributes(attrs...))
			}
			if resp.Usage.OutputTokens > 0 {
				usage.OutputTokens = int64(resp.Usage.OutputTokens)
				outputTokens.Record(ctx, usage.OutputTokens, metric.WithAttributes(attrs...))
			}
			usage.TotalTokens = usage.InputTokens + usage.OutputTokens

			// Extract tool calls and text from the completed response
			for _, item := range resp.Output {
				switch item.Type {
				case "message":
					msg := item.AsMessage()
					for _, part := range msg.Content {
						if part.Type == "output_text" {
							text := part.AsOutputText()
							if content.Len() == 0 {
								content.WriteString(text.Text)
							}
						}
					}
				case "function_call":
					fc := item.AsFunctionCall()
					toolCalls = append(toolCalls, &LLMToolCall{
						CallID:    fc.CallID,
						Name:      fc.Name,
						Arguments: JSON(fc.Arguments),
					})
				}
			}
		}
	}

	if stream.Err() != nil {
		return nil, stream.Err()
	}

	if content.Len() == 0 && len(toolCalls) == 0 {
		return nil, &ModelFinishedError{
			Reason: "no response from model",
		}
	}

	return &LLMResponse{
		Content:    content.String(),
		ToolCalls:  toolCalls,
		TokenUsage: usage,
	}, nil
}

// convertToCodexResponsesFormat converts the internal message history to the
// OpenAI Responses API input format.
func convertToCodexResponsesFormat(history []*LLMMessage) (systemPrompt string, items []responses.ResponseInputItemUnionParam) {
	var systemParts []string

	for _, msg := range history {
		switch {
		case msg.Role == "system":
			systemParts = append(systemParts, msg.Content)

		case msg.ToolCallID != "":
			// Tool result
			output := msg.Content
			if msg.ToolErrored {
				output = "error: " + output
			}
			items = append(items, responses.ResponseInputItemUnionParam{
				OfFunctionCallOutput: &responses.ResponseInputItemFunctionCallOutputParam{
					CallID: msg.ToolCallID,
					Output: responses.ResponseInputItemFunctionCallOutputOutputUnionParam{
						OfString: param.NewOpt(output),
					},
				},
			})

		case msg.Role == "user":
			items = append(items, responses.ResponseInputItemUnionParam{
				OfMessage: &responses.EasyInputMessageParam{
					Role: responses.EasyInputMessageRoleUser,
					Content: responses.EasyInputMessageContentUnionParam{
						OfString: param.NewOpt(msg.Content),
					},
				},
			})

		case msg.Role == "assistant":
			// Add text content
			if msg.Content != "" {
				items = append(items, responses.ResponseInputItemUnionParam{
					OfMessage: &responses.EasyInputMessageParam{
						Role: responses.EasyInputMessageRoleAssistant,
						Content: responses.EasyInputMessageContentUnionParam{
							OfString: param.NewOpt(msg.Content),
						},
					},
				})
			}
			// Add tool calls as function_call items
			for _, tc := range msg.ToolCalls {
				items = append(items, responses.ResponseInputItemUnionParam{
					OfFunctionCall: &responses.ResponseFunctionToolCallParam{
						CallID:    tc.CallID,
						Name:      tc.Name,
						Arguments: tc.Arguments.String(),
					},
				})
			}
		}
	}

	systemPrompt = strings.Join(systemParts, "\n\n")
	return systemPrompt, items
}

// extractChatGPTAccountID extracts the chatgpt_account_id from a JWT token.
func extractChatGPTAccountID(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ""
	}

	// JWT payloads use base64url encoding (may need padding)
	payload := parts[1]
	switch len(payload) % 4 {
	case 2:
		payload += "=="
	case 3:
		payload += "="
	}

	decoded, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		return ""
	}

	var claims map[string]interface{}
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return ""
	}

	auth, ok := claims["https://api.openai.com/auth"].(map[string]interface{})
	if !ok {
		return ""
	}

	accountID, _ := auth["chatgpt_account_id"].(string)
	return accountID
}
