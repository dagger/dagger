package core

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	telemetry "github.com/dagger/otel-go"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// TestLLMTransportSpanInternal locks in that LLM HTTP spans are marked
// internal: their stdio is the raw provider wire protocol (SSE event
// streams), which otherwise leaks into enclosing tool-call log captures
// (captureLogs skips subtrees beneath internal spans) and into the TUI.
// Failed requests un-hide themselves, since then the bodies are the
// diagnosis.
func TestLLMTransportSpanInternal(t *testing.T) {
	for _, tc := range []struct {
		name         string
		status       int
		body         string
		wantInternal bool
	}{
		{name: "success stays hidden", status: 200, body: `{"ok":true}`, wantInternal: true},
		{name: "error is revealed", status: 500, body: `{"error":{"message":"boom"}}`, wantInternal: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			}))
			defer srv.Close()

			sr := tracetest.NewSpanRecorder()
			tp := sdktrace.NewTracerProvider(
				sdktrace.WithSampler(sdktrace.AlwaysSample()),
				sdktrace.WithSpanProcessor(sr),
			)
			ctx, root := tp.Tracer("llm-otel-test").Start(context.Background(), "root")
			defer root.End()

			client := &http.Client{Transport: newLLMOTelTransport(nil, "test")}
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/v1/messages",
				strings.NewReader(`{"model":"test"}`))
			if err != nil {
				t.Fatal(err)
			}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()

			span := otelprofSpanByName(t, sr.Ended(), "LLM HTTP POST /v1/messages")
			if got := otelprofAttrBool(span, telemetry.UIInternalAttr); got != tc.wantInternal {
				t.Errorf("%s = %v, want %v", telemetry.UIInternalAttr, got, tc.wantInternal)
			}
		})
	}
}

func TestIsStreamingResponse(t *testing.T) {
	for _, tc := range []struct {
		name       string
		accept     string
		reqBody    string
		respCT     string
		statusCode int
		want       bool
	}{
		{
			// OpenAI/Codex: streams via stream:true in the body, and its SSE
			// response does NOT carry a text/event-stream Content-Type. This is
			// the case that regressed — it must be detected as streaming.
			name:       "openai stream body, json response CT",
			reqBody:    `{"model":"gpt-5.5","stream":true,"input":[]}`,
			respCT:     "application/json",
			statusCode: 200,
			want:       true,
		},
		{
			name:       "anthropic accept header + event-stream CT",
			accept:     "text/event-stream",
			reqBody:    `{"model":"claude","stream":true}`,
			respCT:     "text/event-stream; charset=utf-8",
			statusCode: 200,
			want:       true,
		},
		{
			// Google streamGenerateContent: no stream:true in body, relies on CT.
			name:       "response CT fallback",
			reqBody:    `{"contents":[]}`,
			respCT:     "text/event-stream",
			statusCode: 200,
			want:       true,
		},
		{
			name:       "non-streaming request buffers",
			reqBody:    `{"model":"gpt-4.1","messages":[]}`,
			respCT:     "application/json",
			statusCode: 200,
			want:       false,
		},
		{
			// A streaming request that errors returns a JSON error body, which we
			// must buffer (not tee) so the detail can be parsed.
			name:       "streaming request that errors is buffered",
			reqBody:    `{"model":"gpt-5.5","stream":true}`,
			respCT:     "application/json",
			statusCode: 400,
			want:       false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := isStreamingResponse(tc.accept, []byte(tc.reqBody), tc.respCT, tc.statusCode)
			if got != tc.want {
				t.Errorf("isStreamingResponse(%q, %q, %q, %d) = %v, want %v",
					tc.accept, tc.reqBody, tc.respCT, tc.statusCode, got, tc.want)
			}
		})
	}
}
