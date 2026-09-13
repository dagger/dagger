package dagql

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
)

type captureTestValue struct {
	text        string
	lazy        LazyEvalFunc
	encode      func(context.Context) error
	release     func(context.Context) error
	attach      func(context.Context) error
	links       []PersistedSnapshotRefLink
	encodeCalls atomic.Int32
}

func (*captureTestValue) Type() *ast.Type {
	return &ast.Type{NamedType: "CaptureTestValue", NonNull: true}
}
func (v *captureTestValue) LazyEvalFunc() LazyEvalFunc                            { return v.lazy }
func (v *captureTestValue) PersistedSnapshotRefLinks() []PersistedSnapshotRefLink { return v.links }
func (v *captureTestValue) OnRelease(ctx context.Context) error {
	if v.release != nil {
		return v.release(ctx)
	}
	return nil
}
func (v *captureTestValue) AttachDependencyResults(ctx context.Context, _ AnyResult, _ func(AnyResult) (AnyResult, error)) ([]AnyResult, error) {
	if v.attach != nil {
		return nil, v.attach(ctx)
	}
	return nil, nil
}
func (v *captureTestValue) EncodePersistedObject(ctx context.Context, _ *PersistEncodeContext) (PersistedObjectEncoding, error) {
	v.encodeCalls.Add(1)
	if v.encode != nil {
		if err := v.encode(ctx); err != nil {
			return PersistedObjectEncoding{}, err
		}
	}
	data, err := json.Marshal(v.text)
	return PersistedObjectEncoding{JSON: data, SnapshotLinks: v.links}, err
}

type captureTestPartsValue struct{ *captureTestValue }

func (*captureTestPartsValue) Type() *ast.Type {
	return &ast.Type{NamedType: "CaptureTestPartsValue", NonNull: true}
}
func (*captureTestPartsValue) ResolveLazyEvalGroups(context.Context, AnyResult, []PartKey) ([]LazyGroupKey, error) {
	return []LazyGroupKey{"output"}, nil
}
func (v *captureTestPartsValue) LazyEvalFuncForGroup(LazyGroupKey) LazyEvalFunc { return v.lazy }

func init() {
	RegisterPersistedObjectFamily(PersistedObjectFamily{Name: "dagql_test.CaptureTestValue", Typed: (*captureTestValue)(nil), Visitor: persistTestSnapshotRoleVisitor{}})
	RegisterPersistedObjectFamily(PersistedObjectFamily{Name: "dagql_test.CaptureTestPartsValue", Typed: (*captureTestPartsValue)(nil), Visitor: persistTestSnapshotRoleVisitor{}})
}

func attachCaptureTestValue(t *testing.T, ctx context.Context, c *Cache, value Typed) AnyResult {
	t.Helper()
	srv := newDagqlServerForTest(t, &persistCodecRoot{})
	srv.InstallObject(NewClass(srv, ClassOpts[*captureTestValue]{}))
	srv.InstallObject(NewClass(srv, ClassOpts[*captureTestPartsValue]{}))
	frame := persistCodecFrame("capture-value", value)
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

func TestCapturePersistedRecordValuesAndColdCopies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	ctx, c, srv := persistedListTestCache(t, path)
	child := persistedListTestResult(t, ctx, c, srv, "child", String("child-value"))
	list := persistedListTestResult(t, ctx, c, srv, "list", DynamicResultArrayOutput{Elem: String(""), Values: []AnyResult{child}})
	absent := persistedListTestResult(t, ctx, c, srv, "absent", DynamicNullable{Elem: Int(0)})
	for _, res := range []AnyResult{child, list, absent} {
		want, err := DefaultPersistedSelfCodec.EncodeResult(ctx, c, res)
		require.NoError(t, err)
		got, err := c.CapturePersistedRecord(ctx, res)
		require.NoError(t, err)
		require.Equal(t, want.Envelope, got.Envelope)
		require.Equal(t, uint64(res.cacheSharedResult().id), got.ResultID)
		originalCall, err := json.Marshal(res.cacheSharedResult().loadResultCall())
		require.NoError(t, err)
		copiedCall, err := json.Marshal(got.Call)
		require.NoError(t, err)
		require.JSONEq(t, string(originalCall), string(copiedCall))
		got.Call.Field = "caller mutation"
		again, err := c.CapturePersistedRecord(ctx, res)
		require.NoError(t, err)
		require.NotEqual(t, got.Call.Field, again.Call.Field)
	}
	id := list.cacheSharedResult().id
	want, err := c.CapturePersistedRecord(ctx, list)
	require.NoError(t, err)
	require.NoError(t, c.ReleaseSession(ctx, "test-session"))
	require.NoError(t, c.Close(ctx))
	ctx, c, _ = persistedListTestCache(t, path)
	cold := c.resultsByID[id]
	require.False(t, cold.loadPayloadState().hasValue)
	res := Result[Typed]{shared: cold}
	got, err := c.CapturePersistedRecord(ctx, res)
	require.NoError(t, err)
	require.Equal(t, want, got)
	got.Envelope.Items[0].ResultID = 99999
	got.Call.Field = "changed"
	again, err := c.CapturePersistedRecord(ctx, res)
	require.NoError(t, err)
	require.Equal(t, want, again)
	require.False(t, cold.loadPayloadState().hasValue, "capture does not decode a cold row")
	childCold := c.resultsByID[child.cacheSharedResult().id]
	childRecord, err := c.CapturePersistedRecord(ctx, Result[Typed]{shared: childCold})
	require.NoError(t, err)
	childRecord.Envelope.ScalarJSON[0] = 'x'
	again, err = c.CapturePersistedRecord(ctx, Result[Typed]{shared: childCold})
	require.NoError(t, err)
	require.JSONEq(t, `"child-value"`, string(again.Envelope.ScalarJSON))
}

func TestCapturePersistedRecordLazyReadiness(t *testing.T) {
	for _, parts := range []bool{false, true} {
		name := "whole"
		if parts {
			name = "parts"
		}
		t.Run(name, func(t *testing.T) {
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
				var value Typed = obj
				if parts {
					value = &captureTestPartsValue{obj}
				}
				res := attachCaptureTestValue(t, ctx, c, value)
				before := obj.encodeCalls.Load()
				rec, err := c.CapturePersistedRecord(ctx, res)
				require.NoError(t, err)
				require.JSONEq(t, `"original inputs"`, string(rec.Envelope.ObjectJSON))
				select {
				case <-started:
					t.Fatal("capture started lazy work")
				default:
				}
				require.True(t, HasPendingLazyEvaluation(res))
				done := make(chan error, 1)
				go func() { done <- c.Evaluate(ctx, res) }()
				<-started
				_, err = c.CapturePersistedRecord(ctx, res)
				require.ErrorIs(t, err, ErrPersistStateNotReady)
				require.Equal(t, before+1, obj.encodeCalls.Load(), "running body must not reach the codec")
				close(finish)
				require.NoError(t, <-done)
				rec, err = c.CapturePersistedRecord(ctx, res)
				require.NoError(t, err)
				require.JSONEq(t, `"completed value"`, string(rec.Envelope.ObjectJSON))
				require.False(t, HasPendingLazyEvaluation(res))
			})
		})
	}
}

func TestCapturePersistedRecordPendingBookkeeping(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := &lazyBookkeepingSnapshotManager{attachEntered: make(chan struct{}), attachResult: make(chan error)}
		ctx, c := newPartsTestCache(t, mgr)
		defer c.CloseDiscardingPersistence()
		obj := &captureTestValue{text: "pending"}
		obj.lazy = func(context.Context) error {
			obj.text, obj.lazy = "completed", nil
			obj.links = []PersistedSnapshotRefLink{{Role: "snapshot", RefKey: "produced"}}
			return nil
		}
		res := attachCaptureTestValue(t, ctx, c, &captureTestPartsValue{obj})
		done := make(chan error, 1)
		go func() { done <- c.Evaluate(ctx, res) }()
		<-mgr.attachEntered
		injected := errors.New("attachment failed")
		mgr.attachResult <- injected
		require.ErrorIs(t, <-done, injected)
		calls := obj.encodeCalls.Load()
		_, err := c.CapturePersistedRecord(ctx, res)
		require.ErrorIs(t, err, ErrPersistStateNotReady)
		require.Equal(t, calls, obj.encodeCalls.Load())
		go func() { done <- c.Evaluate(ctx, res) }()
		<-mgr.attachEntered
		mgr.attachResult <- nil
		require.NoError(t, <-done)
		rec, err := c.CapturePersistedRecord(ctx, res)
		require.NoError(t, err)
		require.JSONEq(t, `"completed"`, string(rec.Envelope.ObjectJSON))
		require.Equal(t, obj.links, rec.SnapshotLinks)
		rec.SnapshotLinks[0].RefKey = "caller mutation"
		rec.Envelope.ObjectJSON[1] = 'X'
		again, err := c.CapturePersistedRecord(ctx, res)
		require.NoError(t, err)
		require.Equal(t, "produced", again.SnapshotLinks[0].RefKey)
		require.JSONEq(t, `"completed"`, string(again.Envelope.ObjectJSON))
		cacheTestReleaseSession(t, c, ctx)
	})
}

func TestCapturePersistedRecordBlocksOnlyItsUnstartedEvaluation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, c := newPartsTestCache(t, nil)
		defer c.CloseDiscardingPersistence()
		entered, allow := make(chan struct{}), make(chan struct{})
		allowEncode := sync.OnceFunc(func() { close(allow) })
		defer allowEncode()
		var ran atomic.Bool
		obj := &captureTestValue{text: "pending", encode: func(ctx context.Context) error {
			close(entered)
			select {
			case <-allow:
				return nil
			case <-ctx.Done():
				return context.Cause(ctx)
			}
		}}
		obj.lazy = func(context.Context) error { ran.Store(true); obj.lazy = nil; return nil }
		res := attachCaptureTestValue(t, ctx, c, obj)
		captureDone := make(chan error, 1)
		go func() { _, err := c.CapturePersistedRecord(ctx, res); captureDone <- err }()
		<-entered
		// Real unrelated publication proceeds while this codec is paused.
		otherFrame := cacheTestIntCall("unrelated")
		other, err := c.GetOrInitCall(ctx, cacheTestSessionID(t, ctx), noopTypeResolver{}, &CallRequest{ResultCall: otherFrame}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(otherFrame, 17), nil
		})
		require.NoError(t, err)
		require.Equal(t, Typed(Int(17)), other.Unwrap())
		evalEntered := make(chan struct{})
		var first atomic.Bool
		c.testAfterSessionOperationEnter = func(string) {
			if first.CompareAndSwap(false, true) {
				close(evalEntered)
			}
		}
		evalDone := make(chan error, 1)
		go func() { evalDone <- c.Evaluate(ctx, res) }()
		<-evalEntered
		// A mutex wait is not a durable synctest wait. Observe the held
		// mutex directly after the real evaluation has entered instead.
		acquired := res.cacheSharedResult().lazyMu.TryLock()
		if acquired {
			res.cacheSharedResult().lazyMu.Unlock()
		}
		require.False(t, acquired)
		require.False(t, ran.Load())
		allowEncode()
		require.NoError(t, <-captureDone)
		require.NoError(t, <-evalDone)
		require.True(t, ran.Load())
	})
}

func TestCapturePersistedRecordOwnershipAndCancellation(t *testing.T) {
	for _, cancelCapture := range []bool{false, true} {
		name := "success"
		if cancelCapture {
			name = "cancellation"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, c := newPartsTestCache(t, nil)
				defer c.CloseDiscardingPersistence()
				entered, allow := make(chan struct{}), make(chan struct{})
				allowEncode := sync.OnceFunc(func() { close(allow) })
				defer allowEncode()
				var released atomic.Int32
				obj := &captureTestValue{text: "settled", encode: func(ctx context.Context) error {
					close(entered)
					select {
					case <-allow:
						return nil
					case <-ctx.Done():
						return context.Cause(ctx)
					}
				}, release: func(ctx context.Context) error {
					require.NoError(t, context.Cause(ctx), "cleanup must survive caller cancellation")
					released.Add(1)
					return nil
				}}
				res := attachCaptureTestValue(t, ctx, c, obj)
				captureCtx, cancel := context.WithCancelCause(ctx)
				defer cancel(nil)
				done := make(chan error, 1)
				go func() { _, err := c.CapturePersistedRecord(captureCtx, res); done <- err }()
				<-entered
				// Settled capture does not keep lazyMu while its codec waits.
				require.False(t, HasPendingLazyEvaluation(res))
				cacheTestReleaseSession(t, c, ctx)
				c.egraphMu.RLock()
				registered := c.resultsByID[res.cacheSharedResult().id]
				owners := res.cacheSharedResult().incomingOwnershipCount
				c.egraphMu.RUnlock()
				require.Same(t, res.cacheSharedResult(), registered)
				require.Equal(t, int64(1), owners)
				require.Zero(t, released.Load())
				injected := errors.New("capture canceled")
				if cancelCapture {
					cancel(injected)
				} else {
					allowEncode()
				}
				if cancelCapture {
					require.ErrorIs(t, <-done, injected)
				} else {
					require.NoError(t, <-done)
				}
				require.Equal(t, int32(1), released.Load())
				c.egraphMu.RLock()
				_, remains := c.resultsByID[res.cacheSharedResult().id]
				c.egraphMu.RUnlock()
				require.False(t, remains)
				require.Zero(t, c.activeGlobalOperations.Load())
				_, err := c.CapturePersistedRecord(ctx, res)
				require.ErrorContains(t, err, "not registered")
			})
		})
	}
}

func TestCapturePersistedRecordErrorsAndClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, c := newPartsTestCache(t, nil)
		defer c.CloseDiscardingPersistence()
		obj := &captureTestValue{text: "settled"}
		res := attachCaptureTestValue(t, ctx, c, obj)
		_, err := c.CapturePersistedRecord(ctx, nil)
		require.ErrorContains(t, err, "detached")
		ctxOther, other := newPartsTestCache(t, nil)
		defer other.CloseDiscardingPersistence()
		foreign := attachCaptureTestValue(t, ctxOther, other, &captureTestValue{})
		require.Equal(t, res.cacheSharedResult().id, foreign.cacheSharedResult().id)
		_, err = c.CapturePersistedRecord(ctx, foreign)
		require.ErrorContains(t, err, "not registered")
		injected := errors.New("codec failed")
		obj.encode = func(context.Context) error { return injected }
		c.egraphMu.RLock()
		owners := res.cacheSharedResult().incomingOwnershipCount
		c.egraphMu.RUnlock()
		_, err = c.CapturePersistedRecord(ctx, res)
		require.ErrorIs(t, err, injected)
		c.egraphMu.RLock()
		after := res.cacheSharedResult().incomingOwnershipCount
		c.egraphMu.RUnlock()
		require.Equal(t, owners, after)
		require.Zero(t, c.activeGlobalOperations.Load())
		entered, allow := make(chan struct{}), make(chan struct{})
		allowEncode := sync.OnceFunc(func() { close(allow) })
		defer allowEncode()
		obj.encode = func(context.Context) error { close(entered); <-allow; return nil }
		done := make(chan error, 1)
		go func() { _, err := c.CapturePersistedRecord(ctx, res); done <- err }()
		<-entered
		closed := make(chan error, 1)
		go func() { closed <- c.Close(context.Background()) }()
		synctest.Wait()
		require.True(t, c.closing.Load())
		select {
		case err := <-closed:
			t.Fatalf("close passed active capture: %v", err)
		default:
		}
		_, err = c.CapturePersistedRecord(ctx, res)
		require.ErrorIs(t, err, ErrCacheClosed)
		allowEncode()
		require.NoError(t, <-done)
		require.NoError(t, <-closed)
	})
}

func TestCapturePersistedRecordInitialAttachment(t *testing.T) {
	for _, fail := range []bool{false, true} {
		name := "clean"
		if fail {
			name = "failed"
		}
		t.Run(name, func(t *testing.T) {
			ctx, c := newPartsTestCache(t, nil)
			defer c.CloseDiscardingPersistence()
			entered, allow := make(chan struct{}), make(chan struct{})
			finish := sync.OnceFunc(func() { close(allow) })
			defer finish()
			attachErr := errors.New("initial attachment failed")
			var released atomic.Int32
			obj := &captureTestValue{text: "stable", release: func(context.Context) error {
				released.Add(1)
				return nil
			}, attach: func(context.Context) error {
				close(entered)
				<-allow
				if fail {
					return attachErr
				}
				return nil
			}}
			srv := newDagqlServerForTest(t, &persistCodecRoot{})
			srv.InstallObject(NewClass(srv, ClassOpts[*captureTestValue]{}))
			frame := persistCodecFrame("initial-attachment", obj)
			done := make(chan error, 1)
			go func() {
				_, err := c.GetOrInitCall(ctx, "publisher", srv, &CallRequest{ResultCall: frame}, func(context.Context) (AnyResult, error) {
					return NewObjectResultForCall(obj, srv, frame)
				})
				done <- err
			}()
			<-entered
			c.egraphMu.Lock()
			var shared *sharedResult
			for _, row := range c.resultsByID {
				if row.loadPayloadState().self == obj {
					shared = row
					break
				}
			}
			// Keep the failed publication registered so capture sees its failed
			// barrier, instead of merely rejecting an already-collected row.
			if shared != nil {
				c.incrementIncomingOwnershipLocked(ctx, shared)
			}
			c.egraphMu.Unlock()
			require.NotNil(t, shared)
			res := Result[Typed]{shared: shared}
			assertCaptureDoesNotLeak := func() error {
				t.Helper()
				c.egraphMu.RLock()
				owners := shared.incomingOwnershipCount
				c.egraphMu.RUnlock()
				operations := c.activeGlobalOperations.Load()
				_, err := c.CapturePersistedRecord(ctx, res)
				c.egraphMu.RLock()
				after := shared.incomingOwnershipCount
				c.egraphMu.RUnlock()
				require.Equal(t, owners, after)
				require.Equal(t, operations, c.activeGlobalOperations.Load())
				return err
			}
			require.Equal(t, resultAttachmentOpen, shared.attachmentState())
			require.ErrorIs(t, assertCaptureDoesNotLeak(), ErrPersistStateNotReady)
			require.Zero(t, obj.encodeCalls.Load())
			finish()
			if fail {
				require.ErrorIs(t, <-done, attachErr)
				require.Equal(t, resultAttachmentFailed, shared.attachmentState())
				err := assertCaptureDoesNotLeak()
				require.ErrorContains(t, err, "dependency attachment failed")
				require.NotErrorIs(t, err, ErrPersistStateNotReady)
				require.Zero(t, obj.encodeCalls.Load())
			} else {
				require.NoError(t, <-done)
				require.Equal(t, resultAttachmentClean, shared.attachmentState())
				first, err := c.CapturePersistedRecord(ctx, res)
				require.NoError(t, err)
				second, err := c.CapturePersistedRecord(ctx, res)
				require.NoError(t, err)
				require.Equal(t, first, second)
				require.JSONEq(t, `"stable"`, string(first.Envelope.ObjectJSON))
			}
			c.egraphMu.Lock()
			queue, err := c.decrementIncomingOwnershipLocked(ctx, shared, nil)
			releases, collectErr := c.collectUnownedResultsLocked(ctx, queue)
			c.egraphMu.Unlock()
			require.NoError(t, errors.Join(err, collectErr, runOnReleaseFuncs(ctx, releases)))
			require.NoError(t, c.ReleaseSession(ctx, "publisher"))
			require.Equal(t, int32(1), released.Load())
			require.Zero(t, c.activeGlobalOperations.Load())
			c.egraphMu.RLock()
			_, remains := c.resultsByID[shared.id]
			c.egraphMu.RUnlock()
			require.False(t, remains)
		})
	}
}
