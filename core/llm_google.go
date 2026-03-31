package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"strings"

	"github.com/dagger/dagger/engine/slog"
	telemetry "github.com/dagger/otel-go"
	"github.com/googleapis/gax-go/v2/apierror"
	"go.opentelemetry.io/otel/attribute"
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
		APIKey:     endpoint.Key,
		HTTPClient: newLLMOTelHTTPClient("google"),
	})
	if err != nil {
		return nil, err
	}
	return &GenaiClient{
		client:   client,
		endpoint: endpoint,
	}, err
}

func (c *GenaiClient) convertToolsToGenai(tools []LLMTool) ([]*genai.Tool, error) {
	// Gemini expects non empty FunctionDeclarations when tools are provided
	if len(tools) == 0 {
		return nil, nil
	}

	fns := []*genai.FunctionDeclaration{}
	for _, tool := range tools {
		fd := &genai.FunctionDeclaration{
			Name:        tool.Name,
			Description: tool.Description,
		}
		schema, err := bbiSchemaToGenaiSchema(tool.Schema)
		if err != nil {
			return nil, fmt.Errorf("failed to convert schema for tool %s: %w", tool.Name, err)
		}
		// only add Parameters if there are any
		if len(schema.Properties) > 0 {
			fd.Parameters = schema
		}
		fns = append(fns, fd)
	}

	return []*genai.Tool{
		{FunctionDeclarations: fns},
	}, nil
}

func (c *GenaiClient) prepareGenaiHistory(history []*LLMMessage) (genaiHistory []*genai.Content, systemInstruction *genai.Content, err error) {
	// Build a map from CallID to ToolName so that tool result blocks
	// (which only carry CallID) can recover the function name that
	// Google's FunctionResponse.Name requires.
	callIDToToolName := map[string]string{}
	for _, msg := range history {
		for _, block := range msg.Content {
			if block.Kind == LLMContentToolCall && block.CallID != "" {
				callIDToToolName[block.CallID] = block.ToolName
			}
		}
	}

	systemInstruction = &genai.Content{
		Parts: []*genai.Part{},
		Role:  "system",
	}
	// isToolResultOnly reports whether every block in a message is a
	// TOOL_RESULT block.
	isToolResultOnly := func(msg *LLMMessage) bool {
		if len(msg.Content) == 0 {
			return false
		}
		for _, b := range msg.Content {
			if b.Kind != LLMContentToolResult {
				return false
			}
		}
		return true
	}

	for _, msg := range history {
		var content *genai.Content
		switch msg.Role {
		case LLMMessageRoleSystem:
			content = systemInstruction
		case LLMMessageRoleUser:
			// Google's API requires all FunctionResponse parts for a
			// batch of parallel tool calls to be in a single Content.
			// Dagger stores each tool result as a separate user message,
			// so we merge consecutive tool-result-only user messages
			// into one Content.
			if isToolResultOnly(msg) && len(genaiHistory) > 0 {
				prev := genaiHistory[len(genaiHistory)-1]
				if prev.Role == "user" && len(prev.Parts) > 0 && prev.Parts[len(prev.Parts)-1].FunctionResponse != nil {
					// Merge into the previous user Content.
					content = prev
				}
			}
			if content == nil {
				content = &genai.Content{
					Parts: []*genai.Part{},
					Role:  "user",
				}
			}
		case LLMMessageRoleAssistant:
			content = &genai.Content{
				Parts: []*genai.Part{},
				Role:  "model",
			}
		default:
			return nil, nil, fmt.Errorf("unexpected role %s", msg.Role)
		}

		for _, block := range msg.Content {
			switch block.Kind {
			case LLMContentToolResult:
				toolName := callIDToToolName[block.CallID]
				if toolName == "" {
					// Fallback: CallID may already be a function name
					// (e.g. from older history formats).
					toolName = block.CallID
				}
				content.Parts = append(content.Parts, &genai.Part{
					FunctionResponse: &genai.FunctionResponse{
						Name: toolName,
						Response: map[string]any{
							"response": block.Text,
							"error":    block.Errored,
						},
					},
				})
			case LLMContentText:
				text := block.Text
				if text == "" {
					text = " "
				}
				content.Parts = append(content.Parts, &genai.Part{Text: text})
			case LLMContentToolCall:
				var args map[string]any
				if err := json.Unmarshal(block.Arguments.Bytes(), &args); err != nil {
					return nil, nil, err
				}
				part := &genai.Part{
					FunctionCall: &genai.FunctionCall{
						Name: block.ToolName,
						Args: args,
					},
				}
				// Restore thought signature if present (required by Gemini
				// thinking models to associate tool calls with thoughts).
				if block.Signature != "" {
					sig, err := base64.StdEncoding.DecodeString(block.Signature)
					if err != nil {
						return nil, nil, fmt.Errorf("failed to decode thought signature: %w", err)
					}
					part.ThoughtSignature = sig
				}
				content.Parts = append(content.Parts, part)
			case LLMContentThinking:
				part := &genai.Part{
					Text:    block.Text,
					Thought: true,
				}
				if block.Signature != "" {
					sig, err := base64.StdEncoding.DecodeString(block.Signature)
					if err != nil {
						return nil, nil, fmt.Errorf("failed to decode thought signature: %w", err)
					}
					part.ThoughtSignature = sig
				}
				content.Parts = append(content.Parts, part)
			}
		}

		// Only append to genaiHistory if this is a new Content (not
		// merged into an existing one).
		if content.Role == "system" {
			continue
		}
		if len(genaiHistory) == 0 || genaiHistory[len(genaiHistory)-1] != content {
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
	dp *displayPhases,
	onTokenUsage func(*genai.GenerateContentResponseUsageMetadata) LLMTokenUsage,
) (contentBlocks []*LLMContentBlock, tokenUsage LLMTokenUsage, err error) {
	var textContent strings.Builder
	var thinkingContent strings.Builder
	var thinkingSignature string // base64-encoded thought signature for the thinking block

	// Phase index counter. Index 0 is reserved for thinking (if present),
	// index 1 for text. Tool calls start at 2.
	var toolIdx int64 = 1

	// Counter for generating unique call IDs (Google's API doesn't provide them).
	var callCounter int

	// Track whether we've started a thinking phase so we can close it
	// when the first non-thinking part arrives.
	var inThinking bool

	for res, err := range stream {
		if err != nil {
			if apiErr, ok := err.(*apierror.APIError); ok {
				err = fmt.Errorf("google API error occurred: %w", apiErr.Unwrap())
				return contentBlocks, tokenUsage, err
			}

			return contentBlocks, tokenUsage, err
		}

		if res.UsageMetadata != nil {
			tokenUsage = onTokenUsage(res.UsageMetadata)
		}

		if len(res.Candidates) == 0 {
			err = &ModelFinishedError{Reason: "no response from model"}
			return contentBlocks, tokenUsage, err
		}
		candidate := res.Candidates[0]
		if candidate.Content == nil {
			err = &ModelFinishedError{Reason: string(candidate.FinishReason)}
			return contentBlocks, tokenUsage, err
		}

		for _, part := range candidate.Content.Parts {
			if part.Thought && part.Text != "" {
				// Thinking/reasoning content from the model.
				if !inThinking {
					dp.StartThinking(0)
					inThinking = true
				}
				if p := dp.Phase(0); p != nil {
					fmt.Fprint(p.Stdio.Stdout, part.Text)
				}
				thinkingContent.WriteString(part.Text)
				// Capture thought signature (typically on the last thinking chunk).
				if len(part.ThoughtSignature) > 0 {
					thinkingSignature = base64.StdEncoding.EncodeToString(part.ThoughtSignature)
				}
			} else if x := part.Text; x != "" {
				// Regular text content. Close thinking phase if open.
				if inThinking {
					dp.Close(0)
					inThinking = false
				}
				p := dp.Phase(1)
				if p == nil {
					p = dp.StartText(1)
				}
				fmt.Fprint(p.MarkdownW, x)
				textContent.WriteString(x)
			} else if x := part.FunctionCall; x != nil {
				// Close thinking phase if still open.
				if inThinking {
					dp.Close(0)
					inThinking = false
				}
				bytes, err := json.Marshal(x.Args)
				if err != nil {
					return contentBlocks, tokenUsage, err
				}
				callCounter++
				callID := fmt.Sprintf("google-%s-%d", x.Name, callCounter)
				block := &LLMContentBlock{
					Kind:      LLMContentToolCall,
					CallID:    callID,
					ToolName:  x.Name,
					Arguments: JSON(bytes),
				}
				// Preserve thought signature so it can be sent back in
				// subsequent requests (required by Gemini thinking models).
				if len(part.ThoughtSignature) > 0 {
					block.Signature = base64.StdEncoding.EncodeToString(part.ThoughtSignature)
				}
				contentBlocks = append(contentBlocks, block)

				toolIdx++
				p := dp.StartToolCall(toolIdx, callID, x.Name)
				fmt.Fprint(p.Stdio.Stdout, string(bytes))
				dp.Close(toolIdx)
			} else {
				slog.Warn("ignoring unhandled genai part", "part", fmt.Sprintf("%+v", part), "content", fmt.Sprintf("%+v", candidate.Content))
			}
		}
	}

	// Build content blocks: thinking first, then text, then tool calls.
	var result []*LLMContentBlock
	if thinkingContent.Len() > 0 {
		result = append(result, &LLMContentBlock{
			Kind:      LLMContentThinking,
			Text:      thinkingContent.String(),
			Signature: thinkingSignature,
		})
	}
	if textContent.Len() > 0 {
		result = append(result, &LLMContentBlock{
			Kind: LLMContentText,
			Text: textContent.String(),
		})
	}
	contentBlocks = append(result, contentBlocks...)
	return contentBlocks, tokenUsage, nil
}

// buildThinkingConfig returns a ThinkingConfig based on the endpoint settings,
// or nil if thinking is not configured.
func (c *GenaiClient) buildThinkingConfig() *genai.ThinkingConfig {
	mode := c.endpoint.ThinkingMode
	if mode == "" {
		return nil
	}
	tc := &genai.ThinkingConfig{
		IncludeThoughts: true,
	}
	switch mode {
	case "disabled":
		return nil
	case "low":
		tc.ThinkingLevel = genai.ThinkingLevelLow
	case "medium":
		tc.ThinkingLevel = genai.ThinkingLevelMedium
	case "high":
		tc.ThinkingLevel = genai.ThinkingLevelHigh
	case "minimal":
		tc.ThinkingLevel = genai.ThinkingLevelMinimal
	default:
		// "adaptive" or unrecognized — just enable with IncludeThoughts
	}
	return tc
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

func (c *GenaiClient) SendQuery(ctx context.Context, history []*LLMMessage, tools []LLMTool, opts *LLMCallOpts) (_ *LLMResponse, rerr error) {
	parentCtx := ctx

	dp := newDisplayPhases(parentCtx)
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
	genaiTools, err := c.convertToolsToGenai(tools)
	if err != nil {
		return nil, fmt.Errorf("failed to convert tools: %w", err)
	}

	genaiConfig := &genai.GenerateContentConfig{
		SystemInstruction: systemInstruction,
		Tools:             genaiTools,
	}
	if opts != nil && opts.MaxTokens > 0 {
		genaiConfig.MaxOutputTokens = int32(opts.MaxTokens)
	}

	// Configure thinking/reasoning if requested
	if tc := c.buildThinkingConfig(); tc != nil {
		genaiConfig.ThinkingConfig = tc
	}

	chat, err := c.client.Chats.Create(ctx, c.endpoint.Model, genaiConfig, chatHistoryForGenai)
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

	contentBlocks, tokenUsage, err := c.processStreamResponse(
		stream,
		dp,
		tokenHandler,
	)
	if err != nil {
		return nil, err
	}

	// Close all phases so their spans are collected before building the response.
	dp.CloseAll()

	displaySpans, toolCalls := dp.Response()
	return &LLMResponse{
		Content:          contentBlocks,
		TokenUsage:       tokenUsage,
		DisplaySpans:     displaySpans,
		ToolCallDisplays: toolCalls,
	}, nil
}

// TODO: this definitely needs a unit test
func bbiSchemaToGenaiSchema(bbi map[string]any) (*genai.Schema, error) {
	schema := &genai.Schema{}
	for key, param := range bbi {
		switch key {
		case "description":
			if schema.Description != "" {
				schema.Description += " "
			}
			desc, ok := param.(string)
			if !ok {
				return nil, fmt.Errorf("description must be a string, got %T", param)
			}
			schema.Description += desc
		case "properties":
			schema.Properties = map[string]*genai.Schema{}
			props, ok := param.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("properties must be a map, got %T", param)
			}
			for propKey, propParam := range props {
				propParamMap, ok := propParam.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("property %s must be a map, got %T", propKey, propParam)
				}
				propSchema, err := bbiSchemaToGenaiSchema(propParamMap)
				if err != nil {
					return nil, fmt.Errorf("failed to convert property %s: %w", propKey, err)
				}
				schema.Properties[propKey] = propSchema
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
			formatStr, ok := param.(string)
			if !ok {
				return nil, fmt.Errorf("format must be a string, got %T", param)
			}
			switch formatStr {
			case "enum", "date-time":
				// Genai only supports these format values. :(
				schema.Format = formatStr
			case "uri":
				if pattern, ok := bbi["pattern"].(string); ok {
					schema.Description = fmt.Sprintf("URI format, %s.", pattern)
				} else {
					schema.Description = "URI format."
				}
			}
		case "items":
			itemsMap, ok := param.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("items must be a map, got %T", param)
			}
			itemsSchema, err := bbiSchemaToGenaiSchema(itemsMap)
			if err != nil {
				return nil, fmt.Errorf("failed to convert items schema: %w", err)
			}
			schema.Items = itemsSchema
		case "required":
			required, err := toStringSlice(bbi["required"])
			if err != nil {
				return nil, fmt.Errorf("failed to convert required field: %w", err)
			}
			schema.Required = required
		case "enum":
			enum, err := toStringSlice(param)
			if err != nil {
				return nil, fmt.Errorf("failed to convert enum field: %w", err)
			}
			schema.Enum = enum
			schema.Format = "enum"
		}
	}

	return schema, nil
}

func toStringSlice(val any) ([]string, error) {
	var res []string
	switch x := val.(type) {
	case []any:
		for _, v := range x {
			switch y := v.(type) {
			case string:
				res = append(res, y)
			default:
				return nil, fmt.Errorf("array element must be string, got %T", y)
			}
		}
	case []string:
		res = x
	default:
		return nil, fmt.Errorf("value must be []string or []any, got %T", x)
	}
	return res, nil
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
