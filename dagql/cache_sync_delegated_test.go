package dagql

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"gotest.tools/v3/assert"
)

// syncDelegatingCodec is the parts value's family's codec in these tests,
// which says, as Container's says of its metadata calls, that a result of the
// field "delegate" keeps every part of its receiver but its meta part, its
// own. No other test makes such a field.
type syncDelegatingCodec struct{}

func (syncDelegatingCodec) NormalizeForeign(v PersistedPayloadVisit) (ForeignPayload, error) {
	return ForeignPayload{JSON: v.Payload}, nil
}
func (syncDelegatingCodec) ValidateForeign(PersistedPayloadVisit) error { return nil }
func (syncDelegatingCodec) MapSnapshotParts(PersistedPayloadVisit) ([]CapturedCodecOutput, error) {
	return nil, nil
}
func (syncDelegatingCodec) DelegatedParts(call *ResultCall) (uint64, []PartKey, bool) {
	if call.Field != "delegate" || call.Receiver == nil || call.Receiver.ResultID == 0 {
		return 0, nil, false
	}
	return call.Receiver.ResultID, []PartKey{partsTestPartMeta}, true
}

func init() {
	RegisterPersistedObjectFamily(PersistedObjectFamily{Name: "dagql_test.SyncDelegate", Typed: (*cacheTestPartsObject)(nil), Visitor: PersistedNoReferences{}, Transfer: syncDelegatingCodec{}})
}

// syncTestRuns counts the runs of a sync test value's two groups.
type syncTestRuns struct{ meta, out atomic.Int32 }

// syncTestValue is a parts value of field, made on receiver when it is set,
// whose groups count their runs and complete their parts; fail, when set,
// fails its out group.
func syncTestValue(t *testing.T, c *Cache, ctx context.Context, srv *Server, field string, receiver *ObjectResult[*cacheTestPartsObject], fail error) (ObjectResult[*cacheTestPartsObject], *syncTestRuns) {
	t.Helper()
	runs := &syncTestRuns{}
	obj := &cacheTestPartsObject{resolveFn: partsTestDirectResolve}
	var res ObjectResult[*cacheTestPartsObject]
	obj.groupEval = map[LazyGroupKey]LazyEvalFunc{
		partsTestGroupMeta: func(ctx context.Context) error {
			runs.meta.Add(1)
			return c.partHostFor(res.cacheSharedResult()).RunNative(ctx, partsTestGroupMeta, []PartKey{partsTestPartMeta}, func(context.Context) error { return nil })
		},
		partsTestGroupOut: func(ctx context.Context) error {
			runs.out.Add(1)
			if fail != nil {
				return fail
			}
			return c.partHostFor(res.cacheSharedResult()).RunNative(ctx, partsTestGroupOut, []PartKey{partsTestPartFS, partsTestPartXMeta}, func(context.Context) error { return nil })
		},
	}
	frame := &ResultCall{Kind: ResultCallKindField, Type: NewResultCallType((&cacheTestPartsObject{}).Type()), Field: field}
	if receiver != nil {
		frame.Receiver = &ResultCallRef{ResultID: uint64(receiver.cacheSharedResult().id)}
	}
	resAny, err := c.GetOrInitCall(ctx, cacheTestSessionID(t, ctx), srv, &CallRequest{ResultCall: frame}, func(context.Context) (AnyResult, error) {
		return NewObjectResultForCall(obj, srv, frame)
	})
	assert.NilError(t, err)
	res = resAny.(ObjectResult[*cacheTestPartsObject])
	return res, runs
}

// completedElsewhere marks res as a value merged from another engine's
// record of an operation that completed there.
func completedElsewhere(res ObjectResult[*cacheTestPartsObject]) {
	res.Self().completedElsewhere = true
	shared := res.cacheSharedResult()
	shared.payloadMu.Lock()
	shared.imported = true
	shared.payloadMu.Unlock()
}

// A sync of a result that keeps its receiver's parts but its own completes
// its own part and syncs the receiver, by the same sync: a receiver that
// completed elsewhere returns at once, so nothing of its content is needed,
// and the parts the result keeps of it come when something reads them, as
// the receiver's own parts would. A receiver pending here evaluates, and its
// failure is the sync's. A result of any other call evaluates whole, as
// before.
func TestSyncOfADelegatingResultSyncsItsReceiver(t *testing.T) {
	t.Parallel()
	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	ctx = ContextWithCache(ctx, c)
	t.Cleanup(func() { assert.NilError(t, c.CloseDiscardingPersistence()) })
	srv := cacheTestPartsServer(t)
	ctx = ContextWithServer(ctx, srv)

	t.Run("over a receiver that completed elsewhere", func(t *testing.T) {
		receiver, receiverRuns := syncTestValue(t, c, ctx, srv, "elsewhere", nil, nil)
		completedElsewhere(receiver)
		middle, middleRuns := syncTestValue(t, c, ctx, srv, "delegate", &receiver, nil)
		child, childRuns := syncTestValue(t, c, ctx, srv, "delegate", &middle, nil)
		assert.NilError(t, c.Sync(ctx, child))
		assert.Equal(t, int32(1), childRuns.meta.Load(), "the child's own part")
		assert.Equal(t, int32(1), middleRuns.meta.Load(), "the middle's own part, as the child syncs it")
		assert.Equal(t, int32(0), childRuns.out.Load()+middleRuns.out.Load(), "no part they keep of the receiver")
		assert.Equal(t, int32(0), receiverRuns.meta.Load()+receiverRuns.out.Load(), "nothing of the receiver")
		// A read of the part afterwards gets it then.
		assert.NilError(t, c.EvaluateParts(ctx, child, partsTestPartFS))
		assert.Equal(t, int32(1), childRuns.out.Load(), "a read of the kept part evaluates it")
	})

	t.Run("over a receiver pending here", func(t *testing.T) {
		receiver, receiverRuns := syncTestValue(t, c, ctx, srv, "pending", nil, nil)
		child, childRuns := syncTestValue(t, c, ctx, srv, "delegate", &receiver, nil)
		assert.NilError(t, c.Sync(ctx, child))
		assert.Equal(t, int32(1), childRuns.meta.Load())
		assert.Equal(t, int32(1), receiverRuns.meta.Load(), "the receiver's operation runs")
		assert.Equal(t, int32(1), receiverRuns.out.Load(), "the receiver's operation runs")
	})

	t.Run("over a receiver pending here that fails", func(t *testing.T) {
		failed := errors.New("the receiver's operation failed")
		receiver, _ := syncTestValue(t, c, ctx, srv, "failing", nil, failed)
		child, _ := syncTestValue(t, c, ctx, srv, "delegate", &receiver, nil)
		assert.ErrorIs(t, c.Sync(ctx, child), failed)
	})

	t.Run("of a result of another call", func(t *testing.T) {
		receiver, receiverRuns := syncTestValue(t, c, ctx, srv, "elsewhere-too", nil, nil)
		completedElsewhere(receiver)
		own, ownRuns := syncTestValue(t, c, ctx, srv, "own", &receiver, nil)
		assert.NilError(t, c.Sync(ctx, own))
		assert.Equal(t, int32(1), ownRuns.meta.Load())
		assert.Equal(t, int32(1), ownRuns.out.Load(), "a result with work of its own evaluates whole")
		assert.Equal(t, int32(0), receiverRuns.meta.Load()+receiverRuns.out.Load())
	})
}
