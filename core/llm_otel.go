package core

import (
	"bytes"
	"io"
	"net/http"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// maxBodyCapture is the maximum number of bytes to capture from request/response bodies
// in OTel spans. This prevents huge bodies from blowing up trace storage.
const maxBodyCapture = 64 * 1024 // 64 KiB

// llmOTelHTTPClient wraps an http.Client with the OTel tracing transport.
// This satisfies the HTTPClient interface used by both the Anthropic and
// OpenAI SDKs (which expect a Do(*http.Request) method).
func newLLMOTelHTTPClient(provider string) *http.Client {
	return &http.Client{
		Transport: newLLMOTelTransport(nil, provider),
	}
}

// llmOTelTransport is an http.RoundTripper that creates OTel spans for each
// LLM API request and records request/response bodies as span attributes.
// Headers and other sensitive metadata are intentionally NOT captured.
type llmOTelTransport struct {
	base     http.RoundTripper
	provider string // e.g. "anthropic", "openai", "openai-codex"
}

// newLLMOTelTransport wraps a base transport (or http.DefaultTransport if nil)
// with OpenTelemetry tracing that captures LLM HTTP bodies.
func newLLMOTelTransport(base http.RoundTripper, provider string) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &llmOTelTransport{base: base, provider: provider}
}

func (t *llmOTelTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	span := trace.SpanFromContext(ctx)

	// If there's no active span, just pass through
	if !span.IsRecording() {
		return t.base.RoundTrip(req)
	}

	// Capture request body
	if req.Body != nil && req.Body != http.NoBody {
		bodyBytes, err := io.ReadAll(io.LimitReader(req.Body, maxBodyCapture+1))
		if err != nil {
			return nil, err
		}
		// Restore the body for the actual request
		remaining, _ := io.ReadAll(req.Body)
		req.Body.Close()
		fullBody := append(bodyBytes, remaining...)
		req.Body = io.NopCloser(bytes.NewReader(fullBody))

		captured := bodyBytes
		truncated := false
		if len(captured) > maxBodyCapture {
			captured = captured[:maxBodyCapture]
			truncated = true
		}
		span.SetAttributes(
			attribute.String("llm.http.request.url", req.URL.Path),
			attribute.String("llm.http.request.method", req.Method),
			attribute.String("llm.http.request.body", string(captured)),
			attribute.Bool("llm.http.request.body.truncated", truncated),
			attribute.String("llm.http.provider", t.provider),
		)
	}

	resp, err := t.base.RoundTrip(req)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	span.SetAttributes(
		attribute.Int("llm.http.response.status", resp.StatusCode),
	)

	// For non-streaming responses, capture the body.
	// Streaming responses (SSE) will be too large; skip them.
	if resp.Header.Get("Content-Type") != "text/event-stream" && resp.Body != nil {
		bodyBytes, readErr := io.ReadAll(io.LimitReader(resp.Body, maxBodyCapture+1))
		if readErr != nil {
			// Don't fail the request, just skip body capture
			resp.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			return resp, nil
		}
		remaining, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		fullBody := append(bodyBytes, remaining...)
		resp.Body = io.NopCloser(bytes.NewReader(fullBody))

		captured := bodyBytes
		truncated := false
		if len(captured) > maxBodyCapture {
			captured = captured[:maxBodyCapture]
			truncated = true
		}
		span.SetAttributes(
			attribute.String("llm.http.response.body", string(captured)),
			attribute.Bool("llm.http.response.body.truncated", truncated),
		)
	}

	return resp, nil
}
