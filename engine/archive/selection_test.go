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
)

func TestArchiveSelectionProtocol(t *testing.T) {
	id := strings.Repeat("1", 16)
	for _, raw := range []string{"root=bad", "view=bad", "listen=not-a-span", "span_id=bad", "descendants=true", "records=logs", "records=unknown", "after=yesterday", "full=bad", "full=true&root=false", "full=true&listen=" + id} {
		q, err := url.ParseQuery(raw)
		if err != nil {
			t.Fatal(err)
		}
		signal := "logs"
		if q.Has("root") || q.Has("listen") || q.Has("view") || q.Has("full") {
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
		{Logs: &LogSelection{SpanID: id, Descendants: true, After: &after, Records: LogRecordsMetadata}},
	} {
		signal := "logs"
		if opts.Spans != nil {
			signal = "traces"
		}
		q, err := opts.SelectionQuery(signal)
		if err != nil {
			t.Fatal(err)
		}
		spans, logs, err := ParseSelection(signal, q)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(opts.Spans, spans) || !reflect.DeepEqual(opts.Logs, logs) {
			t.Fatalf("roundtrip mismatch: %v %v", spans, logs)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("records") != LogRecordsMetadata || r.URL.Query().Get("span_id") != id {
			t.Errorf("selection missing: %s", r.URL)
		}
		w.Header().Set("Content-Type", enginetel.LiveContentType)
		w.Header().Set(archiveGenerationHeader, "g")
		_ = enginetel.WriteLiveTerminal(w, 9)
	}))
	defer server.Close()
	cursor, err := testArchiveClient(t, server).Logs(t.Context(), strings.Repeat("2", 32), StreamOptions{Generation: "g", HighWater: 9, Logs: &LogSelection{SpanID: id, Records: LogRecordsMetadata}}, nil)
	if err != nil || cursor != 9 {
		t.Fatalf("cursor=%d err=%v", cursor, err)
	}
}

func TestClientInspectIdentity(t *testing.T) {
	trace := strings.Repeat("2", 32)
	for _, bad := range []string{"", "generation", "trace", "source", "state", "cut"} {
		t.Run(bad, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != archiveResourcePath(trace, "inspect") || r.URL.Query().Get("source_session") != "source" {
					t.Errorf("request: %s", r.URL)
				}
				m := Manifest{TraceID: trace, Generation: "g", SourceSession: "source", State: StateClosed}
				switch bad {
				case "generation":
					m.Generation = "bad"
				case "trace":
					m.TraceID = "bad"
				case "source":
					m.SourceSession = "bad"
				case "state":
					m.State = StateActive
				case "cut":
					m.HighWater.Logs = -1
				}
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set(archiveGenerationHeader, "g")
				_ = json.NewEncoder(w).Encode(m)
			}))
			defer server.Close()
			_, err := testArchiveClient(t, server).WithSourceSession("source").Inspect(t.Context(), trace, "g")
			if bad == "" && err != nil {
				t.Fatal(err)
			}
			if bad != "" && !errors.Is(err, ErrCorrupt) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}
