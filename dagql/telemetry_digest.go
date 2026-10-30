package dagql

import (
	"context"

	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/opencontainers/go-digest"
	"go.opentelemetry.io/otel/attribute"
)

// AppendContentPreferredDigestAttr appends the attribute that supplements a
// call span's recipe identity at completion. A returned result can provide
// content learned during execution or on a cache hit. Without output content,
// retain the request's operation shape: returning another result is not itself
// content evidence for this operation. Like other call telemetry, derivation
// failures never fail the operation.
func AppendContentPreferredDigestAttr(ctx context.Context, attrs []attribute.KeyValue, frame *ResultCall, res AnyResult) []attribute.KeyValue {
	if frame == nil {
		return attrs
	}
	if res != nil {
		// Read the immutable frame directly; ResultCall would clone its DAG.
		if output := res.cacheSharedResult().loadResultCall(); output != nil {
			if content := output.ContentDigest(); content != "" {
				return append(attrs, attribute.String(telemetryattrs.DagContentPreferredDigestAttr, content.String()))
			}
		}
	}
	dig, err := frame.ContentPreferredDigestForTelemetry(ctx)
	if err != nil {
		slog.WarnContext(ctx, "failed to derive content-preferred digest", "field", frame.Field, "err", err)
		return attrs
	}
	if dig != "" {
		attrs = append(attrs, attribute.String(telemetryattrs.DagContentPreferredDigestAttr, dig.String()))
	}
	return attrs
}

// ResultFrameFacts reports the recorded output content digest and the type
// name of res's call frame, and whether res has a frame. It reads the shared
// frame directly, which is immutable once stored: ResultCall would clone the
// frame's whole DAG just to read these two facts.
func ResultFrameFacts(res AnyResult) (content digest.Digest, typeName string, ok bool) {
	if res == nil {
		return "", "", false
	}
	frame := res.cacheSharedResult().loadResultCall()
	if frame == nil {
		return "", "", false
	}
	if frame.Type != nil {
		typeName = frame.Type.NamedType
	}
	return frame.ContentDigest(), typeName, true
}
