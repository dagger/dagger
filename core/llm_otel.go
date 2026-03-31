package core

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"

	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/trace"
)

// maxBodyCapture is the maximum number of bytes to capture from request/response bodies.
const maxBodyCapture = 256 * 1024 // 256 KiB

// llmOTelHTTPClient wraps an http.Client with the OTel tracing transport.
func newLLMOTelHTTPClient(provider string) *http.Client {
	return &http.Client{
		Transport: newLLMOTelTransport(nil, provider),
	}
}

// llmOTelTransport creates a child OTel span for each LLM HTTP request and
// logs request/response bodies to span stdio.
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

	req = req.WithContext(ctx)

	stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary,
		log.String("llm.provider", t.provider),
	)

	// Capture and log request body.
	if req.Body != nil && req.Body != http.NoBody {
		captured, fullBody, err := captureBody(req.Body)
		if err != nil {
			span.RecordError(err)
			span.End()
			stdio.Close()
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
		span.End()
		stdio.Close()
		return nil, err
	}

	span.SetAttributes(attribute.Int("http.status_code", resp.StatusCode))

	ct := resp.Header.Get("Content-Type")
	isStreaming := strings.HasPrefix(ct, "text/event-stream")

	if isStreaming && resp.Body != nil {
		// For streaming responses, tee the body through to span stdio as it's
		// read by the SDK. The span and stdio stay open until the body is closed.
		fmt.Fprintf(stdio.Stdout, "<<< %d (streaming)\n", resp.StatusCode)
		resp.Body = &teeReadCloser{
			reader: io.TeeReader(resp.Body, stdio.Stdout),
			closer: resp.Body,
			onClose: func() {
				span.End()
				stdio.Close()
			},
		}
	} else if resp.Body != nil {
		// Non-streaming: buffer, log, and replace.
		captured, fullBody, readErr := captureBody(resp.Body)
		if readErr == nil {
			resp.Body = io.NopCloser(bytes.NewReader(fullBody))
			fmt.Fprintf(stdio.Stdout, "<<< %d\n%s\n", resp.StatusCode, captured)
		}
		span.End()
		stdio.Close()
	} else {
		fmt.Fprintf(stdio.Stdout, "<<< %d (no body)\n", resp.StatusCode)
		span.End()
		stdio.Close()
	}

	return resp, nil
}

// teeReadCloser wraps a tee'd reader with the original body's Close, and
// runs a cleanup function on close (to end the span and close stdio).
type teeReadCloser struct {
	reader  io.Reader
	closer  io.Closer
	onClose func()
}

func (t *teeReadCloser) Read(p []byte) (int, error) {
	return t.reader.Read(p)
}

func (t *teeReadCloser) Close() error {
	err := t.closer.Close()
	if t.onClose != nil {
		t.onClose()
		t.onClose = nil // only run once
	}
	return err
}

// captureBody reads the full body, returning a displayable string and the raw bytes.
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
