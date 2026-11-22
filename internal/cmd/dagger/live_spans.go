package daggercmd

import (
	"context"
	"time"

	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/clientdb"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	telemetry "github.com/dagger/otel-go"
)

// liveSpanQuerier reads spans from a live session's store: a
// *client.Client.
type liveSpanQuerier interface {
	SupportsTelemetryFeature(feature string) bool
	QuerySpans(ctx context.Context, q clientdb.SpanQuery, consume func(*coltracepb.ExportTraceServiceRequest) error) (int64, error)
}

// liveFoldSettleTimeout bounds the read, at exit, of the folded spans the
// final render shows.
const liveFoldSettleTimeout = 10 * time.Second

// startLiveFold makes fe fold the spans of the session's trace it doesn't
// render, reading them back from the session on demand, when the session
// serves span queries; it returns the frontend to settle at exit, nil
// otherwise.
func startLiveFold(fe idtui.Frontend, sess liveSpanQuerier, traceID dagui.TraceID) idtui.FoldFrontend {
	ff, ok := fe.(idtui.FoldFrontend)
	if !ok || !traceID.IsValid() || !sess.SupportsTelemetryFeature(enginetel.LiveFeatureSpanQuery) {
		return nil
	}
	ff.SetSpanQuery(traceID, func(ctx context.Context, ids []dagui.SpanID) ([]sdktrace.ReadOnlySpan, error) {
		var spans []sdktrace.ReadOnlySpan
		for len(ids) > 0 {
			chunk := ids[:min(len(ids), clientdb.MaxSpanQuerySpans)]
			ids = ids[len(chunk):]
			q := clientdb.SpanQuery{Spans: make([]string, len(chunk))}
			for i, id := range chunk {
				q.Spans[i] = id.String()
			}
			if _, err := sess.QuerySpans(ctx, q, func(req *coltracepb.ExportTraceServiceRequest) error {
				spans = append(spans, telemetry.SpansFromPB(req.GetResourceSpans())...)
				return nil
			}); err != nil {
				return spans, err
			}
		}
		return spans, nil
	})
	return ff
}

// settleLiveFold reads the folded spans the final render shows, bounded.
func settleLiveFold(ctx context.Context, ff idtui.FoldFrontend) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), liveFoldSettleTimeout)
	defer cancel()
	ff.SettleFold(ctx)
}
