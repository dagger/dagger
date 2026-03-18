package dagql

import (
	"context"
	"testing"

	"gotest.tools/v3/assert"
	is "gotest.tools/v3/assert/cmp"
)

func TestDetachedReceiverSelectionStaysDetached(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	base := cacheIface.(*cache)
	srv := cacheTestServer(t, base)

	Fields[*cacheTestObject]{
		Func("child", func(_ context.Context, self *cacheTestObject, _ struct{}) (*cacheTestObject, error) {
			return &cacheTestObject{Value: self.Value + 1}, nil
		}),
	}.Install(srv)

	parentCall := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType((&cacheTestObject{}).Type()),
		Field: "parent",
	}
	parent := cacheTestObjectResult(t, srv, parentCall, 1, nil)

	child, err := parent.Select(srvToContext(ctx, srv), srv, Selector{Field: "child"})
	assert.NilError(t, err)
	assert.Assert(t, child != nil)
	assert.Assert(t, is.Equal(sharedResultID(0), child.cacheSharedResult().id))
	assert.Equal(t, 0, base.Size())

	call, err := child.ResultCall()
	assert.NilError(t, err)
	assert.Assert(t, call.Receiver != nil)
	assert.Assert(t, call.Receiver.Call != nil)
	assert.Equal(t, uint64(0), call.Receiver.ResultID)
	assert.Equal(t, "parent", call.Receiver.Call.Field)
}

func TestNthValuePreservesAttachmentLineage(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	base := cacheIface.(*cache)
	srv := cacheTestServer(t, base)
	ctx = srvToContext(ctx, srv)

	list := Array[*cacheTestObject]{
		&cacheTestObject{Value: 1},
		&cacheTestObject{Value: 2},
	}
	listCall := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType(list.Type()),
		Field: "items",
	}
	detachedParent, err := NewResultForCall(list, listCall)
	assert.NilError(t, err)

	detachedChild, err := detachedParent.NthValue(ctx, 1)
	assert.NilError(t, err)
	assert.Assert(t, detachedChild != nil)
	assert.Assert(t, is.Equal(sharedResultID(0), detachedChild.cacheSharedResult().id))

	attachedParent, err := srv.Cache.AttachResult(ctx, detachedParent)
	assert.NilError(t, err)
	assert.Assert(t, attachedParent != nil)
	parentShared := attachedParent.cacheSharedResult()
	assert.Assert(t, parentShared != nil)
	assert.Assert(t, parentShared.id != 0)

	attachedChild, err := attachedParent.NthValue(ctx, 1)
	assert.NilError(t, err)
	assert.Assert(t, attachedChild != nil)
	childShared := attachedChild.cacheSharedResult()
	assert.Assert(t, childShared != nil)
	assert.Assert(t, childShared.id != 0)
	assert.Assert(t, childShared.id != parentShared.id)

	childCall, err := attachedChild.ResultCall()
	assert.NilError(t, err)
	assert.Assert(t, childCall.Receiver != nil)
	assert.Equal(t, uint64(parentShared.id), childCall.Receiver.ResultID)
}

func TestNullableDerefUsesSameSharedResult(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	base := cacheIface.(*cache)
	srv := cacheTestServer(t, base)
	ctx = srvToContext(ctx, srv)

	nullable := Nullable[*cacheTestObject]{
		Value: &cacheTestObject{Value: 7},
		Valid: true,
	}
	call := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType(nullable.Type()),
		Field: "maybeObject",
	}
	detached, err := NewResultForCall(nullable, call)
	assert.NilError(t, err)

	derefDetached, ok := detached.DerefValue()
	assert.Assert(t, ok)
	assert.Assert(t, derefDetached != nil)
	assert.Assert(t, derefDetached.cacheSharedResult() == detached.cacheSharedResult())
	assert.Assert(t, derefDetached.Type().NonNull)
	_, ok = UnwrapAs[*cacheTestObject](derefDetached.Unwrap())
	assert.Assert(t, ok)

	attached, err := srv.Cache.AttachResult(ctx, detached)
	assert.NilError(t, err)
	assert.Assert(t, attached != nil)
	attachedShared := attached.cacheSharedResult()
	assert.Assert(t, attachedShared != nil)
	assert.Assert(t, attachedShared.id != 0)

	derefAttached, ok := attached.DerefValue()
	assert.Assert(t, ok)
	assert.Assert(t, derefAttached != nil)
	assert.Assert(t, derefAttached.cacheSharedResult() == attachedShared)
	assert.Assert(t, derefAttached.Type().NonNull)
	_, ok = UnwrapAs[*cacheTestObject](derefAttached.Unwrap())
	assert.Assert(t, ok)

	id, err := derefAttached.ID()
	assert.NilError(t, err)
	assert.Assert(t, id != nil)
	assert.Assert(t, id.Type().ToAST().NonNull)

	loaded, err := srv.LoadType(ctx, id)
	assert.NilError(t, err)
	assert.Assert(t, loaded != nil)
	assert.Assert(t, loaded.cacheSharedResult() == attachedShared)
	assert.Assert(t, loaded.Type().NonNull)
	_, ok = UnwrapAs[*cacheTestObject](loaded.Unwrap())
	assert.Assert(t, ok)
}

func TestNullableDerefCacheHitsReconstructObjectView(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	base := cacheIface.(*cache)
	srv := cacheTestServer(t, base)
	ctx = srvToContext(ctx, srv)

	nullable := Nullable[*cacheTestObject]{
		Value: &cacheTestObject{Value: 11},
		Valid: true,
	}
	baseCall := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType(nullable.Type()),
		Field: "maybeObject",
	}
	newReq := func() *CallRequest {
		return &CallRequest{
			ResultCall: baseCall.clone(),
		}
	}

	first, err := srv.Cache.GetOrInitCall(ctx, newReq(), func(context.Context) (AnyResult, error) {
		res, err := NewResultForCall(nullable, baseCall)
		if err != nil {
			return nil, err
		}
		deref, ok := res.DerefValue()
		if !ok {
			return nil, nil
		}
		return deref, nil
	})
	assert.NilError(t, err)
	firstObj, ok := first.(ObjectResult[*cacheTestObject])
	assert.Assert(t, ok)
	assert.Assert(t, firstObj.Type().NonNull)
	firstShared := firstObj.cacheSharedResult()
	assert.Assert(t, firstShared != nil)
	assert.Assert(t, firstShared.id != 0)

	second, err := srv.Cache.GetOrInitCall(ctx, newReq(), func(context.Context) (AnyResult, error) {
		return nil, context.Canceled
	})
	assert.NilError(t, err)
	secondObj, ok := second.(ObjectResult[*cacheTestObject])
	assert.Assert(t, ok)
	assert.Assert(t, secondObj.HitCache())
	assert.Assert(t, secondObj.Type().NonNull)
	assert.Assert(t, secondObj.cacheSharedResult() == firstShared)
	_, ok = UnwrapAs[*cacheTestObject](secondObj.Unwrap())
	assert.Assert(t, ok)
}
