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

	keyID := cacheTestID("42")
	initialized := map[int]bool{}
	var initMu sync.Mutex

	wg := new(sync.WaitGroup)
	for i := range 100 {
		wg.Add(1)
		i := i
		go func() {
			defer wg.Done()
			res, err := cacheIface.GetOrInitCall(ctx, CacheKey{
				ID:             keyID,
				ConcurrencyKey: "42",
			}, func(_ context.Context) (AnyResult, error) {
				initMu.Lock()
				initialized[i] = true
				initMu.Unlock()
				return cacheTestIntResult(keyID, i), nil
			})
			assert.NilError(t, err)
			actual := cacheTestUnwrapInt(t, res)
			initMu.Lock()
			wasInitialized := initialized[actual]
			initMu.Unlock()
			assert.Assert(t, wasInitialized)
		}()
	}

	wg.Wait()

	initMu.Lock()
	defer initMu.Unlock()
	assert.Assert(t, is.Len(initialized, 1))
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

func TestCacheContentDigestLookupAcrossDistinctRecipeIDs(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	contentDigest := digest.FromString("shared-content-digest")
	keyA := cacheTestID("content-a").
		With(call.WithContentDigest(contentDigest))
	keyB := cacheTestID("content-b").
		With(call.WithContentDigest(contentDigest))

	resA, err := c.GetOrInitCall(ctx, CacheKey{ID: keyA}, func(context.Context) (AnyResult, error) {
		return newDetachedResult(keyA, NewInt(11)), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !resA.HitCache())
	assert.Equal(t, keyA.Digest().String(), resA.ID().Digest().String())

	initCalls := 0
	resB, err := c.GetOrInitCall(ctx, CacheKey{ID: keyB}, func(context.Context) (AnyResult, error) {
		initCalls++
		return newDetachedResult(keyB, NewInt(22)), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 0, initCalls)
	assert.Assert(t, resB.HitCache())
	assert.Equal(t, keyB.Digest().String(), resB.ID().Digest().String())
	assert.Equal(t, 11, cacheTestUnwrapInt(t, resB))

	resAHit, err := c.GetOrInitCall(ctx, CacheKey{ID: keyA}, func(context.Context) (AnyResult, error) {
		return nil, fmt.Errorf("unexpected initializer call")
	})
	assert.NilError(t, err)
	assert.Assert(t, resAHit.HitCache())
	assert.Equal(t, keyA.Digest().String(), resAHit.ID().Digest().String())
	assert.Equal(t, keyA.Digest().String(), resA.ID().Digest().String())

	assert.NilError(t, resA.Release(ctx))
	assert.NilError(t, resB.Release(ctx))
	assert.NilError(t, resAHit.Release(ctx))
	assert.Equal(t, 0, c.Size())
}
