package telemetry

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dagger/dagger/internal/cloud/auth"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"golang.org/x/oauth2"
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

type scopeTestProcessor struct {
	mu     sync.Mutex
	scopes []string
}

func (p *scopeTestProcessor) OnEmit(_ context.Context, rec *sdklog.Record) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.scopes = append(p.scopes, rec.InstrumentationScope().Name)
	return nil
}
func (*scopeTestProcessor) Enabled(context.Context, sdklog.EnabledParameters) bool { return true }
func (*scopeTestProcessor) Shutdown(context.Context) error                         { return nil }
func (*scopeTestProcessor) ForceFlush(context.Context) error                       { return nil }

// Only records of the chosen scope reach the wrapped processor.
func TestOnlyScope(t *testing.T) {
	t.Parallel()
	next := &scopeTestProcessor{}
	provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(OnlyScope("dagger.io/cache", next)))
	for _, scope := range []string{"dagger.io/cache", "dagger.io/engine", "other"} {
		var rec log.Record
		rec.SetBody(log.StringValue(scope))
		provider.Logger(scope).Emit(t.Context(), rec)
	}
	require.NoError(t, provider.Shutdown(t.Context()))
	require.Equal(t, []string{"dagger.io/cache"}, next.scopes)
}
