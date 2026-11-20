package archive

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	enginetel "github.com/dagger/dagger/engine/telemetry"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	"google.golang.org/protobuf/proto"
)

func TestClientCallClosure(t *testing.T) {
	trace := strings.Repeat("2", 32)
	payload, err := proto.Marshal(&collogspb.ExportLogsServiceRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		serve  func(http.ResponseWriter)
		frames int
		err    error
	}{
		{name: "frames", frames: 2, serve: func(w http.ResponseWriter) {
			w.Header().Set("Content-Type", enginetel.LiveContentType)
			_ = enginetel.WriteLiveFrame(w, 1, payload)
			_ = enginetel.WriteLiveFrame(w, 2, payload)
			_ = enginetel.WriteLiveTerminal(w, 2)
		}},
		{name: "empty", serve: func(w http.ResponseWriter) {
			w.Header().Set("Content-Type", enginetel.LiveContentType)
			_ = enginetel.WriteLiveTerminal(w, 0)
		}},
		{name: "skipped frame", err: ErrCorrupt, serve: func(w http.ResponseWriter) {
			w.Header().Set("Content-Type", enginetel.LiveContentType)
			_ = enginetel.WriteLiveFrame(w, 2, payload)
			_ = enginetel.WriteLiveTerminal(w, 2)
		}},
		{name: "old engine", err: ErrCallClosureUnsupported, serve: func(w http.ResponseWriter) {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != archiveResourcePath(trace, CallClosureResource) {
					t.Errorf("request: %s %s", r.Method, r.URL)
				}
				var q CallClosureQuery
				if err := json.NewDecoder(r.Body).Decode(&q); err != nil || !reflect.DeepEqual(q.Digests, []string{"xxh3:a"}) || !reflect.DeepEqual(q.Known, []string{"xxh3:k"}) {
					t.Errorf("query: %+v %v", q, err)
				}
				tc.serve(w)
			}))
			defer server.Close()
			var frames int
			err := testArchiveClient(t, server).CallClosure(t.Context(), trace, false, []string{"xxh3:a"}, []string{"xxh3:k"}, func(*collogspb.ExportLogsServiceRequest) error {
				frames++
				return nil
			})
			if tc.err == nil && err != nil || tc.err != nil && !errors.Is(err, tc.err) {
				t.Fatalf("err=%v, want %v", err, tc.err)
			}
			if tc.err == nil && frames != tc.frames {
				t.Fatalf("frames=%d, want %d", frames, tc.frames)
			}
		})
	}
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	if err := testArchiveClient(t, server).CallClosure(t.Context(), trace, false, nil, nil, nil); err == nil {
		t.Fatal("accepted a query naming no calls")
	}
}
