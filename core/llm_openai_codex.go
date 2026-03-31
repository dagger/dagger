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

func (c *OpenAICodexClient) SendQuery(ctx context.Context, history []*LLMMessage, tools []LLMTool, opts *LLMCallOpts) (_ *LLMResponse, rerr error) {
	parentCtx := ctx

	dp := newDisplayPhases(parentCtx)
	textPhase := dp.StartText(-1)
	defer func() {
		dp.CloseAll()
		if rerr != nil {
			dp.Abort(rerr)
		}
	}()

	m := telemetry.Meter(parentCtx, InstrumentationLibrary)
	spanCtx := trace.SpanContextFromContext(parentCtx)
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
	var contentBlocks []*LLMContentBlock
	var usage LLMTokenUsage

	for stream.Next() {
		event := stream.Current()

		switch event.Type {
		case "response.output_text.delta":
			e := event.AsResponseOutputTextDelta()
			fmt.Fprint(textPhase.Stdio.Stdout, e.Delta)
			content.WriteString(e.Delta)

		case "response.output_item.added":
			e := event.AsResponseOutputItemAdded()
			if e.Item.Type == "function_call" {
				fc := e.Item.AsFunctionCall()
				dp.StartToolCall(e.OutputIndex, fc.CallID, fc.Name)
			}

		case "response.function_call_arguments.delta":
			e := event.AsResponseFunctionCallArgumentsDelta()
			if p := dp.Phase(e.OutputIndex); p != nil {
				fmt.Fprint(p.Stdio.Stdout, e.Delta)
			}

		case "response.output_item.done":
			e := event.AsResponseOutputItemDone()
			if e.Item.Type == "function_call" {
				fc := e.Item.AsFunctionCall()
				contentBlocks = append(contentBlocks, &LLMContentBlock{
					Kind:      LLMContentToolCall,
					CallID:    fc.CallID,
					ToolName:  fc.Name,
					Arguments: JSON(fc.Arguments),
				})
				dp.Close(e.OutputIndex)
			}

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

			// Extract text from the completed response (tool calls handled above)
			for _, item := range resp.Output {
				if item.Type == "message" {
					msg := item.AsMessage()
					for _, part := range msg.Content {
						if part.Type == "output_text" {
							text := part.AsOutputText()
							if content.Len() == 0 {
								content.WriteString(text.Text)
							}
						}
					}
				}
			}
		}
	}

	if stream.Err() != nil {
		return nil, stream.Err()
	}

	// Prepend text content block if we got any text
	if content.Len() > 0 {
		contentBlocks = append([]*LLMContentBlock{{
			Kind: LLMContentText,
			Text: content.String(),
		}}, contentBlocks...)
	}

	if len(contentBlocks) == 0 {
		return nil, &ModelFinishedError{
			Reason: "no response from model",
		}
	}

	displaySpans, toolCalls := dp.Response()
	return &LLMResponse{
		Content:          contentBlocks,
		TokenUsage:       usage,
		DisplaySpans:     displaySpans,
		ToolCallDisplays: toolCalls,
	}, nil
}

// convertToCodexResponsesFormat converts the internal message history to the
// OpenAI Responses API input format.
func convertToCodexResponsesFormat(history []*LLMMessage) (systemPrompt string, items []responses.ResponseInputItemUnionParam) {
	var systemParts []string

	for _, msg := range history {
		switch msg.Role {
		case LLMMessageRoleSystem:
			systemParts = append(systemParts, msg.TextContent())

		case LLMMessageRoleUser:
			// Check if this is a tool result
			if msg.IsToolResult() {
				for _, block := range msg.Content {
					if block.Kind == LLMContentToolResult {
						output := block.Text
						if block.Errored {
							output = "error: " + output
						}
						items = append(items, responses.ResponseInputItemUnionParam{
							OfFunctionCallOutput: &responses.ResponseInputItemFunctionCallOutputParam{
								CallID: block.CallID,
								Output: responses.ResponseInputItemFunctionCallOutputOutputUnionParam{
									OfString: param.NewOpt(output),
								},
							},
						})
					}
				}
			} else {
				items = append(items, responses.ResponseInputItemUnionParam{
					OfMessage: &responses.EasyInputMessageParam{
						Role: responses.EasyInputMessageRoleUser,
						Content: responses.EasyInputMessageContentUnionParam{
							OfString: param.NewOpt(msg.TextContent()),
						},
					},
				})
			}

		case LLMMessageRoleAssistant:
			// Add text content
			text := msg.TextContent()
			if text != "" {
				items = append(items, responses.ResponseInputItemUnionParam{
					OfMessage: &responses.EasyInputMessageParam{
						Role: responses.EasyInputMessageRoleAssistant,
						Content: responses.EasyInputMessageContentUnionParam{
							OfString: param.NewOpt(text),
						},
					},
				})
			}
			// Add tool calls as function_call items
			for _, block := range msg.ToolCalls() {
				items = append(items, responses.ResponseInputItemUnionParam{
					OfFunctionCall: &responses.ResponseFunctionToolCallParam{
						CallID:    block.CallID,
						Name:      block.ToolName,
						Arguments: block.Arguments.String(),
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
