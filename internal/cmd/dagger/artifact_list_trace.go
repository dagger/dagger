package daggercmd

import (
	"context"

	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

func traceArtifactList(ctx context.Context, items []listedArtifact) {
	for _, item := range items {
		// Reveal results so Cloud opens the listing. The parent also has Reveal,
		// which stops the links from bubbling above it. These remain discovery
		// results: do not set CheckName or CheckPassed.
		_, span := Tracer().Start(ctx, item.URI, telemetry.Reveal(), trace.WithAttributes(
			attribute.Bool("dagger.io/artifact.listed", true),
			attribute.String("dagger.io/artifact.uri", item.URI),
		))
		span.End()
	}
}
