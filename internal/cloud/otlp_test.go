package cloud

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The stall watchdog (consumeSSE): a stored trace is a bounded download that
// should always be transferring, and the fetch runs BEFORE `dagger agent
// --trace`'s interactive loop starts — so a connection the server's edge
// drops without a FIN or RST must become a prompt, named error rather than a
// command wedged on "restoring trace" forever. Measured against a real agent
// trace: Cloud's logs stream reproducibly died mid-stream, and the hang was
// this exact read.

// stalledOTLPClient is an OTLPClient aimed at srv with a test-sized stall
// window, built directly rather than via NewOTLPClient to skip auth.
func stalledOTLPClient(t *testing.T, srv *httptest.Server, stall time.Duration) *OTLPClient {
	t.Helper()
	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	return &OTLPClient{
		h:     http.DefaultClient,
		u:     u,
		stats: newClientStats(),
		stall: stall,
	}
}

func TestFetchAbortsAStalledStream(t *testing.T) {
	t.Parallel()

	// One real event, then silence: the connection stays open, nothing more
	// arrives. Without the watchdog this read blocks until the server dies.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"payload\":1}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done() // hold the stream open, silently, forever
	}))
	t.Cleanup(srv.Close)

	c := stalledOTLPClient(t, srv, 250*time.Millisecond)

	var events int
	start := time.Now()
	err := c.consumeSSE(context.Background(), otlpTraces, "stalled-trace", func([]byte) error {
		events++
		return nil
	})
	require.Error(t, err)
	require.ErrorContains(t, err, "stalled")
	require.ErrorContains(t, err, "after 1 events")
	require.Equal(t, 1, events, "the event before the stall was delivered")
	require.Less(t, time.Since(start), 10*time.Second,
		"the watchdog, not the test timeout, must be what ended the read")
}

func TestStallWatchdogCountsKeepalivesAsProgress(t *testing.T) {
	t.Parallel()

	// Events spaced FURTHER apart than the stall window, with SSE comment
	// keepalives in between. The watchdog is byte-level on purpose: a
	// keepalive never surfaces as an event, but it proves the connection is
	// alive, so it must reset the stall clock.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flush := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"payload\":1}\n\n")
		flush.Flush()
		for range 8 {
			time.Sleep(50 * time.Millisecond)
			fmt.Fprint(w, ": keepalive\n\n")
			flush.Flush()
		}
		fmt.Fprint(w, "data: {\"payload\":2}\n\n")
		flush.Flush()
	}))
	t.Cleanup(srv.Close)

	// 8 keepalives x 50ms = 400ms between the two events, > the 250ms stall.
	c := stalledOTLPClient(t, srv, 250*time.Millisecond)

	var events int
	err := c.consumeSSE(context.Background(), otlpTraces, "kept-alive-trace", func([]byte) error {
		events++
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 2, events, "both events must arrive; the keepalives kept the stream alive")
}
