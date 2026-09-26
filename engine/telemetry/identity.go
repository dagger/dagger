package telemetry

import (
	"context"
	"strings"

	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
)

// EngineInstanceAttr identifies the receiving engine on SDK telemetry without
// replacing the SDK process's own service.instance.id.
const EngineInstanceAttr = "dagger.io/engine.instance.id"

// ProvisionedIdentity reads standard OTEL_RESOURCE_ATTRIBUTES. Engine-controlled
// deployment attributes also accompany telemetry posted by local SDKs. Preserve
// those SDKs' service identity. Remote-engine streams do not use this enrichment.
func ProvisionedIdentity(ctx context.Context, instance string) ([]*commonpb.KeyValue, error) {
	res, err := resource.New(ctx, resource.WithFromEnv())
	if err != nil {
		return nil, err
	}
	attrs := make([]attribute.KeyValue, 0, res.Len()+1)
	for _, kv := range res.Attributes() {
		if !strings.HasPrefix(string(kv.Key), "service.") {
			attrs = append(attrs, kv)
		}
	}
	attrs = append(attrs, attribute.String(EngineInstanceAttr, instance))
	return telemetry.KeyValues(attrs), nil
}

// EnrichResourcePB updates only explicitly provided keys. It leaves other OTLP
// attributes, including value types outside the SDK attribute model, untouched.
func EnrichResourcePB(res *resourcepb.Resource, identity []*commonpb.KeyValue) *resourcepb.Resource {
	if res == nil {
		res = &resourcepb.Resource{}
	}
	indices := make(map[string]int, len(res.Attributes))
	for i, kv := range res.Attributes {
		indices[kv.Key] = i
	}
	for _, kv := range identity {
		if i, ok := indices[kv.Key]; ok {
			res.Attributes[i] = kv
		} else {
			indices[kv.Key] = len(res.Attributes)
			res.Attributes = append(res.Attributes, kv)
		}
	}
	return res
}
