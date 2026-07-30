package telemetry

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/internal/cloud/auth"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"golang.org/x/oauth2"
)

var (
	configuredCloudSpanExporter    sdktrace.SpanExporter
	configuredCloudLogsExporter    sdklog.Exporter
	configuredCloudMetricsExporter sdkmetric.Exporter
	configuredCloudTelemetry       bool
	configuredCloudExportersOnce   sync.Once
)

// NewCloudExporters builds OTLP exporters for Dagger Cloud. cloudURL is the
// endpoint the client is configured with; when empty it falls back to the
// current process's DAGGER_CLOUD_URL and then the default Cloud API URL.
func NewCloudExporters(ctx context.Context, cloudAuth *auth.Cloud, tokenRefreshFn func(context.Context) (*oauth2.Token, error), cloudURL string) (sdktrace.SpanExporter, sdklog.Exporter, sdkmetric.Exporter, error) {
	if cloudAuth == nil || cloudAuth.Token == nil {
		return nil, nil, nil, fmt.Errorf("no cloud auth provided")
	}

	if cloudURL == "" {
		cloudURL = os.Getenv("DAGGER_CLOUD_URL")
	}
	if cloudURL == "" {
		cloudURL = "https://api.dagger.cloud"
	}

	cloudEndpoint, err := url.Parse(cloudURL)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("bad cloud URL: %w", err)
	}

	headers := map[string]string{}
	if cloudAuth.Org != nil {
		headers["X-Dagger-Org"] = cloudAuth.Org.ID
	}

	spanOpts := []otlptracehttp.Option{otlptracehttp.WithEndpointURL(cloudEndpoint.JoinPath("v1", "traces").String())}
	logOpts := []otlploghttp.Option{otlploghttp.WithEndpointURL(cloudEndpoint.JoinPath("v1", "logs").String())}
	metricOpts := []otlpmetrichttp.Option{otlpmetrichttp.WithEndpointURL(cloudEndpoint.JoinPath("v1", "metrics").String())}

	if cloudAuth.Token.TokenType == "Basic" || cloudAuth.Token.TokenType == "OIDC" {
		// non-expiring credentials: Cloud parses engine tokens as HTTP basic
		// auth (token as username) and OIDC tokens as a bearer JWT
		headers["Authorization"] = cloudAuthHeader(cloudAuth)
	} else {
		// OAuth access tokens expire mid-session, so instead of a static
		// Authorization header, all three exporters share an HTTP client that
		// injects the current bearer token per request. oauth2.ReuseTokenSource
		// returns the token until it expires, then refreshes and caches again;
		// without a refresh callback, keep sending the token we have.
		src := oauth2.StaticTokenSource(cloudAuth.Token)
		if tokenRefreshFn != nil {
			src = oauth2.ReuseTokenSource(cloudAuth.Token, tokenSourceFunc(tokenRefreshFn))
		}
		httpClient := &http.Client{
			Transport: &oauth2.Transport{Source: src},
			// the same timeout the exporters' default client uses
			Timeout: 10 * time.Second,
		}
		spanOpts = append(spanOpts, otlptracehttp.WithHTTPClient(httpClient))
		logOpts = append(logOpts, otlploghttp.WithHTTPClient(httpClient))
		metricOpts = append(metricOpts, otlpmetrichttp.WithHTTPClient(httpClient))
	}

	spanExporter, err := otlptracehttp.New(ctx, append(spanOpts, otlptracehttp.WithHeaders(headers))...)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("configure cloud tracing: %w", err)
	}

	logExporter, err := otlploghttp.New(ctx, append(logOpts, otlploghttp.WithHeaders(headers))...)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("configure cloud logging: %w", err)
	}

	metricExporter, err := otlpmetrichttp.New(ctx, append(metricOpts, otlpmetrichttp.WithHeaders(headers))...)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("configure cloud metrics: %w", err)
	}

	return NewSpanHeartbeater(spanExporter), logExporter, metricExporter, nil
}

// cloudAuthHeader converts a Cloud auth struct into an HTTP Authorization header value.
func cloudAuthHeader(ca *auth.Cloud) string {
	switch ca.Token.TokenType {
	case "Basic":
		return "Basic " + base64.StdEncoding.EncodeToString([]byte(ca.Token.AccessToken+":"))
	case "OIDC":
		return "Bearer " + ca.Token.AccessToken
	default:
		return ca.Token.Type() + " " + ca.Token.AccessToken
	}
}

func ConfiguredCloudExporters(ctx context.Context) (sdktrace.SpanExporter, sdklog.Exporter, sdkmetric.Exporter, bool) {
	configuredCloudExportersOnce.Do(func() {
		cloudAuth, err := auth.GetCloudAuth(ctx)
		if err != nil {
			slog.Warn("failed to get cloud auth", "error", err)
			return
		}
		if cloudAuth == nil || cloudAuth.Token == nil {
			return
		}

		spans, logs, metrics, err := NewCloudExporters(ctx, cloudAuth, auth.Token, "")
		if err != nil {
			slog.Warn("failed to configure cloud exporters", "error", err)
			return
		}

		configuredCloudSpanExporter = spans
		configuredCloudLogsExporter = logs
		configuredCloudMetricsExporter = metrics

		configuredCloudTelemetry = true
	})

	return configuredCloudSpanExporter,
		configuredCloudLogsExporter,
		configuredCloudMetricsExporter,
		configuredCloudTelemetry
}

// tokenSourceFunc adapts a context-taking token refresh callback to
// oauth2.TokenSource. Refresh callbacks capture the context they need at
// creation time (OTel invokes exporters from background goroutines), so the
// background context here carries nothing they rely on.
type tokenSourceFunc func(context.Context) (*oauth2.Token, error)

func (fn tokenSourceFunc) Token() (*oauth2.Token, error) {
	return fn(context.Background())
}
