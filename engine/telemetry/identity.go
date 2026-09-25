package telemetry

import (
	"context"
	"log/slog"

	"github.com/dagger/dagger/engine"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
)

const (
	EngineInstanceAttr = "dagger.io/engine.instance.id"
	SessionIDAttr      = "dagger.io/session.id"
	OperationKindAttr  = "dagger.io/operation.kind"
)

// operationResource belongs only to the additional destination. Do not enrich
// ordinary session resources or trust identity posted by a workload. Invalid
// environment entries must not prevent startup or discard valid identity.
func operationResource(ctx context.Context, instance string) *resource.Resource {
	res, err := resource.New(ctx, resource.WithFromEnv())
	if err != nil {
		slog.Warn("incomplete operation telemetry identity", "error", err)
	}
	attrs := []attribute.KeyValue{
		attribute.String("service.name", "dagger-engine"),
		attribute.String("service.version", engine.Version),
		attribute.String("service.instance.id", instance),
		attribute.String(EngineInstanceAttr, instance),
		attribute.String("dagger.io/operation.schema", "1"),
	}
	for _, attr := range res.Attributes() {
		switch string(attr.Key) {
		case "dagger.io/engine.id", "dagger.io/provider.instance.id", "dagger.io/organization.id":
			attrs = append(attrs, attr)
		}
	}
	return resource.NewSchemaless(attrs...)
}
