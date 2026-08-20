package cloud

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
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

type callbackProbeSink struct {
	active       atomic.Int32
	overlap      atomic.Bool
	spans        atomic.Int32
	logs         atomic.Int32
	metrics      atomic.Int32
	seals        atomic.Int32
	sealCanceled atomic.Bool
	sealErr      error
}

func (s *callbackProbeSink) callback(counter *atomic.Int32) error {
	if s.active.Add(1) != 1 {
		s.overlap.Store(true)
	}
	defer s.active.Add(-1)
	counter.Add(1)
	// Keep the callback active long enough for independently fetched events to
	// overlap if FetchTrace forgets to serialize the sink.
	time.Sleep(20 * time.Millisecond)
	return nil
}

func (s *callbackProbeSink) ImportSpans(context.Context, *coltracepb.ExportTraceServiceRequest) error {
	return s.callback(&s.spans)
}

func (s *callbackProbeSink) ImportLogs(context.Context, *collogspb.ExportLogsServiceRequest) error {
	return s.callback(&s.logs)
}

func (s *callbackProbeSink) ImportMetrics(context.Context, *colmetricspb.ExportMetricsServiceRequest) error {
	return s.callback(&s.metrics)
}

func (s *callbackProbeSink) Seal(ctx context.Context) error {
	if ctx.Err() != nil {
		s.sealCanceled.Store(true)
	}
	if err := s.callback(&s.seals); err != nil {
		return err
	}
	return s.sealErr
}

func TestFetchTraceOverlapsStreamsAndSerializesSink(t *testing.T) {
	t.Parallel()

	traceStarted := make(chan struct{})
	logsStarted := make(chan struct{})
	releaseTrace := make(chan struct{})
	var traceOnce, logsOnce sync.Once

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		switch {
		case strings.Contains(r.URL.Path, "/traces/"):
			traceOnce.Do(func() { close(traceStarted) })
			fmt.Fprint(w, ": connected\n\n")
			w.(http.Flusher).Flush()
			select {
			case <-releaseTrace:
				fmt.Fprint(w, "data: {}\n\n")
			case <-r.Context().Done():
			}
		case strings.Contains(r.URL.Path, "/logs/"):
			logsOnce.Do(func() { close(logsStarted) })
			fmt.Fprint(w, "data: {}\n\n")
		case strings.Contains(r.URL.Path, "/metrics/"):
			fmt.Fprint(w, "data: {}\n\n")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	client := stalledOTLPClient(t, srv, 0)
	sink := new(callbackProbeSink)
	fetchDone := make(chan error, 1)
	go func() {
		fetchDone <- client.FetchTrace(t.Context(), "trace-id", sink)
	}()

	require.Eventually(t, func() bool {
		select {
		case <-traceStarted:
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond, "trace stream did not start")
	require.Eventually(t, func() bool {
		select {
		case <-logsStarted:
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond,
		"logs request did not begin while the trace stream was blocked")
	close(releaseTrace)

	require.NoError(t, <-fetchDone)
	require.False(t, sink.overlap.Load(), "TraceImportSink callbacks ran concurrently")
	require.EqualValues(t, 1, sink.spans.Load())
	require.EqualValues(t, 1, sink.logs.Load())
	require.EqualValues(t, 1, sink.metrics.Load())
	require.EqualValues(t, 1, sink.seals.Load())
}

func TestFetchTraceSealsPartialSpansAfterStall(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if strings.Contains(r.URL.Path, "/traces/") {
			fmt.Fprint(w, "data: {}\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		}
	}))
	t.Cleanup(srv.Close)

	sealFailure := errors.New("seal failed")
	sink := &callbackProbeSink{sealErr: sealFailure}
	client := stalledOTLPClient(t, srv, 0).WithStallTimeout(100 * time.Millisecond)
	err := client.FetchTrace(t.Context(), "partial-trace", sink)

	require.ErrorContains(t, err, "no data for 100ms")
	require.True(t, errors.Is(err, ErrStreamStalled), "stream error was masked: %v", err)
	require.True(t, errors.Is(err, sealFailure), "seal error was not joined: %v", err)
	require.EqualValues(t, 1, sink.spans.Load(), "the partial span snapshot was not imported")
	require.EqualValues(t, 1, sink.seals.Load(), "partial imports must be sealed exactly once")
	require.False(t, sink.sealCanceled.Load(), "Seal received the errgroup's canceled context")
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
	require.True(t, errors.Is(err, ErrStreamStalled))
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
	base := stalledOTLPClient(t, srv, 0)
	c := base.WithStallTimeout(250 * time.Millisecond)
	require.NotSame(t, base, c)
	require.Zero(t, base.stall, "WithStallTimeout must not mutate the original client")
	require.Equal(t, 250*time.Millisecond, c.stall)

	var events int
	err := c.consumeSSE(context.Background(), otlpTraces, "kept-alive-trace", func([]byte) error {
		events++
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 2, events, "both events must arrive; the keepalives kept the stream alive")
}
