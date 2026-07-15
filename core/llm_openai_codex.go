package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	telemetry "github.com/dagger/otel-go"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"
	"github.com/openai/openai-go/responses"
	"github.com/openai/openai-go/shared"
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

	opts = append(opts, option.WithHTTPClient(endpoint.otelHTTPClient("openai-codex")))

	svc := responses.NewResponseService(opts...)
	return &OpenAICodexClient{svc: svc, endpoint: endpoint}
}

var _ LLMClient = (*OpenAICodexClient)(nil)

func (c *OpenAICodexClient) IsRetryable(err error) bool {
	return false
}

func (c *OpenAICodexClient) SendQuery(ctx context.Context, history []*LLMMessage, tools []LLMTool, opts *LLMCallOpts) (_ *LLMResponse, rerr error) {
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
		Model:        strings.TrimPrefix(c.endpoint.Model, "openai-codex/"),
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
	if effort := c.endpoint.ReasoningEffort; effort != "" && effort != "none" {
		params.Reasoning = shared.ReasoningParam{
			Effort:  shared.ReasoningEffort(effort),
			Summary: shared.ReasoningSummaryConcise,
		}
	}

	// Use streaming
	stream := c.svc.NewStreaming(ctx, params)
	defer stream.Close()

	var content strings.Builder
	var contentBlocks []*LLMContentBlock
	var usage LLMTokenUsage
	var toolIdx int64

	for stream.Next() {
		event := stream.Current()

		switch event.Type {
		case "response.output_text.delta":
			e := event.AsResponseOutputTextDelta()
			fmt.Fprint(dp.StartText(0).MarkdownW, e.Delta)
			content.WriteString(e.Delta)

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
				toolIdx++
				dp.EmitToolCall(toolIdx, fc.CallID, fc.Name, fc.Arguments)
			}

		case "response.completed":
			e := event.AsResponseCompleted()
			resp := e.Response
			if resp.Usage.InputTokens > 0 {
				usage.InputTokens = resp.Usage.InputTokens
				inputTokens.Record(ctx, usage.InputTokens, metric.WithAttributes(attrs...))
			}
			if resp.Usage.OutputTokens > 0 {
				usage.OutputTokens = resp.Usage.OutputTokens
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
		return nil, codexAPIError(stream.Err())
	}
	// Close the streamed text response phase (if any).
	dp.Close(0)

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

	displaySpans, toolCallDisplays := dp.Response()
	return &LLMResponse{
		Content:          contentBlocks,
		TokenUsage:       usage,
		DisplaySpans:     displaySpans,
		ToolCallDisplays: toolCallDisplays,
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
								Output: output,
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

// codexAPIError turns an opaque openai-go API error into one that surfaces the
// ChatGPT Codex backend's error detail. The backend reports errors as
// {"detail":"..."} — a shape the SDK doesn't recognize (it only reads the
// "error" field), so it otherwise bubbles up a bare "400 Bad Request" with no
// explanation (e.g. an unsupported-model error).
func codexAPIError(err error) error {
	var aerr *openai.Error
	if !errors.As(err, &aerr) {
		return err
	}
	body := aerr.RawJSON()
	if body == "" && aerr.Response != nil && aerr.Response.Body != nil {
		if b, readErr := io.ReadAll(aerr.Response.Body); readErr == nil {
			body = string(b)
		}
	}
	if msg := llmErrorMessage([]byte(body)); msg != "" {
		return fmt.Errorf("codex API error (HTTP %d): %s", aerr.StatusCode, msg)
	}
	return err
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

	var claims map[string]any
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return ""
	}

	auth, ok := claims["https://api.openai.com/auth"].(map[string]any)
	if !ok {
		return ""
	}

	accountID, _ := auth["chatgpt_account_id"].(string)
	return accountID
}
