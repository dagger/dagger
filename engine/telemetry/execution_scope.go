package telemetry

import (
	daggertelemetry "dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/engine"
	"go.opentelemetry.io/otel/attribute"
)

const (
	ClientKindRoot          = "root"
	ClientKindAttached      = "attached"
	ClientKindNested        = "nested"
	ClientKindCloudScaleOut = "cloud_scale_out"
)

func ExecutionScopeAttributes(md *engine.ClientMetadata) []attribute.KeyValue {
	if md == nil {
		return nil
	}

	attrs := make([]attribute.KeyValue, 0, 3)
	if md.SessionID != "" {
		attrs = append(attrs, attribute.String(daggertelemetry.EngineSessionIDAttr, md.SessionID))
	}
	if md.ClientID != "" {
		attrs = append(attrs, attribute.String(daggertelemetry.EngineClientIDAttr, md.ClientID))
	}
	if md.ClientKind != "" {
		attrs = append(attrs, attribute.String(daggertelemetry.EngineClientKindAttr, md.ClientKind))
	}
	return attrs
}

func ExecutionScopeConnectAttributes(md *engine.ClientMetadata) []attribute.KeyValue {
	attrs := ExecutionScopeAttributes(md)
	if md == nil || md.ParentClientID == "" {
		return attrs
	}
	return append(attrs, attribute.String(daggertelemetry.EngineParentClientIDAttr, md.ParentClientID))
}
