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
	inputTokensCacheReads, err := m.Int64Gauge(telemetry.LLMInputTokensCacheReads)
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

	// NB: no max_output_tokens is sent, so an explicit maxTokens cap has no
	// effect here. The ChatGPT backend only officially serves the Codex CLI,
	// which never sends an output cap; an unexpected parameter risks
	// rejection.
	params := responses.ResponseNewParams{
		Model:        strings.TrimPrefix(c.endpoint.Model, "openai-codex/"),
		Instructions: param.NewOpt(systemPrompt),
		Input: responses.ResponseNewParamsInputUnion{
			OfInputItemList: inputItems,
		},
		Store: param.NewOpt(false),
		// Return the encrypted reasoning chain so it can be replayed on the next
		// turn. With Store:false the server keeps no state, so a reasoning model
		// (e.g. gpt-5-codex) would otherwise reject a function call replayed
		// without the reasoning item that produced it.
		Include: []responses.ResponseIncludable{
			responses.ResponseIncludableReasoningEncryptedContent,
		},
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
	// Reasoning summary display spans use negative indices so they never
	// collide with the text (0) or tool-call (1+) phases.
	var reasonIdx int64
	var hasReasoning bool

	for stream.Next() {
		event := stream.Current()

		switch event.Type {
		case "response.output_text.delta":
			e := event.AsResponseOutputTextDelta()
			fmt.Fprint(dp.StartText(0).MarkdownW, e.Delta)
			content.WriteString(e.Delta)

		case "response.output_item.done":
			e := event.AsResponseOutputItemDone()
			switch e.Item.Type {
			case "function_call":
				fc := e.Item.AsFunctionCall()
				contentBlocks = append(contentBlocks, &LLMContentBlock{
					Kind:      LLMContentToolCall,
					CallID:    fc.CallID,
					ToolName:  fc.Name,
					Arguments: JSON(fc.Arguments),
				})
				toolIdx++
				dp.EmitToolCall(toolIdx, fc.CallID, fc.Name, fc.Arguments)
			case "reasoning":
				// Capture the reasoning item (its id, encrypted content, and
				// summary) so it can be replayed before the function call it
				// produced. It's appended in stream order, so it precedes that
				// call. The opaque data is stashed in a THINKING block's
				// Signature; the summary text (if any) is human-readable.
				r := e.Item.AsReasoning()
				summary, sig := encodeCodexReasoning(r)
				contentBlocks = append(contentBlocks, &LLMContentBlock{
					Kind:      LLMContentThinking,
					Text:      summary,
					Signature: sig,
				})
				hasReasoning = true
				if summary != "" {
					reasonIdx--
					p := dp.StartThinking(reasonIdx)
					fmt.Fprint(p.Stdio.Stdout, summary)
					dp.Close(reasonIdx)
				}
			}

		case "response.completed":
			e := event.AsResponseCompleted()
			resp := e.Response
			cachedTokens := resp.Usage.InputTokensDetails.CachedTokens
			usage.InputTokens = uncachedInputTokens(resp.Usage.InputTokens, cachedTokens)
			usage.CachedTokenReads = cachedTokens
			usage.OutputTokens = resp.Usage.OutputTokens
			usage.TotalTokens = usage.InputTokens + usage.OutputTokens + usage.CachedTokenReads
			if resp.Usage.TotalTokens > usage.TotalTokens {
				usage.TotalTokens = resp.Usage.TotalTokens
			}
			if usage.InputTokens > 0 {
				inputTokens.Record(ctx, usage.InputTokens, metric.WithAttributes(attrs...))
			}
			if usage.CachedTokenReads > 0 {
				inputTokensCacheReads.Record(ctx, usage.CachedTokenReads, metric.WithAttributes(attrs...))
			}
			if usage.OutputTokens > 0 {
				outputTokens.Record(ctx, usage.OutputTokens, metric.WithAttributes(attrs...))
			}

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

	// Add the text answer as a content block. When there's reasoning, it must
	// come after the reasoning/tool-call blocks: the Responses API requires each
	// reasoning item to be immediately followed by the item it produced, so a
	// leading text block would strand a reasoning item on replay. Without
	// reasoning, keep the text ahead of any tool calls (unchanged behavior).
	if content.Len() > 0 {
		textBlock := &LLMContentBlock{
			Kind: LLMContentText,
			Text: content.String(),
		}
		if hasReasoning {
			contentBlocks = append(contentBlocks, textBlock)
		} else {
			contentBlocks = append([]*LLMContentBlock{textBlock}, contentBlocks...)
		}
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
			// Emit blocks in their stored order so a reasoning item precedes the
			// function call it produced, as the Responses API requires when the
			// reasoning chain is replayed (Store:false).
			for _, block := range msg.Content {
				switch block.Kind {
				case LLMContentText:
					if block.Text != "" {
						items = append(items, responses.ResponseInputItemUnionParam{
							OfMessage: &responses.EasyInputMessageParam{
								Role: responses.EasyInputMessageRoleAssistant,
								Content: responses.EasyInputMessageContentUnionParam{
									OfString: param.NewOpt(block.Text),
								},
							},
						})
					}
				case LLMContentThinking:
					// Replay a captured reasoning item ahead of its function call.
					// Only when we have its encrypted content: with Store:false a
					// bare id references server state that no longer exists.
					if reasoning, ok := decodeCodexReasoning(block.Signature); ok {
						items = append(items, responses.ResponseInputItemUnionParam{
							OfReasoning: reasoning,
						})
					}
				case LLMContentToolCall:
					// The Responses API rejects an empty arguments string;
					// normalize it to an empty JSON object, mirroring the chat path.
					args := block.Arguments.String()
					if args == "" {
						args = "{}"
					}
					items = append(items, responses.ResponseInputItemUnionParam{
						OfFunctionCall: &responses.ResponseFunctionToolCallParam{
							CallID:    block.CallID,
							Name:      block.ToolName,
							Arguments: args,
						},
					})
				}
			}
		}
	}

	systemPrompt = strings.Join(systemParts, "\n\n")
	return systemPrompt, items
}

// codexReasoning is the opaque data stashed in a THINKING block's Signature so a
// Responses API reasoning item can be round-tripped across turns. Codex requests
// use Store:false, so the server keeps no state and the reasoning item's id +
// encrypted content must be carried in the history and replayed verbatim.
type codexReasoning struct {
	ID               string   `json:"id"`
	EncryptedContent string   `json:"encrypted_content,omitempty"`
	Summary          []string `json:"summary,omitempty"`
}

// encodeCodexReasoning converts a streamed reasoning item into a human-readable
// summary (for display) and an opaque signature (for replay).
func encodeCodexReasoning(r responses.ResponseReasoningItem) (summary string, signature string) {
	cr := codexReasoning{
		ID:               r.ID,
		EncryptedContent: r.EncryptedContent,
	}
	var parts []string
	for _, s := range r.Summary {
		cr.Summary = append(cr.Summary, s.Text)
		if s.Text != "" {
			parts = append(parts, s.Text)
		}
	}
	data, err := json.Marshal(cr)
	if err != nil {
		return strings.Join(parts, "\n\n"), ""
	}
	return strings.Join(parts, "\n\n"), string(data)
}

// decodeCodexReasoning reconstructs a reasoning input item from a THINKING
// block's Signature. It returns ok=false when the signature isn't a codex
// reasoning payload (e.g. another provider's thinking signature) or lacks the
// encrypted content required to replay it under Store:false.
func decodeCodexReasoning(signature string) (*responses.ResponseReasoningItemParam, bool) {
	if signature == "" {
		return nil, false
	}
	var cr codexReasoning
	if err := json.Unmarshal([]byte(signature), &cr); err != nil {
		return nil, false
	}
	if cr.ID == "" || cr.EncryptedContent == "" {
		return nil, false
	}
	item := &responses.ResponseReasoningItemParam{
		ID:               cr.ID,
		EncryptedContent: param.NewOpt(cr.EncryptedContent),
		// Summary is required by the API but may be empty.
		Summary: []responses.ResponseReasoningItemSummaryParam{},
	}
	for _, s := range cr.Summary {
		item.Summary = append(item.Summary, responses.ResponseReasoningItemSummaryParam{Text: s})
	}
	return item, true
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
