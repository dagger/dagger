package cloud

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dagger/dagger/internal/cloud/otlpstream"
	"github.com/stretchr/testify/require"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"golang.org/x/oauth2"

	"github.com/dagger/dagger/internal/cloud/auth"
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

type callbackProbeSink struct {
	active       atomic.Int32
	overlap      atomic.Bool
	spans        atomic.Int32
	logs         atomic.Int32
	metrics      atomic.Int32
	seals        atomic.Int32
	sealCanceled atomic.Bool
	sealErr      error
	// synctest cannot advance its clock while another callback waits on the
	// serialization mutex, so admission tests disable the overlap probe delay.
	skipDelay bool
}

func (s *callbackProbeSink) callback(counter *atomic.Int32) error {
	if s.active.Add(1) != 1 {
		s.overlap.Store(true)
	}
	defer s.active.Add(-1)
	counter.Add(1)
	// Keep the callback active long enough for independently fetched events to
	// overlap if FetchTrace forgets to serialize the sink.
	if !s.skipDelay {
		time.Sleep(20 * time.Millisecond)
	}
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
		w.Header().Set("Content-Type", otlpstream.ContentType)
		fw := otlpstream.NewFrameWriter(w)
		switch {
		case strings.Contains(r.URL.Path, "/traces/"):
			traceOnce.Do(func() { close(traceStarted) })
			_ = fw.WriteHeartbeat()
			w.(http.Flusher).Flush()
			select {
			case <-releaseTrace:
				_ = fw.WriteData([]byte{0x0a, 0x00})
			case <-r.Context().Done():
			}
		case strings.Contains(r.URL.Path, "/logs/"):
			logsOnce.Do(func() { close(logsStarted) })
			_ = fw.WriteData([]byte{0x0a, 0x00})
		case strings.Contains(r.URL.Path, "/metrics/"):
			_ = fw.WriteData([]byte{0x0a, 0x00})
		default:
			http.NotFound(w, r)
		}
		_ = fw.WriteTerminal()
	}))
	t.Cleanup(srv.Close)

	client := testOTLPClient(t, srv, 0)
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
		w.Header().Set("Content-Type", otlpstream.ContentType)
		fw := otlpstream.NewFrameWriter(w)
		if strings.Contains(r.URL.Path, "/traces/") {
			_ = fw.WriteData([]byte{0x0a, 0x00})
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		}
		_ = fw.WriteTerminal()
	}))
	t.Cleanup(srv.Close)

	sealFailure := errors.New("seal failed")
	sink := &callbackProbeSink{sealErr: sealFailure}
	client := testOTLPClient(t, srv, 0).WithStallTimeout(100 * time.Millisecond)
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
	err := c.consumeStream(context.Background(), otlpTraces, "stalled-trace", nil, func([]byte) error {
		payloads++
		return nil
	})
	require.Error(t, err)
	require.ErrorContains(t, err, "stalled")
	require.ErrorIs(t, err, ErrStreamStalled)
	require.ErrorContains(t, err, "after 1 payloads")
	require.Equal(t, 1, payloads, "the payload before the stall was delivered")
	require.Less(t, time.Since(start), 10*time.Second,
		"the watchdog, not the test timeout, must be what ended the read")
	require.Contains(t, c.StatsSummary(), "cloud fetch: 1 requests", "stalls must not reopen the stream")
}

// Admission tests use an in-memory transport: real network I/O is not durably
// blocking in synctest, whereas timers and channels advance its fake clock.
type otlpTestTransport func(*http.Request) (*http.Response, error)

func (f otlpTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type otlpTestBody struct {
	io.Reader
	close func() error
}

func (b *otlpTestBody) Close() error { return b.close() }

type otlpTestReader func([]byte) (int, error)

func (f otlpTestReader) Read(p []byte) (int, error) { return f(p) }

func testOTLPTransportClient(fn otlpTestTransport) *OTLPClient {
	return &OTLPClient{
		h:     &http.Client{Transport: fn},
		u:     &url.URL{Scheme: "https", Host: "cloud.example"},
		stats: newClientStats(),
	}
}

func testOTLPResponse(status int, retryAfter string, body io.ReadCloser) *http.Response {
	header := http.Header{"Content-Type": {otlpstream.ContentType}}
	if retryAfter != "" {
		header.Set("Retry-After", retryAfter)
	}
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     header,
		Body:       body,
	}
}

func testOTLPFrames(t *testing.T) string {
	t.Helper()
	var body strings.Builder
	fw := otlpstream.NewFrameWriter(&body)
	require.NoError(t, fw.WriteData([]byte{0x0a, 0x00}))
	require.NoError(t, fw.WriteTerminal())
	return body.String()
}

func TestOTLPAdmissionRetriesKeepSiblingStreamsRunning(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sink := &callbackProbeSink{skipDelay: true}
		frames := testOTLPFrames(t)
		var traces, logs, metrics atomic.Int32
		var rejectedClosed atomic.Bool
		start := time.Now()
		client := testOTLPTransportClient(func(req *http.Request) (*http.Response, error) {
			switch {
			case strings.Contains(req.URL.Path, "/traces/"):
				if traces.Add(1) == 1 {
					return testOTLPResponse(http.StatusServiceUnavailable, "2", &otlpTestBody{
						Reader: strings.NewReader("admission queue full"),
						close: func() error {
							rejectedClosed.Store(true)
							return nil
						},
					}), nil
				}
				require.Equal(t, 2*time.Second, time.Since(start))
				require.True(t, rejectedClosed.Load())
				require.EqualValues(t, 1, sink.logs.Load(), "logs must progress during admission backoff")
				require.EqualValues(t, 1, sink.metrics.Load(), "metrics must progress during admission backoff")
			case strings.Contains(req.URL.Path, "/logs/"):
				logs.Add(1)
			case strings.Contains(req.URL.Path, "/metrics/"):
				metrics.Add(1)
			}
			return testOTLPResponse(http.StatusOK, "", io.NopCloser(strings.NewReader(frames))), nil
		})
		require.NoError(t, client.FetchTrace(t.Context(), "trace-id", sink))
		require.EqualValues(t, 2, traces.Load())
		require.EqualValues(t, 1, logs.Load())
		require.EqualValues(t, 1, metrics.Load())
		require.EqualValues(t, 1, sink.spans.Load())
		require.EqualValues(t, 1, sink.logs.Load())
		require.EqualValues(t, 1, sink.metrics.Load())
		require.EqualValues(t, 1, sink.seals.Load())
		require.False(t, sink.overlap.Load())
		require.Contains(t, client.StatsSummary(), "cloud fetch: 4 requests")
	})
}

func TestOTLPAdmissionRetryLimits(t *testing.T) {
	for _, tc := range []struct {
		name       string
		retryAfter string
		attempts   int
		elapsed    time.Duration
	}{
		{"attempt limit", "0", 6, 0},
		{"time budget", "11", 3, 22 * time.Second},
		{"oversized delay", "31", 1, 0},
		{"overflowing delay", "99999999999999999999999999999999", 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var attempts, closed int
				client := testOTLPTransportClient(func(*http.Request) (*http.Response, error) {
					attempts++
					return testOTLPResponse(http.StatusServiceUnavailable, tc.retryAfter, &otlpTestBody{
						Reader: strings.NewReader("admission queue full"),
						close:  func() error { closed++; return nil },
					}), nil
				})
				start := time.Now()
				err := client.consumeStream(t.Context(), otlpTraces, "trace-id", nil, func([]byte) error {
					t.Error("rejected response reached callback")
					return nil
				})
				require.ErrorContains(t, err, "503 Service Unavailable")
				require.ErrorContains(t, err, "admission queue full")
				if tc.name != "attempt limit" {
					require.ErrorIs(t, err, context.DeadlineExceeded)
				}
				require.Equal(t, tc.attempts, attempts)
				require.Equal(t, attempts, closed)
				require.Equal(t, tc.elapsed, time.Since(start), "must not shorten Retry-After to fit the budget")
			})
		})
	}
}

func TestOTLPAdmissionBackoffCancellationClosesRejectedBody(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		var attempts int
		closed := false
		client := testOTLPTransportClient(func(*http.Request) (*http.Response, error) {
			attempts++
			return testOTLPResponse(http.StatusServiceUnavailable, "10", &otlpTestBody{
				Reader: strings.NewReader("busy"),
				close:  func() error { closed = true; return nil },
			}), nil
		})
		done := make(chan error, 1)
		go func() {
			resp, err := client.openStream(ctx, otlpTraces, "https://cloud.example/v1/traces/id")
			if resp != nil {
				resp.Body.Close()
			}
			done <- err
		}()
		synctest.Wait()
		require.True(t, closed, "rejected body must close before sleeping, not on the next request")
		cancel()
		require.ErrorIs(t, <-done, context.Canceled)
		require.Equal(t, 1, attempts)
	})
}

func TestParseOTLPRetryAfter(t *testing.T) {
	now := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		value string
		want  time.Duration
		valid bool
	}{
		{"0", 0, true},
		{"2", 2 * time.Second, true},
		{" 2 ", 2 * time.Second, true},
		{now.Add(4 * time.Second).Format(http.TimeFormat), 4 * time.Second, true},
		{now.Add(-time.Second).Format(http.TimeFormat), 0, true},
		{"99999999999999999999999999999999", time.Duration(math.MaxInt64), true},
		{"9223372037", time.Duration(math.MaxInt64), true},
		{"", 0, false},
		{"invalid", 0, false},
		{"-1", 0, false},
		{"+1", 0, false},
		{"1.5", 0, false},
	} {
		t.Run(tc.value, func(t *testing.T) {
			delay, valid := parseOTLPRetryAfter(tc.value, now)
			require.Equal(t, tc.valid, valid)
			require.Equal(t, tc.want, delay)
		})
	}
}

func TestOTLPAdmissionBackoff(t *testing.T) {
	for retry, ceiling := range []time.Duration{
		500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second, 5 * time.Second, 5 * time.Second,
	} {
		for range 100 {
			delay := otlpAdmissionBackoff(retry)
			require.GreaterOrEqual(t, delay, ceiling/2)
			require.LessOrEqual(t, delay, ceiling)
		}
	}
}

func TestOTLPAdmissionFallbackBackoff(t *testing.T) {
	for _, value := range []string{"", "invalid"} {
		t.Run(value, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var attempts int
				var previous time.Time
				client := testOTLPTransportClient(func(*http.Request) (*http.Response, error) {
					if attempts > 0 {
						ceiling := min(500*time.Millisecond<<uint(attempts-1), 5*time.Second)
						require.GreaterOrEqual(t, time.Since(previous), ceiling/2)
						require.LessOrEqual(t, time.Since(previous), ceiling)
					}
					previous = time.Now()
					attempts++
					return testOTLPResponse(http.StatusServiceUnavailable, value, io.NopCloser(strings.NewReader("busy"))), nil
				})
				resp, err := client.openStream(t.Context(), otlpTraces, "https://cloud.example/v1/traces/id")
				if resp != nil {
					require.NoError(t, resp.Body.Close())
				}
				require.Error(t, err)
				require.Equal(t, 6, attempts)
			})
		})
	}
}

func TestOTLPAdmissionRetriesAndOAuthRefresh(t *testing.T) {
	for _, statuses := range [][]int{
		{401, 503, 200},
		{503, 401, 200},
		{503, 401, 503, 401},
		{401, 503, 503, 503, 503, 503, 503},
	} {
		t.Run(fmt.Sprint(statuses), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var attempts, refreshes int
				client := testOTLPTransportClient(func(req *http.Request) (*http.Response, error) {
					require.Less(t, attempts, len(statuses), "unexpected extra retry")
					wantAuth := "Bearer expired"
					if refreshes > 0 {
						wantAuth = "Bearer refreshed"
					}
					require.Equal(t, wantAuth, req.Header.Get("Authorization"))
					status := statuses[attempts]
					attempts++
					return testOTLPResponse(status, "0", io.NopCloser(strings.NewReader("busy"))), nil
				})
				client.auth = &otlpAuthState{
					header: "Bearer expired",
					token:  &oauth2.Token{AccessToken: "expired", RefreshToken: "refresh", TokenType: "Bearer"},
					refresh: func(context.Context, *oauth2.Token) (*oauth2.Token, error) {
						refreshes++
						return &oauth2.Token{AccessToken: "refreshed", TokenType: "Bearer"}, nil
					},
				}
				resp, err := client.openStream(t.Context(), otlpTraces, "https://cloud.example/v1/traces/id")
				if statuses[len(statuses)-1] == 200 {
					require.NoError(t, err)
					require.NoError(t, resp.Body.Close())
				} else {
					require.ErrorContains(t, err, fmt.Sprint(statuses[len(statuses)-1]))
				}
				require.Equal(t, len(statuses), attempts)
				require.Equal(t, 1, refreshes)
			})
		})
	}
}

func TestOTLPAdmissionDoesNotRetryOtherFailures(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var attempts int
			client := testOTLPTransportClient(func(*http.Request) (*http.Response, error) {
				attempts++
				return testOTLPResponse(status, "0", io.NopCloser(strings.NewReader("not admission"))), nil
			})
			resp, err := client.openStream(t.Context(), otlpTraces, "https://cloud.example/v1/traces/id")
			if resp != nil {
				require.NoError(t, resp.Body.Close())
			}
			require.ErrorContains(t, err, http.StatusText(status))
			require.Equal(t, 1, attempts)
		})
	}
	t.Run("transport", func(t *testing.T) {
		var attempts int
		failure := errors.New("network unavailable")
		client := testOTLPTransportClient(func(*http.Request) (*http.Response, error) {
			attempts++
			return nil, failure
		})
		resp, err := client.openStream(t.Context(), otlpTraces, "https://cloud.example/v1/traces/id")
		if resp != nil {
			require.NoError(t, resp.Body.Close())
		}
		require.ErrorIs(t, err, failure)
		require.Equal(t, 1, attempts)
	})
}

func TestOTLPAdmissionBudgetCoversBlockedIO(t *testing.T) {
	for _, stage := range []string{"first rejection body", "retry request", "retry rejection body"} {
		t.Run(stage, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var attempts int
				var closed atomic.Bool
				client := testOTLPTransportClient(func(req *http.Request) (*http.Response, error) {
					attempts++
					if attempts == 1 && stage != "first rejection body" {
						return testOTLPResponse(http.StatusServiceUnavailable, "1", io.NopCloser(strings.NewReader("busy"))), nil
					}
					if stage == "retry request" {
						<-req.Context().Done()
						return nil, req.Context().Err()
					}
					return testOTLPResponse(http.StatusServiceUnavailable, "0", &otlpTestBody{
						Reader: otlpTestReader(func([]byte) (int, error) {
							<-req.Context().Done()
							return 0, req.Context().Err()
						}),
						close: func() error { closed.Store(true); return nil },
					}), nil
				})
				start := time.Now()
				resp, err := client.openStream(t.Context(), otlpTraces, "https://cloud.example/v1/traces/id")
				if resp != nil {
					require.NoError(t, resp.Body.Close())
				}
				require.ErrorIs(t, err, context.DeadlineExceeded)
				require.Equal(t, 30*time.Second, time.Since(start))
				if stage == "first rejection body" {
					require.Equal(t, 1, attempts)
				} else {
					require.Equal(t, 2, attempts)
				}
				if stage != "retry request" {
					require.True(t, closed.Load())
				}
			})
		})
	}
}

func TestOTLPAdmissionBudgetDoesNotLimitAcceptedBody(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var attempts int
		frames := strings.NewReader(testOTLPFrames(t))
		client := testOTLPTransportClient(func(req *http.Request) (*http.Response, error) {
			attempts++
			if attempts == 1 {
				// The admission budget starts at the first 503, not when the
				// initial request began waiting for response headers.
				time.Sleep(time.Minute)
				return testOTLPResponse(http.StatusServiceUnavailable, "1", io.NopCloser(strings.NewReader("busy"))), nil
			}
			var once sync.Once
			return testOTLPResponse(http.StatusOK, "", &otlpTestBody{
				Reader: otlpTestReader(func(p []byte) (int, error) {
					once.Do(func() { time.Sleep(time.Minute) })
					if err := req.Context().Err(); err != nil {
						return 0, err
					}
					return frames.Read(p)
				}),
				close: func() error { return nil },
			}), nil
		})
		var payloads int
		err := client.consumeStream(t.Context(), otlpTraces, "trace-id", nil, func([]byte) error {
			payloads++
			return nil
		})
		require.NoError(t, err)
		require.Equal(t, 2, attempts)
		require.Equal(t, 1, payloads)
	})
}

func TestOTLPRefreshesUnauthorizedRequest(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch r.Header.Get("Authorization") {
		case "Bearer expired":
			http.Error(w, "Failed to validate JWT.", http.StatusUnauthorized)
		case "Bearer refreshed":
			w.Header().Set("Content-Type", otlpstream.ContentType)
			fw := otlpstream.NewFrameWriter(w)
			_ = fw.WriteData([]byte{0x0a, 0x00})
			_ = fw.WriteTerminal()
		default:
			http.Error(w, "unexpected authorization", http.StatusForbidden)
		}
	}))
	t.Cleanup(srv.Close)

	var refreshes atomic.Int32
	client := testOTLPClient(t, srv, 0)
	client.auth = &otlpAuthState{
		header: "Bearer expired",
		token:  &oauth2.Token{AccessToken: "expired", RefreshToken: "refresh", TokenType: "Bearer"},
		refresh: func(_ context.Context, token *oauth2.Token) (*oauth2.Token, error) {
			refreshes.Add(1)
			require.Equal(t, "refresh", token.RefreshToken)
			return &oauth2.Token{AccessToken: "refreshed", RefreshToken: "next-refresh", TokenType: "Bearer"}, nil
		},
	}

	var events int
	err := client.consumeStream(t.Context(), otlpTraces, "trace-id", nil, func([]byte) error {
		events++
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 1, events)
	require.EqualValues(t, 1, refreshes.Load())
	require.EqualValues(t, 2, requests.Load())
	require.Contains(t, client.StatsSummary(), "cloud fetch: 2 requests")
}

func TestOTLPConcurrentUnauthorizedRequestsShareRefresh(t *testing.T) {
	t.Parallel()

	allUnauthorized := make(chan struct{})
	var expiredRequests atomic.Int32
	var refreshedRequests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("Authorization") {
		case "Bearer expired":
			if expiredRequests.Add(1) == 3 {
				close(allUnauthorized)
			}
			<-allUnauthorized
			http.Error(w, "Failed to validate JWT.", http.StatusUnauthorized)
		case "Bearer refreshed":
			refreshedRequests.Add(1)
			w.Header().Set("Content-Type", otlpstream.ContentType)
			fw := otlpstream.NewFrameWriter(w)
			_ = fw.WriteData([]byte{0x0a, 0x00})
			_ = fw.WriteTerminal()
		default:
			http.Error(w, "unexpected authorization", http.StatusForbidden)
		}
	}))
	t.Cleanup(srv.Close)

	var refreshes atomic.Int32
	client := testOTLPClient(t, srv, 0)
	client.auth = &otlpAuthState{
		header: "Bearer expired",
		token:  &oauth2.Token{AccessToken: "expired", RefreshToken: "refresh", TokenType: "Bearer"},
		refresh: func(context.Context, *oauth2.Token) (*oauth2.Token, error) {
			refreshes.Add(1)
			return &oauth2.Token{AccessToken: "refreshed", RefreshToken: "next-refresh", TokenType: "Bearer"}, nil
		},
	}

	sink := new(callbackProbeSink)
	require.NoError(t, client.FetchTrace(t.Context(), "trace-id", sink))
	require.EqualValues(t, 1, refreshes.Load())
	require.EqualValues(t, 3, expiredRequests.Load())
	require.EqualValues(t, 3, refreshedRequests.Load())
	require.Contains(t, client.StatsSummary(), "cloud fetch: 6 requests")
}

func TestOTLPStaticCredentialsRemainUnauthorized(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		token *oauth2.Token
	}{
		{
			name:  "Basic cloud token",
			token: &oauth2.Token{AccessToken: "dag_org_token", TokenType: "Basic", RefreshToken: "must-not-refresh"},
		},
		{
			name:  "CI OIDC token",
			token: &oauth2.Token{AccessToken: "oidc-token", TokenType: "OIDC", RefreshToken: "must-not-refresh"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				http.Error(w, "Failed to validate JWT.", http.StatusUnauthorized)
			}))
			t.Cleanup(srv.Close)

			client, err := NewOTLPClient(t.Context(), &auth.Cloud{Token: test.token})
			require.NoError(t, err)
			client, err = client.WithBaseURL(srv.URL)
			require.NoError(t, err)
			client = client.WithStallTimeout(0)

			err = client.consumeStream(t.Context(), otlpTraces, "trace-id", nil, func([]byte) error { return nil })
			require.ErrorContains(t, err, "401 Unauthorized")
			require.ErrorContains(t, err, "Failed to validate JWT.")
			require.EqualValues(t, 1, requests.Load())
			require.Contains(t, client.StatsSummary(), "cloud fetch: 1 requests")
		})
	}
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
	base := testOTLPClient(t, srv, 0)
	c := base.WithStallTimeout(250 * time.Millisecond)
	require.Zero(t, base.stall)

	var payloads int
	err := c.consumeStream(context.Background(), otlpTraces, "kept-alive-trace", nil, func([]byte) error {
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

	err := c.consumeStream(context.Background(), otlpTraces, "sse-era-trace", nil, func([]byte) error {
		t.Fatal("no payload must be decoded from a mis-negotiated response")
		return nil
	})
	require.Error(t, err)
	require.ErrorContains(t, err, `"text/event-stream"`)
	require.ErrorContains(t, err, otlpstream.ContentType)
	require.Contains(t, c.StatsSummary(), "cloud fetch: 1 requests", "protocol errors must not reopen the stream")
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
	err := c.consumeStream(context.Background(), otlpTraces, "truncated-trace", nil, func([]byte) error {
		payloads++
		return nil
	})
	require.Error(t, err)
	require.ErrorContains(t, err, "truncated")
	require.ErrorContains(t, err, "terminal frame")
	require.ErrorContains(t, err, "after 1 payloads")
	require.Equal(t, 1, payloads)
	require.Contains(t, c.StatsSummary(), "cloud fetch: 1 requests", "truncation must not reopen the stream")
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
	err := c.consumeStream(context.Background(), otlpTraces, "erroring-trace", nil, func([]byte) error {
		payloads++
		return nil
	})
	require.Error(t, err)
	require.ErrorContains(t, err, "clickhouse is having a moment")
	require.Equal(t, 1, payloads, "payloads before the error frame were already real")
	require.Contains(t, c.StatsSummary(), "cloud fetch: 1 requests", "server error frames must not reopen the stream")
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

	err := c.consumeStream(context.Background(), otlpTraces, "replayed-trace", nil, func([]byte) error {
		return nil
	})
	require.Error(t, err)
	require.ErrorContains(t, err, "cursor")
	require.Contains(t, c.StatsSummary(), "cloud fetch: 1 requests", "cursor regression must not reopen the stream")
}

// The selective fetches put the selection on the wire as the server's
// parseTraceStreamOptions / parseLogStreamOptions read it (dagger.io
// api/otlp/stream_options.go): root spelled out, listen repeated, times as
// RFC3339Nano in UTC, and nothing sent for the zero value that the server
// would not default to itself.
func TestSpanSelectionQuery(t *testing.T) {
	t.Parallel()

	require.Equal(t, "root=true", SpanSelection{}.query().Encode(),
		"the zero selection is the server's default: roots, whole trace")

	before := time.Date(2026, 9, 17, 12, 0, 0, 500, time.FixedZone("x", 3600))
	after := time.Date(2026, 9, 17, 11, 0, 0, 0, time.UTC)
	q := SpanSelection{
		NoRoot:      true,
		Listen:      []string{"0102030405060708", "1112131415161718"},
		Incremental: true,
		Before:      &before,
		After:       &after,
		DagUIView:   true,
	}.query()
	require.Equal(t, "false", q.Get("root"))
	require.Equal(t, []string{"0102030405060708", "1112131415161718"}, q["listen"])
	require.Equal(t, "true", q.Get("incremental"))
	require.Equal(t, "2026-09-17T11:00:00.0000005Z", q.Get("before"), "before must be sent in UTC")
	require.Equal(t, "2026-09-17T11:00:00Z", q.Get("after"))
	require.Equal(t, "dagui", q.Get("view"))
}

func TestLogSelectionQuery(t *testing.T) {
	t.Parallel()

	require.Empty(t, LogSelection{}.query().Encode(),
		"the zero selection is the server's default: every record of the trace")

	after := time.Date(2026, 9, 17, 11, 0, 0, 0, time.UTC)
	q := LogSelection{
		SpanID:      "0102030405060708",
		Descendants: true,
		After:       &after,
		Records:     LogRecordsLogs,
	}.query()
	require.Equal(t, "0102030405060708", q.Get("span_id"))
	require.Equal(t, "true", q.Get("descendants"))
	require.Equal(t, "2026-09-17T11:00:00Z", q.Get("after"))
	require.Equal(t, "logs", q.Get("records"))
}

func TestFetchSpansAndLogsSendTheSelection(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var requests []*url.URL
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.URL)
		mu.Unlock()
		w.Header().Set("Content-Type", otlpstream.ContentType)
		fw := otlpstream.NewFrameWriter(w)
		_ = fw.WriteHeartbeat()
		_ = fw.WriteData([]byte{0x0a, 0x00}) // one empty resource group
		_ = fw.WriteTerminal()
	}))
	t.Cleanup(srv.Close)

	c := testOTLPClient(t, srv, 0)

	var spanBatches int
	require.NoError(t, c.FetchSpans(t.Context(), "trace-id", SpanSelection{
		NoRoot:      true,
		Listen:      []string{"0102030405060708"},
		Incremental: true,
		DagUIView:   true,
	}, func(_ context.Context, req *coltracepb.ExportTraceServiceRequest) error {
		require.Len(t, req.GetResourceSpans(), 1)
		spanBatches++
		return nil
	}))
	require.Equal(t, 1, spanBatches, "heartbeats must not reach the callback")

	var logBatches int
	require.NoError(t, c.FetchLogs(t.Context(), "trace-id", LogSelection{
		SpanID:      "0102030405060708",
		Descendants: true,
		Records:     LogRecordsLogs,
	}, func(_ context.Context, req *collogspb.ExportLogsServiceRequest) error {
		require.Len(t, req.GetResourceLogs(), 1)
		logBatches++
		return nil
	}))
	require.Equal(t, 1, logBatches)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, requests, 2)
	require.Equal(t, "/v1/traces/trace-id", requests[0].Path)
	require.Equal(t, url.Values{
		"root": {"false"}, "listen": {"0102030405060708"}, "incremental": {"true"}, "view": {"dagui"},
	}, requests[0].Query())
	require.Equal(t, "/v1/logs/trace-id", requests[1].Path)
	require.Equal(t, url.Values{
		"span_id": {"0102030405060708"}, "descendants": {"true"}, "records": {"logs"},
	}, requests[1].Query())

	require.Error(t, c.FetchSpans(t.Context(), "", SpanSelection{}, nil))
	require.Error(t, c.FetchLogs(t.Context(), "", LogSelection{}, nil))
}
