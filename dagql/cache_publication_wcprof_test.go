package dagql

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/dagger/dagger/engine/wcprof"
)

// A publication that waits for another publication's open attachment of the
// recipe's current entry records the wait, reason attach, on its own op and
// span, targeting the publication that holds the attachment open: natively
// as a wait on its op, in a trace as a wait link to its span.
func TestPublicationProfiledAttachWait(t *testing.T) {
	sr, rootCtx, rootSpan := newRecordingRoot("POST /query")
	ctx := cacheTestContext(rootCtx)
	c, err := NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	frame := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType((&captureTestValue{}).Type()),
		Field: "profiled-attach-wait",
	}
	attachStarted, attachRelease := make(chan struct{}), make(chan struct{})
	bValue := &captureTestValue{text: "b", attach: func(context.Context) error {
		close(attachStarted)
		<-attachRelease
		return nil
	}}
	aValue := &captureTestValue{text: "a"}
	waitedOn := make(chan sharedResultID, 1)
	c.testPublicationWaitsOnAttachment = func(res *sharedResult) { waitedOn <- res.id }

	pctx, root := profiledRoot(ctx, "test.publish")
	aStarted, aFinish := make(chan struct{}), make(chan struct{})
	aDone := make(chan error, 1)
	go func() {
		_, err := c.GetOrInitCall(pctx, "session-a", noopTypeResolver{}, &CallRequest{ResultCall: frame.clone()}, func(context.Context) (AnyResult, error) {
			close(aStarted)
			<-aFinish
			return NewResultForCall(aValue, frame.clone())
		})
		aDone <- err
	}()
	<-aStarted
	bDone := make(chan error, 1)
	go func() {
		_, err := c.GetOrInitCall(pctx, "session-b", noopTypeResolver{}, &CallRequest{ResultCall: frame.clone()}, func(context.Context) (AnyResult, error) {
			return NewResultForCall(bValue, frame.clone())
		})
		bDone <- err
	}()
	<-attachStarted
	close(aFinish)
	bID := <-waitedOn
	close(attachRelease)
	require.NoError(t, <-bDone)
	require.NoError(t, <-aDone)
	root.End(wcprof.OutcomeOK)
	rootSpan.End()
	require.NoError(t, c.ReleaseSession(ctx, "session-a"))
	require.NoError(t, c.ReleaseSession(ctx, "session-b"))

	// Native: one attach wait, from one publication's op to the other's.
	events, ops := profiledUnder(t, root.ID())
	var attach []profiledEvent
	for _, ev := range events {
		if ev.Type == "wait" && ev.Reason == "attach" {
			attach = append(attach, ev)
		}
	}
	require.Len(t, attach, 1)
	waiter, holder := ops[attach[0].ParentID], ops[attach[0].TargetID]
	require.Equal(t, "dagql.publishResult", waiter.class, "the waiting publication")
	require.Equal(t, "dagql.publishResult", holder.class, "the publication holding the attachment open")
	require.NotEqual(t, waiter.OpID, holder.OpID)
	require.Equal(t, uint64(bID), holder.ResultID)
	require.GreaterOrEqual(t, attach[0].StartNS, waiter.StartNS)
	require.LessOrEqual(t, attach[0].EndNS, waiter.EndNS)

	// OTel: the waiting publishResult span links to the holder's.
	type attachLink struct{ from, to trace.SpanID }
	var links []attachLink
	pubSpans := map[trace.SpanID]bool{}
	for _, s := range sr.Ended() {
		if s.Name() != publishResultSpanName {
			continue
		}
		pubSpans[s.SpanContext().SpanID()] = true
		for _, l := range s.Links() {
			for _, kv := range l.Attributes {
				if string(kv.Key) == telemetryattrs.WcprofWaitReasonAttr && kv.Value.AsString() == "attach" {
					links = append(links, attachLink{s.SpanContext().SpanID(), l.SpanContext.SpanID()})
				}
			}
		}
	}
	require.Len(t, links, 1)
	require.True(t, pubSpans[links[0].to], "the link targets the holder's publishResult span")
	require.NotEqual(t, links[0].from, links[0].to)
}
