package telemetry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dagger/dagger/internal/cloud/auth"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"golang.org/x/oauth2"
)

// Exercises a real export through the public NewCloudExporters API against a
// real HTTP server, the same way Dagger Cloud receives it. Once the initial
// OAuth token has expired, the request must carry a refreshed Authorization
// header (Cloud authenticates users by bearer token), and the X-Dagger-Org
// header must ride along on the same request (Cloud reads it per request to
// select the org). Token caching and refresh themselves are stdlib behavior
// (oauth2.ReuseTokenSource); what this locks is our wiring of it into the
// exporters' shared HTTP client.
func TestCloudExportRefreshesAuthorizationHeader(t *testing.T) {
	t.Parallel()

	headers := make(chan http.Header, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case headers <- r.Header.Clone():
		default:
		}
	}))
	defer srv.Close()

	ctx := context.Background()
	spans, _, _, err := NewCloudExporters(ctx, &auth.Cloud{
		Token: &oauth2.Token{AccessToken: "expired", Expiry: time.Now().Add(-time.Hour)},
		Org:   &auth.Org{ID: "test-org"},
	}, func(context.Context) (*oauth2.Token, error) {
		return &oauth2.Token{AccessToken: "refreshed", Expiry: time.Now().Add(time.Hour)}, nil
	}, srv.URL)
	require.NoError(t, err)
	defer spans.Shutdown(ctx) //nolint:errcheck

	require.NoError(t, spans.ExportSpans(ctx, tracetest.SpanStubs{{Name: "test-span"}}.Snapshots()))

	select {
	case got := <-headers:
		require.Equal(t, "Bearer refreshed", got.Get("Authorization"))
		require.Equal(t, "test-org", got.Get("X-Dagger-Org"))
	default:
		t.Fatal("no export request reached the server")
	}
}

// Without a refresh callback there is nothing better than the token we have:
// exports must keep carrying it (and let Cloud reject it) rather than fail
// client-side. This locks our StaticTokenSource branch in NewCloudExporters.
func TestCloudExportWithoutRefreshKeepsToken(t *testing.T) {
	t.Parallel()

	headers := make(chan http.Header, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case headers <- r.Header.Clone():
		default:
		}
	}))
	defer srv.Close()

	ctx := context.Background()
	spans, _, _, err := NewCloudExporters(ctx, &auth.Cloud{
		Token: &oauth2.Token{AccessToken: "expired", Expiry: time.Now().Add(-time.Hour)},
	}, nil, srv.URL)
	require.NoError(t, err)
	defer spans.Shutdown(ctx) //nolint:errcheck

	require.NoError(t, spans.ExportSpans(ctx, tracetest.SpanStubs{{Name: "test-span"}}.Snapshots()))

	select {
	case got := <-headers:
		require.Equal(t, "Bearer expired", got.Get("Authorization"))
	default:
		t.Fatal("no export request reached the server")
	}
}

// The client's configured Cloud URL must win over the engine process
// environment, so client and engine telemetry land on the same endpoint.
func TestNewCloudExportersUsesExplicitCloudURL(t *testing.T) {
	t.Setenv("DAGGER_CLOUD_URL", "://bad-engine-cloud-url")

	spans, logs, metrics, err := NewCloudExporters(context.Background(), &auth.Cloud{
		Token: &oauth2.Token{AccessToken: "token"},
	}, nil, "https://client-cloud.example")
	require.NoError(t, err)
	require.NotNil(t, spans)
	require.NotNil(t, logs)
	require.NotNil(t, metrics)
}
