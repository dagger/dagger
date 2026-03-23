package telemetry

import (
	"context"
	"encoding/base64"
	"fmt"
	"maps"
	"net/url"
	"os"
	"sync"

	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/internal/cloud/auth"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
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

func NewCloudExporters(ctx context.Context, cloudAuth *auth.Cloud, tokenRefreshFn func(context.Context) (*oauth2.Token, error)) (sdktrace.SpanExporter, sdklog.Exporter, sdkmetric.Exporter, error) {
	if cloudAuth == nil || cloudAuth.Token == nil {
		return nil, nil, nil, fmt.Errorf("no cloud auth provided")
	}

	authHeader := cloudAuthHeader(cloudAuth)

	cloudURL := os.Getenv("DAGGER_CLOUD_URL")
	if cloudURL == "" {
		cloudURL = "https://api.dagger.cloud"
	}

	cloudEndpoint, err := url.Parse(cloudURL)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("bad cloud URL: %w", err)
	}

	tracesURL := cloudEndpoint.JoinPath("v1", "traces")
	logsURL := cloudEndpoint.JoinPath("v1", "logs")
	metricsURL := cloudEndpoint.JoinPath("v1", "metrics")

	headers := map[string]string{
		"Authorization": authHeader,
	}
	if cloudAuth.Org != nil {
		headers["X-Dagger-Org"] = cloudAuth.Org.ID
	}

	spanExporter, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpointURL(tracesURL.String()),
		otlptracehttp.WithHeaders(headers))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("configure cloud tracing: %w", err)
	}

	logExporter, err := otlploghttp.New(ctx,
		otlploghttp.WithEndpointURL(logsURL.String()),
		otlploghttp.WithHeaders(headers))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("configure cloud logging: %w", err)
	}

	metricExporter, err := otlpmetrichttp.New(ctx,
		otlpmetrichttp.WithEndpointURL(metricsURL.String()),
		otlpmetrichttp.WithHeaders(headers))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("configure cloud metrics: %w", err)
	}

	// If we're using OAuth token auth, wrap the exporters to handle
	// token expiration by refreshing from the corresponding refresh Token.
	if cloudAuth.Token.TokenType != "Basic" && cloudAuth.Token.TokenType != "OIDC" {
		token := cloudAuth.Token

		var wrappedSpanExporter sdktrace.SpanExporter = &refreshingSpanExporter{
			Factory: func(token *oauth2.Token) (sdktrace.SpanExporter, error) {
				authHeader := token.Type() + " " + token.AccessToken
				newHeaders := map[string]string{}
				maps.Copy(newHeaders, headers)
				newHeaders["Authorization"] = authHeader
				return otlptracehttp.New(ctx,
					otlptracehttp.WithEndpointURL(tracesURL.String()),
					otlptracehttp.WithHeaders(newHeaders))
			},
			tokenRefresh: tokenRefreshFn,
			token:        token,
			exp:          spanExporter,
		}
		var wrappedLogExporter sdklog.Exporter = &refreshingLogExporter{
			Factory: func(token *oauth2.Token) (sdklog.Exporter, error) {
				authHeader := token.Type() + " " + token.AccessToken
				newHeaders := map[string]string{}
				maps.Copy(newHeaders, headers)
				newHeaders["Authorization"] = authHeader
				return otlploghttp.New(ctx,
					otlploghttp.WithEndpointURL(logsURL.String()),
					otlploghttp.WithHeaders(newHeaders))
			},
			tokenRefresh: tokenRefreshFn,
			token:        token,
			exp:          logExporter,
		}
		var wrappedMetricExporter sdkmetric.Exporter = &refreshingMetricExporter{
			Factory: func(token *oauth2.Token) (sdkmetric.Exporter, error) {
				authHeader := token.Type() + " " + token.AccessToken
				newHeaders := map[string]string{}
				maps.Copy(newHeaders, headers)
				newHeaders["Authorization"] = authHeader
				return otlpmetrichttp.New(ctx,
					otlpmetrichttp.WithEndpointURL(metricsURL.String()),
					otlpmetrichttp.WithHeaders(newHeaders))
			},
			token: token,
			exp:   metricExporter,
		}
		return NewSpanHeartbeater(wrappedSpanExporter), wrappedLogExporter, wrappedMetricExporter, nil
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

		spans, logs, metrics, err := NewCloudExporters(ctx, cloudAuth, auth.Token)
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

type refreshingSpanExporter struct {
	tokenRefresh func(context.Context) (*oauth2.Token, error)
	Factory      func(*oauth2.Token) (sdktrace.SpanExporter, error)

	token *oauth2.Token
	exp   sdktrace.SpanExporter

	mu sync.Mutex
}

var _ sdktrace.SpanExporter = (*refreshingSpanExporter)(nil)

func (e *refreshingSpanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.refreshIfNecessary(ctx); err != nil {
		return fmt.Errorf("refresh exporter: %w", err)
	}
	return e.exp.ExportSpans(ctx, spans)
}

func (e *refreshingSpanExporter) Shutdown(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.refreshIfNecessary(ctx); err != nil {
		return fmt.Errorf("refresh exporter: %w", err)
	}
	return e.exp.Shutdown(ctx)
}

func (e *refreshingSpanExporter) refreshIfNecessary(ctx context.Context) error {
	if e.token.Valid() {
		return nil
	}
	if err := e.exp.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown old exporter: %w", err)
	}

	if e.tokenRefresh != nil {
		var err error
		e.token, err = e.tokenRefresh(ctx)
		if err != nil {
			return fmt.Errorf("refresh token: %w", err)
		}
		exp, err := e.Factory(e.token)
		if err != nil {
			return fmt.Errorf("create new exporter: %w", err)
		}
		e.exp = exp
	} else {
		slog.Warn("token expired but no refresh function provided")
	}

	return nil
}

type refreshingLogExporter struct {
	tokenRefresh func(context.Context) (*oauth2.Token, error)
	Factory      func(*oauth2.Token) (sdklog.Exporter, error)

	token *oauth2.Token
	exp   sdklog.Exporter

	mu sync.Mutex
}

var _ sdklog.Exporter = (*refreshingLogExporter)(nil)

func (e *refreshingLogExporter) Export(ctx context.Context, logs []sdklog.Record) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.refreshIfNecessary(ctx); err != nil {
		return fmt.Errorf("refresh exporter: %w", err)
	}
	return e.exp.Export(ctx, logs)
}

func (e *refreshingLogExporter) Shutdown(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.refreshIfNecessary(ctx); err != nil {
		return fmt.Errorf("refresh exporter: %w", err)
	}
	return e.exp.Shutdown(ctx)
}

func (e *refreshingLogExporter) ForceFlush(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.refreshIfNecessary(ctx); err != nil {
		return fmt.Errorf("refresh exporter: %w", err)
	}
	return e.exp.ForceFlush(ctx)
}

func (e *refreshingLogExporter) refreshIfNecessary(ctx context.Context) error {
	if e.token.Valid() {
		return nil
	}
	if err := e.exp.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown old exporter: %w", err)
	}
	if e.tokenRefresh != nil {
		var err error
		e.token, err = e.tokenRefresh(ctx)
		if err != nil {
			return fmt.Errorf("refresh token: %w", err)
		}
		exp, err := e.Factory(e.token)
		if err != nil {
			return fmt.Errorf("create new exporter: %w", err)
		}
		e.exp = exp
	} else {
		slog.Warn("token expired but no refresh function provided")
	}
	return nil
}

type refreshingMetricExporter struct {
	tokenRefresh func(context.Context, *oauth2.Token) (*oauth2.Token, error)
	Factory      func(*oauth2.Token) (sdkmetric.Exporter, error)

	token *oauth2.Token
	exp   sdkmetric.Exporter

	mu sync.Mutex
}

var _ sdkmetric.Exporter = (*refreshingMetricExporter)(nil)

func (e *refreshingMetricExporter) Temporality(ik sdkmetric.InstrumentKind) metricdata.Temporality {
	return e.exp.Temporality(ik)
}

func (e *refreshingMetricExporter) Aggregation(ik sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return e.exp.Aggregation(ik)
}

func (e *refreshingMetricExporter) Export(ctx context.Context, metrics *metricdata.ResourceMetrics) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.refreshIfNecessary(ctx); err != nil {
		return fmt.Errorf("refresh exporter: %w", err)
	}
	return e.exp.Export(ctx, metrics)
}

func (e *refreshingMetricExporter) Shutdown(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.refreshIfNecessary(ctx); err != nil {
		return fmt.Errorf("refresh exporter: %w", err)
	}
	return e.exp.Shutdown(ctx)
}

func (e *refreshingMetricExporter) ForceFlush(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.refreshIfNecessary(ctx); err != nil {
		return fmt.Errorf("refresh exporter: %w", err)
	}
	return e.exp.ForceFlush(ctx)
}

func (e *refreshingMetricExporter) refreshIfNecessary(ctx context.Context) error {
	if e.token.Valid() {
		return nil
	}
	if err := e.exp.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown old exporter: %w", err)
	}
	if e.tokenRefresh != nil {
		var err error
		e.token, err = e.tokenRefresh(ctx, e.token)
		if err != nil {
			return fmt.Errorf("refresh token: %w", err)
		}
		exp, err := e.Factory(e.token)
		if err != nil {
			return fmt.Errorf("create new exporter: %w", err)
		}
		e.exp = exp
	} else {
		slog.Warn("token expired but no refresh function provided")
	}
	return nil
}
