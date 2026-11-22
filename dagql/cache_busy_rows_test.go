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

		ends, stop := c.WatchBusyEnds(func(r uint64) bool { return r == row })
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
	// The row isn't registered before its call, so the watch wants any
	// row's end.
	ends, stop := c.WatchBusyEnds(func(uint64) bool { return true })
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

// A watch of busy rows' ends hears only of the rows it wants: an end of
// another row, which a watch wanting every row hears of in the same end,
// wakes it not. An end of its row does.
func TestBusyEndsWakeOnlyTheWatchesThatWantTheRow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, c := newPartsTestCache(t, nil)
		defer c.CloseDiscardingPersistence()
		value := func(name string) (AnyResult, uint64) {
			obj := &captureTestValue{text: name}
			obj.lazy = func(context.Context) error {
				obj.lazy = nil
				return nil
			}
			res := attachNamedCaptureTestValue(t, ctx, c, name, obj)
			return res, uint64(res.cacheSharedResult().id)
		}
		wanted, wantedRow := value("wanted")
		other, otherRow := value("other")
		require.NotEqual(t, wantedRow, otherRow, "two rows")

		mine, stopMine := c.WatchBusyEnds(func(r uint64) bool { return r == wantedRow })
		defer stopMine()
		every, stopEvery := c.WatchBusyEnds(func(uint64) bool { return true })
		defer stopEvery()

		require.NoError(t, c.Evaluate(ctx, other))
		synctest.Wait()
		select {
		case <-every:
		default:
			t.Fatal("the other row's end was not heard")
		}
		select {
		case <-mine:
			t.Fatal("a watch heard of an end of a row it doesn't want")
		default:
		}

		require.NoError(t, c.Evaluate(ctx, wanted))
		synctest.Wait()
		select {
		case <-mine:
		default:
			t.Fatal("the watch didn't hear of its row's end")
		}
	})
}

// attachNamedCaptureTestValue is attachCaptureTestValue under a call of its
// own name, so that values attached under different names are different
// rows.
func attachNamedCaptureTestValue(t *testing.T, ctx context.Context, c *Cache, name string, value Typed) AnyResult {
	t.Helper()
	srv := newDagqlServerForTest(t, &persistCodecRoot{})
	srv.InstallObject(NewClass(srv, ClassOpts[*captureTestValue]{}))
	frame := persistCodecFrame(name, value)
	res, err := c.GetOrInitCall(ctx, cacheTestSessionID(t, ctx), srv, &CallRequest{ResultCall: frame}, func(context.Context) (AnyResult, error) {
		class, _ := srv.ObjectType(value.Type().Name())
		res, err := NewResultForCall(value, frame)
		if err != nil {
			return nil, err
		}
		return class.New(res)
	})
	require.NoError(t, err)
	return res
}
