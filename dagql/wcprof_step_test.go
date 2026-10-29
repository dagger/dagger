package dagql

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine/wcprof"
)

// A profiled step is an op in the native profile, nested under the op it
// began under, with a failed step failed.
func TestStepNative(t *testing.T) {
	wcprof.EnsureRecorder()
	ctx := wcprof.ContextWithProfiling(context.Background())
	ctx, outerOp := wcprof.BeginOp(ctx, wcprof.OpKindInternal, "steptest.call", wcprof.OpOpts{Ident: "step-test"})

	stepCtx, outer := BeginStep(ctx, "steptest.outer")
	for _, fail := range []bool{false, true} {
		_, item := BeginStep(stepCtx, "steptest.item")
		var err error
		if fail {
			err = errors.New("item failed")
		}
		item.End(err)
	}
	outer.End(nil)
	outerOp.End(wcprof.OutcomeOK)

	var buf bytes.Buffer
	require.NoError(t, wcprof.Active().WriteDump(&buf, false))
	header, events, err := wcprof.ReadDump(&buf)
	require.NoError(t, err)
	var outerID uint64
	for _, ev := range events {
		if ev.Type == "op" && header.Strings[ev.ClassID] == "steptest.outer" && ev.ParentID == outerOp.ID() {
			outerID = ev.OpID
		}
	}
	require.NotZero(t, outerID, "no outer step op under its call")
	var itemOutcomes []string
	for _, ev := range events {
		if ev.Type == "op" && header.Strings[ev.ClassID] == "steptest.item" && ev.ParentID == outerID {
			require.Equal(t, wcprof.OpKindInternal.String(), ev.OpKind)
			itemOutcomes = append(itemOutcomes, ev.Outcome)
		}
	}
	require.ElementsMatch(t, []string{wcprof.OutcomeOK.String(), wcprof.OutcomeError.String()}, itemOutcomes)
}

// A step emits no OTel span, even when the engine records profiling spans,
// so work started under it, such as a lazy input's exec, nests directly
// under the span the step began under and the UI shows it.
func TestStepKeepsWorkVisible(t *testing.T) {
	wcprof.EnsureRecorder()
	sr, ctx, root := newRecordingRoot("step-visible-root")
	ctx = wcprof.ContextWithProfiling(ctx)
	require.True(t, OTelProfActive(ctx), "the test needs profiling spans on")

	stepCtx, s := BeginStep(ctx, "steptest.visible")
	_, work := Tracer(stepCtx).Start(stepCtx, "steptest.work")
	work.End()
	s.End(nil)
	root.End()

	for _, span := range sr.Ended() {
		require.NotEqual(t, "steptest.visible", span.Name(), "a step emitted an OTel span")
	}
	require.Equal(t, root.SpanContext().SpanID(), spanByName(t, sr.Ended(), "steptest.work").Parent().SpanID(),
		"work under a step isn't directly under the span the step began under")
}

// A profiled step is a native op without telemetry recording too.
func TestStepNativeOnly(t *testing.T) {
	wcprof.EnsureRecorder()
	ctx := wcprof.ContextWithProfiling(context.Background())
	stepCtx, s := BeginStep(ctx, "steptest.nativeonly")
	require.NotZero(t, wcprof.CurrentOpID(stepCtx), "no native op")
	s.End(nil)
}

// Work that isn't profiled records nothing and allocates nothing.
func TestStepUnprofiled(t *testing.T) {
	wcprof.EnsureRecorder()
	sr, ctx, root := newRecordingRoot("unprofiled-root")
	stepCtx, s := BeginStep(ctx, "steptest.unprofiled")
	require.Equal(t, ctx, stepCtx)
	s.End(errors.New("ignored"))
	root.End()
	for _, span := range sr.Ended() {
		require.NotEqual(t, "steptest.unprofiled", span.Name())
	}
	allocs := testing.AllocsPerRun(100, func() {
		_, s := BeginStep(ctx, "steptest.unprofiled")
		s.End(nil)
	})
	require.Zero(t, allocs)
}

func BenchmarkStepUnprofiled(b *testing.B) {
	wcprof.EnsureRecorder()
	_, ctx, root := newRecordingRoot("bench-root")
	defer root.End()
	b.ReportAllocs()
	for b.Loop() {
		_, s := BeginStep(ctx, "steptest.bench")
		s.End(nil)
	}
}
