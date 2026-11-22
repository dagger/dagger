package dagui

import (
	"strconv"
	"testing"
	"time"

	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/engine/telemetryattrs"
)

// A wait on no span of the trace -- one of another trace, or none, as a
// waiter joining work of an untraced session records -- is on an operation
// outside it: the rows above its carrier read it, folded or not.
func TestFoldForeignWaits(t *testing.T) {
	for _, target := range []struct {
		name string
		ctx  trace.SpanContext
	}{
		{"another trace", trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{2}, SpanID: testID(9).SpanID})},
		{"no span", trace.SpanContext{}},
	} {
		t.Run(target.name, func(t *testing.T) {
			waiter := tracetest.SpanStubFromReadOnlySpan(lateTestSpan(3, 2, false))
			waiter.Links = []sdktrace.Link{{
				SpanContext: target.ctx,
				Attributes: []attribute.KeyValue{
					attribute.String(telemetry.LinkPurposeAttr, telemetryattrs.LinkPurposeWait),
					attribute.String(telemetryattrs.WcprofWaitReasonAttr, "singleflight"),
					attribute.String(telemetryattrs.WcprofWaitStartUnixNanoAttr, strconv.FormatInt(time.Unix(10, 0).UnixNano(), 10)),
					attribute.String(telemetryattrs.WcprofWaitEndUnixNanoAttr, strconv.FormatInt(time.Unix(90, 0).UnixNano(), 10)),
				},
			}}
			spans := []sdktrace.ReadOnlySpan{
				lateTestSpan(1, 0, false),
				lateTestSpan(2, 1, false),
				waiter.Snapshot(),
			}
			full := NewDB()
			if err := full.ExportSpans(t.Context(), spans); err != nil {
				t.Fatal(err)
			}
			folded, store := newFoldedDB(nil)
			if err := store.ExportSpans(t.Context(), spans); err != nil {
				t.Fatal(err)
			}
			now := time.Unix(200, 0)
			full.UpdateRowWaits(now, 0)
			folded.UpdateRowWaits(now, 0)
			want := full.Spans.Map[testID(2)].TimeBreakdown(now)
			if want.Waiting == 0 {
				t.Fatal("the full DB should read the row waiting")
			}
			span := folded.Spans.Map[testID(2)]
			if span == nil {
				t.Fatal("row 2 missing")
			}
			if got := span.TimeBreakdown(now); !sameBreakdown(got, want) {
				t.Errorf("breakdown self %v waiting %v, want self %v waiting %v", got.Self, got.Waiting, want.Self, want.Waiting)
			}
		})
	}
}
