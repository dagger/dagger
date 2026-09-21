package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	telemetry "github.com/dagger/otel-go"
	"github.com/openai/openai-go/v3"
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

	// turnStates holds each conversation's sticky-routing token, keyed by
	// its prompt cache key (see codexTurnState).
	turnStates sync.Map
}

// codexTurnState is the backend's sticky-routing token for one turn of a
// conversation. The backend hands it out in the x-codex-turn-state header
// of the first response of a turn — one user prompt through to the model's
// final answer, however many tool-call rounds that takes — and Codex replays
// it unchanged on every request of that turn, and never across turns. It is
// what keeps a turn's requests on the node holding its cache; the prompt
// cache key alone only influences placement.
type codexTurnState struct {
	// turn identifies the turn the token belongs to: the index of the
	// prompt that opened it (see codexTurnIndex).
	turn  int
	token string
}

const codexTurnStateHeader = "x-codex-turn-state"

// codexTurnIndex locates the prompt that opened the current turn: the last
// user message carrying anything other than tool results.
func codexTurnIndex(history []*LLMMessage) int {
	for i := len(history) - 1; i >= 0; i-- {
		msg := history[i]
		if msg.Role != LLMMessageRoleUser {
			continue
		}
		for _, block := range msg.Content {
			if block.Kind != LLMContentToolResult {
				return i
			}
		}
	}
	return -1
}

// turnRequestOptions returns the per-request options that keep a
// conversation's requests together on the backend: the session-id header the
// ChatGPT backend derives cache affinity from, and the current turn's
// sticky-routing token when one has been issued.
func (c *OpenAICodexClient) turnRequestOptions(cacheKey string, turn int) []option.RequestOption {
	opts := []option.RequestOption{option.WithHeader("session-id", cacheKey)}
	if st, ok := c.turnStates.Load(cacheKey); ok {
		if st := st.(*codexTurnState); st.turn == turn {
			opts = append(opts, option.WithHeader(codexTurnStateHeader, st.token))
		}
	}
	return opts
}

// recordTurnState keeps the sticky-routing token issued at the start of a
// turn. Like Codex, the first token wins for the rest of the turn.
func (c *OpenAICodexClient) recordTurnState(cacheKey string, turn int, resp *http.Response) {
	if resp == nil {
		return
	}
	token := resp.Header.Get(codexTurnStateHeader)
	if token == "" {
		return
	}
	if st, ok := c.turnStates.Load(cacheKey); ok && st.(*codexTurnState).turn == turn {
		return
	}
	c.turnStates.Store(cacheKey, &codexTurnState{turn: turn, token: token})
}

func newOpenAICodexClient(endpoint *LLMEndpoint) *OpenAICodexClient {
	var opts []option.RequestOption

	// The base URL for the Codex API; the SDK appends "responses" to it.
	opts = append(opts, option.WithBaseURL(endpoint.BaseURL+"/codex"))

	// Use the OAuth access token as the API key (sets Authorization: Bearer <token>)
	opts = append(opts, option.WithAPIKey(endpoint.AuthToken))

	// Extract chatgpt_account_id from JWT for required header. Both this and
	// the bearer token above are only the values observed at construction:
	// when the endpoint carries a credential source, credentialTransport
	// recomputes both from the current token on every request.
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

// IsRetryable reports whether a failed turn is worth resending. The ChatGPT
// backend gives no reliable signal to key off, so nothing is retried here; a
// rejected credential is the one exception and is handled centrally, since
// resending only helps once the credential has been re-resolved (see
// sendQueryWithRetry).
func (c *OpenAICodexClient) IsRetryable(err error) bool {
	return false
}

//nolint:gocyclo // streaming response handling is clearest as one protocol state machine
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
	systemPrompt, inputItems, err := convertToCodexResponsesFormat(history)
	if err != nil {
		return nil, err
	}

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

	cacheKey := openAIPromptCacheKey(history)

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
		// Return the encrypted reasoning chain so it can be resubmitted on the next
		// turn. With Store:false the server keeps no state, so a reasoning model
		// (e.g. gpt-5-codex) would otherwise reject a resubmitted function call
		// without the reasoning item that produced it.
		Include: []responses.ResponseIncludable{
			responses.ResponseIncludableReasoningEncryptedContent,
		},
		// Pin the conversation to a cache node, as Codex does with its thread
		// id (see openAIPromptCacheKey).
		PromptCacheKey: param.NewOpt(cacheKey),
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
	turn := codexTurnIndex(history)
	var httpResp *http.Response
	reqOpts := append(c.turnRequestOptions(cacheKey, turn), option.WithResponseInto(&httpResp))
	stream := c.svc.NewStreaming(ctx, params, reqOpts...)
	defer stream.Close()
	c.recordTurnState(cacheKey, turn, httpResp)

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
				// summary) so it can be resubmitted before the function call it
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

		case "response.incomplete", "response.failed":
			// A turn cut short ends with one of these instead of
			// response.completed. Anything streamed so far is partial — a
			// truncated tool call, half a message — so report the stop rather
			// than letting the partial turn read as a clean finish.
			var resp responses.Response
			if event.Type == "response.incomplete" {
				resp = event.AsResponseIncomplete().Response
			} else {
				resp = event.AsResponseFailed().Response
			}
			reason := resp.IncompleteDetails.Reason
			if reason == "" {
				reason = resp.Error.Message
			}
			if reason == "" {
				reason = string(resp.Status)
			}
			return nil, &ModelFinishedError{Reason: reason}

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
	// leading text block would strand a reasoning item when resubmitted. Without
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
func convertToCodexResponsesFormat(history []*LLMMessage) (systemPrompt string, items []responses.ResponseInputItemUnionParam, err error) {
	var systemParts []string

	for i, msg := range history {
		if err := validateOpenAIMessage(msg); err != nil {
			return "", nil, fmt.Errorf("OpenAI Responses message %d: %w", i, err)
		}
		switch msg.Role {
		case LLMMessageRoleSystem:
			systemParts = append(systemParts, msg.TextContent())

		case LLMMessageRoleUser:
			var parts responses.ResponseInputMessageContentListParam
			flushParts := func() {
				if len(parts) > 0 {
					items = append(items, responses.ResponseInputItemUnionParam{
						OfMessage: &responses.EasyInputMessageParam{
							Role:    responses.EasyInputMessageRoleUser,
							Content: responses.EasyInputMessageContentUnionParam{OfInputItemContentList: parts},
						},
					})
					parts = nil
				}
			}
			for _, block := range msg.Content {
				if block.Kind == LLMContentToolResult {
					flushParts()
					output, err := codexToolOutput(block)
					if err != nil {
						return "", nil, fmt.Errorf("OpenAI Responses tool result %q: %w", block.CallID, err)
					}
					items = append(items, responses.ResponseInputItemUnionParam{
						OfFunctionCallOutput: &responses.ResponseInputItemFunctionCallOutputParam{
							CallID: param.NewOpt(block.CallID), Output: output,
						},
					})
					continue
				}
				part, err := codexContentPart(block)
				if err != nil {
					return "", nil, fmt.Errorf("OpenAI Responses message %d: %w", i, err)
				}
				parts = append(parts, part)
			}
			flushParts()

		case LLMMessageRoleAssistant:
			// Emit blocks in their stored order so a reasoning item precedes the
			// function call it produced, as the Responses API requires when the
			// reasoning chain is resubmitted (Store:false).
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
					// Resubmit a captured reasoning item ahead of its function call.
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
	return systemPrompt, items, nil
}

func codexContentPart(block *LLMContentBlock) (responses.ResponseInputContentUnionParam, error) {
	switch block.Kind {
	case LLMContentText:
		return responses.ResponseInputContentUnionParam{
			OfInputText: &responses.ResponseInputTextParam{Text: block.Text},
		}, nil
	case LLMContentImage:
		if err := validateOpenAIImage(block); err != nil {
			return responses.ResponseInputContentUnionParam{}, err
		}
		return responses.ResponseInputContentUnionParam{
			OfInputImage: &responses.ResponseInputImageParam{
				ImageURL: param.NewOpt(openAIMediaDataURL(block)), Detail: responses.ResponseInputImageDetailAuto,
			},
		}, nil
	case LLMContentDocument:
		if block.MIMEType != "application/pdf" {
			return responses.ResponseInputContentUnionParam{}, fmt.Errorf("unsupported OpenAI Responses document MIME type %q (expected PDF)", block.MIMEType)
		}
		return responses.ResponseInputContentUnionParam{
			OfInputFile: &responses.ResponseInputFileParam{
				FileData: param.NewOpt(openAIMediaDataURL(block)), Filename: param.NewOpt("document.pdf"),
			},
		}, nil
	case LLMContentAudio:
		return responses.ResponseInputContentUnionParam{}, fmt.Errorf("audio input is unsupported by the OpenAI Responses API; use an audio-capable chat-completions model")
	default:
		return responses.ResponseInputContentUnionParam{}, fmt.Errorf("unsupported OpenAI Responses content kind %q", block.Kind)
	}
}

func codexToolOutput(block *LLMContentBlock) (responses.ResponseInputItemFunctionCallOutputOutputUnionParam, error) {
	output := responses.ResponseInputItemFunctionCallOutputOutputUnionParam{}
	if len(block.Content) == 0 {
		text := block.Text
		if block.Errored {
			text = "error: " + text
		}
		output.OfString = param.NewOpt(text)
		return output, nil
	}
	// Responses supports structured function outputs directly; unlike chat,
	// no synthetic user message or media-to-text projection is necessary.
	var parts responses.ResponseFunctionCallOutputItemListParam
	text := block.Text
	if block.Errored {
		text = "error: " + text
	}
	if text != "" {
		parts = append(parts, responses.ResponseFunctionCallOutputItemUnionParam{
			OfInputText: &responses.ResponseInputTextContentParam{Text: text},
		})
	}
	for _, child := range block.Content {
		part, err := codexContentPart(child)
		if err != nil {
			return output, err
		}
		switch {
		case part.OfInputText != nil:
			parts = append(parts, responses.ResponseFunctionCallOutputItemUnionParam{
				OfInputText: &responses.ResponseInputTextContentParam{Text: part.OfInputText.Text},
			})
		case part.OfInputImage != nil:
			parts = append(parts, responses.ResponseFunctionCallOutputItemUnionParam{
				OfInputImage: &responses.ResponseInputImageContentParam{
					ImageURL: part.OfInputImage.ImageURL, Detail: responses.ResponseInputImageContentDetailAuto,
				},
			})
		case part.OfInputFile != nil:
			parts = append(parts, responses.ResponseFunctionCallOutputItemUnionParam{
				OfInputFile: &responses.ResponseInputFileContentParam{
					FileData: part.OfInputFile.FileData, Filename: part.OfInputFile.Filename,
				},
			})
		}
	}
	output.OfResponseFunctionCallOutputItemArray = parts
	return output, nil
}

// codexReasoning is the opaque data stashed in a THINKING block's Signature so a
// Responses API reasoning item can be round-tripped across turns. Codex requests
// use Store:false, so the server keeps no state and the reasoning item's id +
// encrypted content must be carried in the history and resubmitted verbatim.
type codexReasoning struct {
	ID               string   `json:"id"`
	EncryptedContent string   `json:"encrypted_content,omitempty"`
	Summary          []string `json:"summary,omitempty"`
}

// encodeCodexReasoning converts a streamed reasoning item into a human-readable
// summary (for display) and an opaque signature (for resubmission).
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
// encrypted content required to resubmit it under Store:false.
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
		return &codexError{statusCode: aerr.StatusCode, message: msg, err: err}
	}
	return err
}

// codexError carries the backend's own explanation as the message while
// keeping the SDK error reachable underneath, so status-based classification
// (isAuthFailure) still works on it.
type codexError struct {
	statusCode int
	message    string
	err        error
}

func (e *codexError) Error() string {
	return fmt.Sprintf("codex API error (HTTP %d): %s", e.statusCode, e.message)
}

func (e *codexError) Unwrap() error { return e.err }

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
