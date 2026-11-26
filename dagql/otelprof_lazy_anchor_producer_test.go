package dagql

import (
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"gotest.tools/v3/assert"

	"github.com/dagger/dagger/engine"
)

// A lazy result's later evaluations link its anchor only while the anchor
// links the session's call for the result. A call with its own span replaces
// one whose span named no call (captureSessionLazySpanContext), such as a
// module object's field returning a result first obtained under the client's
// request, and installs nothing new when the call is trivial. The next
// evaluation then links the new call and becomes the anchor: linking the old
// anchor would take a trace view back to the request.
func TestLazyEvaluationAnchorFollowsAReplacedCall(t *testing.T) {
	t.Parallel()
	const resultID = sharedResultID(7)
	const sessionID = "session-a"
	sr, rootCtx, root := newLazyRecordingRoot("POST /query")
	defer root.End()
	c := &Cache{}
	setCall := func(name string) trace.SpanContext {
		_, s := Tracer(rootCtx).Start(rootCtx, name)
		s.End()
		c.sessionMu.Lock()
		c.sessionLazySpansBySession = map[string]map[sharedResultID]trace.SpanContext{sessionID: {resultID: s.SpanContext()}}
		c.sessionMu.Unlock()
		return s.SpanContext()
	}
	evaluate := func() sdktrace.ReadOnlySpan {
		t.Helper()
		evalCtx := engine.ContextWithClientMetadata(rootCtx, &engine.ClientMetadata{SessionID: sessionID, ClientID: "client-1"})
		_, lazySpan, isResume := c.beginOTelLazyOp(evalCtx, resultID, "metadata", &ResultCall{Field: "withExec"}, "")
		assert.Assert(t, isResume)
		var err error
		endOTelLazyOp(lazySpan, nil, isResume, resultID, true, false, "", lazySpanCacheState{}, &err)
		return spanBySpanID(t, sr.Ended(), lazySpan.SpanContext().SpanID())
	}
	linked := func(s sdktrace.ReadOnlySpan) []trace.SpanID {
		var ids []trace.SpanID
		for _, link := range s.Links() {
			ids = append(ids, link.SpanContext.SpanID())
		}
		return ids
	}

	request := setCall("POST /query")
	anchor := evaluate()
	assert.DeepEqual(t, linked(anchor), []trace.SpanID{request.SpanID()})
	assert.DeepEqual(t, linked(evaluate()), []trace.SpanID{anchor.SpanContext().SpanID()})

	// the replaced call is linked, not the old anchor, and later
	// evaluations link the new anchor
	call := setCall("Object.field")
	replaced := evaluate()
	assert.DeepEqual(t, linked(replaced), []trace.SpanID{call.SpanID()})
	assert.DeepEqual(t, linked(evaluate()), []trace.SpanID{replaced.SpanContext().SpanID()})
}
