package client

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"

	"github.com/dagger/dagger/engine/clientdb"
	enginetel "github.com/dagger/dagger/engine/telemetry"
)

// A span query the engine answers 410 Gone -- its session ended -- reports
// ErrLiveSessionGone, so a frontend stops asking; other failures don't.
func TestQuerySpansSessionGone(t *testing.T) {
	for _, tc := range []struct {
		status int
		gone   bool
	}{
		{http.StatusGone, true},
		{http.StatusInternalServerError, false},
		{http.StatusServiceUnavailable, false},
	} {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, `get client record: session "x" not found`, tc.status)
			})
			c := &Client{
				telemetryHTTP:     &httpClient{inner: &http.Client{Transport: handlerTransport{handler}}},
				telemetryFeatures: map[string]bool{enginetel.LiveFeatureSpanQuery: true},
			}
			_, err := c.QuerySpans(t.Context(), clientdb.SpanQuery{Spans: []string{"0000000000000001"}},
				func(*coltracepb.ExportTraceServiceRequest) error { return nil })
			require.Error(t, err)
			require.Equal(t, tc.gone, errors.Is(err, ErrLiveSessionGone), "error: %v", err)
		})
	}
}
