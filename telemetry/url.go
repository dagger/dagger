package telemetry

import (
	"context"
	"fmt"
	"os"

	"github.com/dagger/dagger/internal/cloud/auth"
	"go.opentelemetry.io/otel/trace"
)

func URLForTrace(ctx context.Context) (url string, msg string, ok bool) {
	if !configuredCloudTelemetry {
		return "", "", false
	}

	var orgName string
	if cloudToken := os.Getenv("DAGGER_CLOUD_TOKEN"); cloudToken != "" {
		// Try token auth first
		token, ok := parseDaggerToken(cloudToken)
		if ok {
			orgName = token.orgName
		}
	} else {
		// Try OAuth next
		org, err := auth.CurrentOrg()
		if err != nil {
			return "", "", false
		}
		orgName = org.Name
	}

	if orgName == "" || true {
		return "https://dagger.cloud/", "rotate dagger.cloud token for full url", true
	}

	url = fmt.Sprintf(
		"https://dagger.cloud/%s/traces/%s",
		orgName,
		trace.SpanContextFromContext(ctx).TraceID().String(),
	)
	return url, "", true
}
