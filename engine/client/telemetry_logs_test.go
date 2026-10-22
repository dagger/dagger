package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	otlplogsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/proto"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/archive"
	enginetel "github.com/dagger/dagger/engine/telemetry"
)

type handlerTransport struct{ h http.Handler }

type nopLogExporter struct{}

func (nopLogExporter) Export(context.Context, []sdklog.Record) error { return nil }
func (nopLogExporter) Shutdown(context.Context) error                { return nil }
func (nopLogExporter) ForceFlush(context.Context) error              { return nil }

func (t handlerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	t.h.ServeHTTP(rec, req)
	return rec.Result(), nil
}

func TestLiveTelemetryFeatures(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}
	require.Empty(t, liveTelemetryFeatures(resp), "an older engine names none")
	resp.Header.Set(enginetel.LiveFeaturesHeader, "log-query, log-presence,,future")
	require.Equal(t, map[string]bool{"log-query": true, "log-presence": true, "future": true}, liveTelemetryFeatures(resp))
}

func TestQueryLogs(t *testing.T) {
	span := "00000000000000a1"
	payload, err := proto.Marshal(&collogspb.ExportLogsServiceRequest{ResourceLogs: []*otlplogsv1.ResourceLogs{{}}})
	require.NoError(t, err)
	var gotCursor string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q archive.LogQuery
		if r.Method != http.MethodPost || r.URL.Path != "/v1/logs/query" || archive.DecodeLogBody(r.Body, &q) != nil || q.Spans[0].SpanID != span || !q.Follow {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		gotCursor = r.Header.Get(enginetel.LiveCursorHeader)
		w.Header().Set("Content-Type", enginetel.LiveContentType)
		_ = enginetel.WriteLiveHello(w, 3)
		_ = enginetel.WriteLiveFrame(w, 5, payload)
		_ = enginetel.WriteLiveFrame(w, 9, nil)
		_ = enginetel.WriteLiveTerminal(w, 9)
	})
	c := &Client{telemetryHTTP: &httpClient{inner: &http.Client{Transport: handlerTransport{handler}}}}
	q := archive.LogQuery{Spans: []archive.LogSpan{{SpanID: span}}, Follow: true}

	_, err = c.QueryLogs(t.Context(), q, 0, nil)
	require.ErrorIs(t, err, ErrLiveLogsUnsupported, "an older engine is never asked")

	c.telemetryFeatures = map[string]bool{enginetel.LiveFeatureLogQuery: true}
	var cursors []int64
	cursor, err := c.QueryLogs(t.Context(), q, 3, func(next int64, batch *collogspb.ExportLogsServiceRequest) error {
		cursors = append(cursors, next)
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, "3", gotCursor)
	require.Equal(t, []int64{5, 9}, cursors)
	require.EqualValues(t, 9, cursor)

	_, err = c.QueryLogs(t.Context(), archive.LogQuery{}, 0, nil)
	require.ErrorContains(t, err, "no spans")
}

func TestQueryLogsRejectsBadStreams(t *testing.T) {
	for name, write := range map[string]func(http.ResponseWriter){
		"terminal mismatch": func(w http.ResponseWriter) { _ = enginetel.WriteLiveTerminal(w, 7) },
		"backwards":         func(w http.ResponseWriter) { _ = enginetel.WriteLiveFrame(w, 1, nil) },
		"truncated":         func(http.ResponseWriter) {},
	} {
		t.Run(name, func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", enginetel.LiveContentType)
				write(w)
			})
			c := &Client{
				telemetryHTTP:     &httpClient{inner: &http.Client{Transport: handlerTransport{handler}}},
				telemetryFeatures: map[string]bool{enginetel.LiveFeatureLogQuery: true},
			}
			cursor, err := c.QueryLogs(context.Background(), archive.LogQuery{Spans: []archive.LogSpan{{SpanID: "00000000000000a1"}}}, 2, func(int64, *collogspb.ExportLogsServiceRequest) error { return nil })
			require.Error(t, err)
			require.EqualValues(t, 2, cursor)
		})
	}
}

func TestLogsPresenceNegotiation(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		ask, cloud, publishes bool
		features              string
		noIngest, nested      bool
		wantPresence          bool
	}{
		{name: "asked and served", ask: true, features: "log-presence", wantPresence: true},
		{name: "not asked", features: "log-presence"},
		{name: "older engine", ask: true},
		{name: "engine publishes to cloud", ask: true, cloud: true, publishes: true, features: "log-presence", wantPresence: true},
		// The client would forward logs to Cloud itself, which needs them whole.
		{name: "engine did not confirm cloud", ask: true, cloud: true, features: "log-presence"},
		// The store would lack the client's own display text, which the
		// frontend then reads from it too.
		{name: "engine does not store client telemetry", ask: true, features: "log-presence", noIngest: true},
		{name: "nested client", ask: true, features: "log-presence", nested: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logsQuery string
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/logs" {
					logsQuery = r.URL.RawQuery
				}
				if tc.features != "" {
					w.Header().Set(enginetel.LiveFeaturesHeader, tc.features)
				}
				if tc.publishes {
					w.Header().Set(engine.CloudTelemetryPublisherHeader, engine.CloudTelemetryPublisherEngine)
				}
				if !tc.noIngest {
					w.Header().Set(engine.ClientTelemetryIngestHeader, engine.ClientTelemetryIngestStore)
				}
				w.Header().Set("Content-Type", enginetel.LiveContentType)
				_ = enginetel.WriteLiveHello(w, 0)
				_ = enginetel.WriteLiveTerminal(w, 0)
			})
			c := &Client{
				Params: Params{
					EngineTrace:          tracetest.NewInMemoryExporter(),
					EngineLogs:           nopLogExporter{},
					EngineLogsPresence:   tc.ask,
					EngineCloudTelemetry: tc.cloud,
				},
				telemetry: new(errgroup.Group),
			}
			if tc.nested {
				c.nestedSessionPort = 1234
			}
			hc := &httpClient{inner: &http.Client{Transport: handlerTransport{handler}}}
			require.NoError(t, c.exportTraces(t.Context(), hc))
			require.NoError(t, c.exportLogs(t.Context(), hc))
			require.NoError(t, c.telemetry.Wait())
			require.Equal(t, tc.wantPresence, c.LogsPresence())
			if tc.wantPresence {
				require.Equal(t, "display=presence", logsQuery)
			} else {
				require.Empty(t, logsQuery)
			}
		})
	}
}

func TestWaitTelemetryIdle(t *testing.T) {
	c := &Client{}
	start := time.Now()
	c.WaitTelemetryIdle(t.Context(), 50*time.Millisecond)
	require.GreaterOrEqual(t, time.Since(start), 50*time.Millisecond)

	// A batch arriving while it waits pushes the quiet period out.
	start = time.Now()
	go func() {
		time.Sleep(30 * time.Millisecond)
		c.telemetryFrameAt.Store(time.Now().UnixNano())
	}()
	c.WaitTelemetryIdle(t.Context(), 50*time.Millisecond)
	require.GreaterOrEqual(t, time.Since(start), 80*time.Millisecond)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	start = time.Now()
	c.WaitTelemetryIdle(ctx, time.Hour)
	require.Less(t, time.Since(start), time.Second)
}

func TestSearchLogsClient(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var s archive.LogSearch
		if r.URL.Path != "/v1/logs/search" || archive.DecodeLogBody(r.Body, &s) != nil {
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(archive.LogSearchResult{Matches: []archive.LogMatch{{Text: s.Pattern}}, Next: 4})
	})
	c := &Client{
		telemetryHTTP:     &httpClient{inner: &http.Client{Transport: handlerTransport{handler}}},
		telemetryFeatures: map[string]bool{enginetel.LiveFeatureLogSearch: true},
	}
	result, err := c.SearchLogs(t.Context(), archive.LogSearch{Pattern: "boom"})
	require.NoError(t, err)
	require.Equal(t, "boom", result.Matches[0].Text)
	_, err = c.SearchLogs(t.Context(), archive.LogSearch{})
	require.ErrorContains(t, err, "no pattern")
}
