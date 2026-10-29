package dagql

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine/wcprof"
)

// What a client the call runs reports under the call's span (e.g. a build
// tool inside the call's exec, given the call's span as its traceparent)
// resolves in the native dump to the call's op.
func TestReportedSpanUnderCallResolvesToCallOp(t *testing.T) {
	_, rootCtx, callSpan := newRecordingRoot("withExec")
	defer callSpan.End()
	ctx := cacheTestContext(rootCtx)
	c, err := NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	ctx = ContextWithCache(ctx, c)
	wcprof.EnsureRecorder()
	ctx = wcprof.ContextWithProfiling(ctx)

	call := cacheTestIntCall("withExec")
	_, err = c.GetOrInitCall(ctx, "test-session", noopTypeResolver{},
		&CallRequest{ResultCall: call, ConcurrencyKey: "test-session"},
		func(context.Context) (AnyResult, error) {
			return cacheTestDetachedResult(call, NewInt(1)), nil
		})
	require.NoError(t, err)

	sc := callSpan.SpanContext()
	now := time.Now().UnixNano()
	wcprof.RecordReported(ctx, wcprof.ReportedOp{
		TraceID:      sc.TraceID().String(),
		SpanID:       "00000000000000aa",
		ParentSpanID: sc.SpanID().String(),
		Kind:         wcprof.OpKindTaskGroup,
		Name:         "test.reportedWalk",
		StartUnixNS:  now,
		EndUnixNS:    now + 1,
	})

	var buf bytes.Buffer
	require.NoError(t, wcprof.Active().WriteDump(&buf, false))
	header, events, err := wcprof.ReadDump(&buf)
	require.NoError(t, err)
	calls := map[uint64]bool{}
	var walk *wcprof.DumpEvent
	for i, ev := range events {
		switch {
		case ev.Type == "op" && ev.OpKind == wcprof.OpKindCall.String() && header.Strings[ev.ClassID] == profCallClass(call):
			calls[ev.OpID] = true
		case ev.Type == "op" && header.Strings[ev.ClassID] == "test.reportedWalk":
			walk = &events[i]
		}
	}
	require.NotNil(t, walk)
	require.True(t, calls[walk.ParentID], "reported walk's parent %d is not the call's op (%v)", walk.ParentID, calls)
}
