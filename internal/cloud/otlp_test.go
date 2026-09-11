package cloud

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/dagger/dagger/internal/cloud/otlpstream"
	"github.com/stretchr/testify/require"
)

// The transport half of the fetch (consumeStream), against fakes that
// misbehave in the ways a real Cloud stream has been seen to:
//
//   - the stall watchdog: a stored trace is a bounded download that should
//     always be transferring, and the fetch runs BEFORE `dagger agent
//     --trace`'s interactive loop starts — so a connection the server's edge
//     drops without a FIN or RST must become a prompt, named error rather
//     than a command wedged on "restoring trace" forever;
//   - the protocol tripwire: Cloud replaced the SSE-of-protojson endpoints
//     with the binary otlpstream framing (dagger.io#5226), and a client that
//     scans a mis-negotiated body for its own framing can "find" frames
//     inside the payloads — an agent trace's logs carry its LLM provider's
//     SSE stream verbatim, which is how the mismatch first surfaced. Wrong
//     Content-Type must fail up front, by name;
//   - truncation: the terminal frame is the only end of trace, so a
//     connection that merely closes is a hole, not a completed fetch.

// framedHandler serves one otlpstream response, handing the body writes to
// fn and flushing around them like the real server's live writer.
func framedHandler(fn func(w *otlpstream.FrameWriter, flush func())) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", otlpstream.ContentType)
		flusher := w.(http.Flusher)
		fn(otlpstream.NewFrameWriter(w), flusher.Flush)
	})
}

// testOTLPClient is an OTLPClient aimed at srv with a test-sized stall
// window, built directly rather than via NewOTLPClient to skip auth.
func testOTLPClient(t *testing.T, srv *httptest.Server, stall time.Duration) *OTLPClient {
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

	// One real payload, then silence: the connection stays open, nothing more
	// arrives. Without the watchdog this read blocks until the server dies.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", otlpstream.ContentType)
		fw := otlpstream.NewFrameWriter(w)
		_ = fw.WriteData([]byte("payload-1"))
		w.(http.Flusher).Flush()
		<-r.Context().Done() // hold the stream open, silently, forever
	}))
	t.Cleanup(srv.Close)

	c := testOTLPClient(t, srv, 250*time.Millisecond)

	var payloads int
	start := time.Now()
	err := c.consumeStream(context.Background(), otlpTraces, "stalled-trace", func([]byte) error {
		payloads++
		return nil
	})
	require.Error(t, err)
	require.ErrorContains(t, err, "stalled")
	require.ErrorContains(t, err, "after 1 payloads")
	require.Equal(t, 1, payloads, "the payload before the stall was delivered")
	require.Less(t, time.Since(start), 10*time.Second,
		"the watchdog, not the test timeout, must be what ended the read")
}

func TestStallWatchdogCountsHeartbeatsAsProgress(t *testing.T) {
	t.Parallel()

	// Payloads spaced FURTHER apart than the stall window, with heartbeat
	// frames in between. The watchdog is byte-level on purpose: a heartbeat
	// never reaches the sink, but it proves the connection is alive, so it
	// must reset the stall clock — exactly the job the real server's 15s
	// heartbeats exist to do.
	srv := httptest.NewServer(framedHandler(func(fw *otlpstream.FrameWriter, flush func()) {
		_ = fw.WriteData([]byte("payload-1"))
		flush()
		for range 8 {
			time.Sleep(50 * time.Millisecond)
			_ = fw.WriteHeartbeat()
			flush()
		}
		_ = fw.WriteData([]byte("payload-2"))
		_ = fw.WriteTerminal()
		flush()
	}))
	t.Cleanup(srv.Close)

	// 8 heartbeats x 50ms = 400ms between the two payloads, > the 250ms stall.
	c := testOTLPClient(t, srv, 250*time.Millisecond)

	var payloads int
	err := c.consumeStream(context.Background(), otlpTraces, "kept-alive-trace", func([]byte) error {
		payloads++
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 2, payloads, "both payloads must arrive; the heartbeats kept the stream alive")
}

// TestFetchRefusesAServerSpeakingAnotherProtocol is the regression that
// motivated the framed client: an SSE-era server (or any endpoint that is
// not the binary stream) must be refused by Content-Type, up front and by
// name — never scanned for frames, which is how a protocol mismatch once
// surfaced as an unmarshal error on a payload three layers away.
func TestFetchRefusesAServerSpeakingAnotherProtocol(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: connected\n\ndata: {\"resourceSpans\":[]}\n\n")
	}))
	t.Cleanup(srv.Close)

	c := testOTLPClient(t, srv, 0)

	err := c.consumeStream(context.Background(), otlpTraces, "sse-era-trace", func([]byte) error {
		t.Fatal("no payload must be decoded from a mis-negotiated response")
		return nil
	})
	require.Error(t, err)
	require.ErrorContains(t, err, `"text/event-stream"`)
	require.ErrorContains(t, err, otlpstream.ContentType)
}

// TestFetchTreatsAClosedConnectionAsTruncation: end of connection is NOT end
// of trace. The terminal frame is the completion marker, so a stream that
// merely closes — an edge dropping the connection mid-transfer — is a hole
// the restore must refuse (§12), not a trace that happened to be short.
func TestFetchTreatsAClosedConnectionAsTruncation(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(framedHandler(func(fw *otlpstream.FrameWriter, flush func()) {
		_ = fw.WriteData([]byte("payload-1"))
		flush()
		// return without a terminal frame: the connection just ends
	}))
	t.Cleanup(srv.Close)

	c := testOTLPClient(t, srv, 0)

	var payloads int
	err := c.consumeStream(context.Background(), otlpTraces, "truncated-trace", func([]byte) error {
		payloads++
		return nil
	})
	require.Error(t, err)
	require.ErrorContains(t, err, "truncated")
	require.ErrorContains(t, err, "terminal frame")
	require.ErrorContains(t, err, "after 1 payloads")
	require.Equal(t, 1, payloads)
}

// TestFetchSurfacesAServerErrorFrame: the framed protocol can say WHY a
// stream died (the SSE one could only close), and the client must relay
// that verbatim rather than reporting a generic disconnect.
func TestFetchSurfacesAServerErrorFrame(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(framedHandler(func(fw *otlpstream.FrameWriter, flush func()) {
		_ = fw.WriteData([]byte("payload-1"))
		_ = fw.WriteError("clickhouse is having a moment")
		flush()
	}))
	t.Cleanup(srv.Close)

	c := testOTLPClient(t, srv, 0)

	var payloads int
	err := c.consumeStream(context.Background(), otlpTraces, "erroring-trace", func([]byte) error {
		payloads++
		return nil
	})
	require.Error(t, err)
	require.ErrorContains(t, err, "clickhouse is having a moment")
	require.Equal(t, 1, payloads, "payloads before the error frame were already real")
}

// TestFetchRejectsACursorRegression: cursors are the stream's own ordering
// proof; one that repeats or goes backwards means frames were replayed or
// reordered, and a restore built on that would be quietly wrong.
func TestFetchRejectsACursorRegression(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", otlpstream.ContentType)
		_ = otlpstream.WriteFrame(w, otlpstream.FrameData, 1, []byte("payload-1"))
		_ = otlpstream.WriteFrame(w, otlpstream.FrameData, 1, []byte("payload-1-again"))
		_ = otlpstream.WriteFrame(w, otlpstream.FrameTerminal, 2, nil)
	}))
	t.Cleanup(srv.Close)

	c := testOTLPClient(t, srv, 0)

	err := c.consumeStream(context.Background(), otlpTraces, "replayed-trace", func([]byte) error {
		return nil
	})
	require.Error(t, err)
	require.ErrorContains(t, err, "cursor")
}
