package dagql

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/opencontainers/go-digest"
	"gotest.tools/v3/assert"
	is "gotest.tools/v3/assert/cmp"
)

func TestSessionCacheReleaseAndClose(t *testing.T) {
	t.Parallel()

	t.Run("basic", func(t *testing.T) {
		ctx := t.Context()
		cacheIface, err := NewCache(ctx, "")
		assert.NilError(t, err)
		base := cacheIface.(*cache)

		sc1 := NewSessionCache(base)
		sc2 := NewSessionCache(base)

		key1 := cacheTestIntCall("session-1")
		key2 := cacheTestIntCall("session-2")
		key3 := cacheTestIntCall("session-3")

		_, err = sc1.GetOrInitCall(ctx, noopTypeResolver{}, &CallRequest{ResultCall: key1}, ValueFunc(cacheTestIntResult(key1, 1)))
		assert.NilError(t, err)
		_, err = sc1.GetOrInitCall(ctx, noopTypeResolver{}, &CallRequest{ResultCall: key2}, ValueFunc(cacheTestIntResult(key2, 2)))
		assert.NilError(t, err)
		assert.Equal(t, 2, base.Size())

		_, err = sc2.GetOrInitCall(ctx, noopTypeResolver{}, &CallRequest{ResultCall: key2}, ValueFunc(cacheTestIntResult(key2, 2)))
		assert.NilError(t, err)
		_, err = sc2.GetOrInitCall(ctx, noopTypeResolver{}, &CallRequest{ResultCall: key3}, ValueFunc(cacheTestIntResult(key3, 3)))
		assert.NilError(t, err)
		assert.Equal(t, 3, base.Size())

		err = sc1.ReleaseAndClose(ctx)
		assert.NilError(t, err)
		assert.Equal(t, 2, base.Size())

		_, err = sc1.GetOrInitCall(ctx, noopTypeResolver{}, &CallRequest{ResultCall: cacheTestIntCall("closed-session")}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(cacheTestIntCall("closed-session"), 9), nil
		})
		assert.ErrorContains(t, err, "session cache is closed")
		assert.Equal(t, 2, base.Size())

		err = sc2.ReleaseAndClose(ctx)
		assert.NilError(t, err)
		assert.Equal(t, 0, base.Size())
	})

	t.Run("close while running", func(t *testing.T) {
		ctx := t.Context()
		cacheIface, err := NewCache(ctx, "")
		assert.NilError(t, err)
		base := cacheIface.(*cache)
		sc := NewSessionCache(base)

		key1 := cacheTestIntCall("close-running-1")
		key2 := cacheTestIntCall("close-running-2")
		_, err = sc.GetOrInitCall(ctx, noopTypeResolver{}, &CallRequest{ResultCall: key1}, ValueFunc(cacheTestIntResult(key1, 1)))
		assert.NilError(t, err)
		assert.Equal(t, 1, base.Size())

		startCh := make(chan struct{})
		stopCh := make(chan struct{})
		errCh := make(chan error, 1)
		go func() {
			_, err := sc.GetOrInitCall(ctx, noopTypeResolver{}, &CallRequest{ResultCall: key2}, func(context.Context) (AnyResult, error) {
				close(startCh)
				<-stopCh
				return cacheTestIntResult(key2, 2), nil
			})
			errCh <- err
		}()

		select {
		case <-startCh:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for running call")
		}

		closeErrCh := make(chan error, 1)
		go func() {
			closeErrCh <- sc.ReleaseAndClose(ctx)
		}()

		select {
		case err := <-closeErrCh:
			t.Fatalf("ReleaseAndClose returned before running call finished: %v", err)
		case <-time.After(100 * time.Millisecond):
		}

		close(stopCh)
		err = <-closeErrCh
		assert.NilError(t, err)

		runErr := <-errCh
		assert.ErrorContains(t, runErr, "session cache was closed during execution")
		assert.Equal(t, 0, base.Size())
	})
}

func TestSessionCachePersistableReturnedCacheBackedResultRetainsReceiverAcrossSessions(t *testing.T) {
	t.Parallel()

	t.Run("retains exact receiver across session close", func(t *testing.T) {
		ctx := t.Context()
		cacheIface, err := NewCache(ctx, "")
		assert.NilError(t, err)
		base := cacheIface.(*cache)

		ctxSessionA := engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
			ClientID:  "persistable-wrapper-client-a",
			SessionID: "persistable-wrapper-session-a",
		})
		ctxSessionB := engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
			ClientID:  "persistable-wrapper-client-b",
			SessionID: "persistable-wrapper-session-b",
		})

		sc1 := NewSessionCache(base)

		receiverCallA := &ResultCall{
			Kind:  ResultCallKindField,
			Type:  NewResultCallType(Int(0).Type()),
			Field: "persistable-wrapper-receiver",
		}
		receiverInitCallsA := 0
		receiverResA, err := sc1.GetOrInitCall(ctxSessionA, noopTypeResolver{}, &CallRequest{
			ResultCall:    receiverCallA,
			IsPersistable: false,
		}, func(context.Context) (AnyResult, error) {
			receiverInitCallsA++
			return cacheTestIntResult(receiverCallA, 41), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 1, receiverInitCallsA)

		leafCall := cacheTestIntCall("persistable-wrapper-leaf")
		leafInitCallsA := 0
		leafResA, err := sc1.GetOrInitCall(ctxSessionA, noopTypeResolver{}, &CallRequest{
			ResultCall:    leafCall,
			IsPersistable: false,
		}, func(context.Context) (AnyResult, error) {
			leafInitCallsA++
			return cacheTestIntResult(leafCall, 99), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 1, leafInitCallsA)

		receiverSharedA := receiverResA.cacheSharedResult()
		assert.Assert(t, receiverSharedA != nil)
		receiverID := receiverSharedA.id

		wrapperCallA := &ResultCall{
			Kind:  ResultCallKindField,
			Type:  NewResultCallType(Int(0).Type()),
			Field: "persistable-wrapper",
			Receiver: &ResultCallRef{
				ResultID: uint64(receiverID),
			},
		}
		wrapperInitCallsA := 0
		wrapperResA, err := sc1.GetOrInitCall(ctxSessionA, noopTypeResolver{}, &CallRequest{
			ResultCall:    wrapperCallA,
			IsPersistable: true,
		}, func(context.Context) (AnyResult, error) {
			wrapperInitCallsA++
			return leafResA, nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 1, wrapperInitCallsA)
		assert.Equal(t, leafResA.cacheSharedResult().id, wrapperResA.cacheSharedResult().id)

		assert.NilError(t, sc1.ReleaseAndClose(ctxSessionA))
		assert.Equal(t, 1, base.EntryStats().RetainedCalls)

		base.egraphMu.RLock()
		_, receiverStillPresent := base.resultsByID[receiverID]
		base.egraphMu.RUnlock()
		assert.Check(t, receiverStillPresent, "persistable returned shared result should retain its exact receiver after session close")

		sc2 := NewSessionCache(base)
		receiverCallB := &ResultCall{
			Kind:  ResultCallKindField,
			Type:  NewResultCallType(Int(0).Type()),
			Field: "persistable-wrapper-receiver",
		}
		receiverInitCallsB := 0
		receiverResB, err := sc2.GetOrInitCall(ctxSessionB, noopTypeResolver{}, &CallRequest{
			ResultCall:    receiverCallB,
			IsPersistable: false,
		}, func(context.Context) (AnyResult, error) {
			receiverInitCallsB++
			return cacheTestIntResult(receiverCallB, 123), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, receiverInitCallsB)
		assert.Assert(t, receiverResB.HitCache())
		assert.Equal(t, 41, cacheTestUnwrapInt(t, receiverResB))

		assert.NilError(t, sc2.ReleaseAndClose(ctxSessionB))
	})

	t.Run("stable receiver eq class still allows downstream hit after recompute", func(t *testing.T) {
		ctx := t.Context()
		cacheIface, err := NewCache(ctx, "")
		assert.NilError(t, err)
		base := cacheIface.(*cache)

		sharedEq := call.ExtraDigest{
			Digest: digest.FromString("session-returned-cache-backed-shared"),
			Label:  "eq-shared",
		}
		noiseA := call.ExtraDigest{
			Digest: digest.FromString("session-returned-cache-backed-noise-a"),
			Label:  "noise-a",
		}
		noiseB := call.ExtraDigest{
			Digest: digest.FromString("session-returned-cache-backed-noise-b"),
			Label:  "noise-b",
		}

		parentACall := cacheTestIntCall("session-returned-parent-a")
		parentBCall := cacheTestIntCall("session-returned-parent-b")
		parentAOutCall := cacheTestIntCall("session-returned-parent-a", sharedEq, noiseA)
		parentBOutCall := cacheTestIntCall("session-returned-parent-b", sharedEq, noiseB)
		leafCall := cacheTestIntCall("session-returned-leaf")

		childReqFor := func(receiver AnyResult) *CallRequest {
			return &CallRequest{
				ResultCall: &ResultCall{
					Kind:     ResultCallKindField,
					Type:     NewResultCallType(Int(0).Type()),
					Field:    "session-returned-child",
					Receiver: &ResultCallRef{ResultID: uint64(receiver.cacheSharedResult().id)},
				},
				IsPersistable: true,
			}
		}

		sc1 := NewSessionCache(base)

		parentARes, err := sc1.GetOrInitCall(ctx, noopTypeResolver{}, &CallRequest{ResultCall: parentACall}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(parentAOutCall, 11), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !parentARes.HitCache())

		leafRes, err := sc1.GetOrInitCall(ctx, noopTypeResolver{}, &CallRequest{ResultCall: leafCall}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(leafCall, 201), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !leafRes.HitCache())

		childARes, err := sc1.GetOrInitCall(ctx, noopTypeResolver{}, childReqFor(parentARes), func(context.Context) (AnyResult, error) {
			return leafRes, nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !childARes.HitCache())
		assert.Assert(t, childARes.cacheSharedResult() == leafRes.cacheSharedResult())

		parentAID := parentARes.cacheSharedResult().id
		childSharedID := childARes.cacheSharedResult().id

		assert.NilError(t, sc1.ReleaseAndClose(ctx))

		base.egraphMu.RLock()
		_, childStillPresent := base.resultsByID[childSharedID]
		base.egraphMu.RUnlock()
		assert.Check(t, childStillPresent, "persistable returned shared result should stay live after session close")
		// NOTE: this variant is intentionally *not* asserting that parentA stays
		// live. The point here is to test whether a recomputed equivalent receiver
		// can still drive a downstream hit via the same eq-class, even if the
		// original non-persistable receiver itself was dropped.
		_ = parentAID

		sc2 := NewSessionCache(base)

		parentBRes, err := sc2.GetOrInitCall(ctx, noopTypeResolver{}, &CallRequest{ResultCall: parentBCall}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(parentBOutCall, 12), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !parentBRes.HitCache())

		childBInitCalls := 0
		childBRes, err := sc2.GetOrInitCall(ctx, noopTypeResolver{}, childReqFor(parentBRes), func(context.Context) (AnyResult, error) {
			childBInitCalls++
			return cacheTestPlainResult(NewInt(999)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, childBInitCalls)
		assert.Assert(t, childBRes.HitCache())
		assert.Equal(t, 201, cacheTestUnwrapInt(t, childBRes))

		assert.NilError(t, sc2.ReleaseAndClose(ctx))
	})
}

func TestSessionCachePersistableReturnedHandleIDRetainsReferencedObjectAcrossSessions(t *testing.T) {
	t.Parallel()

	baseCtx := t.Context()
	cacheIface, err := NewCache(baseCtx, "")
	assert.NilError(t, err)
	base := cacheIface.(*cache)

	ctxInner := engine.ContextWithClientMetadata(baseCtx, &engine.ClientMetadata{
		ClientID:  "persistable-id-inner-client",
		SessionID: "persistable-id-inner-session",
	})
	ctxOuter := engine.ContextWithClientMetadata(baseCtx, &engine.ClientMetadata{
		ClientID:  "persistable-id-outer-client",
		SessionID: "persistable-id-outer-session",
	})
	ctxReplay := engine.ContextWithClientMetadata(baseCtx, &engine.ClientMetadata{
		ClientID:  "persistable-id-replay-client",
		SessionID: "persistable-id-replay-session",
	})

	srvInner := cacheTestObjectResolverServer(t, base, 1)
	var innerObj ObjectResult[*cacheTestObject]
	assert.NilError(t, srvInner.Select(ctxInner, srvInner.Root(), &innerObj, Selector{
		Field: "obj",
	}))
	innerObjID, err := innerObj.ID()
	assert.NilError(t, err)

	outerCache := NewSessionCache(base)
	returnedIDCall := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType((ID[*cacheTestObject]{}).Type()),
		Field: "persistable-returned-object-id",
	}

	outerInitCalls := 0
	outerRes, err := outerCache.GetOrInitCall(ctxOuter, noopTypeResolver{}, &CallRequest{
		ResultCall:    returnedIDCall,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		outerInitCalls++
		return cacheTestDetachedResult(returnedIDCall, NewID[*cacheTestObject](innerObjID)), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 1, outerInitCalls)

	returnedID, ok := UnwrapAs[ID[*cacheTestObject]](outerRes)
	assert.Assert(t, ok)
	encodedReturnedID, err := returnedID.Encode()
	assert.NilError(t, err)
	encodedInnerID, err := innerObjID.Encode()
	assert.NilError(t, err)
	assert.Equal(t, encodedInnerID, encodedReturnedID)

	assert.NilError(t, srvInner.Cache.ReleaseAndClose(ctxInner))
	assert.NilError(t, outerCache.ReleaseAndClose(ctxOuter))

	srvReplay := cacheTestObjectResolverServer(t, base, 1)
	replayInitCalls := 0
	replayRes, err := srvReplay.Cache.GetOrInitCall(ctxReplay, noopTypeResolver{}, &CallRequest{
		ResultCall:    returnedIDCall,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		replayInitCalls++
		return cacheTestDetachedResult(returnedIDCall, NewID[*cacheTestObject](innerObjID)), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 0, replayInitCalls)
	assert.Assert(t, replayRes.HitCache())

	replayID, ok := UnwrapAs[ID[*cacheTestObject]](replayRes)
	assert.Assert(t, ok)

	var loaded ObjectResult[*cacheTestObject]
	assert.NilError(t, srvReplay.Select(ctxReplay, srvReplay.Root(), &loaded, Selector{
		Field: "loadCacheTestObjectFromID",
		Args: []NamedInput{
			{
				Name:  "id",
				Value: replayID,
			},
		},
	}))
	assert.Equal(t, 0, loaded.Self().Value)

	assert.NilError(t, srvReplay.Cache.ReleaseAndClose(ctxReplay))
}

func TestSessionCacheErrorThenSuccessIsCached(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	base := cacheIface.(*cache)
	sc := NewSessionCache(base)

	key := cacheTestIntCall("session-error-then-success")
	steps := []error{
		fmt.Errorf("boom 1"),
		fmt.Errorf("boom 2"),
		nil,
		fmt.Errorf("should not run"),
	}
	callCount := 0
	fn := func(context.Context) (AnyResult, error) {
		assert.Assert(t, callCount < len(steps))
		stepErr := steps[callCount]
		callCount++
		if stepErr != nil {
			return nil, stepErr
		}
		return cacheTestIntResult(key, 99), nil
	}

	_, err = sc.GetOrInitCall(ctx, noopTypeResolver{}, &CallRequest{ResultCall: key}, fn)
	assert.ErrorContains(t, err, "boom 1")
	assert.Equal(t, 1, callCount)

	_, err = sc.GetOrInitCall(ctx, noopTypeResolver{}, &CallRequest{ResultCall: key}, fn)
	assert.ErrorContains(t, err, "boom 2")
	assert.Equal(t, 2, callCount)

	res, err := sc.GetOrInitCall(ctx, noopTypeResolver{}, &CallRequest{ResultCall: key}, fn)
	assert.NilError(t, err)
	assert.Equal(t, 3, callCount)
	assert.Assert(t, !res.HitCache())
	assert.Equal(t, 99, cacheTestUnwrapInt(t, res))

	res, err = sc.GetOrInitCall(ctx, noopTypeResolver{}, &CallRequest{ResultCall: key}, fn)
	assert.NilError(t, err)
	assert.Equal(t, 3, callCount)
	assert.Assert(t, res.HitCache())
	assert.Equal(t, 99, cacheTestUnwrapInt(t, res))
	assert.Equal(t, 1, base.Size())

	assert.NilError(t, sc.ReleaseAndClose(ctx))
	assert.Equal(t, 0, base.Size())
}

func TestSessionCacheTelemetryBehavior(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	base := cacheIface.(*cache)
	sc := NewSessionCache(base)

	key := cacheTestIntCall("telemetry")

	var beginCalls atomic.Int32
	var doneCalls atomic.Int32
	var beginSawResult atomic.Bool
	var cachedValsMu sync.Mutex
	var cachedVals []bool
	telemetryOpt := WithTelemetry(func(ctx context.Context, res AnyResult) (context.Context, func(AnyResult, bool, *error)) {
		beginCalls.Add(1)
		if res != nil {
			beginSawResult.Store(true)
		}
		return ctx, func(_ AnyResult, cached bool, _ *error) {
			doneCalls.Add(1)
			cachedValsMu.Lock()
			cachedVals = append(cachedVals, cached)
			cachedValsMu.Unlock()
		}
	})

	_, err = sc.GetOrInitCall(ctx, noopTypeResolver{}, &CallRequest{ResultCall: key}, ValueFunc(cacheTestIntResult(key, 1)), telemetryOpt)
	assert.NilError(t, err)

	_, err = sc.GetOrInitCall(ctx, noopTypeResolver{}, &CallRequest{ResultCall: key}, func(context.Context) (AnyResult, error) {
		return nil, fmt.Errorf("unexpected initializer call")
	}, telemetryOpt)
	assert.NilError(t, err)

	_, err = sc.GetOrInitCall(ctx, noopTypeResolver{}, &CallRequest{
		ResultCall: key,
		DoNotCache: true,
	}, ValueFunc(cacheTestIntResult(key, 2)), telemetryOpt)
	assert.NilError(t, err)

	repeatedCtx := WithRepeatedTelemetry(ctx)
	_, err = sc.GetOrInitCall(repeatedCtx, noopTypeResolver{}, &CallRequest{ResultCall: key}, func(context.Context) (AnyResult, error) {
		return nil, fmt.Errorf("unexpected initializer call")
	}, telemetryOpt)
	assert.NilError(t, err)

	assert.Equal(t, int32(3), beginCalls.Load())
	assert.Equal(t, int32(3), doneCalls.Load())
	assert.Assert(t, !beginSawResult.Load())
	assert.DeepEqual(t, []bool{false, false, true}, cachedVals)
}

func TestSessionCacheTelemetryCacheHitOnly(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	base := cacheIface.(*cache)
	sc := NewSessionCache(base)

	key := cacheTestIntCall("telemetry-hit-only")
	key2 := cacheTestIntCall("telemetry-hit-only-second")

	var beginCalls atomic.Int32
	var doneCalls atomic.Int32
	var beginSawResult atomic.Bool
	var cachedValsMu sync.Mutex
	var cachedVals []bool
	telemetryOpt := WithTelemetry(func(ctx context.Context, res AnyResult) (context.Context, func(AnyResult, bool, *error)) {
		beginCalls.Add(1)
		if res != nil {
			beginSawResult.Store(true)
		}
		return ctx, func(_ AnyResult, cached bool, _ *error) {
			doneCalls.Add(1)
			cachedValsMu.Lock()
			cachedVals = append(cachedVals, cached)
			cachedValsMu.Unlock()
		}
	})
	hitOnlyOpt := WithTelemetryPolicy(TelemetryPolicyCacheHitOnly)

	_, err = sc.GetOrInitCall(ctx, noopTypeResolver{}, &CallRequest{ResultCall: key}, ValueFunc(cacheTestIntResult(key, 1)), telemetryOpt, hitOnlyOpt)
	assert.NilError(t, err)
	assert.Equal(t, int32(0), beginCalls.Load())
	assert.Equal(t, int32(0), doneCalls.Load())

	_, err = sc.GetOrInitCall(ctx, noopTypeResolver{}, &CallRequest{ResultCall: key}, func(context.Context) (AnyResult, error) {
		return nil, fmt.Errorf("unexpected initializer call")
	}, telemetryOpt, hitOnlyOpt)
	assert.NilError(t, err)
	assert.Equal(t, int32(1), beginCalls.Load())
	assert.Equal(t, int32(1), doneCalls.Load())
	assert.Assert(t, beginSawResult.Load())
	assert.DeepEqual(t, []bool{true}, cachedVals)

	_, err = sc.GetOrInitCall(ctx, noopTypeResolver{}, &CallRequest{ResultCall: key}, func(context.Context) (AnyResult, error) {
		return nil, fmt.Errorf("unexpected initializer call")
	}, telemetryOpt, hitOnlyOpt)
	assert.NilError(t, err)
	assert.Equal(t, int32(1), beginCalls.Load())
	assert.Equal(t, int32(1), doneCalls.Load())
	assert.DeepEqual(t, []bool{true}, cachedVals)

	repeatedCtx := WithRepeatedTelemetry(ctx)
	_, err = sc.GetOrInitCall(repeatedCtx, noopTypeResolver{}, &CallRequest{ResultCall: key}, func(context.Context) (AnyResult, error) {
		return nil, fmt.Errorf("unexpected initializer call")
	}, telemetryOpt, hitOnlyOpt)
	assert.NilError(t, err)
	assert.Equal(t, int32(2), beginCalls.Load())
	assert.Equal(t, int32(2), doneCalls.Load())
	assert.DeepEqual(t, []bool{true, true}, cachedVals)

	_, err = sc.GetOrInitCall(ctx, noopTypeResolver{}, &CallRequest{ResultCall: key2}, ValueFunc(cacheTestIntResult(key2, 2)), telemetryOpt, hitOnlyOpt)
	assert.NilError(t, err)
	assert.Equal(t, int32(2), beginCalls.Load())
	assert.Equal(t, int32(2), doneCalls.Load())

	_, err = sc.GetOrInitCall(ctx, noopTypeResolver{}, &CallRequest{ResultCall: key2}, func(context.Context) (AnyResult, error) {
		return nil, fmt.Errorf("unexpected initializer call")
	}, telemetryOpt)
	assert.NilError(t, err)
	assert.Equal(t, int32(3), beginCalls.Load())
	assert.Equal(t, int32(3), doneCalls.Load())
	assert.DeepEqual(t, []bool{true, true, true}, cachedVals)
}

func TestSessionCacheDoNotCacheResultNotTrackedOnClose(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	base := cacheIface.(*cache)
	sc := NewSessionCache(base)

	key := cacheTestIntCall("session-donotcache-untracked")
	var releaseCalls atomic.Int32
	res, err := sc.GetOrInitCall(ctx, noopTypeResolver{}, &CallRequest{
		ResultCall: key,
		DoNotCache: true,
	}, ValueFunc(cacheTestIntResultWithOnRelease(key, 1, func(context.Context) error {
		releaseCalls.Add(1)
		return nil
	})))
	assert.NilError(t, err)
	assert.Assert(t, is.Equal(int32(0), releaseCalls.Load()))

	assert.NilError(t, sc.ReleaseAndClose(ctx))
	assert.Assert(t, is.Equal(int32(0), releaseCalls.Load()))

	assert.NilError(t, res.Release(ctx))
	assert.Assert(t, is.Equal(int32(1), releaseCalls.Load()))
}

func TestSessionCacheDoNotCacheNilResult(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	base := cacheIface.(*cache)
	sc := NewSessionCache(base)

	key := cacheTestIntCall("session-donotcache-nil")
	initCalls := 0

	res, err := sc.GetOrInitCall(ctx, noopTypeResolver{}, &CallRequest{
		ResultCall: key,
		DoNotCache: true,
	}, func(context.Context) (AnyResult, error) {
		initCalls++
		return nil, nil
	})
	assert.NilError(t, err)
	assert.Assert(t, res == nil)
	assert.Equal(t, 1, initCalls)
	assert.Equal(t, 0, base.Size())

	res, err = sc.GetOrInitCall(ctx, noopTypeResolver{}, &CallRequest{
		ResultCall: key,
		DoNotCache: true,
	}, func(context.Context) (AnyResult, error) {
		initCalls++
		return nil, nil
	})
	assert.NilError(t, err)
	assert.Assert(t, res == nil)
	assert.Equal(t, 2, initCalls)
	assert.Equal(t, 0, base.Size())
}

func TestSessionCacheAttachResultTrackedOnClose(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	base := cacheIface.(*cache)
	sc := NewSessionCache(base)

	key := cacheTestIntCall("session-attach-tracked")
	var releaseCalls atomic.Int32
	detached := cacheTestDetachedResult(key, cacheTestOnReleaseInt{
		Int: NewInt(7),
		onRelease: func(context.Context) error {
			releaseCalls.Add(1)
			return nil
		},
	})

	attached, err := sc.AttachResult(ctx, noopTypeResolver{}, detached)
	assert.NilError(t, err)
	assert.Assert(t, attached != nil)
	assert.Assert(t, is.Equal(int32(0), releaseCalls.Load()))
	assert.Assert(t, is.Equal(1, base.Size()))

	assert.NilError(t, sc.ReleaseAndClose(ctx))
	assert.Assert(t, is.Equal(int32(1), releaseCalls.Load()))
	assert.Assert(t, is.Equal(0, base.Size()))
}

func TestSessionCacheReleaseAndCloseWithNilResult(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	base := cacheIface.(*cache)
	sc := NewSessionCache(base)

	key := cacheTestIntCall("session-nil-result")
	res, err := sc.GetOrInitCall(ctx, noopTypeResolver{}, &CallRequest{ResultCall: key}, func(context.Context) (AnyResult, error) {
		return nil, nil
	})
	assert.NilError(t, err)
	assert.Assert(t, res == nil)

	assert.NilError(t, sc.ReleaseAndClose(ctx))
	assert.Equal(t, 0, base.Size())
}

func TestSessionCacheGetOrInitCallNilID(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	sc := NewSessionCache(cacheIface.(*cache))

	called := false
	_, err = sc.GetOrInitCall(ctx, noopTypeResolver{}, &CallRequest{ResultCall: &ResultCall{}}, func(context.Context) (AnyResult, error) {
		called = true
		return nil, nil
	})
	assert.ErrorContains(t, err, "missing field")
	assert.Assert(t, !called)
}

func TestSessionCacheErrorThenNilResultStaysNil(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	base := cacheIface.(*cache)
	sc := NewSessionCache(base)

	key := cacheTestIntCall("session-error-then-nil")
	initCalls := 0

	_, err = sc.GetOrInitCall(ctx, noopTypeResolver{}, &CallRequest{ResultCall: key}, func(context.Context) (AnyResult, error) {
		initCalls++
		return nil, errors.New("boom")
	})
	assert.ErrorContains(t, err, "boom")
	assert.Equal(t, 1, initCalls)

	res, err := sc.GetOrInitCall(ctx, noopTypeResolver{}, &CallRequest{ResultCall: key}, func(context.Context) (AnyResult, error) {
		initCalls++
		return nil, nil
	})
	assert.NilError(t, err)
	assert.Assert(t, res == nil)
	assert.Equal(t, 2, initCalls)

	initCalledAgain := false
	res, err = sc.GetOrInitCall(ctx, noopTypeResolver{}, &CallRequest{ResultCall: key}, func(context.Context) (AnyResult, error) {
		initCalledAgain = true
		return cacheTestIntResult(key, 42), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, res == nil)
	assert.Assert(t, !initCalledAgain)
	assert.Equal(t, 1, base.Size())
}

func TestSessionCacheArbitraryReleaseAndClose(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	base := cacheIface.(*cache)

	sc1 := NewSessionCache(base)
	sc2 := NewSessionCache(base)

	res, err := sc1.GetOrInitArbitrary(ctx, "session-arbitrary-1", ArbitraryValueFunc("a"))
	assert.NilError(t, err)
	assert.Assert(t, !res.HitCache())
	assert.Equal(t, "a", res.Value())

	res, err = sc1.GetOrInitArbitrary(ctx, "session-arbitrary-2", ArbitraryValueFunc("b"))
	assert.NilError(t, err)
	assert.Assert(t, !res.HitCache())
	assert.Equal(t, "b", res.Value())
	assert.Equal(t, 2, base.Size())

	res, err = sc2.GetOrInitArbitrary(ctx, "session-arbitrary-2", ArbitraryValueFunc("ignored"))
	assert.NilError(t, err)
	assert.Assert(t, res.HitCache())
	assert.Equal(t, "b", res.Value())

	res, err = sc2.GetOrInitArbitrary(ctx, "session-arbitrary-3", ArbitraryValueFunc("c"))
	assert.NilError(t, err)
	assert.Assert(t, !res.HitCache())
	assert.Equal(t, "c", res.Value())
	assert.Equal(t, 3, base.Size())

	assert.NilError(t, sc1.ReleaseAndClose(ctx))
	assert.Equal(t, 2, base.Size())

	_, err = sc1.GetOrInitArbitrary(ctx, "session-arbitrary-closed", ArbitraryValueFunc("x"))
	assert.ErrorContains(t, err, "session cache is closed")
	assert.Equal(t, 2, base.Size())

	assert.NilError(t, sc2.ReleaseAndClose(ctx))
	assert.Equal(t, 0, base.Size())
}

func TestSessionCacheArbitraryCloseWhileRunning(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	base := cacheIface.(*cache)
	sc := NewSessionCache(base)

	_, err = sc.GetOrInitArbitrary(ctx, "session-arbitrary-base", ArbitraryValueFunc("base"))
	assert.NilError(t, err)
	assert.Equal(t, 1, base.Size())

	startCh := make(chan struct{})
	stopCh := make(chan struct{})
	errCh := make(chan error, 1)
	go func() {
		_, err := sc.GetOrInitArbitrary(ctx, "session-arbitrary-running", func(context.Context) (any, error) {
			close(startCh)
			<-stopCh
			return "running", nil
		})
		errCh <- err
	}()

	select {
	case <-startCh:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for running call")
	}

	closeErrCh := make(chan error, 1)
	go func() {
		closeErrCh <- sc.ReleaseAndClose(ctx)
	}()

	select {
	case err := <-closeErrCh:
		t.Fatalf("ReleaseAndClose returned before running arbitrary call finished: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(stopCh)
	assert.NilError(t, <-closeErrCh)

	runErr := <-errCh
	assert.ErrorContains(t, runErr, "session cache was closed during execution")
	assert.Equal(t, 0, base.Size())
}
