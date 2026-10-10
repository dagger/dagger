package telemetry

import (
	"context"
	"testing"

	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestWorkloadEvaluationKeepsPresentationParent(t *testing.T) {
	work := tracetest.NewSpanRecorder()
	ordinary := tracetest.NewSpanRecorder()
	export := &WorkloadExport{spans: work, resource: resource.Empty()}
	presentation := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(ordinary),
		sdktrace.WithSpanProcessor(export.SpanProcessor("session")),
	)
	private := export.OperationTracerProvider("session")
	t.Cleanup(func() {
		require.NoError(t, presentation.Shutdown(context.Background()))
		require.NoError(t, private.Shutdown(context.Background()))
	})
	tracer := presentation.Tracer("dagger.io/core")
	rootCtx, root := tracer.Start(t.Context(), "request")
	_, producer := tracer.Start(rootCtx, "builder")
	producer.End()
	consumerCtx, consumer := tracer.Start(rootCtx, "stdout")
	consumerCtx = WithWorkloadTracer(consumerCtx, private.Tracer(WorkloadScope))
	evaluation := StartWorkloadSpan(consumerCtx, "evaluate")

	callback := trace.ContextWithSpanContext(consumerCtx, producer.SpanContext())
	callback = WithWorkloadParent(callback, evaluation.SpanContext())
	childCtx, child := tracer.Start(callback, "execution")
	_, grandchild := tracer.Start(childCtx, "setup")
	grandchild.End()
	child.End()
	evaluation.End()
	referenceCtx := WithWorkloadReference(consumerCtx)
	prepared := StartWorkloadSpan(WithWorkloadRoot(referenceCtx), "prepared")
	referenceCtx = WithWorkloadParent(referenceCtx, prepared.SpanContext())
	reused := StartWorkloadSpan(referenceCtx, "prepared-input")
	_, copySpan := tracer.Start(referenceCtx, "copy")
	copySpan.End()
	reused.End()
	prepared.End()
	consumer.End()
	root.End()

	find := func(spans []sdktrace.ReadOnlySpan, name string) sdktrace.ReadOnlySpan {
		t.Helper()
		for _, span := range spans {
			if span.Name() == name {
				return span
			}
		}
		t.Fatalf("missing %s", name)
		return nil
	}
	require.Equal(t, producer.SpanContext().SpanID(), find(ordinary.Ended(), "execution").Parent().SpanID())
	require.Equal(t, evaluation.SpanContext().SpanID(), find(work.Ended(), "execution").Parent().SpanID())
	require.Equal(t, consumer.SpanContext().SpanID(), find(work.Ended(), "evaluate").Parent().SpanID())
	require.Equal(t, child.SpanContext().SpanID(), find(work.Ended(), "setup").Parent().SpanID())
	require.False(t, find(work.Ended(), "prepared").Parent().IsValid(), "preparation cannot add dependencies to its caller")
	require.Equal(t, prepared.SpanContext().SpanID(), find(work.Ended(), "prepared-input").Parent().SpanID())
	require.Equal(t, consumer.SpanContext().SpanID(), find(ordinary.Ended(), "copy").Parent().SpanID())
	require.Equal(t, prepared.SpanContext().SpanID(), find(work.Ended(), "copy").Parent().SpanID())
	for _, name := range []string{"prepared", "prepared-input"} {
		var referenceOnly bool
		for _, attr := range find(work.Ended(), name).Attributes() {
			if string(attr.Key) == telemetryattrs.WorkloadReferenceAttr {
				referenceOnly = attr.Value.AsBool()
			}
		}
		require.True(t, referenceOnly, "preparation and its dependencies are not consumer uses")
	}
	for _, span := range ordinary.Ended() {
		require.NotEqual(t, "evaluate", span.Name(), "private evaluation must not enter the user trace")
	}
	require.Nil(t, StartWorkloadSpan(t.Context(), "disabled"))
	require.Nil(t, StartWorkloadSpan(WithoutWorkloadSpans(consumerCtx), "preparation"))
}
