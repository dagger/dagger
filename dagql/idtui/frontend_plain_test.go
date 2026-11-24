package idtui

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/dagql/dagui"
)

func plainTestSpan(id, parent int, name string, start, end time.Time) sdktrace.ReadOnlySpan {
	spanID := func(i int) trace.SpanID {
		var sid trace.SpanID
		binary.BigEndian.PutUint64(sid[:], uint64(i))
		return sid
	}
	traceID := trace.TraceID{1}
	stub := tracetest.SpanStub{
		Name:        name,
		SpanContext: trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: spanID(id)}),
		StartTime:   start,
		EndTime:     end,
	}
	if parent != 0 {
		stub.Parent = trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: spanID(parent)})
	}
	return stub.Snapshot()
}

// A frame with no new telemetry prints nothing, so it must cost next to
// nothing: a session holding a large trace renders 20 frames a second for as
// long as it waits, and each used to walk every span.
func TestPlainIdleFramesDontWalkSpans(t *testing.T) {
	const n = 20_000
	start := time.Unix(100, 0)
	spans := []sdktrace.ReadOnlySpan{plainTestSpan(1, 0, "root", start, time.Time{})}
	for i := 2; i <= n; i++ {
		at := start.Add(time.Duration(i) * time.Millisecond)
		spans = append(spans, plainTestSpan(i, 1+i/100, fmt.Sprintf("span %d", i), at, at.Add(time.Millisecond)))
	}
	fe := NewPlain(io.Discard).(*frontendPlain)
	t.Cleanup(fe.ticker.Stop)
	fe.FrontendOpts.Verbosity = dagui.ShowCompletedVerbosity
	require.NoError(t, fe.SpanExporter().ExportSpans(context.Background(), spans))

	// The first frame walks and prints everything.
	began := time.Now()
	fe.render()
	full := time.Since(began)

	fastIdle := time.Duration(math.MaxInt64)
	for range 5 {
		began = time.Now()
		for range 100 {
			fe.render()
		}
		fastIdle = min(fastIdle, time.Since(began))
	}
	// Walking made each idle frame cost most of a full frame.
	require.Less(t, fastIdle, full, "100 idle frames over %d spans took %s; one full frame took %s", n, fastIdle, full)
}

// A span held back in one frame, because another span's context held the
// output, prints on a later frame with no new telemetry to prompt it.
func TestPlainHeldSpanPrintsOnALaterFrame(t *testing.T) {
	var out strings.Builder
	fe := NewPlain(&out).(*frontendPlain)
	t.Cleanup(fe.ticker.Stop)
	fe.FrontendOpts.Verbosity = dagui.ShowCompletedVerbosity
	start := time.Unix(100, 0)
	// Two unrelated running spans: printing the first takes the output for
	// its context, which holds the second back for the rest of the frame.
	require.NoError(t, fe.SpanExporter().ExportSpans(context.Background(), []sdktrace.ReadOnlySpan{
		plainTestSpan(1, 0, "first", start, time.Time{}),
		plainTestSpan(2, 0, "second", start.Add(time.Second), time.Time{}),
	}))
	fe.render()
	require.Contains(t, out.String(), "first")
	require.NotContains(t, out.String(), "second", "held back by first's context")

	fe.render()
	require.Contains(t, out.String(), "second", "printed on the next frame")
}

// A virtual log, such as the Cloud trace URL, prints on the next frame, with
// no telemetry after it to prompt that frame.
func TestPlainVirtualLogPrintsOnNextFrame(t *testing.T) {
	var out strings.Builder
	fe := NewPlain(&out).(*frontendPlain)
	t.Cleanup(fe.ticker.Stop)
	fe.FrontendOpts.Verbosity = dagui.ShowCompletedVerbosity
	span := plainTestSpan(1, 0, "connect", time.Unix(100, 0), time.Time{})
	require.NoError(t, fe.SpanExporter().ExportSpans(context.Background(), []sdktrace.ReadOnlySpan{span}))
	fe.render()
	require.Contains(t, out.String(), "connect")

	ctx := trace.ContextWithSpanContext(context.Background(), span.SpanContext())
	fe.SetCloudURL(ctx, "https://dagger.cloud/traces/example", "", true)
	fe.render()
	require.Contains(t, out.String(), "https://dagger.cloud/traces/example")
}
