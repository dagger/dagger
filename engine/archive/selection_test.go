package archive

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	enginetel "github.com/dagger/dagger/engine/telemetry"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
)

func TestArchiveSelectionProtocol(t *testing.T) {
	id := strings.Repeat("1", 16)
	for _, raw := range []string{"root=bad", "view=bad", "listen=not-a-span", "span_id=bad", "descendants=true", "records=logs", "records=unknown", "after=yesterday", "full=bad", "full=true&root=false", "full=true&listen=" + id, "conversation=bad", "conversation=true&full=true", "conversation=true&root=false&listen=" + id, "exact=not-a-span&root=false", "exact=" + id, "exact=" + id + "&root=false&listen=" + id} {
		q, err := url.ParseQuery(raw)
		if err != nil {
			t.Fatal(err)
		}
		signal := "logs"
		if q.Has("root") || q.Has("listen") || q.Has("exact") || q.Has("view") || q.Has("full") || q.Has("conversation") {
			signal = "traces"
		}
		if _, _, err := ParseSelection(signal, q); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	after := time.Unix(1, 2).UTC()
	for _, opts := range []StreamOptions{
		{Spans: &SpanSelection{Full: true, DagUIView: true}},
		{Spans: &SpanSelection{NoRoot: true, Listen: []string{id}, DagUIView: true}},
		{Spans: &SpanSelection{NoRoot: true, Exact: []string{id}, DagUIView: true}},
		{Spans: &SpanSelection{Conversation: true, DagUIView: true}},
		{Spans: &SpanSelection{Conversation: true, Listen: []string{id}, DagUIView: true}},
		{Logs: &LogSelection{SpanID: id, Descendants: true, After: &after, Records: LogRecordsMetadata}},
		{Metrics: &MetricSelection{Names: []string{"a", "b"}}},
	} {
		signal := "logs"
		if opts.Spans != nil {
			signal = "traces"
		}
		if opts.Metrics != nil {
			signal = "metrics"
		}
		q, err := opts.SelectionQuery(signal)
		if err != nil {
			t.Fatal(err)
		}
		spans, logs, err := ParseSelection(signal, q)
		if err != nil {
			t.Fatal(err)
		}
		metrics, err := ParseMetricSelection(signal, q)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(opts.Spans, spans) || !reflect.DeepEqual(opts.Logs, logs) || !reflect.DeepEqual(opts.Metrics, metrics) {
			t.Fatalf("roundtrip mismatch: %v %v %v", spans, logs, metrics)
		}
	}
	for signal, raw := range map[string]string{"logs": "metric=a", "metrics": "metric="} {
		q, err := url.ParseQuery(raw)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ParseMetricSelection(signal, q); err == nil {
			t.Errorf("accepted %s for %s", raw, signal)
		}
	}
	if _, err := (StreamOptions{Metrics: &MetricSelection{}}).SelectionQuery("metrics"); err == nil {
		t.Error("accepted a metric selection naming no metrics")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("records") != LogRecordsMetadata || r.URL.Query().Get("span_id") != id {
			t.Errorf("selection missing: %s", r.URL)
		}
		w.Header().Set("Content-Type", enginetel.LiveContentType)
		_ = enginetel.WriteLiveTerminal(w, 9)
	}))
	defer server.Close()
	cursor, err := testArchiveClient(t, server).Logs(t.Context(), strings.Repeat("2", 32), StreamOptions{HighWater: 9, Logs: &LogSelection{SpanID: id, Records: LogRecordsMetadata}}, nil)
	if err != nil || cursor != 9 {
		t.Fatalf("cursor=%d err=%v", cursor, err)
	}
}

// TestMetricSelectionRequiresEngineSupport: an engine that predates metric
// selection ignores it and streams every metric, so a selected read is
// refused unless the response says the selection was applied.
func TestMetricSelectionRequiresEngineSupport(t *testing.T) {
	for _, applied := range []bool{false, true} {
		var frames int
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := r.URL.Query()["metric"]; !reflect.DeepEqual(got, []string{"a", "b"}) {
				t.Errorf("metric selection: %v", got)
			}
			w.Header().Set("Content-Type", enginetel.LiveContentType)
			if applied {
				w.Header().Set(SelectionHeader, SelectionMetrics)
			}
			_ = enginetel.WriteLiveFrame(w, 1, nil)
			_ = enginetel.WriteLiveTerminal(w, 1)
		}))
		cursor, err := testArchiveClient(t, server).Metrics(t.Context(), strings.Repeat("2", 32), StreamOptions{HighWater: 1, Metrics: &MetricSelection{Names: []string{"a", "b"}}}, func(int64, *colmetricspb.ExportMetricsServiceRequest) error {
			frames++
			return nil
		})
		server.Close()
		if applied {
			if err != nil || cursor != 1 || frames != 1 {
				t.Fatalf("applied: cursor=%d frames=%d err=%v", cursor, frames, err)
			}
			continue
		}
		if !errors.Is(err, ErrSelectionUnsupported) || errors.Is(err, ErrTransient) || frames != 0 {
			t.Fatalf("ignored: frames=%d err=%v", frames, err)
		}
	}
}

func TestClientInspectIdentity(t *testing.T) {
	trace := strings.Repeat("2", 32)
	seal := time.Unix(1, 0).UTC()
	for _, bad := range []string{"", "trace", "state", "seal", "cut"} {
		t.Run(bad, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != archivePath+"/"+trace || r.URL.RawQuery != "" {
					t.Errorf("request: %s", r.URL)
				}
				m := Manifest{TraceID: trace, State: StateClosed, SealAt: &seal}
				switch bad {
				case "trace":
					m.TraceID = "bad"
				case "state":
					m.State = StateActive
				case "seal":
					m.SealAt = nil
				case "cut":
					m.HighWater.Logs = -1
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(m)
			}))
			defer server.Close()
			_, err := testArchiveClient(t, server).Inspect(t.Context(), trace)
			if bad == "" && err != nil {
				t.Fatal(err)
			}
			if bad != "" && !errors.Is(err, ErrCorrupt) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}
