package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"strings"

	"github.com/dagger/dagger/engine/slog"
	telemetry "github.com/dagger/otel-go"
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
	// Build a map from CallID to ToolName so that tool result blocks (which
	// only carry CallID) can recover the function name that Google's
	// FunctionResponse.Name requires.
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
			// Google's API requires all FunctionResponse parts for a batch of
			// parallel tool calls to be in a single Content. Dagger stores each
			// tool result as a separate user message, so we merge consecutive
			// tool-result-only user messages into one Content.
			if isToolResultOnly(msg) && len(genaiHistory) > 0 {
				prev := genaiHistory[len(genaiHistory)-1]
				if prev.Role == "user" && len(prev.Parts) > 0 && prev.Parts[len(prev.Parts)-1].FunctionResponse != nil {
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
					// Fallback: CallID may already be a function name.
					toolName = block.CallID
				}
				content.Parts = append(content.Parts, &genai.Part{
					FunctionResponse: &genai.FunctionResponse{
						Name: toolName,
						// Genai expects a json format response
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
				if len(block.Arguments) > 0 {
					if err := json.Unmarshal(block.Arguments.Bytes(), &args); err != nil {
						return nil, nil, err
					}
				}
				part := &genai.Part{
					FunctionCall: &genai.FunctionCall{
						Name: block.ToolName,
						Args: args,
					},
				}
				// Gemini attaches a thought signature to the function-call part
				// when reasoning; it must be replayed so the model can continue
				// the reasoning chain across tool calls.
				if sig := decodeThoughtSignature(block.Signature); sig != nil {
					part.ThoughtSignature = sig
				}
				content.Parts = append(content.Parts, part)
			case LLMContentThinking:
				// Round-trip thinking: replay the thought summary and its opaque
				// signature so Gemini can resume the reasoning it started.
				content.Parts = append(content.Parts, &genai.Part{
					Text:             block.Text,
					Thought:          true,
					ThoughtSignature: decodeThoughtSignature(block.Signature),
				})
			}
		}

		if content.Role == "system" {
			continue
		}
		// Only append if this is a new Content (not merged into an existing one).
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
	stdout io.Writer,
	onTokenUsage func(*genai.GenerateContentResponseUsageMetadata) LLMTokenUsage,
) (contentBlocks []*LLMContentBlock, tokenUsage LLMTokenUsage, err error) {
	var textContent strings.Builder
	var thinkingContent strings.Builder
	var thinkingSig string
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
			return contentBlocks, tokenUsage, &ModelFinishedError{Reason: "no response from model"}
		}
		candidate := res.Candidates[0]
		if candidate.Content == nil {
			return contentBlocks, tokenUsage, &ModelFinishedError{Reason: string(candidate.FinishReason)}
		}

		for _, part := range candidate.Content.Parts {
			sig := ""
			if len(part.ThoughtSignature) > 0 {
				sig = base64.StdEncoding.EncodeToString(part.ThoughtSignature)
			}
			switch {
			case part.Thought:
				// Thought summary text. Accumulate it (and its signature)
				// separately from the reply so it round-trips but doesn't
				// pollute the visible response. Checked before part.Text
				// because thought parts carry text too.
				thinkingContent.WriteString(part.Text)
				if sig != "" {
					thinkingSig = sig
				}
			case part.FunctionCall != nil:
				x := part.FunctionCall
				args, err := json.Marshal(x.Args)
				if err != nil {
					return contentBlocks, tokenUsage, fmt.Errorf("failed to marshal tool call arguments: %w", err)
				}
				contentBlocks = append(contentBlocks, &LLMContentBlock{
					Kind:      LLMContentToolCall,
					CallID:    x.Name,
					ToolName:  x.Name,
					Arguments: JSON(args),
					Signature: sig,
				})
			case part.Text != "":
				fmt.Fprint(stdout, part.Text)
				textContent.WriteString(part.Text)
			default:
				slog.Warn("ignoring unhandled genai part", "part", fmt.Sprintf("%+v", part), "content", fmt.Sprintf("%+v", candidate.Content))
			}
		}
	}
	// Prepend the accumulated text as a single TEXT block, ahead of tool calls.
	if textContent.Len() > 0 {
		contentBlocks = append([]*LLMContentBlock{{
			Kind: LLMContentText,
			Text: textContent.String(),
		}}, contentBlocks...)
	}
	// Prepend accumulated thinking ahead of everything else, matching the order
	// the model produced it (thinking precedes the reply and tool calls).
	if thinkingContent.Len() > 0 || thinkingSig != "" {
		contentBlocks = append([]*LLMContentBlock{{
			Kind:      LLMContentThinking,
			Text:      thinkingContent.String(),
			Signature: thinkingSig,
		}}, contentBlocks...)
	}
	return contentBlocks, tokenUsage, nil
}

// decodeThoughtSignature turns a base64-encoded thought signature (as stored on
// an LLMContentBlock) back into the raw bytes Gemini expects. It returns nil for
// an empty or malformed signature, so the part is sent without one.
func decodeThoughtSignature(signature string) []byte {
	if signature == "" {
		return nil
	}
	sig, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return nil
	}
	return sig
}

// thinkingConfig maps the endpoint's ThinkingMode/ThinkingBudget onto Gemini's
// thinking configuration. It returns nil (thinking disabled) unless a mode is
// set. When enabled, thought summaries are requested so they can be captured
// and round-tripped; a positive budget caps the thinking tokens (otherwise the
// model thinks dynamically).
func (c *GenaiClient) thinkingConfig() *genai.ThinkingConfig {
	switch c.endpoint.ThinkingMode {
	case "", "disabled", "none":
		return nil
	}
	cfg := &genai.ThinkingConfig{IncludeThoughts: true}
	if c.endpoint.ThinkingBudget > 0 {
		budget := int32(c.endpoint.ThinkingBudget)
		cfg.ThinkingBudget = &budget
	}
	return cfg
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
	genaiTools, err := c.convertToolsToGenai(tools)
	if err != nil {
		return nil, fmt.Errorf("failed to convert tools: %w", err)
	}

	chat, err := c.client.Chats.Create(ctx, c.endpoint.Model, &genai.GenerateContentConfig{
		SystemInstruction: systemInstruction,
		Tools:             genaiTools,
		ThinkingConfig:    c.thinkingConfig(),
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

	contentBlocks, tokenUsage, err := c.processStreamResponse(
		stream,
		stdio.Stdout,
		tokenHandler,
	)
	if err != nil {
		return nil, err
	}

	return &LLMResponse{
		Content:    contentBlocks,
		TokenUsage: tokenUsage,
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
