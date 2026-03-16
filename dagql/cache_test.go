package dagql

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagger/dagger/engine"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
	"golang.org/x/sync/errgroup"
	"gotest.tools/v3/assert"
	is "gotest.tools/v3/assert/cmp"

	"github.com/dagger/dagger/dagql/call"
)

type cacheTestOnReleaseInt struct {
	Int
	onRelease func(context.Context) error
}

func (v cacheTestOnReleaseInt) OnRelease(ctx context.Context) error {
	if v.onRelease == nil {
		return nil
	}
	return v.onRelease(ctx)
}

type cacheTestOpaqueValue struct {
	value     string
	onRelease func(context.Context) error
}

func (v cacheTestOpaqueValue) OnRelease(ctx context.Context) error {
	if v.onRelease == nil {
		return nil
	}
	return v.onRelease(ctx)
}

type cacheTestSizedInt struct {
	Int
	sizeBytes     int64
	sizeSource    *atomic.Int64
	usageIdentity string
	sizeCalls     *atomic.Int32
	sizeMayChange bool
}

func (v cacheTestSizedInt) CacheUsageSize(context.Context) (int64, bool, error) {
	if v.sizeCalls != nil {
		v.sizeCalls.Add(1)
	}
	if v.sizeSource != nil {
		return v.sizeSource.Load(), true, nil
	}
	return v.sizeBytes, true, nil
}

func (v cacheTestSizedInt) CacheUsageIdentity() (string, bool) {
	if v.usageIdentity == "" {
		return "", false
	}
	return v.usageIdentity, true
}

func (v cacheTestSizedInt) CacheUsageMayChange() bool {
	return v.sizeMayChange
}

type cacheTestOwnedDepsInt struct {
	Int
	ownedResults []AnyResult
}

func (v *cacheTestOwnedDepsInt) AttachOwnedResults(
	ctx context.Context,
	attach func(AnyResult) (AnyResult, error),
) ([]AnyResult, error) {
	if v == nil {
		return nil, nil
	}
	attached := make([]AnyResult, 0, len(v.ownedResults))
	for i, dep := range v.ownedResults {
		if dep == nil {
			continue
		}
		attachedDep, err := attach(dep)
		if err != nil {
			return nil, err
		}
		v.ownedResults[i] = attachedDep
		attached = append(attached, attachedDep)
	}
	return attached, nil
}

func cacheTestUnwrapInt(t *testing.T, res AnyResult) int {
	t.Helper()
	v, ok := UnwrapAs[Int](res)
	assert.Assert(t, ok, "expected Int result, got %T", res)
	return int(v)
}

func cacheTestSharedResultEntryID(res AnyResult) string {
	shared := res.cacheSharedResult()
	if shared == nil || shared.id == 0 {
		return ""
	}
	return fmt.Sprintf("dagql.result.%d", shared.id)
}

type cacheTestQuery struct{}

func (cacheTestQuery) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Query",
		NonNull:   true,
	}
}

type cacheTestObject struct {
	Value     int
	onRelease func(context.Context) error
}

func (*cacheTestObject) Type() *ast.Type {
	return &ast.Type{
		NamedType: "CacheTestObject",
		NonNull:   true,
	}
}

func (obj *cacheTestObject) OnRelease(ctx context.Context) error {
	if obj.onRelease == nil {
		return nil
	}
	return obj.onRelease(ctx)
}

func cacheTestServer(t *testing.T, base Cache) *Server {
	t.Helper()
	srv := NewServer(cacheTestQuery{}, NewSessionCache(base))
	Fields[*cacheTestObject]{
		Func("value", func(_ context.Context, self *cacheTestObject, _ struct{}) (Int, error) {
			return NewInt(self.Value), nil
		}),
	}.Install(srv)
	return srv
}

func cacheTestObjectResult(
	t *testing.T,
	srv *Server,
	frame *ResultCall,
	value int,
	onRelease func(context.Context) error,
) ObjectResult[*cacheTestObject] {
	t.Helper()
	res, err := NewObjectResultForCall(&cacheTestObject{
		Value:     value,
		onRelease: onRelease,
	}, srv, frame)
	assert.NilError(t, err)
	return res
}

func TestCacheConcurrent(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	keyCall := cacheTestIntCall("42")
	initialized := map[int]bool{}
	var initMu sync.Mutex
	const totalCallers = 100
	const concurrencyKey = "42"

	firstCallEntered := make(chan struct{})
	unblockFirstCall := make(chan struct{})

	callConcKeys := callConcurrencyKeys{
		callKey:        cacheTestCallDigest(keyCall).String(),
		concurrencyKey: concurrencyKey,
	}

	wg := new(sync.WaitGroup)
	wg.Add(1)
	go func() {
		defer wg.Done()
		res, err := cacheIface.GetOrInitCall(ctx, &CallRequest{
			ResultCall:     keyCall,
			ConcurrencyKey: concurrencyKey,
		}, func(_ context.Context) (AnyResult, error) {
			initMu.Lock()
			initialized[0] = true
			initMu.Unlock()
			close(firstCallEntered)
			<-unblockFirstCall
			return cacheTestIntResult(keyCall, 0), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, cacheTestUnwrapInt(t, res))
	}()

	select {
	case <-firstCallEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for first caller to enter init callback")
	}

	for i := 1; i < totalCallers; i++ {
		wg.Add(1)
		i := i
		go func() {
			defer wg.Done()
			res, err := cacheIface.GetOrInitCall(ctx, &CallRequest{
				ResultCall:     keyCall,
				ConcurrencyKey: concurrencyKey,
			}, func(_ context.Context) (AnyResult, error) {
				initMu.Lock()
				initialized[i] = true
				initMu.Unlock()
				return cacheTestIntResult(keyCall, i), nil
			})
			assert.NilError(t, err)
			assert.Equal(t, 0, cacheTestUnwrapInt(t, res))
		}()
	}

	waiterCountReached := false
	waiterPollDeadline := time.Now().Add(3 * time.Second)
	lastObservedWaiters := -1
	for time.Now().Before(waiterPollDeadline) {
		c.callsMu.Lock()
		oc := c.ongoingCalls[callConcKeys]
		if oc != nil {
			lastObservedWaiters = oc.waiters
		}
		c.callsMu.Unlock()

		if oc != nil && lastObservedWaiters == totalCallers {
			waiterCountReached = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	assert.Assert(t, waiterCountReached, "expected %d waiters, last observed %d", totalCallers, lastObservedWaiters)

	close(unblockFirstCall)

	ongoingCleared := false
	clearPollDeadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(clearPollDeadline) {
		c.callsMu.Lock()
		_, exists := c.ongoingCalls[callConcKeys]
		c.callsMu.Unlock()
		if !exists {
			ongoingCleared = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	assert.Assert(t, ongoingCleared, "ongoing call was not cleared")

	wg.Wait()

	initMu.Lock()
	defer initMu.Unlock()
	assert.Assert(t, is.Len(initialized, 1))
	assert.Assert(t, initialized[0])
	assert.Equal(t, 1, cacheIface.Size())
}

func TestCacheErrors(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)

	keyCall := cacheTestIntCall("42")

	myErr := errors.New("nope")
	_, err = cacheIface.GetOrInitCall(ctx, &CallRequest{ResultCall: keyCall}, func(_ context.Context) (AnyResult, error) {
		return nil, myErr
	})
	assert.Assert(t, is.ErrorIs(err, myErr))

	otherErr := errors.New("nope 2")
	_, err = cacheIface.GetOrInitCall(ctx, &CallRequest{ResultCall: keyCall}, func(_ context.Context) (AnyResult, error) {
		return nil, otherErr
	})
	assert.Assert(t, is.ErrorIs(err, otherErr))

	res, err := cacheIface.GetOrInitCall(ctx, &CallRequest{ResultCall: keyCall}, func(_ context.Context) (AnyResult, error) {
		return cacheTestIntResult(keyCall, 1), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 1, cacheTestUnwrapInt(t, res))

	res, err = cacheIface.GetOrInitCall(ctx, &CallRequest{ResultCall: keyCall}, func(_ context.Context) (AnyResult, error) {
		return nil, errors.New("ignored")
	})
	assert.NilError(t, err)
	assert.Equal(t, 1, cacheTestUnwrapInt(t, res))
}

func TestCacheRecursiveCall(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)

	key1Call := cacheTestIntCall("1")

	// recursive calls that are guaranteed to result in deadlock should error out
	_, err = cacheIface.GetOrInitCall(ctx, &CallRequest{ResultCall: key1Call}, func(ctx context.Context) (AnyResult, error) {
		_, err := cacheIface.GetOrInitCall(ctx, &CallRequest{ResultCall: key1Call}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(cacheTestIntCall("2"), 2), nil
		})
		return nil, err
	})
	assert.Assert(t, is.ErrorIs(err, ErrCacheRecursiveCall))

	// verify same cache can be called recursively with different keys
	key10Call := cacheTestIntCall("10")
	key11Call := cacheTestIntCall("11")
	v, err := cacheIface.GetOrInitCall(ctx, &CallRequest{ResultCall: key10Call}, func(ctx context.Context) (AnyResult, error) {
		res, err := cacheIface.GetOrInitCall(ctx, &CallRequest{ResultCall: key11Call}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(key11Call, 12), nil
		})
		if err != nil {
			return nil, err
		}
		return cacheTestIntResult(key10Call, cacheTestUnwrapInt(t, res)), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 12, cacheTestUnwrapInt(t, v))

	// verify other cache instances can be called with same keys
	cacheIface2, err := NewCache(ctx, "")
	assert.NilError(t, err)
	key100Call := cacheTestIntCall("100")
	v, err = cacheIface.GetOrInitCall(ctx, &CallRequest{ResultCall: key100Call}, func(ctx context.Context) (AnyResult, error) {
		res, err := cacheIface2.GetOrInitCall(ctx, &CallRequest{ResultCall: key100Call}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(key100Call, 101), nil
		})
		if err != nil {
			return nil, err
		}
		return cacheTestIntResult(key100Call, cacheTestUnwrapInt(t, res)), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 101, cacheTestUnwrapInt(t, v))
}

func TestCacheContextCancel(t *testing.T) {
	t.Run("cancels after all are canceled", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		cacheIface, err := NewCache(ctx, "")
		assert.NilError(t, err)

		keyCall := cacheTestIntCall("1")
		ctx1, cancel1 := context.WithCancel(ctx)
		ctx2, cancel2 := context.WithCancel(ctx)
		ctx3, cancel3 := context.WithCancel(ctx)

		errCh1 := make(chan error, 1)
		started1 := make(chan struct{})
		go func() {
			defer close(errCh1)
			_, err := cacheIface.GetOrInitCall(ctx1, &CallRequest{
				ResultCall:     keyCall,
				ConcurrencyKey: "1",
			}, func(ctx context.Context) (AnyResult, error) {
				close(started1)
				<-ctx.Done()
				return nil, fmt.Errorf("oh no 1")
			})
			errCh1 <- err
		}()
		select {
		case <-started1:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for started1")
		}

		errCh2 := make(chan error, 1)
		go func() {
			defer close(errCh2)
			_, err := cacheIface.GetOrInitCall(ctx2, &CallRequest{
				ResultCall:     keyCall,
				ConcurrencyKey: "1",
			}, func(ctx context.Context) (AnyResult, error) {
				<-ctx.Done()
				return nil, fmt.Errorf("oh no 2")
			})
			errCh2 <- err
		}()

		errCh3 := make(chan error, 1)
		go func() {
			defer close(errCh3)
			_, err := cacheIface.GetOrInitCall(ctx3, &CallRequest{
				ResultCall:     keyCall,
				ConcurrencyKey: "1",
			}, func(context.Context) (AnyResult, error) {
				return nil, fmt.Errorf("oh no 3")
			})
			errCh3 <- err
		}()

		cancel2()
		select {
		case err := <-errCh2:
			assert.Assert(t, is.ErrorIs(err, context.Canceled))
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for errCh2")
		}
		select {
		case err := <-errCh1:
			t.Fatal("unexpected error from 1st client", err)
		case err := <-errCh3:
			t.Fatal("unexpected error from 3rd client", err)
		default:
		}

		cancel3()
		select {
		case err := <-errCh3:
			assert.Assert(t, is.ErrorIs(err, context.Canceled))
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for errCh3")
		}
		select {
		case err := <-errCh1:
			t.Fatal("unexpected error from 1st client", err)
		default:
		}

		cancel1()
		select {
		case err := <-errCh1:
			assert.Assert(t, is.ErrorIs(err, context.Canceled))
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for errCh1")
		}
	})

	t.Run("succeeds if others are canceled", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		cacheIface, err := NewCache(ctx, "")
		assert.NilError(t, err)

		keyCall := cacheTestIntCall("1")
		ctx1, cancel1 := context.WithCancel(ctx)
		t.Cleanup(cancel1)
		ctx2, cancel2 := context.WithCancel(ctx)

		resCh1 := make(chan AnyResult, 1)
		errCh1 := make(chan error, 1)
		started1 := make(chan struct{})
		stop1 := make(chan struct{})
		go func() {
			defer close(resCh1)
			defer close(errCh1)
			res, err := cacheIface.GetOrInitCall(ctx1, &CallRequest{
				ResultCall:     keyCall,
				ConcurrencyKey: "1",
			}, func(context.Context) (AnyResult, error) {
				close(started1)
				<-stop1
				return cacheTestIntResult(keyCall, 0), nil
			})
			resCh1 <- res
			errCh1 <- err
		}()
		select {
		case <-started1:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for started1")
		}

		errCh2 := make(chan error, 1)
		go func() {
			defer close(errCh2)
			_, err := cacheIface.GetOrInitCall(ctx2, &CallRequest{
				ResultCall:     keyCall,
				ConcurrencyKey: "1",
			}, func(context.Context) (AnyResult, error) {
				return nil, fmt.Errorf("unexpected initializer call")
			})
			errCh2 <- err
		}()

		cancel2()
		select {
		case err := <-errCh2:
			assert.Assert(t, is.ErrorIs(err, context.Canceled))
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for errCh2")
		}

		close(stop1)
		select {
		case res := <-resCh1:
			assert.Equal(t, 0, cacheTestUnwrapInt(t, res))
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for resCh1")
		}
		select {
		case err := <-errCh1:
			assert.NilError(t, err)
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for errCh1")
		}
	})

	t.Run("last waiter canceled fn returns value still releases", func(t *testing.T) {
		t.Parallel()
		// TODO: Re-enable this test once we define and implement the intended
		// last-waiter cleanup semantics for canceled waiters when fn later returns.
		t.Skip("TODO: re-enable after last-waiter canceled cleanup semantics are decided")
		ctx := t.Context()
		cacheIface, err := NewCache(ctx, "")
		assert.NilError(t, err)

		keyCall := cacheTestIntCall("cancel-last-waiter-release")
		ctx1, cancel1 := context.WithCancel(ctx)
		defer cancel1()

		started := make(chan struct{})
		allowReturn := make(chan struct{})
		released := make(chan struct{})

		errCh := make(chan error, 1)
		go func() {
			_, err := cacheIface.GetOrInitCall(ctx1, &CallRequest{
				ResultCall:     keyCall,
				ConcurrencyKey: "1",
			}, func(context.Context) (AnyResult, error) {
				close(started)
				<-allowReturn
				return cacheTestIntResultWithOnRelease(keyCall, 1, func(context.Context) error {
					close(released)
					return nil
				}), nil
			})
			errCh <- err
		}()

		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for call start")
		}

		cancel1()
		select {
		case err := <-errCh:
			assert.Assert(t, is.ErrorIs(err, context.Canceled))
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for canceled wait return")
		}

		close(allowReturn)
		select {
		case <-released:
		case <-time.After(5 * time.Second):
			t.Fatal("expected release after call returns with no waiters")
		}
	})
}

func TestCacheResultRelease(t *testing.T) {
	t.Parallel()
	t.Run("basic", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		cacheIface, err := NewCache(ctx, "")
		assert.NilError(t, err)
		c, ok := cacheIface.(*cache)
		assert.Assert(t, ok)

		key1Call := cacheTestIntCall("1")
		key2Call := cacheTestIntCall("2")

		res1A, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: key1Call}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(key1Call, 1), nil
		})
		assert.NilError(t, err)
		res1B, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: key1Call}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(key1Call, 1), nil
		})
		assert.NilError(t, err)

		res2, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: key2Call}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(key2Call, 2), nil
		})
		assert.NilError(t, err)

		assert.Equal(t, 0, len(c.ongoingCalls))
		assert.Equal(t, 2, len(c.resultOutputEqClasses))

		err = res2.Release(ctx)
		assert.NilError(t, err)
		assert.Equal(t, 0, len(c.ongoingCalls))
		assert.Equal(t, 1, len(c.resultOutputEqClasses))

		err = res1A.Release(ctx)
		assert.NilError(t, err)
		assert.Equal(t, 0, len(c.ongoingCalls))
		assert.Equal(t, 1, len(c.resultOutputEqClasses))

		err = res1B.Release(ctx)
		assert.NilError(t, err)
		assert.Equal(t, 0, len(c.ongoingCalls))
		assert.Equal(t, 0, len(c.resultOutputEqClasses))
	})

	t.Run("onRelease", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		cacheIface, err := NewCache(ctx, "")
		assert.NilError(t, err)
		c, ok := cacheIface.(*cache)
		assert.Assert(t, ok)

		key1Call := cacheTestIntCall("1")
		key2Call := cacheTestIntCall("2")

		releaseCalledCh := make(chan struct{})
		res1A, err := c.GetOrInitCall(ctx, &CallRequest{
			ResultCall:     key1Call,
			ConcurrencyKey: "1",
		}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResultWithOnRelease(key1Call, 1, func(context.Context) error {
				close(releaseCalledCh)
				return nil
			}), nil
		})
		assert.NilError(t, err)
		res1B, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: key1Call}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(key1Call, 1), nil
		})
		assert.NilError(t, err)

		err = res1A.Release(ctx)
		assert.NilError(t, err)
		select {
		case <-releaseCalledCh:
			// shouldn't be called until every result is released
			t.Fatal("unexpected release call")
		default:
		}

		err = res1B.Release(ctx)
		assert.NilError(t, err)
		select {
		case <-releaseCalledCh:
			// it was called now that every result is released
		default:
			t.Fatal("expected release call")
		}

		// test error in onRelease
		res2, err := c.GetOrInitCall(ctx, &CallRequest{
			ResultCall:     key2Call,
			ConcurrencyKey: "1",
		}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResultWithOnRelease(key2Call, 2, func(context.Context) error {
				return fmt.Errorf("oh no")
			}), nil
		})
		assert.NilError(t, err)

		err = res2.Release(ctx)
		assert.ErrorContains(t, err, "oh no")
	})
}

func TestSkipDedupe(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)

	keyCall := cacheTestIntCall("1")
	var eg errgroup.Group

	valCh1 := make(chan int, 1)
	started1 := make(chan struct{})
	stop1 := make(chan struct{})
	eg.Go(func() error {
		_, err := cacheIface.GetOrInitCall(ctx, &CallRequest{ResultCall: keyCall}, func(context.Context) (AnyResult, error) {
			defer close(valCh1)
			close(started1)
			valCh1 <- 1
			<-stop1
			return cacheTestIntResult(keyCall, 1), nil
		})
		return err
	})

	valCh2 := make(chan int, 1)
	started2 := make(chan struct{})
	stop2 := make(chan struct{})
	eg.Go(func() error {
		_, err := cacheIface.GetOrInitCall(ctx, &CallRequest{ResultCall: keyCall}, func(context.Context) (AnyResult, error) {
			defer close(valCh2)
			close(started2)
			valCh2 <- 2
			<-stop2
			return cacheTestIntResult(keyCall, 2), nil
		})
		return err
	})

	select {
	case <-started1:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for started1")
	}
	select {
	case <-started2:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for started2")
	}

	close(stop1)
	close(stop2)

	select {
	case val := <-valCh1:
		assert.Equal(t, 1, val)
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for valCh1")
	}
	select {
	case val := <-valCh2:
		assert.Equal(t, 2, val)
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for valCh2")
	}

	assert.NilError(t, eg.Wait())
}

func TestCacheNilKeyIDRejected(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)

	_, err = cacheIface.GetOrInitCall(ctx, &CallRequest{ResultCall: &ResultCall{}}, func(context.Context) (AnyResult, error) {
		return nil, fmt.Errorf("unexpected initializer call")
	})
	assert.ErrorContains(t, err, "missing field")
}

func TestCacheDifferentConcurrencyKeysDoNotDedupe(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)

	keyCall := cacheTestIntCall("different-concurrency")
	release := make(chan struct{})
	startedA := make(chan struct{})
	startedB := make(chan struct{})
	errCh := make(chan error, 2)
	var initCalls atomic.Int32

	go func() {
		_, err := cacheIface.GetOrInitCall(ctx, &CallRequest{
			ResultCall:     keyCall,
			ConcurrencyKey: "a",
		}, func(context.Context) (AnyResult, error) {
			initCalls.Add(1)
			close(startedA)
			<-release
			return cacheTestIntResult(keyCall, 1), nil
		})
		errCh <- err
	}()
	go func() {
		_, err := cacheIface.GetOrInitCall(ctx, &CallRequest{
			ResultCall:     keyCall,
			ConcurrencyKey: "b",
		}, func(context.Context) (AnyResult, error) {
			initCalls.Add(1)
			close(startedB)
			<-release
			return cacheTestIntResult(keyCall, 2), nil
		})
		errCh <- err
	}()

	select {
	case <-startedA:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for startedA")
	}
	select {
	case <-startedB:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for startedB")
	}

	close(release)
	assert.NilError(t, <-errCh)
	assert.NilError(t, <-errCh)
	assert.Equal(t, int32(2), initCalls.Load())
}

func TestCacheNilResultIsCached(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	keyCall := cacheTestIntCall("nil-result")
	initCalls := 0

	res, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: keyCall}, func(context.Context) (AnyResult, error) {
		initCalls++
		return nil, nil
	})
	assert.NilError(t, err)
	assert.Assert(t, res != nil)
	assert.Assert(t, res.Unwrap() == nil)

	res, err = c.GetOrInitCall(ctx, &CallRequest{ResultCall: keyCall}, func(context.Context) (AnyResult, error) {
		initCalls++
		return cacheTestIntResult(keyCall, 42), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, res != nil)
	assert.Assert(t, res.Unwrap() == nil)
	assert.Equal(t, 1, initCalls)
	assert.Equal(t, 1, c.Size())
}

func TestEquivalencySetCacheHits(t *testing.T) {
	t.Parallel()

	// Basic case: equivalent upstream outputs enable a single downstream cache hit
	// even when the downstream recipes are distinct.
	t.Run("basic", func(t *testing.T) {
		ctx := t.Context()
		cacheIface, err := NewCache(ctx, "")
		assert.NilError(t, err)
		c := cacheIface.(*cache)

		sharedEq := call.ExtraDigest{
			Digest: digest.FromString("shared-eq-basic"),
			Label:  "eq-shared",
		}
		noiseA := call.ExtraDigest{
			Digest: digest.FromString("basic-noise-a"),
			Label:  "noise-a",
		}
		noiseB := call.ExtraDigest{
			Digest: digest.FromString("basic-noise-b"),
			Label:  "noise-b",
		}
		f1OutCall := cacheTestIntCall("content-f-1", sharedEq, noiseA)
		f2OutCall := cacheTestIntCall("content-f-2", sharedEq, noiseB)

		fInitCalls := 0
		f1Call := cacheTestIntCall("content-f-1")
		f2Call := cacheTestIntCall("content-f-2")
		f1Res, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: f1Call}, func(context.Context) (AnyResult, error) {
			fInitCalls++
			return cacheTestIntResult(f1OutCall, 11), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !f1Res.HitCache())

		f2Res, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: f2Call}, func(context.Context) (AnyResult, error) {
			fInitCalls++
			return cacheTestIntResult(f2OutCall, 22), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !f2Res.HitCache())
		assert.Equal(t, 2, fInitCalls)
		assert.Assert(t, cacheTestMustRecipeID(t, f1Res).Digest() != cacheTestMustRecipeID(t, f2Res).Digest())

		g1Res, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: &ResultCall{
			Kind:     ResultCallKindField,
			Type:     NewResultCallType(Int(0).Type()),
			Field:    "content-g",
			Receiver: &ResultCallRef{ResultID: uint64(f1Res.cacheSharedResult().id)},
		}}, func(context.Context) (AnyResult, error) {
			return cacheTestPlainResult(NewInt(111)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !g1Res.HitCache())

		g2InitCalls := 0
		g2Res, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: &ResultCall{
			Kind:     ResultCallKindField,
			Type:     NewResultCallType(Int(0).Type()),
			Field:    "content-g",
			Receiver: &ResultCallRef{ResultID: uint64(f2Res.cacheSharedResult().id)},
		}}, func(context.Context) (AnyResult, error) {
			g2InitCalls++
			return cacheTestPlainResult(NewInt(222)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, g2InitCalls)
		assert.Assert(t, g2Res.HitCache())
		assert.Equal(t, 111, cacheTestUnwrapInt(t, g2Res))
		assert.Equal(t, cacheTestMustEncodeID(t, g1Res), cacheTestMustEncodeID(t, g2Res))

		assert.NilError(t, f1Res.Release(ctx))
		assert.NilError(t, f2Res.Release(ctx))
		assert.NilError(t, g1Res.Release(ctx))
		assert.NilError(t, g2Res.Release(ctx))
		assert.Equal(t, 0, c.Size())
	})

	// Deeper chain: equivalence learned at f-level should enable hits at g-level,
	// which then propagate to h-level and i-level for distinct downstream recipes.
	t.Run("deep_chain", func(t *testing.T) {
		ctx := t.Context()
		cacheIface, err := NewCache(ctx, "")
		assert.NilError(t, err)
		c := cacheIface.(*cache)

		sharedEq := call.ExtraDigest{
			Digest: digest.FromString("deep-shared-eq"),
			Label:  "eq-shared",
		}
		noiseA := call.ExtraDigest{
			Digest: digest.FromString("deep-noise-a"),
			Label:  "noise-a",
		}
		noiseB := call.ExtraDigest{
			Digest: digest.FromString("deep-noise-b"),
			Label:  "noise-b",
		}
		f1Call := cacheTestIntCall("deep-f-1")
		f2Call := cacheTestIntCall("deep-f-2")
		f1OutCall := cacheTestIntCall("deep-f-1", sharedEq, noiseA)
		f2OutCall := cacheTestIntCall("deep-f-2", sharedEq, noiseB)

		fInitCalls := 0
		f1Res, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: f1Call}, func(context.Context) (AnyResult, error) {
			fInitCalls++
			return cacheTestIntResult(f1OutCall, 21), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !f1Res.HitCache())
		f2Res, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: f2Call}, func(context.Context) (AnyResult, error) {
			fInitCalls++
			return cacheTestIntResult(f2OutCall, 22), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !f2Res.HitCache())
		assert.Equal(t, 2, fInitCalls)

		g1Res, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: &ResultCall{
			Kind:     ResultCallKindField,
			Type:     NewResultCallType(Int(0).Type()),
			Field:    "deep-g",
			Receiver: &ResultCallRef{ResultID: uint64(f1Res.cacheSharedResult().id)},
		}}, func(context.Context) (AnyResult, error) {
			return cacheTestPlainResult(NewInt(121)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !g1Res.HitCache())

		h1Res, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: &ResultCall{
			Kind:     ResultCallKindField,
			Type:     NewResultCallType(Int(0).Type()),
			Field:    "deep-h",
			Receiver: &ResultCallRef{ResultID: uint64(g1Res.cacheSharedResult().id)},
		}}, func(context.Context) (AnyResult, error) {
			return cacheTestPlainResult(NewInt(221)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !h1Res.HitCache())

		i1Res, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: &ResultCall{
			Kind:     ResultCallKindField,
			Type:     NewResultCallType(Int(0).Type()),
			Field:    "deep-i",
			Receiver: &ResultCallRef{ResultID: uint64(h1Res.cacheSharedResult().id)},
		}}, func(context.Context) (AnyResult, error) {
			return cacheTestPlainResult(NewInt(321)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !i1Res.HitCache())

		g2InitCalls := 0
		g2Res, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: &ResultCall{
			Kind:     ResultCallKindField,
			Type:     NewResultCallType(Int(0).Type()),
			Field:    "deep-g",
			Receiver: &ResultCallRef{ResultID: uint64(f2Res.cacheSharedResult().id)},
		}}, func(context.Context) (AnyResult, error) {
			g2InitCalls++
			return cacheTestPlainResult(NewInt(122)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, g2InitCalls)
		assert.Assert(t, g2Res.HitCache())
		assert.Equal(t, 121, cacheTestUnwrapInt(t, g2Res))
		assert.Equal(t, cacheTestMustEncodeID(t, g1Res), cacheTestMustEncodeID(t, g2Res))

		h2InitCalls := 0
		h2Res, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: &ResultCall{
			Kind:     ResultCallKindField,
			Type:     NewResultCallType(Int(0).Type()),
			Field:    "deep-h",
			Receiver: &ResultCallRef{ResultID: uint64(g2Res.cacheSharedResult().id)},
		}}, func(context.Context) (AnyResult, error) {
			h2InitCalls++
			return cacheTestPlainResult(NewInt(222)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, h2InitCalls)
		assert.Assert(t, h2Res.HitCache())
		assert.Equal(t, 221, cacheTestUnwrapInt(t, h2Res))
		assert.Equal(t, cacheTestMustEncodeID(t, h1Res), cacheTestMustEncodeID(t, h2Res))

		i2InitCalls := 0
		i2Res, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: &ResultCall{
			Kind:     ResultCallKindField,
			Type:     NewResultCallType(Int(0).Type()),
			Field:    "deep-i",
			Receiver: &ResultCallRef{ResultID: uint64(h2Res.cacheSharedResult().id)},
		}}, func(context.Context) (AnyResult, error) {
			i2InitCalls++
			return cacheTestPlainResult(NewInt(322)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, i2InitCalls)
		assert.Assert(t, i2Res.HitCache())
		assert.Equal(t, 321, cacheTestUnwrapInt(t, i2Res))
		assert.Equal(t, cacheTestMustEncodeID(t, i1Res), cacheTestMustEncodeID(t, i2Res))

		assert.NilError(t, f1Res.Release(ctx))
		assert.NilError(t, f2Res.Release(ctx))
		assert.NilError(t, g1Res.Release(ctx))
		assert.NilError(t, g2Res.Release(ctx))
		assert.NilError(t, h1Res.Release(ctx))
		assert.NilError(t, h2Res.Release(ctx))
		assert.NilError(t, i1Res.Release(ctx))
		assert.NilError(t, i2Res.Release(ctx))
		assert.Equal(t, 0, c.Size())
	})

	// Late equivalence with noisy metadata: distinct recipes miss until h-level
	// outputs publish overlapping extra digests; once learned, downstream
	// i-level lookups should hit even with non-overlapping extras elsewhere.
	t.Run("late_extra_digests_at_h", func(t *testing.T) {
		ctx := t.Context()
		cacheIface, err := NewCache(ctx, "")
		assert.NilError(t, err)
		c := cacheIface.(*cache)

		f1Only := call.ExtraDigest{Digest: digest.FromString("late-f1-only"), Label: "f1-only"}
		f2Only := call.ExtraDigest{Digest: digest.FromString("late-f2-only"), Label: "f2-only"}
		g1Only := call.ExtraDigest{Digest: digest.FromString("late-g1-only"), Label: "g1-only"}
		g2Only := call.ExtraDigest{Digest: digest.FromString("late-g2-only"), Label: "g2-only"}
		sharedA := call.ExtraDigest{Digest: digest.FromString("late-shared-a"), Label: "shared-a"}
		sharedB := call.ExtraDigest{Digest: digest.FromString("late-shared-b"), Label: "shared-b"}
		h1Only := call.ExtraDigest{Digest: digest.FromString("late-h1-only"), Label: "h1-only"}
		h2Only := call.ExtraDigest{Digest: digest.FromString("late-h2-only"), Label: "h2-only"}

		f1Call := cacheTestIntCall("late-f-1")
		f2Call := cacheTestIntCall("late-f-2")
		f1Res, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: f1Call}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(cacheTestIntCall("late-f-1", f1Only), 41), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !f1Res.HitCache())

		g1Res, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: &ResultCall{
			Kind:     ResultCallKindField,
			Type:     NewResultCallType(Int(0).Type()),
			Field:    "late-g",
			Receiver: &ResultCallRef{ResultID: uint64(f1Res.cacheSharedResult().id)},
		}}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(&ResultCall{
				Kind:         ResultCallKindField,
				Type:         NewResultCallType(Int(0).Type()),
				Field:        "late-g",
				Receiver:     &ResultCallRef{ResultID: uint64(f1Res.cacheSharedResult().id)},
				ExtraDigests: []call.ExtraDigest{g1Only},
			}, 141), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !g1Res.HitCache())

		h1Res, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: &ResultCall{
			Kind:     ResultCallKindField,
			Type:     NewResultCallType(Int(0).Type()),
			Field:    "late-h",
			Receiver: &ResultCallRef{ResultID: uint64(g1Res.cacheSharedResult().id)},
		}}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(&ResultCall{
				Kind:         ResultCallKindField,
				Type:         NewResultCallType(Int(0).Type()),
				Field:        "late-h",
				Receiver:     &ResultCallRef{ResultID: uint64(g1Res.cacheSharedResult().id)},
				ExtraDigests: []call.ExtraDigest{sharedA, sharedB, h1Only},
			}, 241), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !h1Res.HitCache())

		i1Res, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: &ResultCall{
			Kind:     ResultCallKindField,
			Type:     NewResultCallType(Int(0).Type()),
			Field:    "late-i",
			Receiver: &ResultCallRef{ResultID: uint64(h1Res.cacheSharedResult().id)},
		}}, func(context.Context) (AnyResult, error) {
			return cacheTestPlainResult(NewInt(341)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !i1Res.HitCache())

		f2InitCalls := 0
		f2Res, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: f2Call}, func(context.Context) (AnyResult, error) {
			f2InitCalls++
			return cacheTestIntResult(cacheTestIntCall("late-f-2", f2Only), 42), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 1, f2InitCalls)
		assert.Assert(t, !f2Res.HitCache())

		g2InitCalls := 0
		g2Res, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: &ResultCall{
			Kind:     ResultCallKindField,
			Type:     NewResultCallType(Int(0).Type()),
			Field:    "late-g",
			Receiver: &ResultCallRef{ResultID: uint64(f2Res.cacheSharedResult().id)},
		}}, func(context.Context) (AnyResult, error) {
			g2InitCalls++
			return cacheTestIntResult(&ResultCall{
				Kind:         ResultCallKindField,
				Type:         NewResultCallType(Int(0).Type()),
				Field:        "late-g",
				Receiver:     &ResultCallRef{ResultID: uint64(f2Res.cacheSharedResult().id)},
				ExtraDigests: []call.ExtraDigest{g2Only},
			}, 142), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 1, g2InitCalls)
		assert.Assert(t, !g2Res.HitCache())

		h2InitCalls := 0
		h2Res, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: &ResultCall{
			Kind:     ResultCallKindField,
			Type:     NewResultCallType(Int(0).Type()),
			Field:    "late-h",
			Receiver: &ResultCallRef{ResultID: uint64(g2Res.cacheSharedResult().id)},
		}}, func(context.Context) (AnyResult, error) {
			h2InitCalls++
			return cacheTestIntResult(&ResultCall{
				Kind:         ResultCallKindField,
				Type:         NewResultCallType(Int(0).Type()),
				Field:        "late-h",
				Receiver:     &ResultCallRef{ResultID: uint64(g2Res.cacheSharedResult().id)},
				ExtraDigests: []call.ExtraDigest{sharedA, sharedB, h2Only},
			}, 242), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 1, h2InitCalls)
		assert.Assert(t, !h2Res.HitCache())

		i2InitCalls := 0
		i2Res, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: &ResultCall{
			Kind:     ResultCallKindField,
			Type:     NewResultCallType(Int(0).Type()),
			Field:    "late-i",
			Receiver: &ResultCallRef{ResultID: uint64(h2Res.cacheSharedResult().id)},
		}}, func(context.Context) (AnyResult, error) {
			i2InitCalls++
			return cacheTestPlainResult(NewInt(342)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, i2InitCalls)
		assert.Assert(t, i2Res.HitCache())
		assert.Equal(t, 341, cacheTestUnwrapInt(t, i2Res))
		assert.Equal(t, cacheTestMustEncodeID(t, i1Res), cacheTestMustEncodeID(t, i2Res))

		assert.NilError(t, f1Res.Release(ctx))
		assert.NilError(t, f2Res.Release(ctx))
		assert.NilError(t, g1Res.Release(ctx))
		assert.NilError(t, g2Res.Release(ctx))
		assert.NilError(t, h1Res.Release(ctx))
		assert.NilError(t, h2Res.Release(ctx))
		assert.NilError(t, i1Res.Release(ctx))
		assert.NilError(t, i2Res.Release(ctx))
		assert.Equal(t, 0, c.Size())
	})

	// Multi-input case: downstream z(x,y) should hit across distinct recipes once
	// both input lanes are equivalent (x1~x2 and y1~y2) via shared extra digests.
	// Basically, same as earlier tests but with multiple inputs.
	t.Run("multi_input_all_inputs_equivalent_hit", func(t *testing.T) {
		ctx := t.Context()
		cacheIface, err := NewCache(ctx, "")
		assert.NilError(t, err)
		c := cacheIface.(*cache)

		xShared := call.ExtraDigest{Digest: digest.FromString("multi-x-shared"), Label: "x-shared"}
		xNoise1 := call.ExtraDigest{Digest: digest.FromString("multi-x-noise-1"), Label: "x-noise-1"}
		xNoise2 := call.ExtraDigest{Digest: digest.FromString("multi-x-noise-2"), Label: "x-noise-2"}
		yShared := call.ExtraDigest{Digest: digest.FromString("multi-y-shared"), Label: "y-shared"}
		yNoise1 := call.ExtraDigest{Digest: digest.FromString("multi-y-noise-1"), Label: "y-noise-1"}
		yNoise2 := call.ExtraDigest{Digest: digest.FromString("multi-y-noise-2"), Label: "y-noise-2"}

		x1Call := cacheTestIntCall("multi-x-1")
		x2Call := cacheTestIntCall("multi-x-2")
		y1Call := cacheTestIntCall("multi-y-1")
		y2Call := cacheTestIntCall("multi-y-2")
		x1OutCall := cacheTestIntCall("multi-x-1", xShared, xNoise1)
		x2OutCall := cacheTestIntCall("multi-x-2", xShared, xNoise2)
		y1OutCall := cacheTestIntCall("multi-y-1", yShared, yNoise1)
		y2OutCall := cacheTestIntCall("multi-y-2", yShared, yNoise2)

		x1Res, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: x1Call}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(x1OutCall, 11), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !x1Res.HitCache())
		x2Res, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: x2Call}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(x2OutCall, 12), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !x2Res.HitCache())

		y1Res, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: y1Call}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(y1OutCall, 21), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !y1Res.HitCache())
		y2Res, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: y2Call}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(y2OutCall, 22), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !y2Res.HitCache())

		zReq := func(xRes, yRes AnyResult) *CallRequest {
			xShared := xRes.cacheSharedResult()
			yShared := yRes.cacheSharedResult()
			return &CallRequest{
				ResultCall: &ResultCall{
					Kind:  ResultCallKindField,
					Type:  NewResultCallType(Int(0).Type()),
					Field: "multi-z",
					Args: []*ResultCallArg{
						{Name: "x", Value: &ResultCallLiteral{Kind: ResultCallLiteralKindResultRef, ResultRef: &ResultCallRef{ResultID: uint64(xShared.id)}}},
						{Name: "y", Value: &ResultCallLiteral{Kind: ResultCallLiteralKindResultRef, ResultRef: &ResultCallRef{ResultID: uint64(yShared.id)}}},
					},
				},
			}
		}

		z1Res, err := c.GetOrInitCall(ctx, zReq(x1Res, y1Res), func(context.Context) (AnyResult, error) {
			return cacheTestPlainResult(NewInt(501)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !z1Res.HitCache())

		z2InitCalls := 0
		z2Res, err := c.GetOrInitCall(ctx, zReq(x2Res, y2Res), func(context.Context) (AnyResult, error) {
			z2InitCalls++
			return cacheTestPlainResult(NewInt(502)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, z2InitCalls)
		assert.Assert(t, z2Res.HitCache())
		assert.Equal(t, 501, cacheTestUnwrapInt(t, z2Res))
		assert.Equal(t, cacheTestMustEncodeID(t, z1Res), cacheTestMustEncodeID(t, z2Res))

		assert.NilError(t, x1Res.Release(ctx))
		assert.NilError(t, x2Res.Release(ctx))
		assert.NilError(t, y1Res.Release(ctx))
		assert.NilError(t, y2Res.Release(ctx))
		assert.NilError(t, z1Res.Release(ctx))
		assert.NilError(t, z2Res.Release(ctx))
		assert.Equal(t, 0, c.Size())
	})

	// Multi-input miss case: if only one input lane is equivalent (x1~x2) but
	// the other lane is not (y1 !~ y2), z(x,y) must miss and execute.
	t.Run("multi_input_partial_equivalence_miss", func(t *testing.T) {
		ctx := t.Context()
		cacheIface, err := NewCache(ctx, "")
		assert.NilError(t, err)
		c := cacheIface.(*cache)

		xShared := call.ExtraDigest{Digest: digest.FromString("multi-partial-x-shared"), Label: "x-shared"}
		xNoise1 := call.ExtraDigest{Digest: digest.FromString("multi-partial-x-noise-1"), Label: "x-noise-1"}
		xNoise2 := call.ExtraDigest{Digest: digest.FromString("multi-partial-x-noise-2"), Label: "x-noise-2"}
		yOnly1 := call.ExtraDigest{Digest: digest.FromString("multi-partial-y-only-1"), Label: "y-only-1"}
		yOnly2 := call.ExtraDigest{Digest: digest.FromString("multi-partial-y-only-2"), Label: "y-only-2"}

		x1Call := cacheTestIntCall("multi-partial-x-1")
		x2Call := cacheTestIntCall("multi-partial-x-2")
		y1Call := cacheTestIntCall("multi-partial-y-1")
		y2Call := cacheTestIntCall("multi-partial-y-2")
		x1OutCall := cacheTestIntCall("multi-partial-x-1", xShared, xNoise1)
		x2OutCall := cacheTestIntCall("multi-partial-x-2", xShared, xNoise2)
		y1OutCall := cacheTestIntCall("multi-partial-y-1", yOnly1)
		y2OutCall := cacheTestIntCall("multi-partial-y-2", yOnly2)

		x1Res, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: x1Call}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(x1OutCall, 31), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !x1Res.HitCache())
		x2Res, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: x2Call}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(x2OutCall, 32), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !x2Res.HitCache())

		y1Res, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: y1Call}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(y1OutCall, 41), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !y1Res.HitCache())
		y2Res, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: y2Call}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(y2OutCall, 42), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !y2Res.HitCache())

		zReq := func(xRes, yRes AnyResult) *CallRequest {
			xShared := xRes.cacheSharedResult()
			yShared := yRes.cacheSharedResult()
			return &CallRequest{
				ResultCall: &ResultCall{
					Kind:  ResultCallKindField,
					Type:  NewResultCallType(Int(0).Type()),
					Field: "multi-partial-z",
					Args: []*ResultCallArg{
						{Name: "x", Value: &ResultCallLiteral{Kind: ResultCallLiteralKindResultRef, ResultRef: &ResultCallRef{ResultID: uint64(xShared.id)}}},
						{Name: "y", Value: &ResultCallLiteral{Kind: ResultCallLiteralKindResultRef, ResultRef: &ResultCallRef{ResultID: uint64(yShared.id)}}},
					},
				},
			}
		}

		z1Res, err := c.GetOrInitCall(ctx, zReq(x1Res, y1Res), func(context.Context) (AnyResult, error) {
			return cacheTestPlainResult(NewInt(701)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !z1Res.HitCache())

		z2InitCalls := 0
		z2Res, err := c.GetOrInitCall(ctx, zReq(x2Res, y2Res), func(context.Context) (AnyResult, error) {
			z2InitCalls++
			return cacheTestPlainResult(NewInt(702)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 1, z2InitCalls)
		assert.Assert(t, !z2Res.HitCache())
		assert.Equal(t, 702, cacheTestUnwrapInt(t, z2Res))
		assert.Assert(t, cacheTestMustEncodeID(t, z2Res) != cacheTestMustEncodeID(t, z1Res))

		assert.NilError(t, x1Res.Release(ctx))
		assert.NilError(t, x2Res.Release(ctx))
		assert.NilError(t, y1Res.Release(ctx))
		assert.NilError(t, y2Res.Release(ctx))
		assert.NilError(t, z1Res.Release(ctx))
		assert.NilError(t, z2Res.Release(ctx))
		assert.Equal(t, 0, c.Size())
	})

	// Transitive bridge case: f1 and f3 do not share a direct digest, but f2
	// links both sides (A on f1/f2 and B on f2/f3), so equivalence should merge
	// transitively. After caching g(f1), a lookup of g(f3) should hit while still
	// returning g3Key as the request-facing ID digest.
	t.Run("transitive_extra_digest_merge_bridge_hit", func(t *testing.T) {
		ctx := t.Context()
		cacheIface, err := NewCache(ctx, "")
		assert.NilError(t, err)
		c := cacheIface.(*cache)

		bridgeA := call.ExtraDigest{Digest: digest.FromString("bridge-a"), Label: "bridge-a"}
		bridgeB := call.ExtraDigest{Digest: digest.FromString("bridge-b"), Label: "bridge-b"}
		noise1 := call.ExtraDigest{Digest: digest.FromString("bridge-noise-1"), Label: "noise-1"}
		noise2 := call.ExtraDigest{Digest: digest.FromString("bridge-noise-2"), Label: "noise-2"}
		noise3 := call.ExtraDigest{Digest: digest.FromString("bridge-noise-3"), Label: "noise-3"}

		f1Call := cacheTestIntCall("bridge-f-1")
		f2Call := cacheTestIntCall("bridge-f-2")
		f3Call := cacheTestIntCall("bridge-f-3")
		f1OutCall := cacheTestIntCall("bridge-f-1", bridgeA, noise1)
		f2OutCall := cacheTestIntCall("bridge-f-2", bridgeA, bridgeB, noise2)
		f3OutCall := cacheTestIntCall("bridge-f-3", bridgeB, noise3)

		f1Res, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: f1Call}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(f1OutCall, 101), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !f1Res.HitCache())
		f2Res, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: f2Call}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(f2OutCall, 102), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !f2Res.HitCache())
		f3Res, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: f3Call}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(f3OutCall, 103), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !f3Res.HitCache())

		gReq := func(parent AnyResult) *CallRequest {
			parentShared := parent.cacheSharedResult()
			return &CallRequest{
				ResultCall: &ResultCall{
					Kind:     ResultCallKindField,
					Type:     NewResultCallType(Int(0).Type()),
					Field:    "bridge-g",
					Receiver: &ResultCallRef{ResultID: uint64(parentShared.id)},
				},
			}
		}

		g1Res, err := c.GetOrInitCall(ctx, gReq(f1Res), func(context.Context) (AnyResult, error) {
			return cacheTestPlainResult(NewInt(901)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !g1Res.HitCache())

		g3InitCalls := 0
		g3Res, err := c.GetOrInitCall(ctx, gReq(f3Res), func(context.Context) (AnyResult, error) {
			g3InitCalls++
			return cacheTestPlainResult(NewInt(903)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, g3InitCalls)
		assert.Assert(t, g3Res.HitCache())
		assert.Equal(t, 901, cacheTestUnwrapInt(t, g3Res))
		assert.Equal(t, cacheTestMustEncodeID(t, g1Res), cacheTestMustEncodeID(t, g3Res))

		assert.NilError(t, g1Res.Release(ctx))
		assert.NilError(t, g3Res.Release(ctx))
		assert.NilError(t, f1Res.Release(ctx))
		assert.NilError(t, f2Res.Release(ctx))
		assert.NilError(t, f3Res.Release(ctx))
		assert.Equal(t, 0, c.Size())
	})

	// Negative bridge case: f1 and f2 overlap on A, but f3 only overlaps with B
	// and f2 does not carry B, so there is no bridge from f1 to f3.
	// We still expect g(f2) to hit from g(f1), while g(f3) must remain a miss.
	t.Run("transitive_bridge_no_bridge_no_hit", func(t *testing.T) {
		ctx := t.Context()
		cacheIface, err := NewCache(ctx, "")
		assert.NilError(t, err)
		c := cacheIface.(*cache)

		bridgeA := call.ExtraDigest{Digest: digest.FromString("nobridge-a"), Label: "bridge-a"}
		bridgeB := call.ExtraDigest{Digest: digest.FromString("nobridge-b"), Label: "bridge-b"}
		other := call.ExtraDigest{Digest: digest.FromString("nobridge-other"), Label: "other"}
		noise1 := call.ExtraDigest{Digest: digest.FromString("nobridge-noise-1"), Label: "noise-1"}
		noise2 := call.ExtraDigest{Digest: digest.FromString("nobridge-noise-2"), Label: "noise-2"}
		noise3 := call.ExtraDigest{Digest: digest.FromString("nobridge-noise-3"), Label: "noise-3"}

		f1Call := cacheTestIntCall("nobridge-f-1")
		f2Call := cacheTestIntCall("nobridge-f-2")
		f3Call := cacheTestIntCall("nobridge-f-3")
		f1OutCall := cacheTestIntCall("nobridge-f-1", bridgeA, noise1)
		f2OutCall := cacheTestIntCall("nobridge-f-2", bridgeA, other, noise2)
		f3OutCall := cacheTestIntCall("nobridge-f-3", bridgeB, noise3)

		f1Res, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: f1Call}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(f1OutCall, 111), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !f1Res.HitCache())
		f2Res, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: f2Call}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(f2OutCall, 112), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !f2Res.HitCache())
		f3Res, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: f3Call}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(f3OutCall, 113), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !f3Res.HitCache())

		gReq := func(parent AnyResult) *CallRequest {
			parentShared := parent.cacheSharedResult()
			return &CallRequest{
				ResultCall: &ResultCall{
					Kind:     ResultCallKindField,
					Type:     NewResultCallType(Int(0).Type()),
					Field:    "nobridge-g",
					Receiver: &ResultCallRef{ResultID: uint64(parentShared.id)},
				},
			}
		}

		g1Res, err := c.GetOrInitCall(ctx, gReq(f1Res), func(context.Context) (AnyResult, error) {
			return cacheTestPlainResult(NewInt(911)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !g1Res.HitCache())

		g2InitCalls := 0
		g2Res, err := c.GetOrInitCall(ctx, gReq(f2Res), func(context.Context) (AnyResult, error) {
			g2InitCalls++
			return cacheTestPlainResult(NewInt(912)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, g2InitCalls)
		assert.Assert(t, g2Res.HitCache())
		assert.Equal(t, 911, cacheTestUnwrapInt(t, g2Res))
		assert.Equal(t, cacheTestMustEncodeID(t, g1Res), cacheTestMustEncodeID(t, g2Res))

		g3InitCalls := 0
		g3Res, err := c.GetOrInitCall(ctx, gReq(f3Res), func(context.Context) (AnyResult, error) {
			g3InitCalls++
			return cacheTestPlainResult(NewInt(913)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 1, g3InitCalls)
		assert.Assert(t, !g3Res.HitCache())
		assert.Equal(t, 913, cacheTestUnwrapInt(t, g3Res))
		assert.Assert(t, cacheTestMustEncodeID(t, g3Res) != cacheTestMustEncodeID(t, g1Res))

		assert.NilError(t, g1Res.Release(ctx))
		assert.NilError(t, g2Res.Release(ctx))
		assert.NilError(t, g3Res.Release(ctx))
		assert.NilError(t, f1Res.Release(ctx))
		assert.NilError(t, f2Res.Release(ctx))
		assert.NilError(t, f3Res.Release(ctx))
		assert.Equal(t, 0, c.Size())
	})

	// Fanout/fanin repair case:
	//
	//   branch 1 (seeded first):
	//     f1 -> left1
	//       \-> right1
	//     join1(left1,right1)
	//
	//   branch 2 (different recipe):
	//     f2 -> left2
	//       \-> right2
	//     join2(left2,right2)
	//
	//   equivalence digest introduced later:
	//     f1 ~ f2   (shared extra digest)
	//
	// Expected repair/propagation:
	//   left1 ~ left2
	//   right1 ~ right2
	//   => join1(left1,right1) ~ join2(left2,right2)
	t.Run("fanout_fanin_join_hit_after_repair", func(t *testing.T) {
		ctx := t.Context()
		cacheIface, err := NewCache(ctx, "")
		assert.NilError(t, err)
		c := cacheIface.(*cache)

		shared := call.ExtraDigest{Digest: digest.FromString("fanout-shared"), Label: "fanout-shared"}
		noise1 := call.ExtraDigest{Digest: digest.FromString("fanout-noise-1"), Label: "noise-1"}
		noise2 := call.ExtraDigest{Digest: digest.FromString("fanout-noise-2"), Label: "noise-2"}

		rootReq := func(field string, extras ...call.ExtraDigest) *CallRequest {
			return &CallRequest{
				ResultCall: &ResultCall{
					Kind:         ResultCallKindField,
					Type:         NewResultCallType(Int(0).Type()),
					Field:        field,
					ExtraDigests: slices.Clone(extras),
				},
			}
		}

		f1Res, err := c.GetOrInitCall(ctx, rootReq("fanout-f-1"), func(context.Context) (AnyResult, error) {
			return cacheTestPlainResult(NewInt(101)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !f1Res.HitCache())
		f2Res, err := c.GetOrInitCall(ctx, rootReq("fanout-f-2"), func(context.Context) (AnyResult, error) {
			return cacheTestPlainResult(NewInt(102)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !f2Res.HitCache())

		unaryReq := func(parent AnyResult, field string) *CallRequest {
			parentShared := parent.cacheSharedResult()
			return &CallRequest{
				ResultCall: &ResultCall{
					Kind:     ResultCallKindField,
					Type:     NewResultCallType(Int(0).Type()),
					Field:    field,
					Receiver: &ResultCallRef{ResultID: uint64(parentShared.id)},
				},
			}
		}
		joinReq := func(left, right AnyResult) *CallRequest {
			leftShared := left.cacheSharedResult()
			rightShared := right.cacheSharedResult()
			return &CallRequest{
				ResultCall: &ResultCall{
					Kind:  ResultCallKindField,
					Type:  NewResultCallType(Int(0).Type()),
					Field: "fanout-join",
					Args: []*ResultCallArg{
						{Name: "left", Value: &ResultCallLiteral{Kind: ResultCallLiteralKindResultRef, ResultRef: &ResultCallRef{ResultID: uint64(leftShared.id)}}},
						{Name: "right", Value: &ResultCallLiteral{Kind: ResultCallLiteralKindResultRef, ResultRef: &ResultCallRef{ResultID: uint64(rightShared.id)}}},
					},
				},
			}
		}

		left1Res, err := c.GetOrInitCall(ctx, unaryReq(f1Res, "fanout-left"), func(context.Context) (AnyResult, error) {
			return cacheTestPlainResult(NewInt(1001)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !left1Res.HitCache())
		right1Res, err := c.GetOrInitCall(ctx, unaryReq(f1Res, "fanout-right"), func(context.Context) (AnyResult, error) {
			return cacheTestPlainResult(NewInt(1002)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !right1Res.HitCache())
		join1Res, err := c.GetOrInitCall(ctx, joinReq(left1Res, right1Res), func(context.Context) (AnyResult, error) {
			return cacheTestPlainResult(NewInt(1101)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !join1Res.HitCache())

		f1AliasInitCalls := 0
		f1AliasRes, err := c.GetOrInitCall(ctx, rootReq("fanout-f-1", shared, noise1), func(context.Context) (AnyResult, error) {
			f1AliasInitCalls++
			return cacheTestPlainResult(NewInt(1911)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, f1AliasInitCalls)
		assert.Assert(t, f1AliasRes.HitCache())
		assert.Equal(t, 101, cacheTestUnwrapInt(t, f1AliasRes))

		f2AliasInitCalls := 0
		f2AliasRes, err := c.GetOrInitCall(ctx, rootReq("fanout-f-2", shared, noise2), func(context.Context) (AnyResult, error) {
			f2AliasInitCalls++
			return cacheTestPlainResult(NewInt(1912)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, f2AliasInitCalls)
		assert.Assert(t, f2AliasRes.HitCache())
		assert.Equal(t, 102, cacheTestUnwrapInt(t, f2AliasRes))

		left2InitCalls := 0
		left2Res, err := c.GetOrInitCall(ctx, unaryReq(f2Res, "fanout-left"), func(context.Context) (AnyResult, error) {
			left2InitCalls++
			return cacheTestPlainResult(NewInt(2001)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, left2InitCalls)
		assert.Assert(t, left2Res.HitCache())
		assert.Equal(t, 1001, cacheTestUnwrapInt(t, left2Res))

		right2InitCalls := 0
		right2Res, err := c.GetOrInitCall(ctx, unaryReq(f2Res, "fanout-right"), func(context.Context) (AnyResult, error) {
			right2InitCalls++
			return cacheTestPlainResult(NewInt(2002)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, right2InitCalls)
		assert.Assert(t, right2Res.HitCache())
		assert.Equal(t, 1002, cacheTestUnwrapInt(t, right2Res))

		join2InitCalls := 0
		join2Res, err := c.GetOrInitCall(ctx, joinReq(left2Res, right2Res), func(context.Context) (AnyResult, error) {
			join2InitCalls++
			return cacheTestPlainResult(NewInt(2101)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, join2InitCalls)
		assert.Assert(t, join2Res.HitCache())
		assert.Equal(t, 1101, cacheTestUnwrapInt(t, join2Res))
		assert.Equal(t, cacheTestMustEncodeID(t, join1Res), cacheTestMustEncodeID(t, join2Res))

		assert.NilError(t, left1Res.Release(ctx))
		assert.NilError(t, left2Res.Release(ctx))
		assert.NilError(t, right1Res.Release(ctx))
		assert.NilError(t, right2Res.Release(ctx))
		assert.NilError(t, join1Res.Release(ctx))
		assert.NilError(t, join2Res.Release(ctx))
		assert.NilError(t, f1AliasRes.Release(ctx))
		assert.NilError(t, f2AliasRes.Release(ctx))
		assert.NilError(t, f1Res.Release(ctx))
		assert.NilError(t, f2Res.Release(ctx))
		assert.Equal(t, 0, c.Size())
	})

	t.Run("fanout_fanin_late_merge_enables_downstream_join_input_hit", func(t *testing.T) {
		ctx := t.Context()
		cacheIface, err := NewCache(ctx, "")
		assert.NilError(t, err)
		c := cacheIface.(*cache)

		shared := call.ExtraDigest{Digest: digest.FromString("fanout-late-shared"), Label: "fanout-shared"}
		noise1 := call.ExtraDigest{Digest: digest.FromString("fanout-late-noise-1"), Label: "noise-1"}
		noise2 := call.ExtraDigest{Digest: digest.FromString("fanout-late-noise-2"), Label: "noise-2"}

		rootReq := func(field string, extras ...call.ExtraDigest) *CallRequest {
			return &CallRequest{
				ResultCall: &ResultCall{
					Kind:         ResultCallKindField,
					Type:         NewResultCallType(Int(0).Type()),
					Field:        field,
					ExtraDigests: slices.Clone(extras),
				},
			}
		}

		f1Res, err := c.GetOrInitCall(ctx, rootReq("fanout-late-f-1"), func(context.Context) (AnyResult, error) {
			return cacheTestPlainResult(NewInt(301)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !f1Res.HitCache())
		f2Res, err := c.GetOrInitCall(ctx, rootReq("fanout-late-f-2"), func(context.Context) (AnyResult, error) {
			return cacheTestPlainResult(NewInt(302)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !f2Res.HitCache())

		unaryReq := func(parent AnyResult, field string) *CallRequest {
			parentShared := parent.cacheSharedResult()
			return &CallRequest{
				ResultCall: &ResultCall{
					Kind:     ResultCallKindField,
					Type:     NewResultCallType(Int(0).Type()),
					Field:    field,
					Receiver: &ResultCallRef{ResultID: uint64(parentShared.id)},
				},
			}
		}
		joinReq := func(left, right AnyResult) *CallRequest {
			leftShared := left.cacheSharedResult()
			rightShared := right.cacheSharedResult()
			return &CallRequest{
				ResultCall: &ResultCall{
					Kind:  ResultCallKindField,
					Type:  NewResultCallType(Int(0).Type()),
					Field: "fanout-late-join",
					Args: []*ResultCallArg{
						{Name: "left", Value: &ResultCallLiteral{Kind: ResultCallLiteralKindResultRef, ResultRef: &ResultCallRef{ResultID: uint64(leftShared.id)}}},
						{Name: "right", Value: &ResultCallLiteral{Kind: ResultCallLiteralKindResultRef, ResultRef: &ResultCallRef{ResultID: uint64(rightShared.id)}}},
					},
				},
			}
		}
		topReq := func(join AnyResult) *CallRequest {
			joinShared := join.cacheSharedResult()
			return &CallRequest{
				ResultCall: &ResultCall{
					Kind:  ResultCallKindField,
					Type:  NewResultCallType(Int(0).Type()),
					Field: "fanout-late-top",
					Args: []*ResultCallArg{
						{Name: "join", Value: &ResultCallLiteral{Kind: ResultCallLiteralKindResultRef, ResultRef: &ResultCallRef{ResultID: uint64(joinShared.id)}}},
					},
				},
			}
		}

		left1Res, err := c.GetOrInitCall(ctx, unaryReq(f1Res, "fanout-late-left"), func(context.Context) (AnyResult, error) {
			return cacheTestPlainResult(NewInt(3001)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !left1Res.HitCache())
		right1Res, err := c.GetOrInitCall(ctx, unaryReq(f1Res, "fanout-late-right"), func(context.Context) (AnyResult, error) {
			return cacheTestPlainResult(NewInt(3002)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !right1Res.HitCache())
		join1Res, err := c.GetOrInitCall(ctx, joinReq(left1Res, right1Res), func(context.Context) (AnyResult, error) {
			return cacheTestPlainResult(NewInt(3101)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !join1Res.HitCache())

		left2Res, err := c.GetOrInitCall(ctx, unaryReq(f2Res, "fanout-late-left"), func(context.Context) (AnyResult, error) {
			return cacheTestPlainResult(NewInt(4001)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !left2Res.HitCache())
		right2Res, err := c.GetOrInitCall(ctx, unaryReq(f2Res, "fanout-late-right"), func(context.Context) (AnyResult, error) {
			return cacheTestPlainResult(NewInt(4002)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !right2Res.HitCache())
		join2Res, err := c.GetOrInitCall(ctx, joinReq(left2Res, right2Res), func(context.Context) (AnyResult, error) {
			return cacheTestPlainResult(NewInt(4101)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !join2Res.HitCache())

		f1AliasInitCalls := 0
		f1AliasRes, err := c.GetOrInitCall(ctx, rootReq("fanout-late-f-1", shared, noise1), func(context.Context) (AnyResult, error) {
			f1AliasInitCalls++
			return cacheTestPlainResult(NewInt(3911)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, f1AliasInitCalls)
		assert.Assert(t, f1AliasRes.HitCache())
		assert.Equal(t, 301, cacheTestUnwrapInt(t, f1AliasRes))

		f2AliasInitCalls := 0
		f2AliasRes, err := c.GetOrInitCall(ctx, rootReq("fanout-late-f-2", shared, noise2), func(context.Context) (AnyResult, error) {
			f2AliasInitCalls++
			return cacheTestPlainResult(NewInt(3912)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, f2AliasInitCalls)
		assert.Assert(t, f2AliasRes.HitCache())
		assert.Equal(t, 302, cacheTestUnwrapInt(t, f2AliasRes))

		top1Res, err := c.GetOrInitCall(ctx, topReq(join1Res), func(context.Context) (AnyResult, error) {
			return cacheTestPlainResult(NewInt(5101)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !top1Res.HitCache())

		top2InitCalls := 0
		top2Res, err := c.GetOrInitCall(ctx, topReq(join2Res), func(context.Context) (AnyResult, error) {
			top2InitCalls++
			return cacheTestPlainResult(NewInt(5201)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, top2InitCalls)
		assert.Assert(t, top2Res.HitCache())
		assert.Equal(t, 5101, cacheTestUnwrapInt(t, top2Res))
		assert.Equal(t, cacheTestMustEncodeID(t, top1Res), cacheTestMustEncodeID(t, top2Res))

		assert.NilError(t, left1Res.Release(ctx))
		assert.NilError(t, left2Res.Release(ctx))
		assert.NilError(t, right1Res.Release(ctx))
		assert.NilError(t, right2Res.Release(ctx))
		assert.NilError(t, join1Res.Release(ctx))
		assert.NilError(t, join2Res.Release(ctx))
		assert.NilError(t, f1Res.Release(ctx))
		assert.NilError(t, f2Res.Release(ctx))
		assert.NilError(t, f1AliasRes.Release(ctx))
		assert.NilError(t, f2AliasRes.Release(ctx))
		assert.NilError(t, top1Res.Release(ctx))
		assert.NilError(t, top2Res.Release(ctx))
		assert.Equal(t, 0, c.Size())
	})
}

func TestDirectDigestLookupHitsWithoutTermIndex(t *testing.T) {
	t.Run("exact_recipe_digest_hit_without_term_index", func(t *testing.T) {
		ctx := t.Context()
		cacheIface, err := NewCache(ctx, "")
		assert.NilError(t, err)
		c := cacheIface.(*cache)

		requestID := call.New().Append(Int(0).Type(), "direct-recipe-request")
		requestCall := cacheTestIntCall("direct-recipe-request")
		outputCall := cacheTestIntCall("direct-recipe-output")
		outputID := call.New().Append(Int(0).Type(), "direct-recipe-output")
		assert.Assert(t, requestID.Digest() != outputID.Digest())

		firstRes, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: requestCall}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(outputCall, 51), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !firstRes.HitCache())

		c.egraphMu.Lock()
		c.inputEqClassToTerms = make(map[eqClassID]map[egraphTermID]struct{})
		c.outputEqClassToTerms = make(map[eqClassID]map[egraphTermID]struct{})
		c.egraphTerms = make(map[egraphTermID]*egraphTerm)
		c.egraphTermsByTermDigest = make(map[string]map[egraphTermID]struct{})
		c.resultOutputEqClasses = make(map[sharedResultID]map[eqClassID]struct{})
		c.termInputProvenance = make(map[egraphTermID][]egraphInputProvenanceKind)
		c.egraphMu.Unlock()

		initCalls := 0
		hitRes, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: requestCall}, func(context.Context) (AnyResult, error) {
			initCalls++
			return cacheTestIntResult(requestCall, 52), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, initCalls)
		assert.Assert(t, hitRes.HitCache())
		assert.Equal(t, 51, cacheTestUnwrapInt(t, hitRes))
		assert.Equal(t, cacheTestMustEncodeID(t, firstRes), cacheTestMustEncodeID(t, hitRes))

		assert.NilError(t, firstRes.Release(ctx))
		assert.NilError(t, hitRes.Release(ctx))
		assert.Equal(t, 0, c.Size())
	})

	t.Run("extra_digest_hit_without_term_index", func(t *testing.T) {
		ctx := t.Context()
		cacheIface, err := NewCache(ctx, "")
		assert.NilError(t, err)
		c := cacheIface.(*cache)

		shared := call.ExtraDigest{
			Digest: digest.FromString("direct-extra-hit-shared"),
			Label:  "shared",
		}
		storedRequestID := call.New().Append(Int(0).Type(), "direct-extra-stored")
		storedRequestCall := cacheTestIntCall("direct-extra-stored")
		storedOutputCall := cacheTestIntCall("direct-extra-stored", shared)
		lookupID := call.New().Append(Int(0).Type(), "direct-extra-lookup").With(call.WithExtraDigest(shared))
		lookupCall := cacheTestIntCall("direct-extra-lookup", shared)
		assert.Assert(t, storedRequestID.Digest() != lookupID.Digest())

		firstRes, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: storedRequestCall}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(storedOutputCall, 71), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !firstRes.HitCache())

		c.egraphMu.Lock()
		c.inputEqClassToTerms = make(map[eqClassID]map[egraphTermID]struct{})
		c.outputEqClassToTerms = make(map[eqClassID]map[egraphTermID]struct{})
		c.egraphTerms = make(map[egraphTermID]*egraphTerm)
		c.egraphTermsByTermDigest = make(map[string]map[egraphTermID]struct{})
		c.resultOutputEqClasses = make(map[sharedResultID]map[eqClassID]struct{})
		c.termInputProvenance = make(map[egraphTermID][]egraphInputProvenanceKind)
		c.egraphMu.Unlock()

		initCalls := 0
		hitRes, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: lookupCall}, func(context.Context) (AnyResult, error) {
			initCalls++
			return cacheTestIntResult(lookupCall, 72), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, initCalls)
		assert.Assert(t, hitRes.HitCache())
		assert.Equal(t, 71, cacheTestUnwrapInt(t, hitRes))
		assert.Equal(t, cacheTestMustEncodeID(t, firstRes), cacheTestMustEncodeID(t, hitRes))

		assert.NilError(t, firstRes.Release(ctx))
		assert.NilError(t, hitRes.Release(ctx))
		assert.Equal(t, 0, c.Size())
	})
}

func TestIndexResultDigestsUsesExplicitRequestAndResponseIDs(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	requestExtra := call.ExtraDigest{
		Digest: digest.FromString("index-explicit-request-extra"),
		Label:  "request-extra",
	}
	responseExtra := call.ExtraDigest{
		Digest: digest.FromString("index-explicit-response-extra"),
		Label:  "response-extra",
	}
	requestCall := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType(Int(0).Type()),
		Field: "index-explicit-request",
		ExtraDigests: []call.ExtraDigest{
			requestExtra,
		},
	}
	responseCall := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType(Int(0).Type()),
		Field: "index-explicit-response",
		ExtraDigests: []call.ExtraDigest{
			responseExtra,
		},
	}

	res, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: requestCall}, func(context.Context) (AnyResult, error) {
		return NewResultForCall(NewInt(42), responseCall)
	})
	assert.NilError(t, err)
	shared := res.cacheSharedResult()
	assert.Assert(t, shared != nil)

	requestDigest, err := requestCall.RecipeDigest()
	assert.NilError(t, err)
	responseDigest, err := responseCall.RecipeDigest()
	assert.NilError(t, err)
	for _, dig := range []digest.Digest{
		requestDigest,
		requestExtra.Digest,
		responseDigest,
		responseExtra.Digest,
	} {
		postings := c.egraphResultsByDigest[dig.String()]
		_, ok := postings[shared.id]
		assert.Assert(t, ok, "expected posting for digest %s", dig)
	}

	assert.NilError(t, res.Release(ctx))
}

func TestStructuralHitCanReuseResultFromSameOutputEqClass(t *testing.T) {
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	shared := call.ExtraDigest{
		Digest: digest.FromString("shared-output-eq-class"),
		Label:  "shared",
	}

	t1Key := call.New().Append(Int(0).Type(), "output-eq-term-1")
	t1Call := cacheTestIntCall("output-eq-term-1")
	t2Key := call.New().Append(Int(0).Type(), "output-eq-term-2")
	t2Call := cacheTestIntCall("output-eq-term-2")
	assert.Assert(t, t1Key.Digest() != t2Key.Digest())

	t1OutCall := cacheTestIntCall("output-eq-out-1", shared)
	t2OutCall := cacheTestIntCall("output-eq-out-2", shared)

	t1Res, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: t1Call}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(t1OutCall, 11), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !t1Res.HitCache())

	t2Res, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: t2Call}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(t2OutCall, 22), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !t2Res.HitCache())

	assert.NilError(t, t1Res.Release(ctx))

	initCalls := 0
	hitRes, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: t1Call}, func(context.Context) (AnyResult, error) {
		initCalls++
		return cacheTestIntResult(t1Call, 33), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 0, initCalls)
	assert.Assert(t, hitRes.HitCache())
	assert.Equal(t, 22, cacheTestUnwrapInt(t, hitRes))
	assert.Equal(t, cacheTestMustEncodeID(t, t2Res), cacheTestMustEncodeID(t, hitRes))

	assert.NilError(t, t2Res.Release(ctx))
	assert.NilError(t, hitRes.Release(ctx))
	assert.Equal(t, 0, c.Size())
}

func TestTeachCallEquivalentToResult(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	parentCall := cacheTestIntCall("teach-parent")
	parentRes, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: parentCall}, func(context.Context) (AnyResult, error) {
		return cacheTestPlainResult(NewInt(42)), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !parentRes.HitCache())

	parentShared := parentRes.cacheSharedResult()
	assert.Assert(t, parentShared != nil)

	childCall := &ResultCall{
		Kind:     ResultCallKindField,
		Type:     NewResultCallType(Int(0).Type()),
		Field:    "teach-child",
		Receiver: &ResultCallRef{ResultID: uint64(parentShared.id)},
	}

	assert.NilError(t, c.TeachCallEquivalentToResult(ctx, childCall, parentRes))

	childInitCalls := 0
	childRes, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: childCall.clone()}, func(context.Context) (AnyResult, error) {
		childInitCalls++
		return cacheTestPlainResult(NewInt(99)), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 0, childInitCalls)
	assert.Assert(t, childRes.HitCache())
	assert.Equal(t, 42, cacheTestUnwrapInt(t, childRes))
	assert.Equal(t, cacheTestMustEncodeID(t, parentRes), cacheTestMustEncodeID(t, childRes))

	assert.NilError(t, childRes.Release(ctx))
	assert.NilError(t, parentRes.Release(ctx))
	assert.Equal(t, 0, c.Size())
}

func TestPendingResultCallRefRecipeID(t *testing.T) {
	t.Parallel()

	parentCall := cacheTestIntCall("pending-parent")
	childCall := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType(Int(0).Type()),
		Field: "pending-child",
		Receiver: &ResultCallRef{
			Call: parentCall.clone(),
		},
	}

	childRes, err := NewResultForCall(NewInt(2), childCall)
	assert.NilError(t, err)

	parentRecipeID, err := parentCall.RecipeID()
	assert.NilError(t, err)
	childRecipeID, err := childRes.RecipeID()
	assert.NilError(t, err)
	assert.Assert(t, childRecipeID.Receiver() != nil)
	assert.Equal(t, parentRecipeID.Digest(), childRecipeID.Receiver().Digest())
}

func TestAttachResultNormalizesPendingResultCallRef(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	parentCall := cacheTestIntCall("normalize-parent")
	parentRes, err := NewResultForCall(NewInt(1), parentCall)
	assert.NilError(t, err)
	attachedParent, err := c.AttachResult(ctx, parentRes)
	assert.NilError(t, err)

	childCall := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType(Int(0).Type()),
		Field: "normalize-child",
		Receiver: &ResultCallRef{
			Call: parentCall.clone(),
		},
	}
	childRes, err := NewResultForCall(NewInt(2), childCall)
	assert.NilError(t, err)
	attachedChild, err := c.AttachResult(ctx, childRes)
	assert.NilError(t, err)

	parentShared := attachedParent.cacheSharedResult()
	childShared := attachedChild.cacheSharedResult()
	assert.Assert(t, parentShared != nil && parentShared.id != 0)
	assert.Assert(t, childShared != nil && childShared.id != 0)
	assert.Assert(t, childShared.resultCall != nil)
	assert.Assert(t, childShared.resultCall.Receiver != nil)
	assert.Equal(t, uint64(parentShared.id), childShared.resultCall.Receiver.ResultID)
	assert.Assert(t, childShared.resultCall.Receiver.Call == nil)

	assert.NilError(t, attachedChild.Release(ctx))
	assert.NilError(t, attachedParent.Release(ctx))
	assert.Equal(t, 0, c.Size())
}

func TestObjectResultResultCallAndReceiver(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	srv := cacheTestServer(t, cacheIface)

	objType := (&cacheTestObject{}).Type()

	parentFrame := &ResultCall{
		Kind:  ResultCallKindField,
		Field: "parent",
		Type:  NewResultCallType(objType),
	}
	parentRes := cacheTestObjectResult(t, srv, parentFrame, 11, nil)
	attachedParentAny, err := srv.Cache.AttachResult(ctx, parentRes)
	assert.NilError(t, err)
	attachedParent := attachedParentAny.(ObjectResult[*cacheTestObject])

	parentID, err := attachedParent.ID()
	assert.NilError(t, err)
	parentDig, err := attachedParent.ContentPreferredDigest()
	assert.NilError(t, err)
	parentRecipeID, err := attachedParent.RecipeID()
	assert.NilError(t, err)
	assert.Equal(t, parentRecipeID.ContentPreferredDigest().String(), parentDig.String())

	argOnlyFrame := &ResultCall{
		Kind:  ResultCallKindField,
		Field: "argOnly",
		Type:  NewResultCallType(objType),
		Args: []*ResultCallArg{
			{
				Name:  "msg",
				Value: &ResultCallLiteral{Kind: ResultCallLiteralKindString, StringValue: "hello"},
			},
		},
	}
	argOnlyRes := cacheTestObjectResult(t, srv, argOnlyFrame, 12, nil)
	attachedArgOnlyAny, err := srv.Cache.AttachResult(ctx, argOnlyRes)
	assert.NilError(t, err)
	attachedArgOnly := attachedArgOnlyAny.(ObjectResult[*cacheTestObject])
	argOnlyDig, err := attachedArgOnly.ContentPreferredDigest()
	assert.NilError(t, err)
	argOnlyRecipeID, err := attachedArgOnly.RecipeID()
	assert.NilError(t, err)
	assert.Equal(t, argOnlyRecipeID.ContentPreferredDigest().String(), argOnlyDig.String())

	receiverOnlyFrame := &ResultCall{
		Kind:  ResultCallKindField,
		Field: "receiverOnly",
		Type:  NewResultCallType(objType),
		Receiver: &ResultCallRef{
			ResultID: parentID.EngineResultID(),
		},
	}
	receiverOnlyRes := cacheTestObjectResult(t, srv, receiverOnlyFrame, 13, nil)
	attachedReceiverOnlyAny, err := srv.Cache.AttachResult(ctx, receiverOnlyRes)
	assert.NilError(t, err)
	attachedReceiverOnly := attachedReceiverOnlyAny.(ObjectResult[*cacheTestObject])
	receiverOnlyDig, err := attachedReceiverOnly.ContentPreferredDigest()
	assert.NilError(t, err)
	receiverOnlyRecipeID, err := attachedReceiverOnly.RecipeID()
	assert.NilError(t, err)
	assert.Equal(t, receiverOnlyRecipeID.ContentPreferredDigest().String(), receiverOnlyDig.String())

	childFrame := &ResultCall{
		Kind:  ResultCallKindField,
		Field: "child",
		Type:  NewResultCallType(objType),
		Receiver: &ResultCallRef{
			ResultID: parentID.EngineResultID(),
		},
	}
	childRes := cacheTestObjectResult(t, srv, childFrame, 22, nil)
	attachedChildAny, err := srv.Cache.AttachResult(ctx, childRes)
	assert.NilError(t, err)
	attachedChild := attachedChildAny.(ObjectResult[*cacheTestObject])

	childCall, err := attachedChild.ResultCall()
	assert.NilError(t, err)
	assert.Equal(t, "child", childCall.Field)
	assert.Equal(t, parentID.EngineResultID(), childCall.Receiver.ResultID)

	receiver, err := attachedChild.Receiver(ctx, srv)
	assert.NilError(t, err)
	assert.Assert(t, receiver != nil)

	receiverObj := receiver.(ObjectResult[*cacheTestObject])
	assert.Equal(t, 11, receiverObj.Self().Value)

	receiverCall, err := receiverObj.ResultCall()
	assert.NilError(t, err)
	assert.Equal(t, "parent", receiverCall.Field)
}

func TestInputSpecsInputsFromResultCallArgs(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	specs := NewInputSpecs(
		InputSpec{Name: "msg", Type: String("")},
		InputSpec{Name: "count", Type: Int(0), Default: NewInt(7)},
	)
	args := []*ResultCallArg{
		{
			Name:  "msg",
			Value: &ResultCallLiteral{Kind: ResultCallLiteralKindString, StringValue: "hello"},
		},
	}

	inputs, err := specs.InputsFromResultCallArgs(ctx, args, "")
	assert.NilError(t, err)

	msg := inputs["msg"].(String)
	count := inputs["count"].(Int)
	assert.Equal(t, "hello", msg.String())
	assert.Equal(t, 7, count.Int())
}

func TestResultContentPreferredDigestMatchesRecipeID(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	srv := cacheTestServer(t, cacheIface)

	objType := (&cacheTestObject{}).Type()

	parentFrame := &ResultCall{
		Kind:  ResultCallKindField,
		Field: "parent",
		Type:  NewResultCallType(objType),
	}
	parentRes := cacheTestObjectResult(t, srv, parentFrame, 11, nil)
	attachedParentAny, err := srv.Cache.AttachResult(ctx, parentRes)
	assert.NilError(t, err)
	attachedParent := attachedParentAny.(ObjectResult[*cacheTestObject])

	parentID, err := attachedParent.ID()
	assert.NilError(t, err)

	childFrame := &ResultCall{
		Kind:  ResultCallKindField,
		Field: "child",
		Type:  NewResultCallType(objType),
		Receiver: &ResultCallRef{
			ResultID: parentID.EngineResultID(),
		},
		Args: []*ResultCallArg{
			{
				Name:  "msg",
				Value: &ResultCallLiteral{Kind: ResultCallLiteralKindString, StringValue: "hello"},
			},
		},
	}
	childRes := cacheTestObjectResult(t, srv, childFrame, 22, nil)
	attachedChildAny, err := srv.Cache.AttachResult(ctx, childRes)
	assert.NilError(t, err)
	attachedChild := attachedChildAny.(ObjectResult[*cacheTestObject])

	got, err := attachedChild.ContentPreferredDigest()
	assert.NilError(t, err)
	recipeID, err := attachedChild.RecipeID()
	assert.NilError(t, err)
	assert.Equal(t, recipeID.ContentPreferredDigest().String(), got.String())
}

func TestResultContentPreferredDigestUsesContentDigest(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	srv := cacheTestServer(t, cacheIface)

	contentDig := digest.FromString("service-content")
	objType := (&cacheTestObject{}).Type()
	frame := &ResultCall{
		Kind:         ResultCallKindField,
		Field:        "service",
		Type:         NewResultCallType(objType),
		ExtraDigests: []call.ExtraDigest{{Label: call.ExtraDigestLabelContent, Digest: contentDig}},
	}
	res := cacheTestObjectResult(t, srv, frame, 33, nil)
	attachedAny, err := srv.Cache.AttachResult(ctx, res)
	assert.NilError(t, err)
	attached := attachedAny.(ObjectResult[*cacheTestObject])

	got, err := attached.ContentPreferredDigest()
	assert.NilError(t, err)
	assert.Equal(t, contentDig.String(), got.String())

	recipeID, err := attached.RecipeID()
	assert.NilError(t, err)
	assert.Equal(t, contentDig.String(), recipeID.ContentPreferredDigest().String())
}

func TestLookupCacheForIDExtraDigestFallback(t *testing.T) {
	t.Parallel()

	t.Run("hit_on_exact_output_digest_match", func(t *testing.T) {
		ctx := t.Context()
		cacheIface, err := NewCache(ctx, "")
		assert.NilError(t, err)
		c := cacheIface.(*cache)

		shared := call.ExtraDigest{
			Digest: digest.FromString("fallback-extra-shared"),
			Label:  "shared",
		}

		sourceKey := call.New().Append(Int(0).Type(), "_contextDirectory")
		sourceCall := cacheTestIntCall("_contextDirectory")
		sourceRes, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: sourceCall}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(&ResultCall{
				Kind:         ResultCallKindField,
				Type:         NewResultCallType(Int(0).Type()),
				Field:        "_contextDirectory",
				ExtraDigests: []call.ExtraDigest{shared},
			}, 71), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !sourceRes.HitCache())

		requestKey := sourceKey.
			WithArgument(call.NewArgument("variant", call.NewLiteralInt(1), false)).
			With(call.WithExtraDigest(shared))
		requestCall := &ResultCall{
			Kind:         ResultCallKindField,
			Type:         NewResultCallType(Int(0).Type()),
			Field:        "_contextDirectory",
			ExtraDigests: []call.ExtraDigest{shared},
			Args: []*ResultCallArg{{
				Name:  "variant",
				Value: &ResultCallLiteral{Kind: ResultCallLiteralKindInt, IntValue: 1},
			}},
		}
		assert.Assert(t, sourceKey.Digest() != requestKey.Digest())

		requestInitCalls := 0
		requestRes, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: requestCall}, func(context.Context) (AnyResult, error) {
			requestInitCalls++
			return cacheTestIntResult(requestCall, 999), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, requestInitCalls)
		assert.Assert(t, requestRes.HitCache())
		assert.Equal(t, 71, cacheTestUnwrapInt(t, requestRes))
		assert.Equal(t, cacheTestMustEncodeID(t, sourceRes), cacheTestMustEncodeID(t, requestRes))
		foundShared := false
		for _, extra := range cacheTestMustRecipeID(t, requestRes).ExtraDigests() {
			if extra.Digest == shared.Digest && extra.Label == shared.Label {
				foundShared = true
				break
			}
		}
		assert.Assert(t, foundShared)

		assert.NilError(t, sourceRes.Release(ctx))
		assert.NilError(t, requestRes.Release(ctx))
		assert.Equal(t, 0, c.Size())
	})

	t.Run("miss_without_exact_output_digest_match", func(t *testing.T) {
		ctx := t.Context()
		cacheIface, err := NewCache(ctx, "")
		assert.NilError(t, err)
		c := cacheIface.(*cache)

		sourceExtra := call.ExtraDigest{
			Digest: digest.FromString("fallback-extra-source"),
			Label:  "source",
		}
		requestExtra := call.ExtraDigest{
			Digest: digest.FromString("fallback-extra-request"),
			Label:  "request",
		}

		sourceCall := cacheTestIntCall("fallback-miss-source")
		sourceRes, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: sourceCall}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(cacheTestIntCall("fallback-miss-source", sourceExtra), 81), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !sourceRes.HitCache())

		requestCall := cacheTestIntCall("fallback-miss-request", requestExtra)
		requestInitCalls := 0
		requestRes, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: requestCall}, func(context.Context) (AnyResult, error) {
			requestInitCalls++
			return cacheTestIntResult(requestCall, 82), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 1, requestInitCalls)
		assert.Assert(t, !requestRes.HitCache())
		assert.Equal(t, 82, cacheTestUnwrapInt(t, requestRes))

		assert.NilError(t, sourceRes.Release(ctx))
		assert.NilError(t, requestRes.Release(ctx))
		assert.Equal(t, 0, c.Size())
	})
}

func TestCacheReleaseLifecycleEquivalentGraphMixedReleaseOrder(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	sharedEq := call.ExtraDigest{
		Digest: digest.FromString("release-shared-eq"),
		Label:  "eq-shared",
	}
	noiseA := call.ExtraDigest{
		Digest: digest.FromString("release-noise-a"),
		Label:  "noise-a",
	}
	noiseB := call.ExtraDigest{
		Digest: digest.FromString("release-noise-b"),
		Label:  "noise-b",
	}

	f1Call := cacheTestIntCall("release-f-1")
	f2Call := cacheTestIntCall("release-f-2")
	f1OutCall := cacheTestIntCall("release-f-1", sharedEq, noiseA)
	f2OutCall := cacheTestIntCall("release-f-2", sharedEq, noiseB)

	f1Res, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: f1Call}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(f1OutCall, 101), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !f1Res.HitCache())

	f2Res, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: f2Call}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(f2OutCall, 102), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !f2Res.HitCache())

	g1Res, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: &ResultCall{
		Kind:     ResultCallKindField,
		Type:     NewResultCallType(Int(0).Type()),
		Field:    "release-g",
		Receiver: &ResultCallRef{ResultID: uint64(f1Res.cacheSharedResult().id)},
	}}, func(context.Context) (AnyResult, error) {
		return cacheTestPlainResult(NewInt(201)), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !g1Res.HitCache())

	g2InitCalls := 0
	g2Res, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: &ResultCall{
		Kind:     ResultCallKindField,
		Type:     NewResultCallType(Int(0).Type()),
		Field:    "release-g",
		Receiver: &ResultCallRef{ResultID: uint64(f2Res.cacheSharedResult().id)},
	}}, func(context.Context) (AnyResult, error) {
		g2InitCalls++
		return cacheTestPlainResult(NewInt(202)), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 0, g2InitCalls)
	assert.Assert(t, g2Res.HitCache())
	assert.Equal(t, 201, cacheTestUnwrapInt(t, g2Res))
	assert.Equal(t, cacheTestMustEncodeID(t, g1Res), cacheTestMustEncodeID(t, g2Res))

	h1Res, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: &ResultCall{
		Kind:     ResultCallKindField,
		Type:     NewResultCallType(Int(0).Type()),
		Field:    "release-h",
		Receiver: &ResultCallRef{ResultID: uint64(g1Res.cacheSharedResult().id)},
	}}, func(context.Context) (AnyResult, error) {
		return cacheTestPlainResult(NewInt(301)), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !h1Res.HitCache())

	h2InitCalls := 0
	h2Res, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: &ResultCall{
		Kind:     ResultCallKindField,
		Type:     NewResultCallType(Int(0).Type()),
		Field:    "release-h",
		Receiver: &ResultCallRef{ResultID: uint64(g2Res.cacheSharedResult().id)},
	}}, func(context.Context) (AnyResult, error) {
		h2InitCalls++
		return cacheTestPlainResult(NewInt(302)), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 0, h2InitCalls)
	assert.Assert(t, h2Res.HitCache())
	assert.Equal(t, 301, cacheTestUnwrapInt(t, h2Res))
	assert.Equal(t, cacheTestMustEncodeID(t, h1Res), cacheTestMustEncodeID(t, h2Res))

	j1Res, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: &ResultCall{
		Kind:     ResultCallKindField,
		Type:     NewResultCallType(Int(0).Type()),
		Field:    "release-j",
		Receiver: &ResultCallRef{ResultID: uint64(g1Res.cacheSharedResult().id)},
	}}, func(context.Context) (AnyResult, error) {
		return cacheTestPlainResult(NewInt(401)), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !j1Res.HitCache())

	j2InitCalls := 0
	j2Res, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: &ResultCall{
		Kind:     ResultCallKindField,
		Type:     NewResultCallType(Int(0).Type()),
		Field:    "release-j",
		Receiver: &ResultCallRef{ResultID: uint64(g2Res.cacheSharedResult().id)},
	}}, func(context.Context) (AnyResult, error) {
		j2InitCalls++
		return cacheTestPlainResult(NewInt(402)), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 0, j2InitCalls)
	assert.Assert(t, j2Res.HitCache())
	assert.Equal(t, 401, cacheTestUnwrapInt(t, j2Res))
	assert.Equal(t, cacheTestMustEncodeID(t, j1Res), cacheTestMustEncodeID(t, j2Res))

	// Five unique shared results are alive: f1, f2, g, h, j.
	assert.Equal(t, 5, c.Size())
	assert.Equal(t, 5, len(c.resultOutputEqClasses))

	// Release in intentionally mixed order to verify ref-count/e-graph cleanup.
	assert.NilError(t, g2Res.Release(ctx))
	assert.Equal(t, 5, c.Size())
	assert.Equal(t, 5, len(c.resultOutputEqClasses))

	assert.NilError(t, h1Res.Release(ctx))
	assert.Equal(t, 5, c.Size())
	assert.Equal(t, 5, len(c.resultOutputEqClasses))

	assert.NilError(t, f1Res.Release(ctx))
	assert.Equal(t, 4, c.Size())
	assert.Equal(t, 4, len(c.resultOutputEqClasses))

	assert.NilError(t, j2Res.Release(ctx))
	assert.Equal(t, 4, c.Size())
	assert.Equal(t, 4, len(c.resultOutputEqClasses))

	assert.NilError(t, g1Res.Release(ctx))
	assert.Equal(t, 3, c.Size())
	assert.Equal(t, 3, len(c.resultOutputEqClasses))

	assert.NilError(t, h2Res.Release(ctx))
	assert.Equal(t, 2, c.Size())
	assert.Equal(t, 2, len(c.resultOutputEqClasses))

	assert.NilError(t, f2Res.Release(ctx))
	assert.Equal(t, 1, c.Size())
	assert.Equal(t, 1, len(c.resultOutputEqClasses))

	assert.NilError(t, j1Res.Release(ctx))
	assert.Equal(t, 0, c.Size())
	assert.Equal(t, 0, len(c.ongoingCalls))
	assert.Assert(t, c.egraphDigestToClass == nil)
	assert.Assert(t, c.egraphParents == nil)
	assert.Assert(t, c.egraphRanks == nil)
	assert.Assert(t, c.inputEqClassToTerms == nil)
	assert.Assert(t, c.egraphTerms == nil)
	assert.Assert(t, c.egraphTermsByTermDigest == nil)
	assert.Assert(t, c.resultOutputEqClasses == nil)
	assert.Assert(t, c.termInputProvenance == nil)
	assert.Equal(t, eqClassID(0), c.nextEgraphClassID)
	assert.Equal(t, egraphTermID(0), c.nextEgraphTermID)
}

func TestHitTeachesReturnedRequestIDToCache(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	shared := call.ExtraDigest{
		Digest: digest.FromString("teach-hit-request-id-shared"),
		Label:  "eq-shared",
	}

	parentBKey := call.New().Append(Int(0).Type(), "teach-hit-parent-b")
	parentACall := cacheTestIntCall("teach-hit-parent-a")
	parentBCall := cacheTestIntCall("teach-hit-parent-b")
	parentAOutCall := cacheTestIntCall("teach-hit-parent-a", shared)
	parentBOutCall := cacheTestIntCall("teach-hit-parent-b", shared)

	parentARes, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: parentACall}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(parentAOutCall, 1), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !parentARes.HitCache())

	parentBRes, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: parentBCall}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(parentBOutCall, 2), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !parentBRes.HitCache())

	childBKey := parentBKey.Append(Int(0).Type(), "teach-hit-child")

	childARes, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: &ResultCall{
		Kind:     ResultCallKindField,
		Type:     NewResultCallType(Int(0).Type()),
		Field:    "teach-hit-child",
		Receiver: &ResultCallRef{ResultID: uint64(parentARes.cacheSharedResult().id)},
	}}, func(context.Context) (AnyResult, error) {
		return cacheTestPlainResult(NewInt(1001)), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !childARes.HitCache())

	childBInitCalls := 0
	childBRes, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: &ResultCall{
		Kind:     ResultCallKindField,
		Type:     NewResultCallType(Int(0).Type()),
		Field:    "teach-hit-child",
		Receiver: &ResultCallRef{ResultID: uint64(parentBRes.cacheSharedResult().id)},
	}}, func(context.Context) (AnyResult, error) {
		childBInitCalls++
		return cacheTestPlainResult(NewInt(1002)), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 0, childBInitCalls)
	assert.Assert(t, childBRes.HitCache())
	assert.Equal(t, cacheTestMustEncodeID(t, childARes), cacheTestMustEncodeID(t, childBRes))

	c.egraphMu.RLock()
	resolvedChildB, resolveErr := c.resolveSharedResultForInputIDLocked(ctx, childBKey)
	c.egraphMu.RUnlock()
	assert.NilError(t, resolveErr)
	assert.Assert(t, resolvedChildB != nil)
	assert.Equal(t, childBRes.cacheSharedResult().id, resolvedChildB.id)

	assert.NilError(t, childARes.Release(ctx))
	assert.NilError(t, childBRes.Release(ctx))
	assert.NilError(t, parentARes.Release(ctx))
	assert.NilError(t, parentBRes.Release(ctx))
	assert.Equal(t, 0, c.Size())
}

func TestExtraDigestLabelIsolation(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	sharedBytes := digest.FromString("label-isolation-shared-bytes")
	sharedA := call.ExtraDigest{Digest: sharedBytes, Label: "label-a"}
	sharedB := call.ExtraDigest{Digest: sharedBytes, Label: "label-b"}
	noiseA := call.ExtraDigest{Digest: digest.FromString("label-isolation-noise-a"), Label: "noise-a"}
	noiseB := call.ExtraDigest{Digest: digest.FromString("label-isolation-noise-b"), Label: "noise-b"}
	contentA := digest.FromString("label-isolation-content-a")
	contentB := digest.FromString("label-isolation-content-b")

	f1Key := call.New().Append(Int(0).Type(), "label-isolation-f-1")
	f2Key := call.New().Append(Int(0).Type(), "label-isolation-f-2")
	f1Call := cacheTestIntCall("label-isolation-f-1")
	f2Call := cacheTestIntCall("label-isolation-f-2")
	assert.Assert(t, f1Key.Digest() != f2Key.Digest())

	// Labels differ for the shared digest bytes, while content digests are also
	// present and intentionally different. This verifies label-only differences
	// are informational and do not block equivalence/hits.
	f1Out := f1Key.
		With(call.WithContentDigest(contentA)).
		With(call.WithExtraDigest(sharedA)).
		With(call.WithExtraDigest(noiseA))
	f2Out := f2Key.
		With(call.WithContentDigest(contentB)).
		With(call.WithExtraDigest(sharedB)).
		With(call.WithExtraDigest(noiseB))
	assert.Assert(t, contentA != contentB)
	assert.Equal(t, contentA.String(), f1Out.ContentDigest().String())
	assert.Equal(t, contentB.String(), f2Out.ContentDigest().String())
	f1HasLabelA := false
	f1HasLabelB := false
	for _, extra := range f1Out.ExtraDigests() {
		if extra.Digest != sharedBytes {
			continue
		}
		if extra.Label == sharedA.Label {
			f1HasLabelA = true
		}
		if extra.Label == sharedB.Label {
			f1HasLabelB = true
		}
	}
	f2HasLabelA := false
	f2HasLabelB := false
	for _, extra := range f2Out.ExtraDigests() {
		if extra.Digest != sharedBytes {
			continue
		}
		if extra.Label == sharedA.Label {
			f2HasLabelA = true
		}
		if extra.Label == sharedB.Label {
			f2HasLabelB = true
		}
	}
	assert.Assert(t, f1HasLabelA)
	assert.Assert(t, f2HasLabelB)
	assert.Assert(t, !f1HasLabelB)
	assert.Assert(t, !f2HasLabelA)

	f1Res, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: f1Call}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(f1Call, 501).(Result[Int]).WithContentDigest(contentA).ResultWithCall(
			&ResultCall{
				Kind:         ResultCallKindField,
				Type:         NewResultCallType(Int(0).Type()),
				Field:        "label-isolation-f-1",
				ExtraDigests: []call.ExtraDigest{sharedA, noiseA},
			},
		), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !f1Res.HitCache())

	f2Res, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: f2Call}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(f2Call, 502).(Result[Int]).WithContentDigest(contentB).ResultWithCall(
			&ResultCall{
				Kind:         ResultCallKindField,
				Type:         NewResultCallType(Int(0).Type()),
				Field:        "label-isolation-f-2",
				ExtraDigests: []call.ExtraDigest{sharedB, noiseB},
			},
		), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !f2Res.HitCache())

	g1Key := f1Key.Append(Int(0).Type(), "label-isolation-g")
	g2Key := f2Key.Append(Int(0).Type(), "label-isolation-g")
	assert.Assert(t, g1Key.Digest() != g2Key.Digest())

	g1Res, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: &ResultCall{
		Kind:     ResultCallKindField,
		Type:     NewResultCallType(Int(0).Type()),
		Field:    "label-isolation-g",
		Receiver: &ResultCallRef{ResultID: uint64(f1Res.cacheSharedResult().id)},
	}}, func(context.Context) (AnyResult, error) {
		return cacheTestPlainResult(NewInt(601)), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !g1Res.HitCache())

	g2InitCalls := 0
	g2Res, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: &ResultCall{
		Kind:     ResultCallKindField,
		Type:     NewResultCallType(Int(0).Type()),
		Field:    "label-isolation-g",
		Receiver: &ResultCallRef{ResultID: uint64(f2Res.cacheSharedResult().id)},
	}}, func(context.Context) (AnyResult, error) {
		g2InitCalls++
		return cacheTestPlainResult(NewInt(602)), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 0, g2InitCalls)
	assert.Assert(t, g2Res.HitCache())
	assert.Equal(t, 601, cacheTestUnwrapInt(t, g2Res))
	assert.Equal(t, cacheTestMustEncodeID(t, g1Res), cacheTestMustEncodeID(t, g2Res))

	assert.NilError(t, f1Res.Release(ctx))
	assert.NilError(t, f2Res.Release(ctx))
	assert.NilError(t, g1Res.Release(ctx))
	assert.NilError(t, g2Res.Release(ctx))
	assert.Equal(t, 0, c.Size())
	assert.Equal(t, 0, len(c.egraphTerms))
	assert.Equal(t, 0, len(c.egraphDigestToClass))
}

func TestCacheHitReturnIDGetsContentDigestFromEqClassMetadata(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	contentDigest := digest.FromString("hit-return-id-content-digest")
	requestCall := cacheTestIntCall("hit-return-id-request")
	outputCall := cacheTestIntCall("hit-return-id-output")

	res1, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: requestCall}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(outputCall, 77).(Result[Int]).WithContentDigest(contentDigest), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, contentDigest.String(), cacheTestMustRecipeID(t, res1).ContentDigest().String())

	shared := res1.cacheSharedResult()
	assert.Assert(t, shared != nil)
	initCalls := 0
	res2, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: requestCall}, func(context.Context) (AnyResult, error) {
		initCalls++
		return cacheTestIntResult(requestCall, 88), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 0, initCalls)
	assert.Assert(t, res2.HitCache())
	assert.Equal(t, contentDigest.String(), cacheTestMustRecipeID(t, res2).ContentDigest().String())

	assert.NilError(t, res1.Release(ctx))
	assert.NilError(t, res2.Release(ctx))
	assert.Equal(t, 0, c.Size())
}

func TestCacheFreshReturnIDGetsContentDigestFromEqClassMetadata(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	contentDigest := digest.FromString("fresh-return-id-content-digest")
	sourceCall := cacheTestIntCall("fresh-return-id-source")
	sourceOutCall := cacheTestIntCall("fresh-return-id-output")

	sourceRes, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: sourceCall}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(sourceOutCall, 91).(Result[Int]).WithContentDigest(contentDigest), nil
	})
	assert.NilError(t, err)
	shared := sourceRes.cacheSharedResult()
	assert.Assert(t, shared != nil)

	requestCall := cacheTestIntCall("fresh-return-id-request")
	wrappedRes, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: requestCall}, func(context.Context) (AnyResult, error) {
		return Result[Typed]{
			shared: shared,
		}, nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !wrappedRes.HitCache())
	assert.Equal(t, contentDigest.String(), cacheTestMustRecipeID(t, wrappedRes).ContentDigest().String())

	assert.NilError(t, sourceRes.Release(ctx))
	assert.NilError(t, wrappedRes.Release(ctx))
	assert.Equal(t, 0, c.Size())
}

func TestCachePostCallAndSafeToPersistMetadataPreserved(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	keyCall := cacheTestIntCall("metadata")
	postCallCount := 0

	res1, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: keyCall}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(keyCall, 7).(Result[Int]).
			ResultWithPostCall(func(context.Context) error {
				postCallCount++
				return nil
			}).
			WithSafeToPersistCache(true), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, res1.IsSafeToPersistCache())
	assert.NilError(t, res1.PostCall(ctx))
	assert.Equal(t, 1, postCallCount)

	res2, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: keyCall}, func(context.Context) (AnyResult, error) {
		return nil, fmt.Errorf("unexpected initializer call")
	})
	assert.NilError(t, err)
	assert.Assert(t, res2.HitCache())
	assert.Assert(t, res2.IsSafeToPersistCache())
	assert.NilError(t, res2.PostCall(ctx))
	assert.Equal(t, 2, postCallCount)

	assert.NilError(t, res1.Release(ctx))
	assert.NilError(t, res2.Release(ctx))
	assert.Equal(t, 0, c.Size())
}

func TestDerefValuePropagatesSafeToPersistMetadataForNullables(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		value    Typed
		expected int
	}{
		{
			name: "dynamic-nullable",
			value: DynamicNullable{
				Elem:  NewInt(0),
				Value: NewInt(21),
				Valid: true,
			},
			expected: 21,
		},
		{
			name: "nullable-generic",
			value: Nullable[Int]{
				Value: NewInt(42),
				Valid: true,
			},
			expected: 42,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			call := &ResultCall{
				Kind:  ResultCallKindField,
				Type:  NewResultCallType(tc.value.Type()),
				Field: tc.name + "-safe",
			}
			outer := cacheTestDetachedResult(call, tc.value).WithSafeToPersistCache(true)

			deref, ok := outer.DerefValue()
			assert.Assert(t, ok)
			assert.Assert(t, deref.IsSafeToPersistCache())
			assert.Equal(t, tc.expected, cacheTestUnwrapInt(t, deref))
		})
	}
}

func TestCacheDoNotCacheNormalizesNestedHitMetadata(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	innerCall := cacheTestIntCall("inner")
	innerRes, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: innerCall}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(innerCall, 9), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !innerRes.HitCache())

	outerCall := cacheTestIntCall("outer")
	outerRes, err := c.GetOrInitCall(ctx, &CallRequest{
		ResultCall: outerCall,
		DoNotCache: true,
	}, func(ctx context.Context) (AnyResult, error) {
		nested, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: innerCall}, func(context.Context) (AnyResult, error) {
			return nil, fmt.Errorf("unexpected nested initializer call")
		})
		if err != nil {
			return nil, err
		}
		assert.Assert(t, nested.HitCache())
		defer nested.Release(ctx)
		return nested, nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !outerRes.HitCache())
	assert.Equal(t, 9, cacheTestUnwrapInt(t, outerRes))

	assert.NilError(t, outerRes.Release(ctx))
	assert.Equal(t, 1, c.Size())
	assert.NilError(t, innerRes.Release(ctx))
	assert.Equal(t, 0, c.Size())
}

func TestCacheSecondaryIndexesCleanedOnRelease(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	storageID := call.New().Append(Int(0).Type(), "storage-key")
	storageCall := cacheTestIntCall("storage-key")
	resultID := storageID.
		With(call.WithExtraDigest(call.ExtraDigest{Digest: digest.FromString("result-digest")})).
		With(call.WithContentDigest(digest.FromString("result-content")))

	res, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: storageCall}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(storageCall, 44).(Result[Int]).
			WithContentDigest(digest.FromString("result-content")).
			ResultWithCall(&ResultCall{
				Kind:         ResultCallKindField,
				Type:         NewResultCallType(Int(0).Type()),
				Field:        "storage-key",
				ExtraDigests: []call.ExtraDigest{{Digest: digest.FromString("result-digest")}},
			}), nil
	})
	assert.NilError(t, err)

	storageKey := storageID.Digest().String()
	resultOutputEq := resultID.ContentPreferredDigest().String()
	assert.Assert(t, storageKey != resultOutputEq)
	assert.Equal(t, 1, len(c.resultOutputEqClasses))
	assert.Assert(t, len(c.egraphTerms) > 0)
	assert.Assert(t, c.Size() > 0)

	assert.NilError(t, res.Release(ctx))
	assert.Equal(t, 0, len(c.ongoingCalls))
	assert.Equal(t, 0, len(c.resultOutputEqClasses))
	assert.Equal(t, 0, len(c.egraphTerms))
	assert.Equal(t, 0, len(c.resultOutputEqClasses))
}

func TestCacheReleaseRemovesDigestPostingsFromEntireOutputEqClass(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	requestCall := cacheTestIntCall("release-eq-class-request")
	outputCall := cacheTestIntCall("release-eq-class-output")
	keeperCall := cacheTestIntCall("release-eq-class-keeper")
	foreignDigest := digest.FromString("release-eq-class-foreign")

	res, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: requestCall}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(outputCall, 44), nil
	})
	assert.NilError(t, err)
	keeperRes, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: keeperCall}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(keeperCall, 55), nil
	})
	assert.NilError(t, err)

	shared := res.cacheSharedResult()
	assert.Assert(t, shared != nil)

	c.egraphMu.Lock()
	outputEqClasses := c.outputEqClassesForResultLocked(shared.id)
	assert.Assert(t, len(outputEqClasses) > 0)

	var outputEqID eqClassID
	for eqID := range outputEqClasses {
		outputEqID = eqID
		break
	}
	assert.Assert(t, outputEqID != 0)

	foreignEqID := c.ensureEqClassForDigestLocked(ctx, foreignDigest.String())
	outputEqID = c.mergeEqClassesLocked(ctx, outputEqID, foreignEqID)
	assert.Assert(t, outputEqID != 0)
	assert.Assert(t, c.eqClassToDigests[outputEqID] != nil)
	_, ok := c.eqClassToDigests[outputEqID][foreignDigest.String()]
	assert.Assert(t, ok)

	foreignSet := c.egraphResultsByDigest[foreignDigest.String()]
	if foreignSet == nil {
		foreignSet = make(map[sharedResultID]struct{})
		c.egraphResultsByDigest[foreignDigest.String()] = foreignSet
	}
	foreignSet[shared.id] = struct{}{}
	c.egraphMu.Unlock()

	assert.NilError(t, res.Release(ctx))

	c.egraphMu.RLock()
	_, ok = c.egraphResultsByDigest[foreignDigest.String()]
	c.egraphMu.RUnlock()
	assert.Assert(t, !ok)
	assert.Equal(t, 1, c.Size())

	assert.NilError(t, keeperRes.Release(ctx))
	assert.Equal(t, 0, c.Size())
}

func TestCacheArrayResultRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	keyCall := cacheTestIntCall("array-result")
	res1, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: keyCall}, func(context.Context) (AnyResult, error) {
		return cacheTestDetachedResult(&ResultCall{
			Kind:  ResultCallKindField,
			Type:  NewResultCallType(NewIntArray[int]().Type()),
			Field: "array-result",
		}, NewIntArray(1, 2, 3)), nil
	})
	assert.NilError(t, err)
	enum1, ok := res1.Unwrap().(Enumerable)
	assert.Assert(t, ok)
	assert.Equal(t, 3, enum1.Len())
	nth2, err := enum1.Nth(2)
	assert.NilError(t, err)
	v2, ok := nth2.(Int)
	assert.Assert(t, ok)
	assert.Equal(t, 2, int(v2))

	res2, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: keyCall}, func(context.Context) (AnyResult, error) {
		return nil, fmt.Errorf("unexpected initializer call")
	})
	assert.NilError(t, err)
	assert.Assert(t, res2.HitCache())
	enum2, ok := res2.Unwrap().(Enumerable)
	assert.Assert(t, ok)
	assert.Equal(t, 3, enum2.Len())

	assert.NilError(t, res1.Release(ctx))
	assert.NilError(t, res2.Release(ctx))
	assert.Equal(t, 0, c.Size())
}

func TestCacheObjectResultRoundTripAndRelease(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)
	srv := cacheTestServer(t, c)

	keyCall := cacheTestIntCall("object-result")
	var releaseCalls atomic.Int32

	res1, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: keyCall}, func(context.Context) (AnyResult, error) {
		return cacheTestObjectResult(t, srv, keyCall, 42, func(context.Context) error {
			releaseCalls.Add(1)
			return nil
		}), nil
	})
	assert.NilError(t, err)
	obj1, ok := res1.(ObjectResult[*cacheTestObject])
	assert.Assert(t, ok)
	assert.Equal(t, 42, obj1.Self().Value)
	assert.Assert(t, !obj1.HitCache())

	res2, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: keyCall}, func(context.Context) (AnyResult, error) {
		return nil, fmt.Errorf("unexpected initializer call")
	})
	assert.NilError(t, err)
	obj2, ok := res2.(ObjectResult[*cacheTestObject])
	assert.Assert(t, ok)
	assert.Equal(t, 42, obj2.Self().Value)
	assert.Assert(t, obj2.HitCache())

	assert.NilError(t, res1.Release(ctx))
	assert.Equal(t, int32(0), releaseCalls.Load())
	assert.NilError(t, res2.Release(ctx))
	assert.Equal(t, int32(1), releaseCalls.Load())
	assert.Equal(t, 0, c.Size())
}

func TestCacheTTLWithDBUsesStorageAndCallIndexes(t *testing.T) {
	t.Parallel()
	ctx := engine.ContextWithClientMetadata(t.Context(), &engine.ClientMetadata{
		ClientID:  "cache-test-client",
		SessionID: "cache-test-session",
	})
	dbPath := filepath.Join(t.TempDir(), "cache.db")

	cacheIface, err := NewCache(ctx, dbPath)
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	keyCall := cacheTestIntCall("ttl-key")
	initCalls := 0

	res1, err := c.GetOrInitCall(ctx, &CallRequest{
		ResultCall: keyCall,
		TTL:        60,
	}, func(context.Context) (AnyResult, error) {
		initCalls++
		return cacheTestIntResult(keyCall, 5).(Result[Int]).WithSafeToPersistCache(true), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 1, initCalls)

	res2, err := c.GetOrInitCall(ctx, &CallRequest{
		ResultCall: keyCall,
		TTL:        60,
	}, func(context.Context) (AnyResult, error) {
		initCalls++
		return cacheTestIntResult(keyCall, 6), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 1, initCalls)
	assert.Assert(t, res2.HitCache())
	assert.Equal(t, 1, len(c.resultOutputEqClasses))

	assert.NilError(t, res1.Release(ctx))
	assert.NilError(t, res2.Release(ctx))
	// Persist-safe only affects DB metadata persistence; in-memory cache entries are
	// released when refs drain.
	assert.Equal(t, 0, len(c.resultOutputEqClasses))
	assert.Equal(t, 0, c.Size())
}

func TestCachePersistableRetainedAcrossSessionClose(t *testing.T) {
	t.Parallel()
	baseCtx := t.Context()
	cacheIface, err := NewCache(baseCtx, "")
	assert.NilError(t, err)
	base := cacheIface.(*cache)

	key := cacheTestIntCall("persistable-across-session-close")
	ctxSessionA := engine.ContextWithClientMetadata(baseCtx, &engine.ClientMetadata{
		ClientID:  "persistable-client-a",
		SessionID: "persistable-session-a",
	})
	ctxSessionB := engine.ContextWithClientMetadata(baseCtx, &engine.ClientMetadata{
		ClientID:  "persistable-client-b",
		SessionID: "persistable-session-b",
	})

	sc1 := NewSessionCache(base)
	initCallsA := 0
	resA, err := sc1.GetOrInitCall(ctxSessionA, &CallRequest{
		ResultCall:    key,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		initCallsA++
		return cacheTestIntResult(key, 41), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 1, initCallsA)
	assert.Assert(t, !resA.HitCache())
	assert.Equal(t, 1, base.EntryStats().RetainedCalls)

	assert.NilError(t, sc1.ReleaseAndClose(ctxSessionA))
	assert.Equal(t, 1, base.EntryStats().RetainedCalls)
	assert.Equal(t, 1, base.Size())

	sc2 := NewSessionCache(base)
	initCallsB := 0
	resB, err := sc2.GetOrInitCall(ctxSessionB, &CallRequest{
		ResultCall:    key,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		initCallsB++
		return cacheTestIntResult(key, 99), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 0, initCallsB)
	assert.Assert(t, resB.HitCache())
	assert.Equal(t, 41, cacheTestUnwrapInt(t, resB))

	assert.NilError(t, sc2.ReleaseAndClose(ctxSessionB))
	assert.Equal(t, 1, base.EntryStats().RetainedCalls)
	assert.Equal(t, 1, base.Size())
}

func TestCacheNonPersistableDropsWhenRefsDrain(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	key := cacheTestIntCall("non-persistable-drops")
	res, err := c.GetOrInitCall(ctx, &CallRequest{
		ResultCall:    key,
		IsPersistable: false,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(key, 7), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 0, c.EntryStats().RetainedCalls)
	assert.Equal(t, 1, len(c.resultOutputEqClasses))

	assert.NilError(t, res.Release(ctx))
	assert.Equal(t, 0, c.EntryStats().RetainedCalls)
	assert.Equal(t, 0, len(c.resultOutputEqClasses))
	assert.Equal(t, 0, c.Size())
}

func TestCachePersistableHitUpgradesExistingResultToRetained(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	key := cacheTestIntCall("persistable-hit-upgrade")
	initCalls := 0

	resA, err := c.GetOrInitCall(ctx, &CallRequest{
		ResultCall:    key,
		IsPersistable: false,
	}, func(context.Context) (AnyResult, error) {
		initCalls++
		return cacheTestIntResult(key, 17), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 1, initCalls)
	assert.Assert(t, !resA.HitCache())
	assert.Equal(t, 0, c.EntryStats().RetainedCalls)

	resB, err := c.GetOrInitCall(ctx, &CallRequest{
		ResultCall:    key,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		initCalls++
		return cacheTestIntResult(key, 99), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 1, initCalls)
	assert.Assert(t, resB.HitCache())
	assert.Equal(t, 17, cacheTestUnwrapInt(t, resB))
	assert.Equal(t, 1, c.EntryStats().RetainedCalls)

	assert.NilError(t, resA.Release(ctx))
	assert.NilError(t, resB.Release(ctx))
	assert.Equal(t, 1, c.EntryStats().RetainedCalls)
	assert.Equal(t, 1, len(c.resultOutputEqClasses))

	initCallsAfter := 0
	resC, err := c.GetOrInitCall(ctx, &CallRequest{
		ResultCall: key,
	}, func(context.Context) (AnyResult, error) {
		initCallsAfter++
		return cacheTestIntResult(key, 123), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 0, initCallsAfter)
	assert.Assert(t, resC.HitCache())
	assert.Equal(t, 17, cacheTestUnwrapInt(t, resC))
	assert.NilError(t, resC.Release(ctx))
}

func TestCacheUsageEntriesIncludeRetainedPersistedResults(t *testing.T) {
	t.Parallel()
	baseCtx := t.Context()
	cacheIface, err := NewCache(baseCtx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	key := cacheTestIntCall("usage-retained-persisted")
	ctxSession := engine.ContextWithClientMetadata(baseCtx, &engine.ClientMetadata{
		ClientID:  "usage-retained-client",
		SessionID: "usage-retained-session",
	})

	sc := NewSessionCache(c)
	res, err := sc.GetOrInitCall(ctxSession, &CallRequest{
		ResultCall:    key,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(key, 123), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !res.HitCache())

	entries := c.UsageEntries()
	assert.Equal(t, 1, len(entries))
	assert.Equal(t, 1, c.EntryStats().RetainedCalls)
	assert.Assert(t, entries[0].ActivelyUsed)
	assert.Assert(t, entries[0].CreatedTimeUnixNano > 0)
	assert.Assert(t, entries[0].MostRecentUseTimeUnixNano > 0)
	assert.Assert(t, entries[0].SizeBytes >= 0)
	assert.Assert(t, entries[0].Description != "")
	assert.Assert(t, entries[0].RecordType != "")

	assert.NilError(t, sc.ReleaseAndClose(ctxSession))
	entries = c.UsageEntries()
	assert.Equal(t, 1, len(entries))
	assert.Assert(t, !entries[0].ActivelyUsed)
	assert.Assert(t, entries[0].MostRecentUseTimeUnixNano >= entries[0].CreatedTimeUnixNano)
}

func TestCacheUsageEntriesTracksMostRecentUseAndInUse(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	key := cacheTestIntCall("usage-most-recent-use")
	res1, err := c.GetOrInitCall(ctx, &CallRequest{
		ResultCall:    key,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(key, 17), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !res1.HitCache())

	entriesBefore := c.UsageEntries()
	assert.Equal(t, 1, len(entriesBefore))
	entryBefore := entriesBefore[0]
	assert.Assert(t, entryBefore.ActivelyUsed)

	time.Sleep(2 * time.Millisecond)

	res2, err := c.GetOrInitCall(ctx, &CallRequest{
		ResultCall:    key,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(key, 99), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, res2.HitCache())
	assert.Equal(t, 17, cacheTestUnwrapInt(t, res2))

	entriesAfterHit := c.UsageEntries()
	assert.Equal(t, 1, len(entriesAfterHit))
	entryAfterHit := entriesAfterHit[0]
	assert.Equal(t, entryBefore.ID, entryAfterHit.ID)
	assert.Assert(t, entryAfterHit.ActivelyUsed)
	assert.Assert(t, entryAfterHit.MostRecentUseTimeUnixNano >= entryBefore.MostRecentUseTimeUnixNano)
	assert.Assert(t, entryAfterHit.CreatedTimeUnixNano <= entryAfterHit.MostRecentUseTimeUnixNano)

	assert.NilError(t, res1.Release(ctx))
	assert.NilError(t, res2.Release(ctx))

	entriesAfterRelease := c.UsageEntries()
	assert.Equal(t, 1, len(entriesAfterRelease))
	entryAfterRelease := entriesAfterRelease[0]
	assert.Assert(t, !entryAfterRelease.ActivelyUsed)
	assert.Assert(t, entryAfterRelease.MostRecentUseTimeUnixNano >= entryAfterHit.MostRecentUseTimeUnixNano)
}

func TestCacheUsageEntriesDeterministicOrdering(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	keyA := cacheTestIntCall("usage-order-a")
	keyB := cacheTestIntCall("usage-order-b")

	resB, err := c.GetOrInitCall(ctx, &CallRequest{
		ResultCall:    keyB,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(keyB, 2), nil
	})
	assert.NilError(t, err)
	resA, err := c.GetOrInitCall(ctx, &CallRequest{
		ResultCall:    keyA,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(keyA, 1), nil
	})
	assert.NilError(t, err)

	entries1 := c.UsageEntries()
	entries2 := c.UsageEntries()
	assert.DeepEqual(t, entries1, entries2)
	assert.Equal(t, 2, len(entries1))
	assert.Assert(t, entries1[0].ID < entries1[1].ID)

	assert.NilError(t, resA.Release(ctx))
	assert.NilError(t, resB.Release(ctx))
}

func TestCacheUsageEntriesMeasureOnlyPruneCandidates(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	sizeCalls := &atomic.Int32{}
	key := cacheTestIntCall("usage-candidate-only")
	res, err := c.GetOrInitCall(ctx, &CallRequest{
		ResultCall:    key,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestSizedIntResult(key, 11, 777, "snapshot://candidate-only", sizeCalls), nil
	})
	assert.NilError(t, err)

	entriesWhileActive := c.UsageEntries()
	assert.Equal(t, 1, len(entriesWhileActive))
	assert.Equal(t, int32(0), sizeCalls.Load())
	assert.Equal(t, int64(0), entriesWhileActive[0].SizeBytes)

	assert.NilError(t, res.Release(ctx))
	entriesAfterRelease := c.UsageEntries()
	assert.Equal(t, 1, len(entriesAfterRelease))
	assert.Equal(t, int32(1), sizeCalls.Load())
	assert.Equal(t, int64(777), entriesAfterRelease[0].SizeBytes)
}

func TestCacheUsageEntriesDedupesByUsageIdentity(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	sizeCalls := &atomic.Int32{}
	keyA := cacheTestIntCall("usage-dedupe-a")
	keyB := cacheTestIntCall("usage-dedupe-b")
	const dedupeIdentity = "snapshot://shared-identity"

	resA, err := c.GetOrInitCall(ctx, &CallRequest{
		ResultCall:    keyA,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestSizedIntResult(keyA, 1, 512, dedupeIdentity, sizeCalls), nil
	})
	assert.NilError(t, err)
	resB, err := c.GetOrInitCall(ctx, &CallRequest{
		ResultCall:    keyB,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestSizedIntResult(keyB, 2, 512, dedupeIdentity, sizeCalls), nil
	})
	assert.NilError(t, err)

	assert.NilError(t, resA.Release(ctx))
	assert.NilError(t, resB.Release(ctx))

	entries := c.UsageEntries()
	assert.Equal(t, 2, len(entries))
	assert.Equal(t, int32(1), sizeCalls.Load())

	var totalBytes int64
	var nonZeroEntries int
	for _, ent := range entries {
		totalBytes += ent.SizeBytes
		if ent.SizeBytes > 0 {
			nonZeroEntries++
		}
	}
	assert.Equal(t, int64(512), totalBytes)
	assert.Equal(t, 1, nonZeroEntries)
}

func TestCacheUsageEntriesDedupesByUsageIdentityDeterministicOwner(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	sizeCalls := &atomic.Int32{}
	keyFirst := cacheTestIntCall("usage-dedupe-owner-first")
	keySecond := cacheTestIntCall("usage-dedupe-owner-second")
	const dedupeIdentity = "snapshot://shared-identity-owner"

	// Intentionally initialize first/second in this order so sharedResult IDs are
	// deterministic and owner tie-break can be asserted directly.
	firstRes, err := c.GetOrInitCall(ctx, &CallRequest{
		ResultCall:    keyFirst,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestSizedIntResult(keyFirst, 1, 333, dedupeIdentity, sizeCalls), nil
	})
	assert.NilError(t, err)
	secondRes, err := c.GetOrInitCall(ctx, &CallRequest{
		ResultCall:    keySecond,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestSizedIntResult(keySecond, 2, 333, dedupeIdentity, sizeCalls), nil
	})
	assert.NilError(t, err)

	assert.NilError(t, firstRes.Release(ctx))
	assert.NilError(t, secondRes.Release(ctx))

	entries1 := c.UsageEntries()
	entries2 := c.UsageEntries()
	assert.DeepEqual(t, entries1, entries2)
	assert.Equal(t, int32(1), sizeCalls.Load())
	assert.Equal(t, 2, len(entries1))

	// Entries are sorted by ID; owner is deterministic smallest sharedResultID.
	assert.Equal(t, "dagql.result.1", entries1[0].ID)
	assert.Equal(t, int64(333), entries1[0].SizeBytes)
	assert.Equal(t, "dagql.result.2", entries1[1].ID)
	assert.Equal(t, int64(0), entries1[1].SizeBytes)
}

func TestCacheUsageEntriesMutableUsageRemeasuresAfterSizeChange(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	sizeCalls := &atomic.Int32{}
	sizeSource := &atomic.Int64{}
	sizeSource.Store(100)

	key := cacheTestIntCall("usage-mutable-refresh")
	res, err := c.GetOrInitCall(ctx, &CallRequest{
		ResultCall:    key,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestMutableSizedIntResult(
			key,
			1,
			sizeSource,
			"snapshot://mutable-refresh",
			sizeCalls,
		), nil
	})
	assert.NilError(t, err)
	assert.NilError(t, res.Release(ctx))

	entries1 := c.UsageEntries()
	assert.Equal(t, 1, len(entries1))
	assert.Equal(t, int64(100), entries1[0].SizeBytes)
	assert.Equal(t, int32(1), sizeCalls.Load())

	sizeSource.Store(200)
	entries2 := c.UsageEntries()
	assert.Equal(t, 1, len(entries2))
	assert.Equal(t, int64(200), entries2[0].SizeBytes)
	assert.Equal(t, int32(2), sizeCalls.Load())
}

func TestCachePruneKeepDuration(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	oldKey := cacheTestIntCall("prune-keep-duration-old")
	oldRes, err := c.GetOrInitCall(ctx, &CallRequest{
		ResultCall:    oldKey,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestSizedIntResult(oldKey, 1, 50, "snapshot://prune-keep-duration-old", nil), nil
	})
	assert.NilError(t, err)

	newKey := cacheTestIntCall("prune-keep-duration-new")
	newRes, err := c.GetOrInitCall(ctx, &CallRequest{
		ResultCall:    newKey,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestSizedIntResult(newKey, 2, 50, "snapshot://prune-keep-duration-new", nil), nil
	})
	assert.NilError(t, err)

	assert.NilError(t, oldRes.Release(ctx))
	assert.NilError(t, newRes.Release(ctx))
	_ = c.UsageEntries()

	oldEntryID := cacheTestSharedResultEntryID(oldRes)
	newEntryID := cacheTestSharedResultEntryID(newRes)
	requireOld := oldRes.cacheSharedResult()
	requireNew := newRes.cacheSharedResult()
	assert.Assert(t, requireOld != nil)
	assert.Assert(t, requireNew != nil)

	now := time.Now()
	c.egraphMu.Lock()
	requireOld.lastUsedAtUnixNano = now.Add(-2 * time.Hour).UnixNano()
	requireOld.createdAtUnixNano = requireOld.lastUsedAtUnixNano
	requireNew.lastUsedAtUnixNano = now.UnixNano()
	requireNew.createdAtUnixNano = requireNew.lastUsedAtUnixNano
	requireOld.depOfPersistedResult = false
	requireNew.depOfPersistedResult = false
	c.egraphMu.Unlock()

	report, err := c.Prune(ctx, []CachePrunePolicy{{
		All:          true,
		KeepDuration: time.Hour,
	}})
	assert.NilError(t, err)
	assert.Equal(t, 1, len(report.Entries))
	assert.Equal(t, oldEntryID, report.Entries[0].ID)

	c.egraphMu.RLock()
	_, oldStillPresent := c.resultsByID[requireOld.id]
	_, newStillPresent := c.resultsByID[requireNew.id]
	c.egraphMu.RUnlock()
	assert.Assert(t, !oldStillPresent)
	assert.Assert(t, newStillPresent)
	assert.Assert(t, newEntryID != "")
}

func TestCachePruneThresholdTargetSpace(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	keys := []*ResultCall{
		cacheTestIntCall("prune-threshold-1"),
		cacheTestIntCall("prune-threshold-2"),
		cacheTestIntCall("prune-threshold-3"),
	}
	results := make([]AnyResult, 0, len(keys))
	for i, key := range keys {
		keyCopy := key
		valueCopy := i + 1
		identityCopy := fmt.Sprintf("snapshot://prune-threshold-%d", i+1)
		res, getErr := c.GetOrInitCall(ctx, &CallRequest{
			ResultCall:    keyCopy,
			IsPersistable: true,
		}, func(context.Context) (AnyResult, error) {
			return cacheTestSizedIntResult(keyCopy, valueCopy, 100, identityCopy, nil), nil
		})
		assert.NilError(t, getErr)
		results = append(results, res)
	}
	for _, res := range results {
		assert.NilError(t, res.Release(ctx))
	}
	_ = c.UsageEntries()

	now := time.Now()
	c.egraphMu.Lock()
	for i, res := range results {
		shared := res.cacheSharedResult()
		assert.Assert(t, shared != nil)
		ts := now.Add(time.Duration(-3+i) * time.Hour).UnixNano()
		shared.lastUsedAtUnixNano = ts
		shared.createdAtUnixNano = ts
		shared.depOfPersistedResult = false
	}
	c.egraphMu.Unlock()

	report, err := c.Prune(ctx, []CachePrunePolicy{{
		All:          true,
		MaxUsedSpace: 250,
		TargetSpace:  100,
	}})
	assert.NilError(t, err)
	assert.Equal(t, 2, len(report.Entries))
	assert.Equal(t, int64(200), report.ReclaimedBytes)
	assert.Equal(t, cacheTestSharedResultEntryID(results[0]), report.Entries[0].ID)
	assert.Equal(t, cacheTestSharedResultEntryID(results[1]), report.Entries[1].ID)
}

func TestCachePruneThresholdNotTriggeredDoesNotForceAllPrune(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	key := cacheTestIntCall("prune-threshold-not-triggered")
	res, err := c.GetOrInitCall(ctx, &CallRequest{
		ResultCall:    key,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestSizedIntResult(key, 1, 100, "snapshot://prune-threshold-not-triggered", nil), nil
	})
	assert.NilError(t, err)
	assert.NilError(t, res.Release(ctx))
	_ = c.UsageEntries()
	c.egraphMu.Lock()
	res.cacheSharedResult().depOfPersistedResult = false
	c.egraphMu.Unlock()

	report, err := c.Prune(ctx, []CachePrunePolicy{{
		All:           true,
		MaxUsedSpace:  1024,
		ReservedSpace: 0,
		MinFreeSpace:  0,
		TargetSpace:   1024,
	}})
	assert.NilError(t, err)
	assert.Equal(t, 0, len(report.Entries))
	assert.Equal(t, int64(0), report.ReclaimedBytes)

	c.egraphMu.RLock()
	_, stillPresent := c.resultsByID[res.cacheSharedResult().id]
	c.egraphMu.RUnlock()
	assert.Assert(t, stillPresent)
}

func TestCachePruneReservedSpacePrecedenceOverMaxUsed(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	keyA := cacheTestIntCall("prune-reserved-a")
	keyB := cacheTestIntCall("prune-reserved-b")

	resA, err := c.GetOrInitCall(ctx, &CallRequest{
		ResultCall:    keyA,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestSizedIntResult(keyA, 1, 100, "snapshot://prune-reserved-a", nil), nil
	})
	assert.NilError(t, err)
	resB, err := c.GetOrInitCall(ctx, &CallRequest{
		ResultCall:    keyB,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestSizedIntResult(keyB, 2, 100, "snapshot://prune-reserved-b", nil), nil
	})
	assert.NilError(t, err)
	assert.NilError(t, resA.Release(ctx))
	assert.NilError(t, resB.Release(ctx))
	_ = c.UsageEntries()
	c.egraphMu.Lock()
	resA.cacheSharedResult().depOfPersistedResult = false
	resB.cacheSharedResult().depOfPersistedResult = false
	c.egraphMu.Unlock()

	report, err := c.Prune(ctx, []CachePrunePolicy{{
		All:           true,
		MaxUsedSpace:  50,
		ReservedSpace: 120,
		TargetSpace:   50,
	}})
	assert.NilError(t, err)
	assert.Equal(t, 1, len(report.Entries))
	assert.Equal(t, int64(100), report.ReclaimedBytes)
}

func TestCachePruneInUseEntriesAreNeverPruned(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	inUseKey := cacheTestIntCall("prune-in-use")
	inUseRes, err := c.GetOrInitCall(ctx, &CallRequest{
		ResultCall:    inUseKey,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestSizedIntResult(inUseKey, 1, 60, "snapshot://prune-in-use", nil), nil
	})
	assert.NilError(t, err)

	prunableKey := cacheTestIntCall("prune-prunable")
	prunableRes, err := c.GetOrInitCall(ctx, &CallRequest{
		ResultCall:    prunableKey,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestSizedIntResult(prunableKey, 2, 60, "snapshot://prune-prunable", nil), nil
	})
	assert.NilError(t, err)
	assert.NilError(t, prunableRes.Release(ctx))
	_ = c.UsageEntries()
	c.egraphMu.Lock()
	inUseRes.cacheSharedResult().depOfPersistedResult = false
	prunableRes.cacheSharedResult().depOfPersistedResult = false
	c.egraphMu.Unlock()

	report, err := c.Prune(ctx, []CachePrunePolicy{{All: true}})
	assert.NilError(t, err)
	assert.Equal(t, 1, len(report.Entries))
	assert.Equal(t, cacheTestSharedResultEntryID(prunableRes), report.Entries[0].ID)

	assert.NilError(t, inUseRes.Release(ctx))
}

func TestCachePruneDeterministicSelectionOrder(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	keys := []*ResultCall{
		cacheTestIntCall("prune-order-a"),
		cacheTestIntCall("prune-order-b"),
		cacheTestIntCall("prune-order-c"),
	}
	results := make([]AnyResult, 0, len(keys))
	for i, key := range keys {
		keyCopy := key
		valueCopy := i + 1
		identityCopy := fmt.Sprintf("snapshot://prune-order-%d", i+1)
		res, getErr := c.GetOrInitCall(ctx, &CallRequest{
			ResultCall:    keyCopy,
			IsPersistable: true,
		}, func(context.Context) (AnyResult, error) {
			return cacheTestSizedIntResult(keyCopy, valueCopy, 10, identityCopy, nil), nil
		})
		assert.NilError(t, getErr)
		results = append(results, res)
	}
	for _, res := range results {
		assert.NilError(t, res.Release(ctx))
	}
	_ = c.UsageEntries()

	now := time.Now().Add(-2 * time.Hour).UnixNano()
	c.egraphMu.Lock()
	for _, res := range results {
		shared := res.cacheSharedResult()
		assert.Assert(t, shared != nil)
		shared.lastUsedAtUnixNano = now
		shared.createdAtUnixNano = now
		shared.depOfPersistedResult = false
	}
	c.egraphMu.Unlock()

	report1, err := c.Prune(ctx, []CachePrunePolicy{{All: true}})
	assert.NilError(t, err)
	assert.Equal(t, 3, len(report1.Entries))

	gotOrder := []string{
		report1.Entries[0].ID,
		report1.Entries[1].ID,
		report1.Entries[2].ID,
	}
	wantOrder := []string{
		cacheTestSharedResultEntryID(results[0]),
		cacheTestSharedResultEntryID(results[1]),
		cacheTestSharedResultEntryID(results[2]),
	}
	assert.DeepEqual(t, gotOrder, wantOrder)
}

func TestCacheAttachOwnedResults(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	parentCall := cacheTestIntCall("parent-with-additional-output")
	childCall := cacheTestIntCall("child-additional-output")
	childPostCallCount := &atomic.Int32{}

	parentRes, err := c.GetOrInitCall(ctx, &CallRequest{
		ResultCall:    parentCall,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		child := cacheTestSizedIntResult(
			childCall,
			2,
			128,
			"snapshot://cache-additional-output",
			nil,
		).WithPostCall(func(context.Context) error {
			childPostCallCount.Add(1)
			return nil
		})
		attachedChild, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: childCall}, ValueFunc(child))
		if err != nil {
			return nil, err
		}
		if err := attachedChild.Release(ctx); err != nil {
			return nil, err
		}
		return cacheTestDetachedResult(parentCall, &cacheTestOwnedDepsInt{
			Int:          NewInt(1),
			ownedResults: []AnyResult{child},
		}), nil
	})
	assert.NilError(t, err)
	parentShared := parentRes.cacheSharedResult()
	assert.Assert(t, parentShared != nil)
	assert.Assert(t, parentShared.cache == c)

	childInitCalls := 0
	childRes, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: childCall}, func(context.Context) (AnyResult, error) {
		childInitCalls++
		return cacheTestIntResult(childCall, 99), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, int32(0), childPostCallCount.Load())
	assert.Equal(t, 0, childInitCalls)
	assert.Assert(t, childRes.HitCache())
	childVal, ok := UnwrapAs[cacheTestSizedInt](childRes)
	assert.Assert(t, ok)
	assert.Equal(t, int64(2), int64(childVal.Int))
	assert.NilError(t, childRes.PostCall(ctx))
	assert.Equal(t, int32(1), childPostCallCount.Load())

	childShared := childRes.cacheSharedResult()
	assert.Assert(t, childShared != nil)
	assert.Assert(t, childShared.cache == c)

	c.egraphMu.RLock()
	cachedParent := c.resultsByID[parentShared.id]
	cachedChild := c.resultsByID[childShared.id]
	parentDependsOnChild := false
	childRetained := false
	if cachedParent != nil {
		_, parentDependsOnChild = cachedParent.deps[childShared.id]
	}
	if cachedChild != nil {
		childRetained = cachedChild.depOfPersistedResult
	}
	c.egraphMu.RUnlock()

	assert.Assert(t, cachedParent != nil)
	assert.Assert(t, cachedChild != nil)
	assert.Assert(t, parentDependsOnChild)
	assert.Assert(t, childRetained)
}

func TestPersistedClosureGraphIncludesFrameRefs(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	mkRes := func(id sharedResultID, op string) *sharedResult {
		return &sharedResult{
			cache:    c,
			id:       id,
			self:     Int(id),
			hasValue: true,
			resultCall: &ResultCall{
				Kind:        ResultCallKindSynthetic,
				SyntheticOp: op,
				Type:        NewResultCallType(Int(0).Type()),
			},
		}
	}

	root := &sharedResult{
		cache:    c,
		id:       1,
		self:     Int(1),
		hasValue: true,
		deps: map[sharedResultID]struct{}{
			2: {},
		},
		resultCall: &ResultCall{
			Kind:  ResultCallKindField,
			Type:  NewResultCallType(Int(0).Type()),
			Field: "root",
			Receiver: &ResultCallRef{
				ResultID: 3,
			},
			Module: &ResultCallModule{
				ResultRef: &ResultCallRef{ResultID: 4},
				Name:      "mod",
			},
			Args: []*ResultCallArg{
				{
					Name: "arg",
					Value: &ResultCallLiteral{
						Kind:      ResultCallLiteralKindResultRef,
						ResultRef: &ResultCallRef{ResultID: 5},
					},
				},
				{
					Name: "list",
					Value: &ResultCallLiteral{
						Kind: ResultCallLiteralKindList,
						ListItems: []*ResultCallLiteral{
							{
								Kind:      ResultCallLiteralKindResultRef,
								ResultRef: &ResultCallRef{ResultID: 6},
							},
						},
					},
				},
				{
					Name: "object",
					Value: &ResultCallLiteral{
						Kind: ResultCallLiteralKindObject,
						ObjectFields: []*ResultCallArg{
							{
								Name: "nested",
								Value: &ResultCallLiteral{
									Kind:      ResultCallLiteralKindResultRef,
									ResultRef: &ResultCallRef{ResultID: 7},
								},
							},
						},
					},
				},
			},
			ImplicitInputs: []*ResultCallArg{
				{
					Name: "implicit",
					Value: &ResultCallLiteral{
						Kind:      ResultCallLiteralKindResultRef,
						ResultRef: &ResultCallRef{ResultID: 8},
					},
				},
			},
		},
	}

	results := map[sharedResultID]*sharedResult{
		root.id: root,
		2:       mkRes(2, "explicit"),
		3:       mkRes(3, "receiver"),
		4:       mkRes(4, "module"),
		5:       mkRes(5, "arg"),
		6:       mkRes(6, "list"),
		7:       mkRes(7, "object"),
		8:       mkRes(8, "implicit"),
	}

	c.egraphMu.Lock()
	c.resultsByID = results
	graph, err := c.persistedClosureGraphLocked(root.id)
	assert.NilError(t, err)
	changed, err := c.markResultAsDepOfPersistedLocked(ctx, root)
	assert.NilError(t, err)
	c.egraphMu.Unlock()

	assert.Assert(t, changed)
	for resultID, res := range results {
		_, ok := graph.resultIDs[resultID]
		assert.Assert(t, ok, "expected result %d in persisted closure graph", resultID)
		assert.Assert(t, res.depOfPersistedResult, "expected result %d to be marked as depOfPersistedResult", resultID)
	}
}

func TestPersistedClosureGraphDoesNotRetainTermProvenanceOnlyResults(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	root := &sharedResult{
		cache:    c,
		id:       1,
		self:     Int(1),
		hasValue: true,
		resultCall: &ResultCall{
			Kind:        ResultCallKindSynthetic,
			SyntheticOp: "root",
			Type:        NewResultCallType(Int(0).Type()),
		},
	}
	provenanceOnly := &sharedResult{
		cache:    c,
		id:       2,
		self:     Int(2),
		hasValue: true,
		resultCall: &ResultCall{
			Kind:        ResultCallKindSynthetic,
			SyntheticOp: "provenanceOnly",
			Type:        NewResultCallType(Int(0).Type()),
		},
	}

	c.egraphMu.Lock()
	c.initEgraphLocked()
	c.resultsByID = map[sharedResultID]*sharedResult{
		root.id:           root,
		provenanceOnly.id: provenanceOnly,
	}

	rootEq := c.ensureEqClassForDigestLocked(ctx, "persisted-closure-root")
	provenanceEq := c.ensureEqClassForDigestLocked(ctx, "persisted-closure-provenance-only")
	c.resultOutputEqClasses[root.id] = map[eqClassID]struct{}{rootEq: {}}
	c.resultOutputEqClasses[provenanceOnly.id] = map[eqClassID]struct{}{provenanceEq: {}}

	termID := egraphTermID(1)
	c.egraphTerms[termID] = newEgraphTerm(termID, digest.FromString("persisted-closure-root-term"), []eqClassID{provenanceEq}, rootEq)
	c.outputEqClassToTerms[rootEq] = map[egraphTermID]struct{}{termID: {}}
	c.termInputProvenance[termID] = []egraphInputProvenanceKind{egraphInputProvenanceKindResult}

	graph, err := c.persistedClosureGraphLocked(root.id)
	assert.NilError(t, err)
	changed, err := c.markResultAsDepOfPersistedLocked(ctx, root)
	assert.NilError(t, err)
	c.egraphMu.Unlock()

	assert.Assert(t, changed)
	_, rootIncluded := graph.resultIDs[root.id]
	assert.Assert(t, rootIncluded)
	_, provenanceIncluded := graph.resultIDs[provenanceOnly.id]
	assert.Assert(t, !provenanceIncluded)
	_, termIncluded := graph.termIDs[termID]
	assert.Assert(t, termIncluded)
	assert.Assert(t, root.depOfPersistedResult)
	assert.Assert(t, !provenanceOnly.depOfPersistedResult)
}

func TestPersistedClosureGraphRejectsPendingResultCallRef(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	root := &sharedResult{
		cache:    c,
		id:       1,
		self:     Int(1),
		hasValue: true,
		resultCall: &ResultCall{
			Kind:  ResultCallKindField,
			Type:  NewResultCallType(Int(0).Type()),
			Field: "root",
			Receiver: &ResultCallRef{
				Call: cacheTestIntCall("pending-parent"),
			},
		},
	}

	c.egraphMu.Lock()
	c.resultsByID = map[sharedResultID]*sharedResult{root.id: root}
	_, err = c.persistedClosureGraphLocked(root.id)
	c.egraphMu.Unlock()

	assert.ErrorContains(t, err, "pending result call ref leaked into persisted closure graph")
}

func TestCacheResultCallDerivedFromRequestID(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	childCall := cacheTestIntCall("frameChild")
	childRes, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: childCall}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(childCall, 7), nil
	})
	assert.NilError(t, err)
	childShared := childRes.cacheSharedResult()
	assert.Assert(t, childShared != nil)
	assert.Assert(t, childShared.id != 0)

	parentRes, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType(Int(0).Type()),
		Field: "frameParent",
		Args: []*ResultCallArg{
			{Name: "child", Value: &ResultCallLiteral{Kind: ResultCallLiteralKindResultRef, ResultRef: &ResultCallRef{ResultID: uint64(childShared.id)}}},
			{Name: "payload", Value: &ResultCallLiteral{Kind: ResultCallLiteralKindDigestedString, DigestedStringValue: "same", DigestedStringDigest: digest.FromString("frame-payload")}},
		},
	}}, func(context.Context) (AnyResult, error) {
		return cacheTestPlainResult(NewInt(8)), nil
	})
	assert.NilError(t, err)
	parentShared := parentRes.cacheSharedResult()
	assert.Assert(t, parentShared != nil)
	assert.Assert(t, parentShared.resultCall != nil)

	frame := parentShared.resultCall
	assert.Equal(t, ResultCallKindField, frame.Kind)
	assert.Equal(t, "frameParent", frame.Field)
	assert.Assert(t, frame.Type != nil)
	assert.Equal(t, "Int", frame.Type.NamedType)
	assert.Equal(t, 2, len(frame.Args))
	assert.Equal(t, "child", frame.Args[0].Name)
	assert.Assert(t, frame.Args[0].Value != nil)
	assert.Equal(t, ResultCallLiteralKindResultRef, frame.Args[0].Value.Kind)
	assert.Assert(t, frame.Args[0].Value.ResultRef != nil)
	assert.Equal(t, uint64(childShared.id), frame.Args[0].Value.ResultRef.ResultID)
	assert.Equal(t, "payload", frame.Args[1].Name)
	assert.Assert(t, frame.Args[1].Value != nil)
	assert.Equal(t, ResultCallLiteralKindDigestedString, frame.Args[1].Value.Kind)
	assert.Equal(t, "same", frame.Args[1].Value.DigestedStringValue)
	assert.Equal(t, digest.FromString("frame-payload"), frame.Args[1].Value.DigestedStringDigest)
}

func TestCacheResultCallFirstWriterWins(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	id := cacheTestIntCall("frame-first-writer")
	firstFrame := &ResultCall{
		Kind:        ResultCallKindSynthetic,
		SyntheticOp: "first",
		Type:        NewResultCallType(Int(0).Type()),
	}
	secondFrame := &ResultCall{
		Kind:        ResultCallKindSynthetic,
		SyntheticOp: "second",
		Type:        NewResultCallType(Int(0).Type()),
	}

	first, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: id}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(id, 1).(Result[Int]).ResultWithCall(firstFrame), nil
	})
	assert.NilError(t, err)
	firstShared := first.cacheSharedResult()
	assert.Assert(t, firstShared != nil)
	assert.Assert(t, firstShared.resultCall != nil)
	assert.Equal(t, "first", firstShared.resultCall.SyntheticOp)

	second, err := c.GetOrInitCall(ctx, &CallRequest{ResultCall: id}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(id, 2).(Result[Int]).ResultWithCall(secondFrame), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, second.HitCache())

	secondShared := second.cacheSharedResult()
	assert.Assert(t, secondShared != nil)
	assert.Assert(t, secondShared.resultCall != nil)
	assert.Equal(t, "first", secondShared.resultCall.SyntheticOp)
}

func TestCacheArbitraryRoundTripAndRelease(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	key := "arbitrary-round-trip"
	var releaseCalls atomic.Int32
	initCalls := 0

	res1, err := c.GetOrInitArbitrary(ctx, key, func(context.Context) (any, error) {
		initCalls++
		return cacheTestOpaqueValue{
			value: "hello",
			onRelease: func(context.Context) error {
				releaseCalls.Add(1)
				return nil
			},
		}, nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !res1.HitCache())
	v1, ok := res1.Value().(cacheTestOpaqueValue)
	assert.Assert(t, ok)
	assert.Equal(t, "hello", v1.value)

	res2, err := c.GetOrInitArbitrary(ctx, key, func(context.Context) (any, error) {
		initCalls++
		return cacheTestOpaqueValue{value: "ignored"}, nil
	})
	assert.NilError(t, err)
	assert.Assert(t, res2.HitCache())
	v2, ok := res2.Value().(cacheTestOpaqueValue)
	assert.Assert(t, ok)
	assert.Equal(t, "hello", v2.value)
	assert.Equal(t, 1, initCalls)
	assert.Equal(t, 1, c.Size())

	assert.NilError(t, res1.Release(ctx))
	assert.Equal(t, int32(0), releaseCalls.Load())
	assert.Equal(t, 1, c.Size())

	assert.NilError(t, res2.Release(ctx))
	assert.Equal(t, int32(1), releaseCalls.Load())
	assert.Equal(t, 0, c.Size())
}

func TestCacheArbitraryConcurrent(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	key := "arbitrary-concurrent"
	initialized := map[int]bool{}
	var initMu sync.Mutex
	const totalCallers = 100

	firstCallEntered := make(chan struct{})
	unblockFirstCall := make(chan struct{})

	wg := new(sync.WaitGroup)
	wg.Add(1)
	go func() {
		defer wg.Done()
		res, err := c.GetOrInitArbitrary(ctx, key, func(context.Context) (any, error) {
			initMu.Lock()
			initialized[0] = true
			initMu.Unlock()
			close(firstCallEntered)
			<-unblockFirstCall
			return "value", nil
		})
		assert.NilError(t, err)
		assert.Equal(t, "value", res.Value())
		assert.NilError(t, res.Release(ctx))
	}()

	select {
	case <-firstCallEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for first caller to enter init callback")
	}

	for i := 1; i < totalCallers; i++ {
		wg.Add(1)
		i := i
		go func() {
			defer wg.Done()
			res, err := c.GetOrInitArbitrary(ctx, key, func(context.Context) (any, error) {
				initMu.Lock()
				initialized[i] = true
				initMu.Unlock()
				return "value", nil
			})
			assert.NilError(t, err)
			assert.Equal(t, "value", res.Value())
			assert.NilError(t, res.Release(ctx))
		}()
	}

	waiterCountReached := false
	waiterPollDeadline := time.Now().Add(3 * time.Second)
	lastObservedWaiters := -1
	for time.Now().Before(waiterPollDeadline) {
		c.callsMu.Lock()
		oc := c.ongoingArbitraryCalls[key]
		if oc != nil {
			lastObservedWaiters = oc.waiters
		}
		c.callsMu.Unlock()

		if oc != nil && lastObservedWaiters == totalCallers {
			waiterCountReached = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	assert.Assert(t, waiterCountReached, "expected %d waiters, last observed %d", totalCallers, lastObservedWaiters)

	close(unblockFirstCall)

	ongoingCleared := false
	clearPollDeadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(clearPollDeadline) {
		c.callsMu.Lock()
		_, exists := c.ongoingArbitraryCalls[key]
		c.callsMu.Unlock()
		if !exists {
			ongoingCleared = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	assert.Assert(t, ongoingCleared, "ongoing arbitrary call was not cleared")

	wg.Wait()

	initMu.Lock()
	defer initMu.Unlock()
	assert.Assert(t, is.Len(initialized, 1))
	assert.Assert(t, initialized[0])
	assert.Equal(t, 0, c.Size())
}

func TestCacheArbitraryRecursiveCall(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	key := "arbitrary-recursive"
	_, err = c.GetOrInitArbitrary(ctx, key, func(ctx context.Context) (any, error) {
		_, err := c.GetOrInitArbitrary(ctx, key, func(context.Context) (any, error) {
			return "should-not-run", nil
		})
		return nil, err
	})
	assert.Assert(t, is.ErrorIs(err, ErrCacheRecursiveCall))
}
