package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// llmUsageTransport observes SDK-visible RoundTrips, including SDK retries.
// It never changes the request context: debug spans, gauges and workload
// accounting retain their existing parentage. The metadata-only usage span is
// a sibling, not a new agent or conversation span.
type llmUsageTransport struct {
	base            http.RoundTripper
	provider, model string
}

func (t *llmUsageTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	protocol := llmUsageProtocol(req)
	if protocol == "" {
		return t.base.RoundTrip(req)
	}
	attrs := []attribute.KeyValue{
		attribute.String(telemetryattrs.LLMUsageSchemaAttr, telemetryattrs.LLMUsageSchemaV1),
		attribute.String(telemetryattrs.LLMUsageScopeAttr, "http_round_trip"),
		attribute.String(telemetryattrs.GenAIProviderNameAttr, t.provider),
		attribute.String(telemetryattrs.GenAIRequestModelAttr, t.model),
		attribute.String(telemetryattrs.GenAIOperationNameAttr, "chat"),
	}
	attrs = append(attrs, genAIAgentAttrsFromContext(req.Context())...)
	_, span := Tracer(req.Context()).Start(req.Context(), telemetryattrs.LLMUsageSpan,
		telemetry.Internal(), trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(attrs...))
	if !span.IsRecording() {
		span.End()
		return t.base.RoundTrip(req)
	}
	p := &llmUsageBody{ctx: req.Context(), span: span, protocol: protocol, values: map[string]int64{}}
	input, output := "includes_cache_read", "includes_reasoning"
	switch protocol {
	case "anthropic":
		input, output = "excludes_cache_read_and_write", "includes_reasoning"
	case "google":
		input, output = "includes_cache_read", "excludes_reasoning"
	}
	span.SetAttributes(attribute.String(telemetryattrs.LLMUsageInputSemanticsAttr, input),
		attribute.String(telemetryattrs.LLMUsageOutputSemanticsAttr, output))
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		p.finish(err, false)
		return resp, err
	}
	p.failed = resp.StatusCode >= 400
	p.streaming = strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream")
	// OpenAI-compatible servers sometimes mislabel SSE as application/json.
	// Detect the framing from bytes, not the response content type alone.
	p.body = resp.Body
	if resp.Body == nil {
		p.finish(nil, true)
	} else {
		resp.Body = p
	}
	return resp, nil
}

func llmUsageProtocol(req *http.Request) string {
	if req.Method != http.MethodPost {
		return ""
	}
	path := strings.TrimRight(req.URL.Path, "/")
	switch {
	case strings.HasSuffix(path, "/messages"):
		return "anthropic"
	case strings.HasSuffix(path, "/chat/completions"):
		return "openai"
	case strings.HasSuffix(path, "/responses"):
		return "responses"
	case strings.Contains(path, "/models/") && (strings.HasSuffix(path, ":streamGenerateContent") || strings.HasSuffix(path, ":generateContent")):
		return "google"
	default:
		return ""
	}
}

// Bound each JSON document/SSE event, not the stream. Oversized events are
// skipped until the next blank line, so later usage is still observable.
const maxLLMUsageFrame = 1024 * 1024

type llmUsageBody struct {
	body                                               io.ReadCloser
	ctx                                                context.Context
	span                                               trace.Span
	protocol                                           string
	mu                                                 sync.Mutex // Close may interrupt a concurrent Read.
	ended, streaming, detected, skipLine, skipEvent    bool
	terminal, finalUsage, googleFinished, failed, seen bool
	line, event, document                              []byte
	values                                             map[string]int64
	observed                                           map[string]bool
	array, arrayClosed, quoted, escaped                bool
	depth                                              int
}

func (p *llmUsageBody) Read(b []byte) (int, error) {
	n, err := p.body.Read(b)
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.ended {
		p.observe(b[:n])
		if err != nil {
			p.finishLocked(err, errors.Is(err, io.EOF))
		}
	}
	return n, err
}

func (p *llmUsageBody) Close() error {
	err := p.body.Close()
	p.finish(err, false)
	return err
}

func (p *llmUsageBody) finish(err error, eof bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.finishLocked(err, eof)
}

func (p *llmUsageBody) finishLocked(err error, eof bool) {
	if p.ended {
		return
	}
	p.ended = true
	if !p.streaming && !p.array && !p.skipEvent {
		p.parse(p.document, true)
	}
	// An unterminated frame cannot certify that the last snapshot was final.
	if p.streaming && (len(bytes.TrimSpace(p.line)) > 0 || len(p.event) > 0 || p.skipEvent) {
		p.finalUsage = false
	}
	if p.array && !p.arrayClosed {
		p.finalUsage = false
	}
	if eof && p.protocol == "google" && p.googleFinished && (!p.array || p.arrayClosed) {
		p.terminal = true
	}
	completeness := "unavailable"
	if len(p.values) > 0 {
		completeness = "partial"
		if p.terminal && p.finalUsage {
			completeness = "final"
		}
	}
	outcome := "interrupted"
	switch {
	case p.ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
		outcome = "cancelled"
	case p.failed || (err != nil && !errors.Is(err, io.EOF)):
		outcome = "error"
	case p.terminal:
		outcome = "success"
	}
	attrs := []attribute.KeyValue{
		attribute.String(telemetryattrs.LLMUsageCompletenessAttr, completeness),
		attribute.String(telemetryattrs.LLMUsageOutcomeAttr, outcome),
	}
	for key, value := range p.values {
		attrs = append(attrs, attribute.Int64(key, value))
	}
	p.span.SetAttributes(attrs...)
	if outcome != "success" {
		p.span.SetStatus(codes.Error, outcome)
	}
	p.span.End()
	p.line, p.event, p.document = nil, nil, nil
}

func (p *llmUsageBody) observe(b []byte) {
	for _, c := range b {
		if !p.detected {
			if c == ' ' || c == '\r' || c == '\n' || c == '\t' {
				continue
			}
			p.detected = true
			if c == '[' && p.protocol == "google" {
				p.array, p.streaming = true, false
				continue
			}
			if c != '{' && c != '[' {
				p.streaming = true
			}
		}
		if p.array {
			p.observeArray(c)
			continue
		}
		if !p.streaming {
			if len(p.document) < maxLLMUsageFrame {
				p.document = append(p.document, c)
			} else {
				p.finalUsage = false
				p.skipEvent = true
			}
			continue
		}
		if c != '\n' {
			if !p.skipLine {
				if len(p.line) < maxLLMUsageFrame {
					p.line = append(p.line, c)
				} else {
					p.finalUsage = false
					p.skipLine, p.skipEvent = true, true
					p.line = nil
				}
			}
			continue
		}
		line := bytes.TrimSuffix(p.line, []byte{'\r'})
		if !p.skipLine && len(line) == 0 {
			if !p.skipEvent {
				p.parse(p.event, false)
			}
			p.event = p.event[:0]
			p.skipEvent = false
		} else if !p.skipLine && !p.skipEvent && bytes.HasPrefix(line, []byte("data:")) {
			data := bytes.TrimPrefix(line[5:], []byte{' '})
			if len(p.event)+len(data)+1 <= maxLLMUsageFrame {
				p.event = append(p.event, data...)
				p.event = append(p.event, '\n')
			} else {
				p.finalUsage = false
				p.skipEvent = true
				p.event = nil
			}
		}
		p.line = p.line[:0]
		p.skipLine = false
	}
}

// Google's REST stream can also be a JSON array. Frame objects without
// retaining intervening content; string escapes and nested objects/arrays must
// not be mistaken for top-level delimiters. Invalid objects are observational
// failures only, and never affect the SDK's decoder.
func (p *llmUsageBody) observeArray(c byte) {
	if p.depth == 0 {
		if c == ']' {
			p.arrayClosed = true
			return
		}
		if c != '{' {
			return
		}
	}
	if len(p.document) < maxLLMUsageFrame {
		p.document = append(p.document, c)
	} else {
		p.skipEvent = true
		p.finalUsage = false
	}
	if p.quoted {
		if p.escaped {
			p.escaped = false
		} else if c == '\\' {
			p.escaped = true
		} else if c == '"' {
			p.quoted = false
		}
		return
	}
	switch c {
	case '"':
		p.quoted = true
	case '{', '[':
		p.depth++
	case '}', ']':
		p.depth--
	}
	if p.depth == 0 {
		if !p.skipEvent {
			p.parse(p.document, false)
		}
		p.document = p.document[:0]
		p.skipEvent = false
	}
}

func (p *llmUsageBody) parse(data []byte, nonstream bool) {
	p.seen = false
	p.observed = map[string]bool{}
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return
	}
	if p.protocol == "openai" && bytes.Equal(data, []byte("[DONE]")) {
		p.terminal = true
		return
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(data, &obj) != nil {
		p.finalUsage = false
		return
	}
	var kind string
	_ = json.Unmarshal(obj["type"], &kind)
	if kind == "error" || kind == "response.failed" || kind == "response.incomplete" {
		p.failed = true
	}
	if raw, ok := obj["error"]; ok && string(raw) != "null" {
		p.failed = true
	}
	switch p.protocol {
	case "anthropic":
		if kind == "message_start" {
			obj = llmUsageObject(obj["message"])
		}
		p.capture(obj["usage"], map[string]string{
			"input_tokens":                telemetryattrs.LLMUsageInputTokensAttr,
			"output_tokens":               telemetryattrs.LLMUsageOutputTokensAttr,
			"cache_read_input_tokens":     telemetryattrs.LLMUsageCacheReadTokensAttr,
			"cache_creation_input_tokens": telemetryattrs.LLMUsageCacheWriteTokensAttr,
		})
		if kind == "message_stop" {
			p.terminal = true
		}
		if kind == "message_delta" {
			p.finalUsage = p.observed[telemetryattrs.LLMUsageOutputTokensAttr]
		}
	case "openai":
		u := p.capture(obj["usage"], map[string]string{
			"prompt_tokens":     telemetryattrs.LLMUsageInputTokensAttr,
			"completion_tokens": telemetryattrs.LLMUsageOutputTokensAttr,
			"total_tokens":      telemetryattrs.LLMUsageTotalTokensAttr,
		})
		p.capture(u["prompt_tokens_details"], map[string]string{"cached_tokens": telemetryattrs.LLMUsageCacheReadTokensAttr})
		p.capture(u["completion_tokens_details"], map[string]string{"reasoning_tokens": telemetryattrs.LLMUsageReasoningTokensAttr})
		if p.seen {
			p.finalUsage = p.snapshotComplete()
		}
	case "responses":
		if response, ok := obj["response"]; ok {
			obj = llmUsageObject(response)
		}
		u := p.capture(obj["usage"], map[string]string{
			"input_tokens":  telemetryattrs.LLMUsageInputTokensAttr,
			"output_tokens": telemetryattrs.LLMUsageOutputTokensAttr,
			"total_tokens":  telemetryattrs.LLMUsageTotalTokensAttr,
		})
		p.capture(u["input_tokens_details"], map[string]string{"cached_tokens": telemetryattrs.LLMUsageCacheReadTokensAttr})
		p.capture(u["output_tokens_details"], map[string]string{"reasoning_tokens": telemetryattrs.LLMUsageReasoningTokensAttr})
		if kind == "response.completed" || kind == "response.failed" || kind == "response.incomplete" {
			p.terminal, p.finalUsage = true, p.snapshotComplete()
		}
	case "google":
		p.parseGoogle(obj)
	}
	if nonstream {
		p.terminal, p.finalUsage = true, p.snapshotComplete()
	}
}

func (p *llmUsageBody) parseGoogle(obj map[string]json.RawMessage) {
	p.capture(obj["usageMetadata"], map[string]string{
		"promptTokenCount":        telemetryattrs.LLMUsageInputTokensAttr,
		"candidatesTokenCount":    telemetryattrs.LLMUsageOutputTokensAttr,
		"cachedContentTokenCount": telemetryattrs.LLMUsageCacheReadTokensAttr,
		"thoughtsTokenCount":      telemetryattrs.LLMUsageReasoningTokensAttr,
		"totalTokenCount":         telemetryattrs.LLMUsageTotalTokensAttr,
	})
	var candidates []struct {
		FinishReason string `json:"finishReason"`
	}
	if json.Unmarshal(obj["candidates"], &candidates) == nil {
		for _, c := range candidates {
			if c.FinishReason != "" && c.FinishReason != "FINISH_REASON_UNSPECIFIED" {
				p.googleFinished = true
				if c.FinishReason != "STOP" {
					p.failed = true
				}
			}
		}
	}
	var feedback struct {
		BlockReason string `json:"blockReason"`
	}
	if json.Unmarshal(obj["promptFeedback"], &feedback) == nil && feedback.BlockReason != "" && feedback.BlockReason != "BLOCK_REASON_UNSPECIFIED" {
		p.failed, p.googleFinished = true, true
	}
	if p.googleFinished && p.seen {
		p.finalUsage = p.snapshotComplete()
	}
}

// Preserve earlier known categories on interruption, but do not promote them
// to final when the terminal snapshot omitted them. Anthropic is the exception:
// input/cache usage arrives at message_start, output at message_delta.
func (p *llmUsageBody) snapshotComplete() bool {
	if !p.seen {
		return false
	}
	for key := range p.values {
		if !p.observed[key] {
			return false
		}
	}
	return true
}

func llmUsageObject(raw json.RawMessage) map[string]json.RawMessage {
	var obj map[string]json.RawMessage
	_ = json.Unmarshal(raw, &obj)
	return obj
}

func (p *llmUsageBody) capture(raw json.RawMessage, fields map[string]string) map[string]json.RawMessage {
	obj := llmUsageObject(raw)
	for field, attr := range fields {
		value, ok := obj[field]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			continue
		}
		var n int64
		if json.Unmarshal(value, &n) == nil && n >= 0 {
			p.values[attr] = n
			p.observed[attr] = true
			p.seen = true
		}
	}
	return obj
}
