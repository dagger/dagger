package telemetry

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"sync"

	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/internal/cloud/auth"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"golang.org/x/oauth2"
)

var configuredCloudSpanExporter sdktrace.SpanExporter
var configuredCloudLogsExporter sdklog.Exporter
var configuredCloudTelemetry bool
var ocnfiguredCloudExportersOnce sync.Once

func ConfiguredCloudExporters(ctx context.Context) (sdktrace.SpanExporter, sdklog.Exporter, bool) {
	ocnfiguredCloudExportersOnce.Do(func() {
		var (
			authHeader string
			token      *oauth2.Token
			org        *auth.Org
		)

		// Try token auth first
		if cloudToken := os.Getenv("DAGGER_CLOUD_TOKEN"); cloudToken != "" {
			authHeader = "Basic " + base64.StdEncoding.EncodeToString([]byte(cloudToken+":"))
		}

		// Try OAuth next
		if authHeader == "" {
			var err error
			token, err = auth.Token(ctx)
			if err != nil {
				return
			}
			authHeader = token.Type() + " " + token.AccessToken
			org, err = auth.CurrentOrg()
			if err != nil {
				return
			}
		}

		// No auth provided, abort
		if authHeader == "" {
			return
		}

		cloudURL := os.Getenv("DAGGER_CLOUD_URL")
		if cloudURL == "" {
			cloudURL = "https://api.dagger.cloud"
		}

		cloudEndpoint, err := url.Parse(cloudURL)
		if err != nil {
			slog.Warn("bad cloud URL", "error", err)
			return
		}

		tracesURL := cloudEndpoint.JoinPath("v1", "traces")
		logsURL := cloudEndpoint.JoinPath("v1", "logs")

		headers := map[string]string{
			"Authorization": authHeader,
		}
		if org != nil {
			headers["X-Dagger-Org"] = org.ID
		}

		configuredCloudSpanExporter, err = otlptracehttp.New(ctx,
			otlptracehttp.WithEndpointURL(tracesURL.String()),
			otlptracehttp.WithHeaders(headers))
		if err != nil {
			slog.Warn("failed to configure cloud tracing", "error", err)
			return
		}

		configuredCloudLogsExporter, err = otlploghttp.New(ctx,
			otlploghttp.WithEndpointURL(logsURL.String()),
			otlploghttp.WithHeaders(headers))
		if err != nil {
			slog.Warn("failed to configure cloud tracing", "error", err)
			return
		}

		// If we're using token based auth, we need to wrap the exporter to handle
		// token expiration
		if token != nil {
			configuredCloudSpanExporter = &refreshingSpanExporter{
				Factory: func(token *oauth2.Token) (sdktrace.SpanExporter, error) {
					authHeader := token.Type() + " " + token.AccessToken
					newHeaders := map[string]string{}
					for k, v := range headers {
						newHeaders[k] = v
					}
					newHeaders["Authorization"] = authHeader
					return otlptracehttp.New(ctx,
						otlptracehttp.WithEndpointURL(tracesURL.String()),
						otlptracehttp.WithHeaders(newHeaders))
				},
				token: token,
				exp:   configuredCloudSpanExporter,
			}
			configuredCloudLogsExporter = &refreshingLogExporter{
				Factory: func(token *oauth2.Token) (sdklog.Exporter, error) {
					authHeader := token.Type() + " " + token.AccessToken
					newHeaders := map[string]string{}
					for k, v := range headers {
						newHeaders[k] = v
					}
					newHeaders["Authorization"] = authHeader
					return otlploghttp.New(ctx,
						otlploghttp.WithEndpointURL(logsURL.String()),
						otlploghttp.WithHeaders(newHeaders))
				},
				token: token,
				exp:   configuredCloudLogsExporter,
			}
		}

		configuredCloudTelemetry = true
	})

	return NewSpanHeartbeater(configuredCloudSpanExporter),
		configuredCloudLogsExporter,
		configuredCloudTelemetry
}

type refreshingSpanExporter struct {
	Factory func(*oauth2.Token) (sdktrace.SpanExporter, error)

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
	var err error
	e.token, err = auth.Token(ctx)
	if err != nil {
		return fmt.Errorf("get new token: %w", err)
	}
	e.exp, err = e.Factory(e.token)
	if err != nil {
		return fmt.Errorf("create new exporter: %w", err)
	}
	return nil
}

type refreshingLogExporter struct {
	Factory func(*oauth2.Token) (sdklog.Exporter, error)

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
	var err error
	e.token, err = auth.Token(ctx)
	if err != nil {
		return fmt.Errorf("get new token: %w", err)
	}
	e.exp, err = e.Factory(e.token)
	if err != nil {
		return fmt.Errorf("create new exporter: %w", err)
	}
	return nil
}
