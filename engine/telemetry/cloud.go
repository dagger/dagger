package telemetry

import (
	"context"
	"encoding/base64"
	"net/url"
	"os"
	"sync"

	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/internal/cloud/auth"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

var configuredCloudSpanExporter sdktrace.SpanExporter
var configuredCloudLogsExporter sdklog.Exporter
var configuredCloudTelemetry bool
var ocnfiguredCloudExportersOnce sync.Once

func ConfiguredCloudExporters(ctx context.Context) (sdktrace.SpanExporter, sdklog.Exporter, bool) {
	ocnfiguredCloudExportersOnce.Do(func() {
		var (
			authHeader string
			org        *auth.Org
		)

		// Try token auth first
		if cloudToken := os.Getenv("DAGGER_CLOUD_TOKEN"); cloudToken != "" {
			authHeader = "Basic " + base64.StdEncoding.EncodeToString([]byte(cloudToken+":"))
		}
		// Try OAuth next
		if authHeader == "" {
			token, err := auth.Token(ctx)
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

		configuredCloudTelemetry = true
	})

	return NewSpanHeartbeater(configuredCloudSpanExporter),
		configuredCloudLogsExporter,
		configuredCloudTelemetry
}
