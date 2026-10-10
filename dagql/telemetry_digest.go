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
// Deriving that shape re-hashes the call's entire transitive recipe; callers
// that don't warrant the walk use RecordOutputContentDigest instead.
// Like other call telemetry, derivation failures never fail the operation.
func RecordContentPreferredDigest(ctx context.Context, span trace.Span, frame *ResultCall, res AnyResult) {
	if span == nil || !span.IsRecording() || frame == nil {
		return
	}
	if recordOutputContentDigest(span, res) {
		return
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

// RecordOutputContentDigest is RecordContentPreferredDigest without the
// recipe fallback: it records the content digest of the returned result, if
// it has one, and otherwise leaves the span without a content-preferred
// digest. It's for calls such as trivial getters, where re-hashing the whole
// recipe would cost far more than the call itself.
func RecordOutputContentDigest(span trace.Span, res AnyResult) {
	if span == nil || !span.IsRecording() {
		return
	}
	recordOutputContentDigest(span, res)
}

func recordOutputContentDigest(span trace.Span, res AnyResult) bool {
	if res == nil {
		return false
	}
	// Read the immutable frame directly; ResultCall would clone its DAG.
	output := res.cacheSharedResult().loadResultCall()
	if output == nil {
		return false
	}
	content := output.ContentDigest()
	if content == "" {
		return false
	}
	span.SetAttributes(attribute.String(telemetryattrs.DagContentPreferredDigestAttr, content.String()))
	return true
}
