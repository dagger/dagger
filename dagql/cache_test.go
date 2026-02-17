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
}
