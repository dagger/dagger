package telemetry

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagger/dagger/internal/cloud/auth"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	"golang.org/x/oauth2"
	"google.golang.org/protobuf/proto"
)

func cloudExporterTestServer(t *testing.T) (*httptest.Server, chan *http.Request) {
	t.Helper()
	requests := make(chan *http.Request, 16)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case requests <- r.Clone(context.Background()):
		default:
		}
	}))
	t.Cleanup(srv.Close)
	return srv, requests
}

func cloudExporterTestRequest(t *testing.T, requests chan *http.Request) *http.Request {
	t.Helper()
	select {
	case r := <-requests:
		return r
	default:
		t.Fatal("no export request reached the server")
		return nil
	}
}

// Once the initial OAuth token has expired, an export carries a refreshed
// Authorization header, and X-Dagger-Org rides along on the same request.
func TestCloudExportRefreshesAuthorizationHeader(t *testing.T) {
	t.Parallel()
	srv, requests := cloudExporterTestServer(t)
	ctx := t.Context()
	spans, _, _, err := NewCloudExporters(ctx, &auth.Cloud{
		Token: &oauth2.Token{AccessToken: "expired", Expiry: time.Now().Add(-time.Hour)},
		Org:   &auth.Org{ID: "test-org"},
	}, func(context.Context) (*oauth2.Token, error) {
		return &oauth2.Token{AccessToken: "refreshed", Expiry: time.Now().Add(time.Hour)}, nil
	}, srv.URL)
	require.NoError(t, err)
	defer spans.Shutdown(ctx) //nolint:errcheck

	require.NoError(t, spans.ExportSpans(ctx, tracetest.SpanStubs{{Name: "test-span"}}.Snapshots()))
	got := cloudExporterTestRequest(t, requests)
	require.Equal(t, "Bearer refreshed", got.Header.Get("Authorization"))
	require.Equal(t, "test-org", got.Header.Get("X-Dagger-Org"))
	require.NotEmpty(t, got.Header.Get(cloudExportHeader), "OAuth exports are sequenced too")
}

// Without a refresh callback, exports keep carrying the token they have.
func TestCloudExportWithoutRefreshKeepsToken(t *testing.T) {
	t.Parallel()
	srv, requests := cloudExporterTestServer(t)
	ctx := t.Context()
	spans, _, _, err := NewCloudExporters(ctx, &auth.Cloud{
		Token: &oauth2.Token{AccessToken: "expired", Expiry: time.Now().Add(-time.Hour)},
	}, nil, srv.URL)
	require.NoError(t, err)
	defer spans.Shutdown(ctx) //nolint:errcheck

	require.NoError(t, spans.ExportSpans(ctx, tracetest.SpanStubs{{Name: "test-span"}}.Snapshots()))
	require.Equal(t, "Bearer expired", cloudExporterTestRequest(t, requests).Header.Get("Authorization"))
}

// An explicit Cloud URL wins over DAGGER_CLOUD_URL in the process environment.
func TestNewCloudExportersUsesExplicitCloudURL(t *testing.T) {
	t.Setenv("DAGGER_CLOUD_URL", "://bad-engine-cloud-url")
	spans, logs, metrics, err := NewCloudExporters(t.Context(), &auth.Cloud{
		Token: &oauth2.Token{AccessToken: "token"},
	}, nil, "https://client-cloud.example")
	require.NoError(t, err)
	require.NotNil(t, spans)
	require.NotNil(t, logs)
	require.NotNil(t, metrics)
}

// An engine token authenticates as HTTP basic auth with a static header, and
// each export of one log exporter carries the next sequence of one writer.
func TestCloudLogExportWithEngineToken(t *testing.T) {
	t.Parallel()
	srv, requests := cloudExporterTestServer(t)
	ctx := t.Context()
	_, logs, _, err := NewCloudExporters(ctx, &auth.Cloud{
		Token: &oauth2.Token{AccessToken: "engine-token", TokenType: "Basic"},
	}, nil, srv.URL+"/prefix")
	require.NoError(t, err)
	defer logs.Shutdown(ctx) //nolint:errcheck

	var rec sdklog.Record
	rec.SetBody(log.StringValue("fact"))
	require.NoError(t, logs.Export(ctx, []sdklog.Record{rec}))
	require.NoError(t, logs.Export(ctx, []sdklog.Record{rec}))
	first := cloudExporterTestRequest(t, requests)
	second := cloudExporterTestRequest(t, requests)
	require.Equal(t, "/prefix/v1/logs", first.URL.Path)
	require.Equal(t, "Basic "+base64.StdEncoding.EncodeToString([]byte("engine-token:")), first.Header.Get("Authorization"))
	firstWriter, firstSeq, ok := strings.Cut(first.Header.Get(cloudExportHeader), "/")
	require.True(t, ok)
	secondWriter, secondSeq, ok := strings.Cut(second.Header.Get(cloudExportHeader), "/")
	require.True(t, ok)
	require.Equal(t, firstWriter, secondWriter)
	require.Equal(t, "1", firstSeq)
	require.Equal(t, "2", secondSeq)
}

// A Cloud response that never comes cannot hold the export forever: the
// request times out, the export is retried, and every record still arrives,
// in order, without a shutdown.
func TestCloudLogExportStalledRequestTimesOut(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	var (
		mu     sync.Mutex
		calls  int
		bodies []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		mu.Lock()
		calls++
		first := calls == 1
		mu.Unlock()
		if first {
			// Withhold the response until the client gives up. The server
			// notices the closed connection only once the body is read.
			<-r.Context().Done()
			return
		}
		var req collogspb.ExportLogsServiceRequest
		require.NoError(t, proto.Unmarshal(body, &req))
		mu.Lock()
		defer mu.Unlock()
		for _, resourceLogs := range req.ResourceLogs {
			for _, scopeLogs := range resourceLogs.ScopeLogs {
				for _, rec := range scopeLogs.LogRecords {
					bodies = append(bodies, rec.Body.GetStringValue())
				}
			}
		}
	}))
	t.Cleanup(srv.Close)

	_, logs, _, err := newCloudExporters(ctx, &auth.Cloud{
		Token: &oauth2.Token{AccessToken: "engine-token", TokenType: "Basic"},
	}, nil, srv.URL, 300*time.Millisecond)
	require.NoError(t, err)
	p := NewBlockingLogProcessor(logs, 4, 2, time.Millisecond)
	const records = 20
	for i := range records {
		require.NoError(t, p.OnEmit(ctx, blockingTestRecord(i)))
	}
	require.NoError(t, p.ForceFlush(ctx))

	mu.Lock()
	got := slices.Clone(bodies)
	stalled := calls > 1
	mu.Unlock()
	require.True(t, stalled, "the first request stalled and was retried")
	require.Len(t, got, records)
	for i, body := range got {
		require.Equal(t, strconv.Itoa(i), body)
	}
	require.NoError(t, p.Shutdown(ctx))
}

// A token refresh against an OAuth endpoint that never answers gives up at
// its bound.
func TestBoundedTokenRefreshGivesUp(t *testing.T) {
	t.Parallel()
	stalled := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	t.Cleanup(stalled.Close)
	refresh := boundedTokenRefresh(func(ctx context.Context) (*oauth2.Token, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, stalled.URL+"/oauth/token", nil)
		if err != nil {
			return nil, err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		resp.Body.Close()
		return &oauth2.Token{AccessToken: "unexpected"}, nil
	}, 100*time.Millisecond)

	start := time.Now()
	_, err := refresh(context.Background())
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(start), 2*time.Second)
}

// With an expired OAuth token and a refresh that stalls, every export still
// returns at its own request timeout instead of waiting on the refresh, and
// so do the other signals' exporters sharing the token source.
func TestCloudExportDoesNotWaitOnStalledRefresh(t *testing.T) {
	t.Parallel()
	srv, _ := cloudExporterTestServer(t)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	spans, logs, _, err := newCloudExporters(t.Context(), &auth.Cloud{
		Token: &oauth2.Token{AccessToken: "expired", Expiry: time.Now().Add(-time.Hour)},
		Org:   &auth.Org{ID: "test-org"},
	}, func(context.Context) (*oauth2.Token, error) {
		<-release // a refresh that ignores cancellation and never returns in time
		return nil, context.Canceled
	}, srv.URL, 50*time.Millisecond)
	require.NoError(t, err)

	for name, export := range map[string]func(context.Context) error{
		"spans": func(ctx context.Context) error {
			return spans.ExportSpans(ctx, tracetest.SpanStubs{{Name: "test-span"}}.Snapshots())
		},
		"logs": func(ctx context.Context) error {
			var rec sdklog.Record
			rec.SetBody(log.StringValue("record"))
			return logs.Export(ctx, []sdklog.Record{rec})
		},
	} {
		ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
		start := time.Now()
		require.Error(t, export(ctx), name)
		require.Less(t, time.Since(start), 2*time.Second, "%s export waited on the stalled refresh", name)
		cancel()
	}
}

// Concurrent exports waiting on an expired token share one refresh: during a
// stalled refresh, callers return at their own deadlines without starting
// refreshes of their own, a failed refresh is not retried at once, and no
// goroutine outlives the refresh's bound. A caller whose context has already
// ended never starts a refresh.
func TestSharedTokenSourceRefreshesOnce(t *testing.T) {
	var refreshes atomic.Int32
	source := &sharedTokenSource{
		token: &oauth2.Token{AccessToken: "expired", Expiry: time.Now().Add(-time.Hour)},
		refresh: boundedTokenRefresh(func(ctx context.Context) (*oauth2.Token, error) {
			refreshes.Add(1)
			<-ctx.Done() // a stalled OAuth endpoint
			return nil, context.Cause(ctx)
		}, 150*time.Millisecond),
	}

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := source.Token(cancelled)
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, refreshes.Load(), "a cancelled caller starts no refresh")

	var wg sync.WaitGroup
	const callers = 20
	start := time.Now()
	for range callers {
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
			defer cancel()
			_, err := source.Token(ctx)
			require.Error(t, err)
		})
	}
	wg.Wait()
	require.Less(t, time.Since(start), 120*time.Millisecond, "callers return at their own deadlines")

	time.Sleep(300 * time.Millisecond)
	require.Equal(t, int32(1), refreshes.Load(), "one refresh for all callers, and no retry right after it failed")
	_, err = source.Token(t.Context())
	require.Error(t, err, "the failed refresh answers during its backoff")
	require.Equal(t, int32(1), refreshes.Load())
	source.mu.Lock()
	inflight := source.inflight
	source.mu.Unlock()
	require.Nil(t, inflight, "the one refresh goroutine ended at its bound; callers started none")
}

// A refreshed token shorter-lived than oauth2's 10s expiry margin is still
// used for part of its lifetime, instead of being refreshed on every export.
func TestSharedTokenSourceUsesShortLivedTokens(t *testing.T) {
	t.Parallel()
	var refreshes atomic.Int32
	source := &sharedTokenSource{
		token: &oauth2.Token{AccessToken: "expired", Expiry: time.Now().Add(-time.Hour)},
		refresh: func(context.Context) (*oauth2.Token, error) {
			n := refreshes.Add(1)
			return &oauth2.Token{AccessToken: fmt.Sprintf("fresh-%d", n), TokenType: "Bearer", Expiry: time.Now().Add(time.Second)}, nil
		},
	}
	for range 50 {
		token, err := source.Token(t.Context())
		require.NoError(t, err)
		require.Equal(t, "fresh-1", token.AccessToken)
	}
	require.Equal(t, int32(1), refreshes.Load(), "one refresh for 50 exports within the token's first half-life")

	require.Eventually(t, func() bool {
		token, err := source.Token(t.Context())
		return err == nil && token.AccessToken == "fresh-2"
	}, 3*time.Second, 50*time.Millisecond, "refreshed again as it nears expiry")
}
