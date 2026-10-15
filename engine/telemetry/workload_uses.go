package telemetry

import (
	"context"
	"strconv"
	"sync"

	"github.com/dagger/dagger/engine/telemetryattrs"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// WorkloadUses holds the results that one session has used, so that each use
// is recorded once for each span that needs the result. The same result can
// be needed by the session's request and by the evaluation of another
// result, and the work of that evaluation includes it.
type WorkloadUses struct {
	mu   sync.Mutex
	seen map[workloadUse]struct{}
}

type workloadUse struct {
	result uint64
	span   trace.SpanID
}

func NewWorkloadUses() *WorkloadUses {
	return &WorkloadUses{seen: make(map[workloadUse]struct{})}
}

type workloadUsesKey struct{}

func WithWorkloadUses(ctx context.Context, uses *WorkloadUses) context.Context {
	if uses == nil {
		return ctx
	}
	return context.WithValue(ctx, workloadUsesKey{}, uses)
}

// RecordWorkloadUse records, only in the workload export, that the session
// needed the content of a result. A session that only asks for an ID does not
// use the result, so it does not pay for the work that produces it. digest is
// called only for the first use of the result below the current span.
func RecordWorkloadUse(ctx context.Context, resultID uint64, digest func() string) {
	uses, _ := ctx.Value(workloadUsesKey{}).(*WorkloadUses)
	tracer, _ := ctx.Value(workloadTracerKey{}).(trace.Tracer)
	if uses == nil || tracer == nil {
		return
	}
	key := workloadUse{resultID, trace.SpanContextFromContext(ctx).SpanID()}
	uses.mu.Lock()
	_, seen := uses.seen[key]
	uses.seen[key] = struct{}{}
	uses.mu.Unlock()
	if seen {
		return
	}
	dig := digest()
	if dig == "" {
		return
	}
	_, span := tracer.Start(ctx, telemetryattrs.WorkloadUseSpan, trace.WithAttributes(
		attribute.String(telemetryattrs.CacheResultIDAttr, strconv.FormatUint(resultID, 10)),
		attribute.String(telemetryattrs.WorkloadUseDigestAttr, dig),
	))
	span.End()
}
