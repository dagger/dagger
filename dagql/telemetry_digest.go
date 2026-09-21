package dagql

import (
	"context"

	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/engine/telemetryattrs"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// RecordContentPreferredDigest supplements a call span's recipe identity at
// completion. A returned result can provide content learned during execution or
// on a cache hit. Without output content, retain the request's operation shape:
// returning another result is not itself content evidence for this operation.
// Like other call telemetry, derivation failures never fail the operation.
func RecordContentPreferredDigest(ctx context.Context, span trace.Span, frame *ResultCall, res AnyResult) {
	if span == nil || !span.IsRecording() || frame == nil {
		return
	}
	if res != nil {
		// Read the immutable frame directly; ResultCall would clone its DAG.
		if output := res.cacheSharedResult().loadResultCall(); output != nil {
			if content := output.ContentDigest(); content != "" {
				span.SetAttributes(attribute.String(telemetryattrs.DagContentPreferredDigestAttr, content.String()))
				return
			}
		}
	}
	dig, err := frame.ContentPreferredDigestForTelemetry(ctx)
	if err != nil {
		slog.WarnContext(ctx, "failed to derive content-preferred digest", "field", frame.Field, "err", err)
		return
	}
	if dig != "" {
		span.SetAttributes(attribute.String(telemetryattrs.DagContentPreferredDigestAttr, dig.String()))
	}
}
