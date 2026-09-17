package archive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	enginetel "github.com/dagger/dagger/engine/telemetry"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	otlplogsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	otlpmetricsv1 "go.opentelemetry.io/proto/otlp/metrics/v1"
	otlpresourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	otlptracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

func TestClientListAllAndUpdateMetadata(t *testing.T) {
	trace1 := strings.Repeat("1", 32)
	excluded := strings.Repeat("2", 32)
	trace3 := strings.Repeat("3", 32)
	pages := map[string]Page{
		"":       {Archives: []Manifest{{TraceID: trace1}}, Next: trace1},
		trace1:   {Archives: []Manifest{{TraceID: excluded}}, Next: excluded},
		excluded: {Archives: []Manifest{{TraceID: trace3}}},
	}
	var metadata MetadataUpdate
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == archivePath:
			if got := r.URL.Query().Get("limit"); got != "1" {
				t.Errorf("list limit = %q, want 1", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(pages[r.URL.Query().Get("after")])
		case r.Method == http.MethodPost && r.URL.Path == archiveResourcePath(trace3, "metadata"):
			if err := json.NewDecoder(r.Body).Decode(&metadata); err != nil {
				t.Errorf("decode metadata: %v", err)
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := testArchiveClient(t, server)

	manifests, err := client.ListAll(context.Background(), ListOptions{Limit: 1, ExcludeTraceID: excluded})
	if err != nil {
		t.Fatal(err)
	}
	if len(manifests) != 2 {
		t.Fatalf("listed %d manifests: %+v", len(manifests), manifests)
	}
	if got := []string{manifests[0].TraceID, manifests[1].TraceID}; !slices.Equal(got, []string{trace1, trace3}) {
		t.Fatalf("listed traces = %v", got)
	}
	if err := client.UpdateMetadata(context.Background(), trace3, MetadataUpdate{Title: "continued work"}); err != nil {
		t.Fatal(err)
	}
	if metadata.Title != "continued work" {
		t.Fatalf("metadata title = %q", metadata.Title)
	}
}

func TestClientTypedErrors(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   error
		state  State
	}{
		{name: "unsupported", status: http.StatusNotFound, body: "not found", want: ErrCleanMiss},
		{name: "not found", status: http.StatusNotFound, body: `{"error":"not_found","message":"missing"}`, want: ErrCleanMiss},
		{name: "evicted", status: http.StatusGone, body: `{"error":"evicted","message":"gone"}`, want: ErrCleanMiss},
		{name: "state", status: http.StatusConflict, body: `{"error":"state","state":"active","message":"active"}`, want: ErrState, state: StateActive},
		{name: "corrupt", status: http.StatusUnprocessableEntity, body: `{"error":"corrupt","message":"bad sidecar"}`, want: ErrCorrupt},
		{name: "transient", status: http.StatusServiceUnavailable, body: "try again", want: ErrTransient},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()
			client := testArchiveClient(t, server)
			err := client.UpdateMetadata(context.Background(), strings.Repeat("1", 32), MetadataUpdate{})
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
			if got := IsCleanMiss(err); got != errors.Is(test.want, ErrCleanMiss) {
				t.Fatalf("IsCleanMiss = %v", got)
			}
			var requestErr *RequestError
			if !errors.As(err, &requestErr) || requestErr.StatusCode != test.status || requestErr.State != test.state {
				t.Fatalf("request error = %+v", requestErr)
			}
		})
	}
}

func TestClientBootstrapVerificationAndDecoding(t *testing.T) {
	traceID := strings.Repeat("a", 32)
	generation := "generation"
	traces := &coltracepb.ExportTraceServiceRequest{ResourceSpans: []*otlptracev1.ResourceSpans{{
		Resource: &otlpresourcev1.Resource{},
		ScopeSpans: []*otlptracev1.ScopeSpans{{Spans: []*otlptracev1.Span{{
			TraceId: bytes.Repeat([]byte{0xaa}, 16), SpanId: bytes.Repeat([]byte{0xbb}, 8), Name: "agent",
		}}}},
	}}}
	logs := &collogspb.ExportLogsServiceRequest{ResourceLogs: []*otlplogsv1.ResourceLogs{{
		Resource:  &otlpresourcev1.Resource{},
		ScopeLogs: []*otlplogsv1.ScopeLogs{{LogRecords: []*otlplogsv1.LogRecord{{Body: &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_StringValue{StringValue: "state"}}}}}},
	}}}
	tracePayload, _ := proto.Marshal(traces)
	logPayload, _ := proto.Marshal(logs)
	sealAt := time.Now().UTC().Format(time.RFC3339Nano)
	data, _, err := BuildBootstrap(BootstrapHeader{
		Generation: generation, TraceID: traceID, SealAt: sealAt,
		HighWater: HighWater{Spans: 5, Logs: 7, Metrics: 9},
	}, []BootstrapSignal{
		{Kind: BootstrapFrameTraces, Payload: tracePayload, Records: 1},
		{Kind: BootstrapFrameLogs, Payload: logPayload, Records: 1},
	}, BootstrapExclusions{SpanIDs: []string{"span"}, LogRowIDs: []int64{4}})
	if err != nil {
		t.Fatal(err)
	}

	var kinds []BootstrapFrameKind
	client, closeServer := bootstrapTestClient(t, generation, data)
	defer closeServer()
	result, err := client.Bootstrap(context.Background(), traceID, generation, func(header BootstrapHeader, batch BootstrapBatch) error {
		if header.TraceID != traceID || header.Generation != generation || header.SealAt != sealAt {
			t.Fatalf("consumer received unvalidated header: %+v", header)
		}
		if batch.Traces != nil {
			kinds = append(kinds, BootstrapFrameTraces)
		}
		if batch.Logs != nil {
			kinds = append(kinds, BootstrapFrameLogs)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(kinds, []BootstrapFrameKind{BootstrapFrameTraces, BootstrapFrameLogs}) {
		t.Fatalf("bootstrap kinds = %v", kinds)
	}
	if result.Header.Generation != generation || result.Header.HighWater.Metrics != 9 || result.Terminal.TraceRecords != 1 || result.Terminal.LogRecords != 1 {
		t.Fatalf("unexpected bootstrap result: %+v", result)
	}

	t.Run("missing terminal is transient", func(t *testing.T) {
		client, closeServer := bootstrapTestClient(t, generation, data[:len(data)-1])
		defer closeServer()
		_, err := client.Bootstrap(context.Background(), traceID, generation, nil)
		if !errors.Is(err, ErrTransient) {
			t.Fatalf("error = %v, want transient", err)
		}
	})

	t.Run("generation mismatch is corruption", func(t *testing.T) {
		client, closeServer := bootstrapTestClient(t, "other-generation", data)
		defer closeServer()
		_, err := client.Bootstrap(context.Background(), traceID, generation, nil)
		if !errors.Is(err, ErrCorrupt) {
			t.Fatalf("error = %v, want corruption", err)
		}
	})

	t.Run("invalid header stops before signal consumption", func(t *testing.T) {
		wrongTrace := strings.Repeat("b", 32)
		invalid, _, err := BuildBootstrap(BootstrapHeader{
			Generation: generation, TraceID: wrongTrace, SealAt: sealAt,
		}, []BootstrapSignal{{Kind: BootstrapFrameTraces, Payload: tracePayload, Records: 1}}, BootstrapExclusions{})
		if err != nil {
			t.Fatal(err)
		}
		client, closeServer := bootstrapTestClient(t, generation, invalid)
		defer closeServer()
		consumed := false
		_, err = client.Bootstrap(context.Background(), traceID, generation, func(BootstrapHeader, BootstrapBatch) error {
			consumed = true
			return nil
		})
		if !errors.Is(err, ErrCorrupt) || consumed {
			t.Fatalf("error=%v consumed=%v", err, consumed)
		}
	})

	t.Run("checksum mismatch is corruption", func(t *testing.T) {
		corrupted := append([]byte(nil), data...)
		checksum := bytes.Index(corrupted, []byte(`"sha256":"`)) + len(`"sha256":"`)
		if checksum < len(`"sha256":"`) {
			t.Fatal("bootstrap terminal checksum not found")
		}
		if corrupted[checksum] == '0' {
			corrupted[checksum] = '1'
		} else {
			corrupted[checksum] = '0'
		}
		client, closeServer := bootstrapTestClient(t, generation, corrupted)
		defer closeServer()
		_, err := client.Bootstrap(context.Background(), traceID, generation, nil)
		if !errors.Is(err, ErrCorrupt) {
			t.Fatalf("error = %v, want corruption", err)
		}
	})

	t.Run("terminal count mismatch is corruption", func(t *testing.T) {
		mismatch, _, err := BuildBootstrap(BootstrapHeader{
			Generation: generation, TraceID: traceID, SealAt: sealAt,
		}, []BootstrapSignal{{Kind: BootstrapFrameTraces, Payload: tracePayload, Records: 2}}, BootstrapExclusions{})
		if err != nil {
			t.Fatal(err)
		}
		client, closeServer := bootstrapTestClient(t, generation, mismatch)
		defer closeServer()
		_, err = client.Bootstrap(context.Background(), traceID, generation, nil)
		if !errors.Is(err, ErrCorrupt) {
			t.Fatalf("error = %v, want corruption", err)
		}
	})
}

func TestClientFiniteSignalStreams(t *testing.T) {
	traceID := strings.Repeat("c", 32)
	generation := "generation"
	traces := &coltracepb.ExportTraceServiceRequest{ResourceSpans: []*otlptracev1.ResourceSpans{{}}}
	logs := &collogspb.ExportLogsServiceRequest{ResourceLogs: []*otlplogsv1.ResourceLogs{{}}}
	metrics := &colmetricspb.ExportMetricsServiceRequest{ResourceMetrics: []*otlpmetricsv1.ResourceMetrics{{}}}
	tracePayload, _ := proto.Marshal(traces)
	logPayload, _ := proto.Marshal(logs)
	metricPayload, _ := proto.Marshal(metrics)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(archiveGenerationHeader); got != generation {
			t.Errorf("generation header = %q", got)
		}
		w.Header().Set("Content-Type", enginetel.LiveContentType)
		w.Header().Set(archiveGenerationHeader, generation)
		switch r.URL.Path {
		case archiveResourcePath(traceID, "traces"):
			if got := r.Header.Get(enginetel.LiveCursorHeader); got != "1" {
				t.Errorf("trace cursor header = %q", got)
			}
			if !slices.Equal(r.URL.Query()["exclude_span"], []string{"span-a", "span-b"}) {
				t.Errorf("span exclusions = %v", r.URL.Query()["exclude_span"])
			}
			_ = enginetel.WriteLiveFrame(w, 3, tracePayload)
			_ = enginetel.WriteLiveTerminal(w, 5) // excluded tail advances the terminal scan cursor
		case archiveResourcePath(traceID, "logs"):
			if !slices.Equal(r.URL.Query()["exclude_log"], []string{"4", "8"}) {
				t.Errorf("log exclusions = %v", r.URL.Query()["exclude_log"])
			}
			_ = enginetel.WriteLiveFrame(w, 2, logPayload)
			_ = enginetel.WriteLiveTerminal(w, 2)
		case archiveResourcePath(traceID, "metrics"):
			_ = enginetel.WriteLiveFrame(w, 1, metricPayload)
			_ = enginetel.WriteLiveTerminal(w, 1)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := testArchiveClient(t, server)

	traceCursor, err := client.Traces(context.Background(), traceID, StreamOptions{
		Generation: generation, Cursor: 1, HighWater: 5, ExcludeSpanIDs: []string{"span-a", "span-b"},
	}, func(cursor int64, batch *coltracepb.ExportTraceServiceRequest) error {
		if cursor != 3 || len(batch.ResourceSpans) != 1 {
			t.Fatalf("trace batch cursor=%d batch=%+v", cursor, batch)
		}
		return nil
	})
	if err != nil || traceCursor != 5 {
		t.Fatalf("traces cursor=%d err=%v", traceCursor, err)
	}
	logCursor, err := client.Logs(context.Background(), traceID, StreamOptions{
		Generation: generation, HighWater: 2, ExcludeLogRowIDs: []int64{4, 8},
	}, func(_ int64, batch *collogspb.ExportLogsServiceRequest) error {
		if len(batch.ResourceLogs) != 1 {
			t.Fatalf("logs batch = %+v", batch)
		}
		return nil
	})
	if err != nil || logCursor != 2 {
		t.Fatalf("logs cursor=%d err=%v", logCursor, err)
	}
	metricCursor, err := client.Metrics(context.Background(), traceID, StreamOptions{
		Generation: generation, HighWater: 1,
	}, func(_ int64, batch *colmetricspb.ExportMetricsServiceRequest) error {
		if len(batch.ResourceMetrics) != 1 {
			t.Fatalf("metrics batch = %+v", batch)
		}
		return nil
	})
	if err != nil || metricCursor != 1 {
		t.Fatalf("metrics cursor=%d err=%v", metricCursor, err)
	}
}

func TestClientStreamEnforcesCursorAndTerminal(t *testing.T) {
	traceID := strings.Repeat("d", 32)
	generation := "generation"
	payload, _ := proto.Marshal(&coltracepb.ExportTraceServiceRequest{})
	tests := []struct {
		name string
		body func(io.Writer)
		want error
	}{
		{name: "missing terminal", body: func(w io.Writer) { _ = enginetel.WriteLiveFrame(w, 1, payload) }, want: ErrTransient},
		{name: "wrong terminal", body: func(w io.Writer) { _ = enginetel.WriteLiveTerminal(w, 1) }, want: ErrCorrupt},
		{name: "non increasing cursor", body: func(w io.Writer) { _ = enginetel.WriteLiveFrame(w, 0, payload) }, want: ErrCorrupt},
		{name: "trailing frame", body: func(w io.Writer) { _ = enginetel.WriteLiveTerminal(w, 2); _ = enginetel.WriteLiveTerminal(w, 2) }, want: ErrCorrupt},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", enginetel.LiveContentType)
				w.Header().Set(archiveGenerationHeader, generation)
				test.body(w)
			}))
			defer server.Close()
			client := testArchiveClient(t, server)
			_, err := client.Traces(context.Background(), traceID, StreamOptions{Generation: generation, HighWater: 2}, nil)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}

	t.Run("consumer failure preserves acknowledged cursor", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", enginetel.LiveContentType)
			w.Header().Set(archiveGenerationHeader, generation)
			_ = enginetel.WriteLiveFrame(w, 2, payload)
			_ = enginetel.WriteLiveTerminal(w, 2)
		}))
		defer server.Close()
		client := testArchiveClient(t, server)
		consumeErr := errors.New("frontend barrier failed")
		cursor, err := client.Traces(context.Background(), traceID, StreamOptions{Generation: generation, Cursor: 1, HighWater: 2}, func(int64, *coltracepb.ExportTraceServiceRequest) error {
			return consumeErr
		})
		if cursor != 1 || !errors.Is(err, consumeErr) {
			t.Fatalf("cursor=%d err=%v", cursor, err)
		}
	})
}

func TestClientBootstrapIdleTimeout(t *testing.T) {
	body := &blockingReadCloser{closed: make(chan struct{})}
	doer := httpDoerFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Type":          []string{BootstrapContentType},
				archiveGenerationHeader: []string{"generation"},
			},
			Body: body,
		}, nil
	})
	client, err := NewClientWithURL(doer, "http://dagger")
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, err = client.WithStallTimeout(20*time.Millisecond).Bootstrap(
		context.Background(), strings.Repeat("a", 32), "generation", nil)
	if !errors.Is(err, ErrStreamStalled) || !errors.Is(err, ErrTransient) {
		t.Fatalf("error = %v, want stalled transient", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("idle timeout took %s", elapsed)
	}
}

type httpDoerFunc func(*http.Request) (*http.Response, error)

func (f httpDoerFunc) Do(req *http.Request) (*http.Response, error) { return f(req) }

type blockingReadCloser struct {
	closed chan struct{}
	once   sync.Once
}

func (b *blockingReadCloser) Read([]byte) (int, error) {
	<-b.closed
	return 0, errors.New("body closed")
}

func (b *blockingReadCloser) Close() error {
	b.once.Do(func() { close(b.closed) })
	return nil
}

func testArchiveClient(t *testing.T, server *httptest.Server) *Client {
	t.Helper()
	client, err := NewClientWithURL(server.Client(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func bootstrapTestClient(t *testing.T, generation string, data []byte) (*Client, func()) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", BootstrapContentType)
		w.Header().Set(archiveGenerationHeader, generation)
		_, _ = w.Write(data)
	}))
	return testArchiveClient(t, server), server.Close
}
