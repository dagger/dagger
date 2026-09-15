package telemetry

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/internal/cloud/auth"
	"go.opentelemetry.io/otel/trace"
)

func URLForTrace(ctx context.Context) (url string, msg string, ok bool) {
	if !configuredCloudTelemetry {
		return "", "", false
	}

	orgName, err := auth.CurrentOrgName()
	if err != nil {
		return "", "", false
	}

	if orgName == "" {
		return "https://dagger.cloud/", "rotate dagger.cloud token for full url", true
	}

	url = fmt.Sprintf(
		"https://dagger.cloud/%s/traces/%s",
		orgName,
		trace.SpanContextFromContext(ctx).TraceID().String(),
	)
	return url, "", true
}
