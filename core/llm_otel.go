package core

import (
	"bytes"
	"fmt"
	"io"
	"net/http"

	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/trace"
)

// maxBodyCapture is the maximum number of bytes to capture from request/response bodies.
const maxBodyCapture = 256 * 1024 // 256 KiB

// llmOTelHTTPClient wraps an http.Client with the OTel tracing transport.
// This satisfies the HTTPClient interface used by the Anthropic, OpenAI,
// and Google SDKs (which expect a Do(*http.Request) method).
func newLLMOTelHTTPClient(provider string) *http.Client {
	return &http.Client{
		Transport: newLLMOTelTransport(nil, provider),
	}
}

// llmOTelTransport is an http.RoundTripper that creates a child OTel span
// for each LLM HTTP request, logging request and response bodies to span
// stdio so they appear in the TUI and traces.
type llmOTelTransport struct {
	base     http.RoundTripper
	provider string
}

func newLLMOTelTransport(base http.RoundTripper, provider string) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &llmOTelTransport{base: base, provider: provider}
}

func (t *llmOTelTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()

	// Only trace if there's an active recording span in the context.
	parent := trace.SpanFromContext(ctx)
	if !parent.IsRecording() {
		return t.base.RoundTrip(req)
	}

	spanName := fmt.Sprintf("LLM HTTP %s %s", req.Method, req.URL.Path)
	ctx, span := Tracer(ctx).Start(ctx, spanName,
		telemetry.Encapsulate(),
		trace.WithAttributes(
			attribute.String("llm.provider", t.provider),
			attribute.String("http.method", req.Method),
			attribute.String("http.url", req.URL.String()),
		),
	)
	defer span.End()

	// Use the new context so the child span is wired up.
	req = req.WithContext(ctx)

	stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary,
		log.String("llm.provider", t.provider),
	)
	defer stdio.Close()

	// Capture and log request body.
	if req.Body != nil && req.Body != http.NoBody {
		captured, fullBody, err := captureBody(req.Body)
		if err != nil {
			span.RecordError(err)
			return nil, err
		}
		req.Body = io.NopCloser(bytes.NewReader(fullBody))
		req.ContentLength = int64(len(fullBody))

		fmt.Fprintf(stdio.Stdout, ">>> %s %s\n%s\n", req.Method, req.URL.Path, captured)
	}

	resp, err := t.base.RoundTrip(req)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		fmt.Fprintf(stdio.Stderr, "<<< error: %s\n", err)
		return nil, err
	}

	span.SetAttributes(attribute.Int("http.status_code", resp.StatusCode))

	// For non-streaming responses, capture and log the body.
	// SSE streams are consumed incrementally by the SDK so we can't buffer them.
	ct := resp.Header.Get("Content-Type")
	isStreaming := ct == "text/event-stream" || ct == "text/event-stream; charset=utf-8"
	if !isStreaming && resp.Body != nil {
		captured, fullBody, readErr := captureBody(resp.Body)
		if readErr == nil {
			resp.Body = io.NopCloser(bytes.NewReader(fullBody))
			fmt.Fprintf(stdio.Stdout, "<<< %d\n%s\n", resp.StatusCode, captured)
		}
	} else {
		fmt.Fprintf(stdio.Stdout, "<<< %d (streaming)\n", resp.StatusCode)
	}

	return resp, nil
}

// captureBody reads up to maxBodyCapture bytes from r, returning both the
// displayable captured portion and the full body bytes (for restoring).
func captureBody(r io.ReadCloser) (captured string, full []byte, err error) {
	full, err = io.ReadAll(r)
	r.Close()
	if err != nil {
		return "", nil, err
	}
	if len(full) <= maxBodyCapture {
		return string(full), full, nil
	}
	return string(full[:maxBodyCapture]) + "\n... (truncated)", full, nil
}
