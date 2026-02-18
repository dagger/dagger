package dagql

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
	"golang.org/x/sync/errgroup"
	"gotest.tools/v3/assert"
	is "gotest.tools/v3/assert/cmp"

	"github.com/dagger/dagger/dagql/call"
)

func cacheTestID(key string) *call.ID {
	return call.New().Append(Int(0).Type(), key)
}

func cacheTestIntResult(id *call.ID, v int) AnyResult {
	return newDetachedResult(id, NewInt(v))
}

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

func cacheTestIntResultWithOnRelease(id *call.ID, v int, onRelease func(context.Context) error) AnyResult {
	return newDetachedResult(id, cacheTestOnReleaseInt{
		Int:       NewInt(v),
		onRelease: onRelease,
	})
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

func cacheTestUnwrapInt(t *testing.T, res AnyResult) int {
	t.Helper()
	v, ok := UnwrapAs[Int](res)
	assert.Assert(t, ok, "expected Int result, got %T", res)
	return int(v)
}

func cacheTestIDHasExtraDigest(id *call.ID, dig digest.Digest, label string) bool {
	if id == nil {
		return false
	}
	for _, extra := range id.ExtraDigests() {
		if extra.Digest == dig && extra.Label == label {
			return true
		}
	}
	return false
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
	id *call.ID,
	value int,
	onRelease func(context.Context) error,
) ObjectResult[*cacheTestObject] {
	t.Helper()
	res, err := NewObjectResultForID(&cacheTestObject{
		Value:     value,
		onRelease: onRelease,
	}, srv, id)
	assert.NilError(t, err)
	return res
}

func TestCacheConcurrent(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	keyID := cacheTestID("42")
	initialized := map[int]bool{}
	var initMu sync.Mutex
	const totalCallers = 100
	const concurrencyKey = "42"

	firstCallEntered := make(chan struct{})
	unblockFirstCall := make(chan struct{})

	callConcKeys := callConcurrencyKeys{
		callKey:        keyID.Digest().String(),
		concurrencyKey: concurrencyKey,
	}

	wg := new(sync.WaitGroup)
	wg.Add(1)
	go func() {
		defer wg.Done()
		res, err := cacheIface.GetOrInitCall(ctx, CacheKey{
			ID:             keyID,
			ConcurrencyKey: concurrencyKey,
		}, func(_ context.Context) (AnyResult, error) {
			initMu.Lock()
			initialized[0] = true
			initMu.Unlock()
			close(firstCallEntered)
			<-unblockFirstCall
			return cacheTestIntResult(keyID, 0), nil
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
			res, err := cacheIface.GetOrInitCall(ctx, CacheKey{
				ID:             keyID,
				ConcurrencyKey: concurrencyKey,
			}, func(_ context.Context) (AnyResult, error) {
				initMu.Lock()
				initialized[i] = true
				initMu.Unlock()
				return cacheTestIntResult(keyID, i), nil
			})
			assert.NilError(t, err)
			assert.Equal(t, 0, cacheTestUnwrapInt(t, res))
		}()
	}

	waiterCountReached := false
	waiterPollDeadline := time.Now().Add(3 * time.Second)
	lastObservedWaiters := -1
	for time.Now().Before(waiterPollDeadline) {
		c.mu.Lock()
		oc := c.ongoingCalls[callConcKeys]
		if oc != nil {
			lastObservedWaiters = oc.waiters
		}
		c.mu.Unlock()

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
		c.mu.Lock()
		_, exists := c.ongoingCalls[callConcKeys]
		c.mu.Unlock()
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

	keyID := cacheTestID("42")

	myErr := errors.New("nope")
	_, err = cacheIface.GetOrInitCall(ctx, CacheKey{ID: keyID}, func(_ context.Context) (AnyResult, error) {
		return nil, myErr
	})
	assert.Assert(t, is.ErrorIs(err, myErr))

	otherErr := errors.New("nope 2")
	_, err = cacheIface.GetOrInitCall(ctx, CacheKey{ID: keyID}, func(_ context.Context) (AnyResult, error) {
		return nil, otherErr
	})
	assert.Assert(t, is.ErrorIs(err, otherErr))

	res, err := cacheIface.GetOrInitCall(ctx, CacheKey{ID: keyID}, func(_ context.Context) (AnyResult, error) {
		return cacheTestIntResult(keyID, 1), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 1, cacheTestUnwrapInt(t, res))

	res, err = cacheIface.GetOrInitCall(ctx, CacheKey{ID: keyID}, func(_ context.Context) (AnyResult, error) {
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

	key1 := cacheTestID("1")

	// recursive calls that are guaranteed to result in deadlock should error out
	_, err = cacheIface.GetOrInitCall(ctx, CacheKey{ID: key1}, func(ctx context.Context) (AnyResult, error) {
		_, err := cacheIface.GetOrInitCall(ctx, CacheKey{ID: key1}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(cacheTestID("2"), 2), nil
		})
		return nil, err
	})
	assert.Assert(t, is.ErrorIs(err, ErrCacheRecursiveCall))

	// verify same cache can be called recursively with different keys
	key10 := cacheTestID("10")
	key11 := cacheTestID("11")
	v, err := cacheIface.GetOrInitCall(ctx, CacheKey{ID: key10}, func(ctx context.Context) (AnyResult, error) {
		res, err := cacheIface.GetOrInitCall(ctx, CacheKey{ID: key11}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(key11, 12), nil
		})
		if err != nil {
			return nil, err
		}
		return cacheTestIntResult(key10, cacheTestUnwrapInt(t, res)), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 12, cacheTestUnwrapInt(t, v))

	// verify other cache instances can be called with same keys
	cacheIface2, err := NewCache(ctx, "")
	assert.NilError(t, err)
	key100 := cacheTestID("100")
	v, err = cacheIface.GetOrInitCall(ctx, CacheKey{ID: key100}, func(ctx context.Context) (AnyResult, error) {
		res, err := cacheIface2.GetOrInitCall(ctx, CacheKey{ID: key100}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(key100, 101), nil
		})
		if err != nil {
			return nil, err
		}
		return cacheTestIntResult(key100, cacheTestUnwrapInt(t, res)), nil
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

		keyID := cacheTestID("1")
		ctx1, cancel1 := context.WithCancel(ctx)
		ctx2, cancel2 := context.WithCancel(ctx)
		ctx3, cancel3 := context.WithCancel(ctx)

		errCh1 := make(chan error, 1)
		started1 := make(chan struct{})
		go func() {
			defer close(errCh1)
			_, err := cacheIface.GetOrInitCall(ctx1, CacheKey{
				ID:             keyID,
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
			_, err := cacheIface.GetOrInitCall(ctx2, CacheKey{
				ID:             keyID,
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
			_, err := cacheIface.GetOrInitCall(ctx3, CacheKey{
				ID:             keyID,
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

		keyID := cacheTestID("1")
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
			res, err := cacheIface.GetOrInitCall(ctx1, CacheKey{
				ID:             keyID,
				ConcurrencyKey: "1",
			}, func(context.Context) (AnyResult, error) {
				close(started1)
				<-stop1
				return cacheTestIntResult(keyID, 0), nil
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
			_, err := cacheIface.GetOrInitCall(ctx2, CacheKey{
				ID:             keyID,
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

		keyID := cacheTestID("cancel-last-waiter-release")
		ctx1, cancel1 := context.WithCancel(ctx)
		defer cancel1()

		started := make(chan struct{})
		allowReturn := make(chan struct{})
		released := make(chan struct{})

		errCh := make(chan error, 1)
		go func() {
			_, err := cacheIface.GetOrInitCall(ctx1, CacheKey{
				ID:             keyID,
				ConcurrencyKey: "1",
			}, func(context.Context) (AnyResult, error) {
				close(started)
				<-allowReturn
				return cacheTestIntResultWithOnRelease(keyID, 1, func(context.Context) error {
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

		key1 := cacheTestID("1")
		key2 := cacheTestID("2")

		res1A, err := c.GetOrInitCall(ctx, CacheKey{ID: key1}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(key1, 1), nil
		})
		assert.NilError(t, err)
		res1B, err := c.GetOrInitCall(ctx, CacheKey{ID: key1}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(key1, 1), nil
		})
		assert.NilError(t, err)

		res2, err := c.GetOrInitCall(ctx, CacheKey{ID: key2}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(key2, 2), nil
		})
		assert.NilError(t, err)

		assert.Equal(t, 0, len(c.ongoingCalls))
		assert.Equal(t, 2, len(c.egraphResultTerms))

		err = res2.Release(ctx)
		assert.NilError(t, err)
		assert.Equal(t, 0, len(c.ongoingCalls))
		assert.Equal(t, 1, len(c.egraphResultTerms))

		err = res1A.Release(ctx)
		assert.NilError(t, err)
		assert.Equal(t, 0, len(c.ongoingCalls))
		assert.Equal(t, 1, len(c.egraphResultTerms))

		err = res1B.Release(ctx)
		assert.NilError(t, err)
		assert.Equal(t, 0, len(c.ongoingCalls))
		assert.Equal(t, 0, len(c.egraphResultTerms))
	})

	t.Run("onRelease", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		cacheIface, err := NewCache(ctx, "")
		assert.NilError(t, err)
		c, ok := cacheIface.(*cache)
		assert.Assert(t, ok)

		key1 := cacheTestID("1")
		key2 := cacheTestID("2")

		releaseCalledCh := make(chan struct{})
		res1A, err := c.GetOrInitCall(ctx, CacheKey{
			ID:             key1,
			ConcurrencyKey: "1",
		}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResultWithOnRelease(key1, 1, func(context.Context) error {
				close(releaseCalledCh)
				return nil
			}), nil
		})
		assert.NilError(t, err)
		res1B, err := c.GetOrInitCall(ctx, CacheKey{ID: key1}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(key1, 1), nil
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
		res2, err := c.GetOrInitCall(ctx, CacheKey{
			ID:             key2,
			ConcurrencyKey: "1",
		}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResultWithOnRelease(key2, 2, func(context.Context) error {
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

	keyID := cacheTestID("1")
	var eg errgroup.Group

	valCh1 := make(chan int, 1)
	started1 := make(chan struct{})
	stop1 := make(chan struct{})
	eg.Go(func() error {
		_, err := cacheIface.GetOrInitCall(ctx, CacheKey{ID: keyID}, func(context.Context) (AnyResult, error) {
			defer close(valCh1)
			close(started1)
			valCh1 <- 1
			<-stop1
			return cacheTestIntResult(keyID, 1), nil
		})
		return err
	})

	valCh2 := make(chan int, 1)
	started2 := make(chan struct{})
	stop2 := make(chan struct{})
	eg.Go(func() error {
		_, err := cacheIface.GetOrInitCall(ctx, CacheKey{ID: keyID}, func(context.Context) (AnyResult, error) {
			defer close(valCh2)
			close(started2)
			valCh2 <- 2
			<-stop2
			return cacheTestIntResult(keyID, 2), nil
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

	_, err = cacheIface.GetOrInitCall(ctx, CacheKey{}, func(context.Context) (AnyResult, error) {
		return nil, fmt.Errorf("unexpected initializer call")
	})
	assert.ErrorContains(t, err, "cache key ID is nil")
}

func TestCacheDifferentConcurrencyKeysDoNotDedupe(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)

	keyID := cacheTestID("different-concurrency")
	release := make(chan struct{})
	startedA := make(chan struct{})
	startedB := make(chan struct{})
	errCh := make(chan error, 2)
	var initCalls atomic.Int32

	go func() {
		_, err := cacheIface.GetOrInitCall(ctx, CacheKey{
			ID:             keyID,
			ConcurrencyKey: "a",
		}, func(context.Context) (AnyResult, error) {
			initCalls.Add(1)
			close(startedA)
			<-release
			return cacheTestIntResult(keyID, 1), nil
		})
		errCh <- err
	}()
	go func() {
		_, err := cacheIface.GetOrInitCall(ctx, CacheKey{
			ID:             keyID,
			ConcurrencyKey: "b",
		}, func(context.Context) (AnyResult, error) {
			initCalls.Add(1)
			close(startedB)
			<-release
			return cacheTestIntResult(keyID, 2), nil
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

	keyID := cacheTestID("nil-result")
	initCalls := 0

	res, err := c.GetOrInitCall(ctx, CacheKey{ID: keyID}, func(context.Context) (AnyResult, error) {
		initCalls++
		return nil, nil
	})
	assert.NilError(t, err)
	assert.Assert(t, res != nil)
	assert.Assert(t, res.Unwrap() == nil)

	res, err = c.GetOrInitCall(ctx, CacheKey{ID: keyID}, func(context.Context) (AnyResult, error) {
		initCalls++
		return cacheTestIntResult(keyID, 42), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, res != nil)
	assert.Assert(t, res.Unwrap() == nil)
	assert.Equal(t, 1, initCalls)
	assert.Equal(t, 1, c.Size())
}

func TestCacheDoNotCacheSkipsStorage(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	keyID := cacheTestID("do-not-cache")

	for i := 1; i <= 2; i++ {
		res, err := c.GetOrInitCall(ctx, CacheKey{
			ID:         keyID,
			DoNotCache: true,
		}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(keyID, i), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !res.HitCache())
		assert.Equal(t, i, cacheTestUnwrapInt(t, res))
	}

	assert.Equal(t, 0, c.Size())
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
		f1Key := cacheTestID("content-f-1")
		f2Key := cacheTestID("content-f-2")
		f1Out := f1Key.With(call.WithExtraDigest(sharedEq)).With(call.WithExtraDigest(noiseA))
		f2Out := f2Key.With(call.WithExtraDigest(sharedEq)).With(call.WithExtraDigest(noiseB))
		assert.Assert(t, f1Key.Digest() != f2Key.Digest())
		assert.Assert(t, f1Out.Digest() != f2Out.Digest())
		assert.Assert(t, cacheTestIDHasExtraDigest(f1Out, sharedEq.Digest, sharedEq.Label))
		assert.Assert(t, cacheTestIDHasExtraDigest(f2Out, sharedEq.Digest, sharedEq.Label))
		assert.Assert(t, cacheTestIDHasExtraDigest(f1Out, noiseA.Digest, noiseA.Label))
		assert.Assert(t, cacheTestIDHasExtraDigest(f2Out, noiseB.Digest, noiseB.Label))

		fInitCalls := 0
		f1Res, err := c.GetOrInitCall(ctx, CacheKey{ID: f1Key}, func(context.Context) (AnyResult, error) {
			fInitCalls++
			return newDetachedResult(f1Out, NewInt(11)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !f1Res.HitCache())

		f2Res, err := c.GetOrInitCall(ctx, CacheKey{ID: f2Key}, func(context.Context) (AnyResult, error) {
			fInitCalls++
			return newDetachedResult(f2Out, NewInt(22)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !f2Res.HitCache())
		assert.Equal(t, 2, fInitCalls)
		assert.Assert(t, f1Res.ID().Digest() != f2Res.ID().Digest())

		g1Key := f1Key.Append(Int(0).Type(), "content-g")
		g2Key := f2Key.Append(Int(0).Type(), "content-g")
		assert.Assert(t, g1Key.Digest() != g2Key.Digest())

		g1Res, err := c.GetOrInitCall(ctx, CacheKey{ID: g1Key}, func(context.Context) (AnyResult, error) {
			return newDetachedResult(g1Key, NewInt(111)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !g1Res.HitCache())

		g2InitCalls := 0
		g2Res, err := c.GetOrInitCall(ctx, CacheKey{ID: g2Key}, func(context.Context) (AnyResult, error) {
			g2InitCalls++
			return newDetachedResult(g2Key, NewInt(222)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, g2InitCalls)
		assert.Assert(t, g2Res.HitCache())
		assert.Equal(t, 111, cacheTestUnwrapInt(t, g2Res))
		assert.Equal(t, g2Key.Digest().String(), g2Res.ID().Digest().String())

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
		f1Key := cacheTestID("deep-f-1")
		f2Key := cacheTestID("deep-f-2")
		f1Out := f1Key.With(call.WithExtraDigest(sharedEq)).With(call.WithExtraDigest(noiseA))
		f2Out := f2Key.With(call.WithExtraDigest(sharedEq)).With(call.WithExtraDigest(noiseB))
		assert.Assert(t, f1Key.Digest() != f2Key.Digest())
		assert.Assert(t, f1Out.Digest() != f2Out.Digest())
		assert.Assert(t, cacheTestIDHasExtraDigest(f1Out, sharedEq.Digest, sharedEq.Label))
		assert.Assert(t, cacheTestIDHasExtraDigest(f2Out, sharedEq.Digest, sharedEq.Label))
		assert.Assert(t, cacheTestIDHasExtraDigest(f1Out, noiseA.Digest, noiseA.Label))
		assert.Assert(t, cacheTestIDHasExtraDigest(f2Out, noiseB.Digest, noiseB.Label))

		fInitCalls := 0
		f1Res, err := c.GetOrInitCall(ctx, CacheKey{ID: f1Key}, func(context.Context) (AnyResult, error) {
			fInitCalls++
			return newDetachedResult(f1Out, NewInt(21)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !f1Res.HitCache())
		f2Res, err := c.GetOrInitCall(ctx, CacheKey{ID: f2Key}, func(context.Context) (AnyResult, error) {
			fInitCalls++
			return newDetachedResult(f2Out, NewInt(22)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !f2Res.HitCache())
		assert.Equal(t, 2, fInitCalls)

		g1Key := f1Key.Append(Int(0).Type(), "deep-g")
		g2Key := f2Key.Append(Int(0).Type(), "deep-g")
		assert.Assert(t, g1Key.Digest() != g2Key.Digest())
		h1Key := g1Key.Append(Int(0).Type(), "deep-h")
		h2Key := g2Key.Append(Int(0).Type(), "deep-h")
		assert.Assert(t, h1Key.Digest() != h2Key.Digest())
		i1Key := h1Key.Append(Int(0).Type(), "deep-i")
		i2Key := h2Key.Append(Int(0).Type(), "deep-i")
		assert.Assert(t, i1Key.Digest() != i2Key.Digest())

		g1Res, err := c.GetOrInitCall(ctx, CacheKey{ID: g1Key}, func(context.Context) (AnyResult, error) {
			return newDetachedResult(g1Key, NewInt(121)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !g1Res.HitCache())

		h1Res, err := c.GetOrInitCall(ctx, CacheKey{ID: h1Key}, func(context.Context) (AnyResult, error) {
			return newDetachedResult(h1Key, NewInt(221)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !h1Res.HitCache())

		i1Res, err := c.GetOrInitCall(ctx, CacheKey{ID: i1Key}, func(context.Context) (AnyResult, error) {
			return newDetachedResult(i1Key, NewInt(321)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !i1Res.HitCache())

		g2InitCalls := 0
		g2Res, err := c.GetOrInitCall(ctx, CacheKey{ID: g2Key}, func(context.Context) (AnyResult, error) {
			g2InitCalls++
			return newDetachedResult(g2Key, NewInt(122)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, g2InitCalls)
		assert.Assert(t, g2Res.HitCache())
		assert.Equal(t, 121, cacheTestUnwrapInt(t, g2Res))
		assert.Equal(t, g2Key.Digest().String(), g2Res.ID().Digest().String())

		h2InitCalls := 0
		h2Res, err := c.GetOrInitCall(ctx, CacheKey{ID: h2Key}, func(context.Context) (AnyResult, error) {
			h2InitCalls++
			return newDetachedResult(h2Key, NewInt(222)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, h2InitCalls)
		assert.Assert(t, h2Res.HitCache())
		assert.Equal(t, 221, cacheTestUnwrapInt(t, h2Res))
		assert.Equal(t, h2Key.Digest().String(), h2Res.ID().Digest().String())

		i2InitCalls := 0
		i2Res, err := c.GetOrInitCall(ctx, CacheKey{ID: i2Key}, func(context.Context) (AnyResult, error) {
			i2InitCalls++
			return newDetachedResult(i2Key, NewInt(322)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, i2InitCalls)
		assert.Assert(t, i2Res.HitCache())
		assert.Equal(t, 321, cacheTestUnwrapInt(t, i2Res))
		assert.Equal(t, i2Key.Digest().String(), i2Res.ID().Digest().String())

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

		f1Key := cacheTestID("late-f-1")
		f2Key := cacheTestID("late-f-2")
		assert.Assert(t, f1Key.Digest() != f2Key.Digest())
		f1Out := f1Key.With(call.WithExtraDigest(f1Only))
		f2Out := f2Key.With(call.WithExtraDigest(f2Only))
		assert.Assert(t, cacheTestIDHasExtraDigest(f1Out, f1Only.Digest, f1Only.Label))
		assert.Assert(t, cacheTestIDHasExtraDigest(f2Out, f2Only.Digest, f2Only.Label))

		g1Key := f1Key.Append(Int(0).Type(), "late-g")
		g2Key := f2Key.Append(Int(0).Type(), "late-g")
		assert.Assert(t, g1Key.Digest() != g2Key.Digest())
		g1Out := g1Key.With(call.WithExtraDigest(g1Only))
		g2Out := g2Key.With(call.WithExtraDigest(g2Only))
		assert.Assert(t, cacheTestIDHasExtraDigest(g1Out, g1Only.Digest, g1Only.Label))
		assert.Assert(t, cacheTestIDHasExtraDigest(g2Out, g2Only.Digest, g2Only.Label))

		h1Key := g1Key.Append(Int(0).Type(), "late-h")
		h2Key := g2Key.Append(Int(0).Type(), "late-h")
		assert.Assert(t, h1Key.Digest() != h2Key.Digest())

		h1Out := h1Key.
			With(call.WithExtraDigest(sharedA)).
			With(call.WithExtraDigest(sharedB)).
			With(call.WithExtraDigest(h1Only))
		h2Out := h2Key.
			With(call.WithExtraDigest(sharedA)).
			With(call.WithExtraDigest(sharedB)).
			With(call.WithExtraDigest(h2Only))
		assert.Assert(t, h1Out.Digest() != h2Out.Digest())
		assert.Assert(t, cacheTestIDHasExtraDigest(h1Out, sharedA.Digest, sharedA.Label))
		assert.Assert(t, cacheTestIDHasExtraDigest(h1Out, sharedB.Digest, sharedB.Label))
		assert.Assert(t, cacheTestIDHasExtraDigest(h2Out, sharedA.Digest, sharedA.Label))
		assert.Assert(t, cacheTestIDHasExtraDigest(h2Out, sharedB.Digest, sharedB.Label))
		assert.Assert(t, cacheTestIDHasExtraDigest(h1Out, h1Only.Digest, h1Only.Label))
		assert.Assert(t, cacheTestIDHasExtraDigest(h2Out, h2Only.Digest, h2Only.Label))

		i1Key := h1Key.Append(Int(0).Type(), "late-i")
		i2Key := h2Key.Append(Int(0).Type(), "late-i")
		assert.Assert(t, i1Key.Digest() != i2Key.Digest())

		f1Res, err := c.GetOrInitCall(ctx, CacheKey{ID: f1Key}, func(context.Context) (AnyResult, error) {
			return newDetachedResult(f1Out, NewInt(41)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !f1Res.HitCache())

		g1Res, err := c.GetOrInitCall(ctx, CacheKey{ID: g1Key}, func(context.Context) (AnyResult, error) {
			return newDetachedResult(g1Out, NewInt(141)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !g1Res.HitCache())

		h1Res, err := c.GetOrInitCall(ctx, CacheKey{ID: h1Key}, func(context.Context) (AnyResult, error) {
			return newDetachedResult(h1Out, NewInt(241)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !h1Res.HitCache())

		i1Res, err := c.GetOrInitCall(ctx, CacheKey{ID: i1Key}, func(context.Context) (AnyResult, error) {
			return newDetachedResult(i1Key, NewInt(341)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !i1Res.HitCache())

		f2InitCalls := 0
		f2Res, err := c.GetOrInitCall(ctx, CacheKey{ID: f2Key}, func(context.Context) (AnyResult, error) {
			f2InitCalls++
			return newDetachedResult(f2Out, NewInt(42)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 1, f2InitCalls)
		assert.Assert(t, !f2Res.HitCache())

		g2InitCalls := 0
		g2Res, err := c.GetOrInitCall(ctx, CacheKey{ID: g2Key}, func(context.Context) (AnyResult, error) {
			g2InitCalls++
			return newDetachedResult(g2Out, NewInt(142)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 1, g2InitCalls)
		assert.Assert(t, !g2Res.HitCache())

		h2InitCalls := 0
		h2Res, err := c.GetOrInitCall(ctx, CacheKey{ID: h2Key}, func(context.Context) (AnyResult, error) {
			h2InitCalls++
			return newDetachedResult(h2Out, NewInt(242)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 1, h2InitCalls)
		assert.Assert(t, !h2Res.HitCache())

		i2InitCalls := 0
		i2Res, err := c.GetOrInitCall(ctx, CacheKey{ID: i2Key}, func(context.Context) (AnyResult, error) {
			i2InitCalls++
			return newDetachedResult(i2Key, NewInt(342)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, i2InitCalls)
		assert.Assert(t, i2Res.HitCache())
		assert.Equal(t, 341, cacheTestUnwrapInt(t, i2Res))
		assert.Equal(t, i2Key.Digest().String(), i2Res.ID().Digest().String())

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

		x1Key := cacheTestID("multi-x-1")
		x2Key := cacheTestID("multi-x-2")
		y1Key := cacheTestID("multi-y-1")
		y2Key := cacheTestID("multi-y-2")
		assert.Assert(t, x1Key.Digest() != x2Key.Digest())
		assert.Assert(t, y1Key.Digest() != y2Key.Digest())

		x1Out := x1Key.With(call.WithExtraDigest(xShared)).With(call.WithExtraDigest(xNoise1))
		x2Out := x2Key.With(call.WithExtraDigest(xShared)).With(call.WithExtraDigest(xNoise2))
		y1Out := y1Key.With(call.WithExtraDigest(yShared)).With(call.WithExtraDigest(yNoise1))
		y2Out := y2Key.With(call.WithExtraDigest(yShared)).With(call.WithExtraDigest(yNoise2))
		assert.Assert(t, cacheTestIDHasExtraDigest(x1Out, xShared.Digest, xShared.Label))
		assert.Assert(t, cacheTestIDHasExtraDigest(x2Out, xShared.Digest, xShared.Label))
		assert.Assert(t, cacheTestIDHasExtraDigest(y1Out, yShared.Digest, yShared.Label))
		assert.Assert(t, cacheTestIDHasExtraDigest(y2Out, yShared.Digest, yShared.Label))

		x1Res, err := c.GetOrInitCall(ctx, CacheKey{ID: x1Key}, func(context.Context) (AnyResult, error) {
			return newDetachedResult(x1Out, NewInt(11)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !x1Res.HitCache())
		x2Res, err := c.GetOrInitCall(ctx, CacheKey{ID: x2Key}, func(context.Context) (AnyResult, error) {
			return newDetachedResult(x2Out, NewInt(12)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !x2Res.HitCache())

		y1Res, err := c.GetOrInitCall(ctx, CacheKey{ID: y1Key}, func(context.Context) (AnyResult, error) {
			return newDetachedResult(y1Out, NewInt(21)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !y1Res.HitCache())
		y2Res, err := c.GetOrInitCall(ctx, CacheKey{ID: y2Key}, func(context.Context) (AnyResult, error) {
			return newDetachedResult(y2Out, NewInt(22)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !y2Res.HitCache())

		zRoot := cacheTestID("multi-z-root")
		z1Key := zRoot.Append(Int(0).Type(), "multi-z",
			call.WithArgs(
				call.NewArgument("x", call.NewLiteralID(x1Key), false),
				call.NewArgument("y", call.NewLiteralID(y1Key), false),
			),
		)
		z2Key := zRoot.Append(Int(0).Type(), "multi-z",
			call.WithArgs(
				call.NewArgument("x", call.NewLiteralID(x2Key), false),
				call.NewArgument("y", call.NewLiteralID(y2Key), false),
			),
		)
		assert.Assert(t, z1Key.Digest() != z2Key.Digest())

		z1Res, err := c.GetOrInitCall(ctx, CacheKey{ID: z1Key}, func(context.Context) (AnyResult, error) {
			return newDetachedResult(z1Key, NewInt(501)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !z1Res.HitCache())

		z2InitCalls := 0
		z2Res, err := c.GetOrInitCall(ctx, CacheKey{ID: z2Key}, func(context.Context) (AnyResult, error) {
			z2InitCalls++
			return newDetachedResult(z2Key, NewInt(502)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, z2InitCalls)
		assert.Assert(t, z2Res.HitCache())
		assert.Equal(t, 501, cacheTestUnwrapInt(t, z2Res))
		assert.Equal(t, z2Key.Digest().String(), z2Res.ID().Digest().String())

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

		x1Key := cacheTestID("multi-partial-x-1")
		x2Key := cacheTestID("multi-partial-x-2")
		y1Key := cacheTestID("multi-partial-y-1")
		y2Key := cacheTestID("multi-partial-y-2")
		assert.Assert(t, x1Key.Digest() != x2Key.Digest())
		assert.Assert(t, y1Key.Digest() != y2Key.Digest())

		x1Out := x1Key.With(call.WithExtraDigest(xShared)).With(call.WithExtraDigest(xNoise1))
		x2Out := x2Key.With(call.WithExtraDigest(xShared)).With(call.WithExtraDigest(xNoise2))
		y1Out := y1Key.With(call.WithExtraDigest(yOnly1))
		y2Out := y2Key.With(call.WithExtraDigest(yOnly2))
		assert.Assert(t, cacheTestIDHasExtraDigest(x1Out, xShared.Digest, xShared.Label))
		assert.Assert(t, cacheTestIDHasExtraDigest(x2Out, xShared.Digest, xShared.Label))
		assert.Assert(t, !cacheTestIDHasExtraDigest(y1Out, yOnly2.Digest, yOnly2.Label))
		assert.Assert(t, !cacheTestIDHasExtraDigest(y2Out, yOnly1.Digest, yOnly1.Label))

		x1Res, err := c.GetOrInitCall(ctx, CacheKey{ID: x1Key}, func(context.Context) (AnyResult, error) {
			return newDetachedResult(x1Out, NewInt(31)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !x1Res.HitCache())
		x2Res, err := c.GetOrInitCall(ctx, CacheKey{ID: x2Key}, func(context.Context) (AnyResult, error) {
			return newDetachedResult(x2Out, NewInt(32)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !x2Res.HitCache())

		y1Res, err := c.GetOrInitCall(ctx, CacheKey{ID: y1Key}, func(context.Context) (AnyResult, error) {
			return newDetachedResult(y1Out, NewInt(41)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !y1Res.HitCache())
		y2Res, err := c.GetOrInitCall(ctx, CacheKey{ID: y2Key}, func(context.Context) (AnyResult, error) {
			return newDetachedResult(y2Out, NewInt(42)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !y2Res.HitCache())

		zRoot := cacheTestID("multi-partial-z-root")
		z1Key := zRoot.Append(Int(0).Type(), "multi-partial-z",
			call.WithArgs(
				call.NewArgument("x", call.NewLiteralID(x1Key), false),
				call.NewArgument("y", call.NewLiteralID(y1Key), false),
			),
		)
		z2Key := zRoot.Append(Int(0).Type(), "multi-partial-z",
			call.WithArgs(
				call.NewArgument("x", call.NewLiteralID(x2Key), false),
				call.NewArgument("y", call.NewLiteralID(y2Key), false),
			),
		)
		assert.Assert(t, z1Key.Digest() != z2Key.Digest())

		z1Res, err := c.GetOrInitCall(ctx, CacheKey{ID: z1Key}, func(context.Context) (AnyResult, error) {
			return newDetachedResult(z1Key, NewInt(701)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !z1Res.HitCache())

		z2InitCalls := 0
		z2Res, err := c.GetOrInitCall(ctx, CacheKey{ID: z2Key}, func(context.Context) (AnyResult, error) {
			z2InitCalls++
			return newDetachedResult(z2Key, NewInt(702)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 1, z2InitCalls)
		assert.Assert(t, !z2Res.HitCache())
		assert.Equal(t, 702, cacheTestUnwrapInt(t, z2Res))
		assert.Equal(t, z2Key.Digest().String(), z2Res.ID().Digest().String())

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

		f1Key := cacheTestID("bridge-f-1")
		f2Key := cacheTestID("bridge-f-2")
		f3Key := cacheTestID("bridge-f-3")
		assert.Assert(t, f1Key.Digest() != f2Key.Digest())
		assert.Assert(t, f2Key.Digest() != f3Key.Digest())
		assert.Assert(t, f1Key.Digest() != f3Key.Digest())

		f1Out := f1Key.With(call.WithExtraDigest(bridgeA)).With(call.WithExtraDigest(noise1))
		f2Out := f2Key.With(call.WithExtraDigest(bridgeA)).With(call.WithExtraDigest(bridgeB)).With(call.WithExtraDigest(noise2))
		f3Out := f3Key.With(call.WithExtraDigest(bridgeB)).With(call.WithExtraDigest(noise3))
		assert.Assert(t, cacheTestIDHasExtraDigest(f1Out, bridgeA.Digest, bridgeA.Label))
		assert.Assert(t, cacheTestIDHasExtraDigest(f2Out, bridgeA.Digest, bridgeA.Label))
		assert.Assert(t, cacheTestIDHasExtraDigest(f2Out, bridgeB.Digest, bridgeB.Label))
		assert.Assert(t, cacheTestIDHasExtraDigest(f3Out, bridgeB.Digest, bridgeB.Label))
		assert.Assert(t, !cacheTestIDHasExtraDigest(f1Out, bridgeB.Digest, bridgeB.Label))
		assert.Assert(t, !cacheTestIDHasExtraDigest(f3Out, bridgeA.Digest, bridgeA.Label))

		g1Key := f1Key.Append(Int(0).Type(), "bridge-g")
		g3Key := f3Key.Append(Int(0).Type(), "bridge-g")
		assert.Assert(t, g1Key.Digest() != g3Key.Digest())

		g1Res, err := c.GetOrInitCall(ctx, CacheKey{ID: g1Key}, func(context.Context) (AnyResult, error) {
			return newDetachedResult(g1Key, NewInt(901)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !g1Res.HitCache())

		f1Res, err := c.GetOrInitCall(ctx, CacheKey{ID: f1Key}, func(context.Context) (AnyResult, error) {
			return newDetachedResult(f1Out, NewInt(101)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !f1Res.HitCache())
		f2Res, err := c.GetOrInitCall(ctx, CacheKey{ID: f2Key}, func(context.Context) (AnyResult, error) {
			return newDetachedResult(f2Out, NewInt(102)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !f2Res.HitCache())
		f3Res, err := c.GetOrInitCall(ctx, CacheKey{ID: f3Key}, func(context.Context) (AnyResult, error) {
			return newDetachedResult(f3Out, NewInt(103)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !f3Res.HitCache())

		g3InitCalls := 0
		g3Res, err := c.GetOrInitCall(ctx, CacheKey{ID: g3Key}, func(context.Context) (AnyResult, error) {
			g3InitCalls++
			return newDetachedResult(g3Key, NewInt(903)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, g3InitCalls)
		assert.Assert(t, g3Res.HitCache())
		assert.Equal(t, 901, cacheTestUnwrapInt(t, g3Res))
		assert.Equal(t, g3Key.Digest().String(), g3Res.ID().Digest().String())

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

		f1Key := cacheTestID("nobridge-f-1")
		f2Key := cacheTestID("nobridge-f-2")
		f3Key := cacheTestID("nobridge-f-3")
		assert.Assert(t, f1Key.Digest() != f2Key.Digest())
		assert.Assert(t, f2Key.Digest() != f3Key.Digest())
		assert.Assert(t, f1Key.Digest() != f3Key.Digest())

		f1Out := f1Key.With(call.WithExtraDigest(bridgeA)).With(call.WithExtraDigest(noise1))
		f2Out := f2Key.With(call.WithExtraDigest(bridgeA)).With(call.WithExtraDigest(other)).With(call.WithExtraDigest(noise2))
		f3Out := f3Key.With(call.WithExtraDigest(bridgeB)).With(call.WithExtraDigest(noise3))
		assert.Assert(t, cacheTestIDHasExtraDigest(f1Out, bridgeA.Digest, bridgeA.Label))
		assert.Assert(t, cacheTestIDHasExtraDigest(f2Out, bridgeA.Digest, bridgeA.Label))
		assert.Assert(t, cacheTestIDHasExtraDigest(f3Out, bridgeB.Digest, bridgeB.Label))
		assert.Assert(t, !cacheTestIDHasExtraDigest(f2Out, bridgeB.Digest, bridgeB.Label))
		assert.Assert(t, !cacheTestIDHasExtraDigest(f3Out, bridgeA.Digest, bridgeA.Label))

		g1Key := f1Key.Append(Int(0).Type(), "nobridge-g")
		g2Key := f2Key.Append(Int(0).Type(), "nobridge-g")
		g3Key := f3Key.Append(Int(0).Type(), "nobridge-g")
		assert.Assert(t, g1Key.Digest() != g2Key.Digest())
		assert.Assert(t, g2Key.Digest() != g3Key.Digest())
		assert.Assert(t, g1Key.Digest() != g3Key.Digest())

		g1Res, err := c.GetOrInitCall(ctx, CacheKey{ID: g1Key}, func(context.Context) (AnyResult, error) {
			return newDetachedResult(g1Key, NewInt(911)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !g1Res.HitCache())

		f1Res, err := c.GetOrInitCall(ctx, CacheKey{ID: f1Key}, func(context.Context) (AnyResult, error) {
			return newDetachedResult(f1Out, NewInt(111)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !f1Res.HitCache())
		f2Res, err := c.GetOrInitCall(ctx, CacheKey{ID: f2Key}, func(context.Context) (AnyResult, error) {
			return newDetachedResult(f2Out, NewInt(112)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !f2Res.HitCache())
		f3Res, err := c.GetOrInitCall(ctx, CacheKey{ID: f3Key}, func(context.Context) (AnyResult, error) {
			return newDetachedResult(f3Out, NewInt(113)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !f3Res.HitCache())

		g2InitCalls := 0
		g2Res, err := c.GetOrInitCall(ctx, CacheKey{ID: g2Key}, func(context.Context) (AnyResult, error) {
			g2InitCalls++
			return newDetachedResult(g2Key, NewInt(912)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, g2InitCalls)
		assert.Assert(t, g2Res.HitCache())
		assert.Equal(t, 911, cacheTestUnwrapInt(t, g2Res))
		assert.Equal(t, g2Key.Digest().String(), g2Res.ID().Digest().String())

		g3InitCalls := 0
		g3Res, err := c.GetOrInitCall(ctx, CacheKey{ID: g3Key}, func(context.Context) (AnyResult, error) {
			g3InitCalls++
			return newDetachedResult(g3Key, NewInt(913)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 1, g3InitCalls)
		assert.Assert(t, !g3Res.HitCache())
		assert.Equal(t, 913, cacheTestUnwrapInt(t, g3Res))
		assert.Equal(t, g3Key.Digest().String(), g3Res.ID().Digest().String())

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
	//   equivalence fact introduced later:
	//     f1 ~ f2   (shared extra digest)
	//
	// Expected repair/propagation:
	//   left1 ~ left2
	//   right1 ~ right2
	//   => join1(left1,right1) ~ join2(left2,right2)
	//
	// So join2 should hit from join1 once repair has propagated through both
	// fanout branches into the fanin join inputs.
	t.Run("fanout_fanin_join_hit_after_repair", func(t *testing.T) {
		ctx := t.Context()
		cacheIface, err := NewCache(ctx, "")
		assert.NilError(t, err)
		c := cacheIface.(*cache)

		shared := call.ExtraDigest{Digest: digest.FromString("fanout-shared"), Label: "fanout-shared"}
		noise1 := call.ExtraDigest{Digest: digest.FromString("fanout-noise-1"), Label: "noise-1"}
		noise2 := call.ExtraDigest{Digest: digest.FromString("fanout-noise-2"), Label: "noise-2"}

		f1Key := cacheTestID("fanout-f-1")
		f2Key := cacheTestID("fanout-f-2")
		assert.Assert(t, f1Key.Digest() != f2Key.Digest())

		left1Key := f1Key.Append(Int(0).Type(), "fanout-left")
		left2Key := f2Key.Append(Int(0).Type(), "fanout-left")
		right1Key := f1Key.Append(Int(0).Type(), "fanout-right")
		right2Key := f2Key.Append(Int(0).Type(), "fanout-right")
		assert.Assert(t, left1Key.Digest() != left2Key.Digest())
		assert.Assert(t, right1Key.Digest() != right2Key.Digest())

		joinRoot := cacheTestID("fanout-join-root")
		join1Key := joinRoot.Append(Int(0).Type(), "fanout-join",
			call.WithArgs(
				call.NewArgument("left", call.NewLiteralID(left1Key), false),
				call.NewArgument("right", call.NewLiteralID(right1Key), false),
			),
		)
		join2Key := joinRoot.Append(Int(0).Type(), "fanout-join",
			call.WithArgs(
				call.NewArgument("left", call.NewLiteralID(left2Key), false),
				call.NewArgument("right", call.NewLiteralID(right2Key), false),
			),
		)
		assert.Assert(t, join1Key.Digest() != join2Key.Digest())

		left1Res, err := c.GetOrInitCall(ctx, CacheKey{ID: left1Key}, func(context.Context) (AnyResult, error) {
			return newDetachedResult(left1Key, NewInt(1001)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !left1Res.HitCache())
		right1Res, err := c.GetOrInitCall(ctx, CacheKey{ID: right1Key}, func(context.Context) (AnyResult, error) {
			return newDetachedResult(right1Key, NewInt(1002)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !right1Res.HitCache())

		join1Res, err := c.GetOrInitCall(ctx, CacheKey{ID: join1Key}, func(context.Context) (AnyResult, error) {
			return newDetachedResult(join1Key, NewInt(1101)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !join1Res.HitCache())

		f1Out := f1Key.With(call.WithExtraDigest(shared)).With(call.WithExtraDigest(noise1))
		f2Out := f2Key.With(call.WithExtraDigest(shared)).With(call.WithExtraDigest(noise2))
		assert.Assert(t, cacheTestIDHasExtraDigest(f1Out, shared.Digest, shared.Label))
		assert.Assert(t, cacheTestIDHasExtraDigest(f2Out, shared.Digest, shared.Label))
		f1Res, err := c.GetOrInitCall(ctx, CacheKey{ID: f1Key}, func(context.Context) (AnyResult, error) {
			return newDetachedResult(f1Out, NewInt(101)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !f1Res.HitCache())
		f2Res, err := c.GetOrInitCall(ctx, CacheKey{ID: f2Key}, func(context.Context) (AnyResult, error) {
			return newDetachedResult(f2Out, NewInt(102)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !f2Res.HitCache())

		left2InitCalls := 0
		left2Res, err := c.GetOrInitCall(ctx, CacheKey{ID: left2Key}, func(context.Context) (AnyResult, error) {
			left2InitCalls++
			return newDetachedResult(left2Key, NewInt(2001)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, left2InitCalls)
		assert.Assert(t, left2Res.HitCache())
		assert.Equal(t, 1001, cacheTestUnwrapInt(t, left2Res))

		right2InitCalls := 0
		right2Res, err := c.GetOrInitCall(ctx, CacheKey{ID: right2Key}, func(context.Context) (AnyResult, error) {
			right2InitCalls++
			return newDetachedResult(right2Key, NewInt(2002)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, right2InitCalls)
		assert.Assert(t, right2Res.HitCache())
		assert.Equal(t, 1002, cacheTestUnwrapInt(t, right2Res))

		join2InitCalls := 0
		join2Res, err := c.GetOrInitCall(ctx, CacheKey{ID: join2Key}, func(context.Context) (AnyResult, error) {
			join2InitCalls++
			return newDetachedResult(join2Key, NewInt(2101)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, join2InitCalls)
		assert.Assert(t, join2Res.HitCache())
		assert.Equal(t, 1101, cacheTestUnwrapInt(t, join2Res))
		assert.Equal(t, join2Key.Digest().String(), join2Res.ID().Digest().String())

		assert.NilError(t, left1Res.Release(ctx))
		assert.NilError(t, left2Res.Release(ctx))
		assert.NilError(t, right1Res.Release(ctx))
		assert.NilError(t, right2Res.Release(ctx))
		assert.NilError(t, join1Res.Release(ctx))
		assert.NilError(t, join2Res.Release(ctx))
		assert.NilError(t, f1Res.Release(ctx))
		assert.NilError(t, f2Res.Release(ctx))
		assert.Equal(t, 0, c.Size())
	})

	// Late-merge variant: both join branches are fully evaluated first (all misses),
	// then f-level equivalence is introduced, and only after that a downstream
	// op that takes join IDs as input should hit across distinct recipes.
	t.Run("fanout_fanin_late_merge_enables_downstream_join_input_hit", func(t *testing.T) {
		ctx := t.Context()
		cacheIface, err := NewCache(ctx, "")
		assert.NilError(t, err)
		c := cacheIface.(*cache)

		shared := call.ExtraDigest{Digest: digest.FromString("fanout-late-shared"), Label: "fanout-shared"}
		noise1 := call.ExtraDigest{Digest: digest.FromString("fanout-late-noise-1"), Label: "noise-1"}
		noise2 := call.ExtraDigest{Digest: digest.FromString("fanout-late-noise-2"), Label: "noise-2"}

		f1Key := cacheTestID("fanout-late-f-1")
		f2Key := cacheTestID("fanout-late-f-2")
		assert.Assert(t, f1Key.Digest() != f2Key.Digest())

		left1Key := f1Key.Append(Int(0).Type(), "fanout-late-left")
		left2Key := f2Key.Append(Int(0).Type(), "fanout-late-left")
		right1Key := f1Key.Append(Int(0).Type(), "fanout-late-right")
		right2Key := f2Key.Append(Int(0).Type(), "fanout-late-right")
		assert.Assert(t, left1Key.Digest() != left2Key.Digest())
		assert.Assert(t, right1Key.Digest() != right2Key.Digest())

		joinRoot := cacheTestID("fanout-late-join-root")
		join1Key := joinRoot.Append(Int(0).Type(), "fanout-late-join",
			call.WithArgs(
				call.NewArgument("left", call.NewLiteralID(left1Key), false),
				call.NewArgument("right", call.NewLiteralID(right1Key), false),
			),
		)
		join2Key := joinRoot.Append(Int(0).Type(), "fanout-late-join",
			call.WithArgs(
				call.NewArgument("left", call.NewLiteralID(left2Key), false),
				call.NewArgument("right", call.NewLiteralID(right2Key), false),
			),
		)
		assert.Assert(t, join1Key.Digest() != join2Key.Digest())

		left1Res, err := c.GetOrInitCall(ctx, CacheKey{ID: left1Key}, func(context.Context) (AnyResult, error) {
			return newDetachedResult(left1Key, NewInt(3001)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !left1Res.HitCache())
		right1Res, err := c.GetOrInitCall(ctx, CacheKey{ID: right1Key}, func(context.Context) (AnyResult, error) {
			return newDetachedResult(right1Key, NewInt(3002)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !right1Res.HitCache())
		join1Res, err := c.GetOrInitCall(ctx, CacheKey{ID: join1Key}, func(context.Context) (AnyResult, error) {
			return newDetachedResult(join1Key, NewInt(3101)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !join1Res.HitCache())

		left2Res, err := c.GetOrInitCall(ctx, CacheKey{ID: left2Key}, func(context.Context) (AnyResult, error) {
			return newDetachedResult(left2Key, NewInt(4001)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !left2Res.HitCache())
		right2Res, err := c.GetOrInitCall(ctx, CacheKey{ID: right2Key}, func(context.Context) (AnyResult, error) {
			return newDetachedResult(right2Key, NewInt(4002)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !right2Res.HitCache())
		join2InitCalls := 0
		join2Res, err := c.GetOrInitCall(ctx, CacheKey{ID: join2Key}, func(context.Context) (AnyResult, error) {
			join2InitCalls++
			return newDetachedResult(join2Key, NewInt(4101)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 1, join2InitCalls)
		assert.Assert(t, !join2Res.HitCache())
		assert.Equal(t, 4101, cacheTestUnwrapInt(t, join2Res))

		f1Out := f1Key.With(call.WithExtraDigest(shared)).With(call.WithExtraDigest(noise1))
		f2Out := f2Key.With(call.WithExtraDigest(shared)).With(call.WithExtraDigest(noise2))
		assert.Assert(t, cacheTestIDHasExtraDigest(f1Out, shared.Digest, shared.Label))
		assert.Assert(t, cacheTestIDHasExtraDigest(f2Out, shared.Digest, shared.Label))
		f1Res, err := c.GetOrInitCall(ctx, CacheKey{ID: f1Key}, func(context.Context) (AnyResult, error) {
			return newDetachedResult(f1Out, NewInt(301)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !f1Res.HitCache())
		f2Res, err := c.GetOrInitCall(ctx, CacheKey{ID: f2Key}, func(context.Context) (AnyResult, error) {
			return newDetachedResult(f2Out, NewInt(302)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !f2Res.HitCache())

		topRoot := cacheTestID("fanout-late-top-root")
		top1Key := topRoot.Append(Int(0).Type(), "fanout-late-top",
			call.WithArgs(call.NewArgument("join", call.NewLiteralID(join1Key), false)),
		)
		top2Key := topRoot.Append(Int(0).Type(), "fanout-late-top",
			call.WithArgs(call.NewArgument("join", call.NewLiteralID(join2Key), false)),
		)
		assert.Assert(t, top1Key.Digest() != top2Key.Digest())

		top1Res, err := c.GetOrInitCall(ctx, CacheKey{ID: top1Key}, func(context.Context) (AnyResult, error) {
			return newDetachedResult(top1Key, NewInt(5101)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !top1Res.HitCache())

		top2InitCalls := 0
		top2Res, err := c.GetOrInitCall(ctx, CacheKey{ID: top2Key}, func(context.Context) (AnyResult, error) {
			top2InitCalls++
			return newDetachedResult(top2Key, NewInt(5201)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, top2InitCalls)
		assert.Assert(t, top2Res.HitCache())
		assert.Equal(t, 5101, cacheTestUnwrapInt(t, top2Res))
		assert.Equal(t, top2Key.Digest().String(), top2Res.ID().Digest().String())

		assert.NilError(t, left1Res.Release(ctx))
		assert.NilError(t, left2Res.Release(ctx))
		assert.NilError(t, right1Res.Release(ctx))
		assert.NilError(t, right2Res.Release(ctx))
		assert.NilError(t, join1Res.Release(ctx))
		assert.NilError(t, join2Res.Release(ctx))
		assert.NilError(t, f1Res.Release(ctx))
		assert.NilError(t, f2Res.Release(ctx))
		assert.NilError(t, top1Res.Release(ctx))
		assert.NilError(t, top2Res.Release(ctx))
		assert.Equal(t, 0, c.Size())
	})
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
		sourceOut := sourceKey.With(call.WithExtraDigest(shared))
		sourceRes, err := c.GetOrInitCall(ctx, CacheKey{ID: sourceKey}, func(context.Context) (AnyResult, error) {
			return newDetachedResult(sourceOut, NewInt(71)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !sourceRes.HitCache())

		requestKey := sourceKey.
			WithArgument(call.NewArgument("variant", call.NewLiteralInt(1), false)).
			With(call.WithExtraDigest(shared))
		assert.Assert(t, sourceKey.Digest() != requestKey.Digest())

		requestInitCalls := 0
		requestRes, err := c.GetOrInitCall(ctx, CacheKey{ID: requestKey}, func(context.Context) (AnyResult, error) {
			requestInitCalls++
			return newDetachedResult(requestKey, NewInt(999)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, requestInitCalls)
		assert.Assert(t, requestRes.HitCache())
		assert.Equal(t, 71, cacheTestUnwrapInt(t, requestRes))
		assert.Equal(t, requestKey.Digest().String(), requestRes.ID().Digest().String())
		assert.Assert(t, cacheTestIDHasExtraDigest(requestRes.ID(), shared.Digest, shared.Label))

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

		sourceKey := cacheTestID("fallback-miss-source")
		sourceOut := sourceKey.With(call.WithExtraDigest(sourceExtra))
		sourceRes, err := c.GetOrInitCall(ctx, CacheKey{ID: sourceKey}, func(context.Context) (AnyResult, error) {
			return newDetachedResult(sourceOut, NewInt(81)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !sourceRes.HitCache())

		requestKey := cacheTestID("fallback-miss-request").With(call.WithExtraDigest(requestExtra))
		requestInitCalls := 0
		requestRes, err := c.GetOrInitCall(ctx, CacheKey{ID: requestKey}, func(context.Context) (AnyResult, error) {
			requestInitCalls++
			return newDetachedResult(requestKey, NewInt(82)), nil
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

	f1Key := cacheTestID("release-f-1")
	f2Key := cacheTestID("release-f-2")
	f1Out := f1Key.With(call.WithExtraDigest(sharedEq)).With(call.WithExtraDigest(noiseA))
	f2Out := f2Key.With(call.WithExtraDigest(sharedEq)).With(call.WithExtraDigest(noiseB))
	assert.Assert(t, f1Key.Digest() != f2Key.Digest())

	f1Res, err := c.GetOrInitCall(ctx, CacheKey{ID: f1Key}, func(context.Context) (AnyResult, error) {
		return newDetachedResult(f1Out, NewInt(101)), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !f1Res.HitCache())

	f2Res, err := c.GetOrInitCall(ctx, CacheKey{ID: f2Key}, func(context.Context) (AnyResult, error) {
		return newDetachedResult(f2Out, NewInt(102)), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !f2Res.HitCache())

	g1Key := f1Key.Append(Int(0).Type(), "release-g")
	g2Key := f2Key.Append(Int(0).Type(), "release-g")
	assert.Assert(t, g1Key.Digest() != g2Key.Digest())

	g1Res, err := c.GetOrInitCall(ctx, CacheKey{ID: g1Key}, func(context.Context) (AnyResult, error) {
		return newDetachedResult(g1Key, NewInt(201)), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !g1Res.HitCache())

	g2InitCalls := 0
	g2Res, err := c.GetOrInitCall(ctx, CacheKey{ID: g2Key}, func(context.Context) (AnyResult, error) {
		g2InitCalls++
		return newDetachedResult(g2Key, NewInt(202)), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 0, g2InitCalls)
	assert.Assert(t, g2Res.HitCache())
	assert.Equal(t, 201, cacheTestUnwrapInt(t, g2Res))
	assert.Equal(t, g2Key.Digest().String(), g2Res.ID().Digest().String())

	h1Key := g1Key.Append(Int(0).Type(), "release-h")
	h2Key := g2Key.Append(Int(0).Type(), "release-h")
	assert.Assert(t, h1Key.Digest() != h2Key.Digest())

	h1Res, err := c.GetOrInitCall(ctx, CacheKey{ID: h1Key}, func(context.Context) (AnyResult, error) {
		return newDetachedResult(h1Key, NewInt(301)), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !h1Res.HitCache())

	h2InitCalls := 0
	h2Res, err := c.GetOrInitCall(ctx, CacheKey{ID: h2Key}, func(context.Context) (AnyResult, error) {
		h2InitCalls++
		return newDetachedResult(h2Key, NewInt(302)), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 0, h2InitCalls)
	assert.Assert(t, h2Res.HitCache())
	assert.Equal(t, 301, cacheTestUnwrapInt(t, h2Res))
	assert.Equal(t, h2Key.Digest().String(), h2Res.ID().Digest().String())

	j1Key := g1Key.Append(Int(0).Type(), "release-j")
	j2Key := g2Key.Append(Int(0).Type(), "release-j")
	assert.Assert(t, j1Key.Digest() != j2Key.Digest())

	j1Res, err := c.GetOrInitCall(ctx, CacheKey{ID: j1Key}, func(context.Context) (AnyResult, error) {
		return newDetachedResult(j1Key, NewInt(401)), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !j1Res.HitCache())

	j2InitCalls := 0
	j2Res, err := c.GetOrInitCall(ctx, CacheKey{ID: j2Key}, func(context.Context) (AnyResult, error) {
		j2InitCalls++
		return newDetachedResult(j2Key, NewInt(402)), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 0, j2InitCalls)
	assert.Assert(t, j2Res.HitCache())
	assert.Equal(t, 401, cacheTestUnwrapInt(t, j2Res))
	assert.Equal(t, j2Key.Digest().String(), j2Res.ID().Digest().String())

	// Five unique shared results are alive: f1, f2, g, h, j.
	assert.Equal(t, 5, c.Size())
	assert.Equal(t, 5, len(c.egraphResultTerms))

	// Release in intentionally mixed order to verify ref-count/e-graph cleanup.
	assert.NilError(t, g2Res.Release(ctx))
	assert.Equal(t, 5, c.Size())
	assert.Equal(t, 5, len(c.egraphResultTerms))

	assert.NilError(t, h1Res.Release(ctx))
	assert.Equal(t, 5, c.Size())
	assert.Equal(t, 5, len(c.egraphResultTerms))

	assert.NilError(t, f1Res.Release(ctx))
	assert.Equal(t, 4, c.Size())
	assert.Equal(t, 4, len(c.egraphResultTerms))

	assert.NilError(t, j2Res.Release(ctx))
	assert.Equal(t, 4, c.Size())
	assert.Equal(t, 4, len(c.egraphResultTerms))

	assert.NilError(t, g1Res.Release(ctx))
	assert.Equal(t, 3, c.Size())
	assert.Equal(t, 3, len(c.egraphResultTerms))

	assert.NilError(t, h2Res.Release(ctx))
	assert.Equal(t, 2, c.Size())
	assert.Equal(t, 2, len(c.egraphResultTerms))

	assert.NilError(t, f2Res.Release(ctx))
	assert.Equal(t, 1, c.Size())
	assert.Equal(t, 1, len(c.egraphResultTerms))

	assert.NilError(t, j1Res.Release(ctx))
	assert.Equal(t, 0, c.Size())
	assert.Equal(t, 0, len(c.ongoingCalls))
	assert.Assert(t, c.egraphDigestToClass == nil)
	assert.Assert(t, c.egraphParents == nil)
	assert.Assert(t, c.egraphRanks == nil)
	assert.Assert(t, c.egraphClassTerms == nil)
	assert.Assert(t, c.egraphTerms == nil)
	assert.Assert(t, c.egraphTermsByDigest == nil)
	assert.Assert(t, c.egraphResultTerms == nil)
	assert.Equal(t, eqClassID(0), c.nextEgraphClassID)
	assert.Equal(t, egraphTermID(0), c.nextEgraphTermID)
}

func TestEquivalentCandidateSelectionIdentityInvariant(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	shared := call.ExtraDigest{
		Digest: digest.FromString("identity-invariant-shared"),
		Label:  "eq-shared",
	}
	noise1 := call.ExtraDigest{
		Digest: digest.FromString("identity-invariant-noise-1"),
		Label:  "noise-1",
	}
	noise2 := call.ExtraDigest{
		Digest: digest.FromString("identity-invariant-noise-2"),
		Label:  "noise-2",
	}
	noise3 := call.ExtraDigest{
		Digest: digest.FromString("identity-invariant-noise-3"),
		Label:  "noise-3",
	}

	f1Key := cacheTestID("identity-f-1")
	f2Key := cacheTestID("identity-f-2")
	f3Key := cacheTestID("identity-f-3")
	assert.Assert(t, f1Key.Digest() != f2Key.Digest())
	assert.Assert(t, f2Key.Digest() != f3Key.Digest())
	assert.Assert(t, f1Key.Digest() != f3Key.Digest())

	g1Key := f1Key.Append(Int(0).Type(), "identity-g")
	g2Key := f2Key.Append(Int(0).Type(), "identity-g")
	g3Key := f3Key.Append(Int(0).Type(), "identity-g")
	assert.Assert(t, g1Key.Digest() != g2Key.Digest())
	assert.Assert(t, g2Key.Digest() != g3Key.Digest())
	assert.Assert(t, g1Key.Digest() != g3Key.Digest())

	// Seed two distinct cached candidates before any f-level equivalence is known.
	g1Res, err := c.GetOrInitCall(ctx, CacheKey{ID: g1Key}, func(context.Context) (AnyResult, error) {
		return newDetachedResult(g1Key, NewInt(1001)), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !g1Res.HitCache())

	g2InitCalls := 0
	g2Res, err := c.GetOrInitCall(ctx, CacheKey{ID: g2Key}, func(context.Context) (AnyResult, error) {
		g2InitCalls++
		return newDetachedResult(g2Key, NewInt(1002)), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 1, g2InitCalls)
	assert.Assert(t, !g2Res.HitCache())
	assert.Assert(t, g1Res.cacheSharedResult() != g2Res.cacheSharedResult())

	// Learn equivalence after both candidates exist, creating an equivalent set.
	f1Out := f1Key.With(call.WithExtraDigest(shared)).With(call.WithExtraDigest(noise1))
	f2Out := f2Key.With(call.WithExtraDigest(shared)).With(call.WithExtraDigest(noise2))
	f3Out := f3Key.With(call.WithExtraDigest(shared)).With(call.WithExtraDigest(noise3))
	assert.Assert(t, cacheTestIDHasExtraDigest(f1Out, shared.Digest, shared.Label))
	assert.Assert(t, cacheTestIDHasExtraDigest(f2Out, shared.Digest, shared.Label))
	assert.Assert(t, cacheTestIDHasExtraDigest(f3Out, shared.Digest, shared.Label))

	f1Res, err := c.GetOrInitCall(ctx, CacheKey{ID: f1Key}, func(context.Context) (AnyResult, error) {
		return newDetachedResult(f1Out, NewInt(101)), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !f1Res.HitCache())
	f2Res, err := c.GetOrInitCall(ctx, CacheKey{ID: f2Key}, func(context.Context) (AnyResult, error) {
		return newDetachedResult(f2Out, NewInt(102)), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !f2Res.HitCache())
	f3Res, err := c.GetOrInitCall(ctx, CacheKey{ID: f3Key}, func(context.Context) (AnyResult, error) {
		return newDetachedResult(f3Out, NewInt(103)), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !f3Res.HitCache())

	// g3 should hit one of the equivalent cached candidates; selection may vary,
	// but the returned result must keep the request-facing ID digest.
	g3InitCalls := 0
	g3Res, err := c.GetOrInitCall(ctx, CacheKey{ID: g3Key}, func(context.Context) (AnyResult, error) {
		g3InitCalls++
		return newDetachedResult(g3Key, NewInt(1003)), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 0, g3InitCalls)
	assert.Assert(t, g3Res.HitCache())
	got := cacheTestUnwrapInt(t, g3Res)
	assert.Assert(t, got == 1001 || got == 1002, "expected hit value from an equivalent candidate, got %d", got)
	assert.Equal(t, g3Key.Digest().String(), g3Res.ID().Digest().String())

	assert.NilError(t, g1Res.Release(ctx))
	assert.NilError(t, g2Res.Release(ctx))
	assert.NilError(t, g3Res.Release(ctx))
	assert.NilError(t, f1Res.Release(ctx))
	assert.NilError(t, f2Res.Release(ctx))
	assert.NilError(t, f3Res.Release(ctx))
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

	f1Key := cacheTestID("label-isolation-f-1")
	f2Key := cacheTestID("label-isolation-f-2")
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
	assert.Assert(t, cacheTestIDHasExtraDigest(f1Out, sharedBytes, sharedA.Label))
	assert.Assert(t, cacheTestIDHasExtraDigest(f2Out, sharedBytes, sharedB.Label))
	assert.Assert(t, !cacheTestIDHasExtraDigest(f1Out, sharedBytes, sharedB.Label))
	assert.Assert(t, !cacheTestIDHasExtraDigest(f2Out, sharedBytes, sharedA.Label))

	f1Res, err := c.GetOrInitCall(ctx, CacheKey{ID: f1Key}, func(context.Context) (AnyResult, error) {
		return newDetachedResult(f1Out, NewInt(501)), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !f1Res.HitCache())

	f2Res, err := c.GetOrInitCall(ctx, CacheKey{ID: f2Key}, func(context.Context) (AnyResult, error) {
		return newDetachedResult(f2Out, NewInt(502)), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !f2Res.HitCache())

	g1Key := f1Key.Append(Int(0).Type(), "label-isolation-g")
	g2Key := f2Key.Append(Int(0).Type(), "label-isolation-g")
	assert.Assert(t, g1Key.Digest() != g2Key.Digest())

	g1Res, err := c.GetOrInitCall(ctx, CacheKey{ID: g1Key}, func(context.Context) (AnyResult, error) {
		return newDetachedResult(g1Key, NewInt(601)), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !g1Res.HitCache())

	g2InitCalls := 0
	g2Res, err := c.GetOrInitCall(ctx, CacheKey{ID: g2Key}, func(context.Context) (AnyResult, error) {
		g2InitCalls++
		return newDetachedResult(g2Key, NewInt(602)), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 0, g2InitCalls)
	assert.Assert(t, g2Res.HitCache())
	assert.Equal(t, 601, cacheTestUnwrapInt(t, g2Res))
	assert.Equal(t, g2Key.Digest().String(), g2Res.ID().Digest().String())

	assert.NilError(t, f1Res.Release(ctx))
	assert.NilError(t, f2Res.Release(ctx))
	assert.NilError(t, g1Res.Release(ctx))
	assert.NilError(t, g2Res.Release(ctx))
	assert.Equal(t, 0, c.Size())
	assert.Equal(t, 0, len(c.egraphTerms))
	assert.Equal(t, 0, len(c.egraphDigestToClass))
}

func TestCachePostCallAndSafeToPersistMetadataPreserved(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	keyID := cacheTestID("metadata")
	postCallCount := 0

	res1, err := c.GetOrInitCall(ctx, CacheKey{ID: keyID}, func(context.Context) (AnyResult, error) {
		return newDetachedResult(keyID, NewInt(7)).
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

	res2, err := c.GetOrInitCall(ctx, CacheKey{ID: keyID}, func(context.Context) (AnyResult, error) {
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
			id := cacheTestID(tc.name + "-safe")
			outer := newDetachedResult(id, tc.value).WithSafeToPersistCache(true)

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

	innerID := cacheTestID("inner")
	innerRes, err := c.GetOrInitCall(ctx, CacheKey{ID: innerID}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(innerID, 9), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !innerRes.HitCache())

	outerID := cacheTestID("outer")
	outerRes, err := c.GetOrInitCall(ctx, CacheKey{
		ID:         outerID,
		DoNotCache: true,
	}, func(ctx context.Context) (AnyResult, error) {
		nested, err := c.GetOrInitCall(ctx, CacheKey{ID: innerID}, func(context.Context) (AnyResult, error) {
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

func TestCacheNestedReturnTransfersInnerRefOwnership(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	innerID := cacheTestID("inner-transfer")
	innerRes, err := c.GetOrInitCall(ctx, CacheKey{ID: innerID}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(innerID, 13), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !innerRes.HitCache())

	outerID := cacheTestID("outer-transfer")
	outerRes, err := c.GetOrInitCall(ctx, CacheKey{ID: outerID}, func(ctx context.Context) (AnyResult, error) {
		nested, err := c.GetOrInitCall(ctx, CacheKey{ID: innerID}, func(context.Context) (AnyResult, error) {
			return nil, fmt.Errorf("unexpected nested initializer call")
		})
		if err != nil {
			return nil, err
		}
		assert.Assert(t, nested.HitCache())
		// Returning nested transfers ownership of its cache ref to outerRes.
		return nested, nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 13, cacheTestUnwrapInt(t, outerRes))

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

	storageID := cacheTestID("storage-key")
	resultID := storageID.
		With(call.WithExtraDigest(call.ExtraDigest{Digest: digest.FromString("result-digest")})).
		With(call.WithContentDigest(digest.FromString("result-content")))

	res, err := c.GetOrInitCall(ctx, CacheKey{ID: storageID}, func(context.Context) (AnyResult, error) {
		return newDetachedResult(resultID, NewInt(44)), nil
	})
	assert.NilError(t, err)

	storageKey := storageID.Digest().String()
	resultOutputEq := resultID.OutputEquivalentDigest().String()

	assert.Assert(t, storageKey != resultOutputEq)
	assert.Equal(t, 1, len(c.egraphResultTerms))
	assert.Assert(t, len(c.egraphTerms) > 0)
	assert.Assert(t, c.Size() > 0)

	assert.NilError(t, res.Release(ctx))
	assert.Equal(t, 0, len(c.ongoingCalls))
	assert.Equal(t, 0, len(c.egraphResultTerms))
	assert.Equal(t, 0, len(c.egraphTerms))
	assert.Equal(t, 0, len(c.egraphResultTerms))
}

func TestCacheArrayResultRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	keyID := cacheTestID("array-result")
	res1, err := c.GetOrInitCall(ctx, CacheKey{ID: keyID}, func(context.Context) (AnyResult, error) {
		return newDetachedResult(keyID, NewIntArray(1, 2, 3)), nil
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

	res2, err := c.GetOrInitCall(ctx, CacheKey{ID: keyID}, func(context.Context) (AnyResult, error) {
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

	keyID := cacheTestID("object-result")
	var releaseCalls atomic.Int32

	res1, err := c.GetOrInitCall(ctx, CacheKey{ID: keyID}, func(context.Context) (AnyResult, error) {
		return cacheTestObjectResult(t, srv, keyID, 42, func(context.Context) error {
			releaseCalls.Add(1)
			return nil
		}), nil
	})
	assert.NilError(t, err)
	obj1, ok := res1.(ObjectResult[*cacheTestObject])
	assert.Assert(t, ok)
	assert.Equal(t, 42, obj1.Self().Value)
	assert.Assert(t, !obj1.HitCache())

	res2, err := c.GetOrInitCall(ctx, CacheKey{ID: keyID}, func(context.Context) (AnyResult, error) {
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

	keyID := cacheTestID("ttl-key")
	initCalls := 0

	res1, err := c.GetOrInitCall(ctx, CacheKey{
		ID:  keyID,
		TTL: 60,
	}, func(context.Context) (AnyResult, error) {
		initCalls++
		return newDetachedResult(keyID, NewInt(5)).WithSafeToPersistCache(true), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 1, initCalls)

	res2, err := c.GetOrInitCall(ctx, CacheKey{
		ID:  keyID,
		TTL: 60,
	}, func(context.Context) (AnyResult, error) {
		initCalls++
		return cacheTestIntResult(keyID, 6), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 1, initCalls)
	assert.Assert(t, res2.HitCache())
	assert.Equal(t, 1, len(c.egraphResultTerms))

	assert.NilError(t, res1.Release(ctx))
	assert.NilError(t, res2.Release(ctx))
	// Persist-safe only affects DB metadata persistence; in-memory cache entries are
	// released when refs drain.
	assert.Equal(t, 0, len(c.egraphResultTerms))
	assert.Equal(t, 0, c.Size())
}

func TestCacheTTLNonPersistableEquivalentIDsCanCrossRecipeLookup(t *testing.T) {
	t.Parallel()
	baseCtx := t.Context()
	dbPath := filepath.Join(t.TempDir(), "cache.db")

	cacheIface, err := NewCache(baseCtx, dbPath)
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	sharedOutputEq := digest.FromString("ttl-nonpersist-session-scoped")
	keyA := cacheTestID("ttl-nonpersist-a").With(call.WithExtraDigest(call.ExtraDigest{Digest: sharedOutputEq}))
	keyB := cacheTestID("ttl-nonpersist-b").With(call.WithExtraDigest(call.ExtraDigest{Digest: sharedOutputEq}))

	ctxSessionA := engine.ContextWithClientMetadata(baseCtx, &engine.ClientMetadata{
		ClientID:  "cache-test-client-a",
		SessionID: "cache-test-session-a",
	})
	ctxSessionB := engine.ContextWithClientMetadata(baseCtx, &engine.ClientMetadata{
		ClientID:  "cache-test-client-b",
		SessionID: "cache-test-session-b",
	})

	resA, err := c.GetOrInitCall(ctxSessionA, CacheKey{
		ID:  keyA,
		TTL: 60,
	}, func(context.Context) (AnyResult, error) {
		return newDetachedResult(keyA, NewInt(11)).WithSafeToPersistCache(false), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !resA.HitCache())
	assert.Equal(t, 11, cacheTestUnwrapInt(t, resA))

	sameSessionInits := 0
	resSameSession, err := c.GetOrInitCall(ctxSessionA, CacheKey{
		ID:  keyB,
		TTL: 60,
	}, func(context.Context) (AnyResult, error) {
		sameSessionInits++
		return newDetachedResult(keyB, NewInt(22)).WithSafeToPersistCache(false), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 1, sameSessionInits)
	assert.Assert(t, !resSameSession.HitCache())
	assert.Equal(t, 22, cacheTestUnwrapInt(t, resSameSession))

	crossSessionInits := 0
	resCrossSession, err := c.GetOrInitCall(ctxSessionB, CacheKey{
		ID:  keyB,
		TTL: 60,
	}, func(context.Context) (AnyResult, error) {
		crossSessionInits++
		return newDetachedResult(keyB, NewInt(33)).WithSafeToPersistCache(false), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 0, crossSessionInits)
	assert.Assert(t, resCrossSession.HitCache())
	assert.Equal(t, 22, cacheTestUnwrapInt(t, resCrossSession))

	assert.NilError(t, resA.Release(ctxSessionA))
	assert.NilError(t, resSameSession.Release(ctxSessionA))
	assert.NilError(t, resCrossSession.Release(ctxSessionB))
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
		c.mu.Lock()
		oc := c.ongoingArbitraryCalls[key]
		if oc != nil {
			lastObservedWaiters = oc.waiters
		}
		c.mu.Unlock()

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
		c.mu.Lock()
		_, exists := c.ongoingArbitraryCalls[key]
		c.mu.Unlock()
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
