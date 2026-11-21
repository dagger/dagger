package dagql

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

// A row being evaluated is busy, as a capture finds it, until its evaluation
// ends, and a watch of evaluations' ends hears of that end then: a waiter
// for the row learns at once that it can be captured, and nothing polls. A
// row not yet evaluated isn't busy, since its record captures its original
// inputs, and neither is a row the cache doesn't have.
func TestBusyRowsEndWithTheirEvaluation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, c := newPartsTestCache(t, nil)
		defer c.CloseDiscardingPersistence()
		started, finish := make(chan struct{}), make(chan struct{})
		obj := &captureTestValue{text: "original inputs"}
		obj.lazy = func(context.Context) error {
			close(started)
			<-finish
			obj.text, obj.lazy = "completed value", nil
			return nil
		}
		res := attachCaptureTestValue(t, ctx, c, obj)
		row := uint64(res.cacheSharedResult().id)
		require.Empty(t, c.BusyRows(ctx, []uint64{row, row + 1000}), "unstarted work and a row the cache lacks aren't busy")

		ends, stop := c.WatchBusyEnds()
		defer stop()
		done := make(chan error, 1)
		go func() { done <- c.Evaluate(ctx, res) }()
		<-started
		synctest.Wait()
		require.Equal(t, []uint64{row}, c.BusyRows(ctx, []uint64{row, row + 1000}))
		_, err := c.CapturePersistedRecord(ctx, res)
		require.ErrorIs(t, err, ErrPersistStateNotReady, "a capture finds the row busy too")
		select {
		case <-ends:
			t.Fatal("the watch heard of an end while the evaluation runs")
		default:
		}

		close(finish)
		require.NoError(t, <-done)
		<-ends
		require.Empty(t, c.BusyRows(ctx, []uint64{row}), "the row is no longer busy once the watch hears of its end")
		_, err = c.CapturePersistedRecord(ctx, res)
		require.NoError(t, err, "and a capture takes it")
	})
}

// A row whose dependencies are being attached as its call completes is busy
// until the attachment ends, as a capture finds it, and a watch of busy rows'
// ends hears of that end, though no evaluation ends: a waiter for the row
// learns at once that it can be captured.
func TestBusyRowsEndWithTheirAttachment(t *testing.T) {
	t.Parallel()
	ctx, c := newPartsTestCache(t, nil)
	defer c.CloseDiscardingPersistence()
	entered, allow := make(chan struct{}), make(chan struct{})
	obj := &captureTestValue{text: "stable", attach: func(context.Context) error {
		close(entered)
		<-allow
		return nil
	}}
	srv := newDagqlServerForTest(t, &persistCodecRoot{})
	srv.InstallObject(NewClass(srv, ClassOpts[*captureTestValue]{}))
	frame := persistCodecFrame("attaching", obj)
	ends, stop := c.WatchBusyEnds()
	defer stop()
	done := make(chan error, 1)
	go func() {
		_, err := c.GetOrInitCall(ctx, "publisher", srv, &CallRequest{ResultCall: frame}, func(context.Context) (AnyResult, error) {
			return NewObjectResultForCall(obj, srv, frame)
		})
		done <- err
	}()
	<-entered
	var row uint64
	c.egraphMu.RLock()
	for _, r := range c.resultsByID {
		if r.loadPayloadState().self == obj {
			row = uint64(r.id)
			break
		}
	}
	c.egraphMu.RUnlock()
	require.NotZero(t, row)
	require.Equal(t, []uint64{row}, c.BusyRows(ctx, []uint64{row}))
	select {
	case <-ends:
		t.Fatal("the watch heard of an end while the attachment runs")
	default:
	}

	close(allow)
	require.NoError(t, <-done)
	select {
	case <-ends:
	case <-time.After(10 * time.Second):
		t.Fatal("the attachment's end woke no watch")
	}
	require.Empty(t, c.BusyRows(ctx, []uint64{row}), "the row is no longer busy once the watch hears of its end")
}
