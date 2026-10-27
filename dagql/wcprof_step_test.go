package dagql

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"

	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/dagger/dagger/engine/wcprof"
)

// A profiled step is an op in the native profile and an internal OTel span,
// nested the same way in both, with a failed step failed in both.
func TestStepNativeAndOTel(t *testing.T) {
	wcprof.EnsureRecorder()
	sr, ctx, root := newRecordingRoot("step-test-root")
	ctx = wcprof.ContextWithProfiling(ctx)
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
	root.End()

	// OTel: internal, passthrough spans of the internal op kind, the items
	// under the outer step under the root.
	ended := sr.Ended()
	outerSpan := spanByName(t, ended, "steptest.outer")
	require.Equal(t, root.SpanContext().SpanID(), outerSpan.Parent().SpanID())
	kind, _ := attrString(outerSpan, telemetryattrs.WcprofOpKindAttr)
	require.Equal(t, wcprof.OpKindInternal.String(), kind)
	internal, _ := attrBool(outerSpan, "dagger.io/ui.internal")
	require.True(t, internal, "step span isn't internal")
	var itemStatus []codes.Code
	for _, s := range ended {
		if s.Name() == "steptest.item" {
			require.Equal(t, outerSpan.SpanContext().SpanID(), s.Parent().SpanID())
			itemStatus = append(itemStatus, s.Status().Code)
		}
	}
	require.ElementsMatch(t, []codes.Code{codes.Unset, codes.Error}, itemStatus)

	// Native: the same nesting, under the op the steps began under.
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

// Without telemetry recording, a profiled step is still a native op.
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
