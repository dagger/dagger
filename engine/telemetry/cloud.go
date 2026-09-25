package telemetry

import (
	"context"
	"os"
	"sync"

	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/internal/cloud/auth"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

var (
	configuredCloudSpanExporter    sdktrace.SpanExporter
	configuredCloudLogsExporter    sdklog.Exporter
	configuredCloudMetricsExporter sdkmetric.Exporter
	configuredCloudTelemetry       bool
	configuredCloudExportersOnce   sync.Once
)

// ConfiguredCloudExporters returns this process's Dagger Cloud exporters,
// built once by NewCloudExporters from the process's Cloud credential:
// DAGGER_CLOUD_TOKEN, or the `dagger login` token with its current
// organization, refreshed and persisted as it expires.
func ConfiguredCloudExporters(ctx context.Context) (sdktrace.SpanExporter, sdklog.Exporter, sdkmetric.Exporter, bool) {
	configuredCloudExportersOnce.Do(func() {
		cloudAuth, status := cloudEmitAuth(ctx)
		if status.Err != nil {
			slog.Warn("failed to get cloud auth", "error", status.Err)
			return
		}
		if !status.Emitting {
			return
		}

		spans, logs, metrics, err := NewCloudExporters(ctx, cloudAuth, BoundedTokenRefresh(auth.Token), "")
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

// CloudEmitStatus says whether this process sends telemetry to Dagger Cloud,
// and why. It applies the same rules as ConfiguredCloudExporters.
type CloudEmitStatus struct {
	Emitting bool
	// Credential is the credential in use: "DAGGER_CLOUD_TOKEN" or
	// "dagger login". Empty when there is none.
	Credential string
	// Org is the org the telemetry goes to, when the credential names one.
	Org string
	// Err is set when the credential could not be read.
	Err error
}

// CloudEmitStatusFor returns this process's CloudEmitStatus. It reads the
// local credential only. It does not ask Dagger Cloud if the credential is
// valid, but it can refresh an expired login token.
func CloudEmitStatusFor(ctx context.Context) CloudEmitStatus {
	_, status := cloudEmitAuth(ctx)
	return status
}

func cloudEmitAuth(ctx context.Context) (*auth.Cloud, CloudEmitStatus) {
	var status CloudEmitStatus
	if os.Getenv("DAGGER_CLOUD_TOKEN") != "" {
		status.Credential = "DAGGER_CLOUD_TOKEN"
	}
	cloudAuth, err := auth.GetCloudAuth(ctx)
	if err != nil {
		status.Err = err
		return nil, status
	}
	if cloudAuth == nil || cloudAuth.Token == nil {
		return nil, status
	}
	if status.Credential == "" {
		status.Credential = "dagger login"
	}
	if cloudAuthHasStaticHeader(cloudAuth) {
		// An engine token names its org; an OIDC token's org is known only
		// to Dagger Cloud.
		if token, ok := auth.ParseDaggerToken(cloudAuth.Token.AccessToken); ok {
			status.Org = token.OrgName()
		}
	} else {
		if cloudAuth.Org == nil {
			// A user's login token names no organization by itself.
			return cloudAuth, status
		}
		status.Org = cloudAuth.Org.Name
	}
	status.Emitting = true
	return cloudAuth, status
}
