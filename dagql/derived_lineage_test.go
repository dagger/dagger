package dagql

import (
	"context"
	"testing"

	"github.com/vektah/gqlparser/v2/ast"
	"gotest.tools/v3/assert"
	is "gotest.tools/v3/assert/cmp"
)

type detachedLineageObject struct {
	Value int
}

func (*detachedLineageObject) Type() *ast.Type {
	return &ast.Type{
		NamedType: "DetachedLineageObject",
		NonNull:   true,
	}
}

func TestDetachedReceiverSelectionPreservesReceiverLineage(t *testing.T) {
	t.Parallel()
	ctx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(ctx, "", nil)
	assert.NilError(t, err)
	base := cacheIface
	ctx = ContextWithCache(ctx, base)
	srv := cacheTestServer(t, base)

	Fields[*detachedLineageObject]{
		Func("child", func(_ context.Context, self *detachedLineageObject, _ struct{}) (*detachedLineageObject, error) {
			return &detachedLineageObject{Value: self.Value + 1}, nil
		}),
	}.Install(srv)

	parentCall := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType((&detachedLineageObject{}).Type()),
		Field: "parent",
	}
	parent := cacheTestDetachedObjectResult(parentCall, srv, &detachedLineageObject{Value: 1})

	child, err := parent.Select(srvToContext(ctx, srv), srv, Selector{Field: "child"})
	assert.NilError(t, err)
	assert.Assert(t, child != nil)
	assert.Assert(t, child.cacheSharedResult().id != 0)

	call, err := child.ResultCall()
	assert.NilError(t, err)
	assert.Assert(t, call.Receiver != nil)
	assert.Assert(t, call.Receiver.Call != nil)
	assert.Equal(t, uint64(0), call.Receiver.ResultID)
	assert.Equal(t, "parent", call.Receiver.Call.Field)
}

func TestNthValuePreservesAttachmentLineage(t *testing.T) {
	t.Parallel()
	ctx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(ctx, "", nil)
	assert.NilError(t, err)
	base := cacheIface
	ctx = ContextWithCache(ctx, base)
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

	attachedParent, err := base.AttachResult(ctx, "test-session", srv, detachedParent)
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

func TestNthValueAttachedObjectResultArrayReturnsCanonicalChild(t *testing.T) {
	t.Parallel()
	ctx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(ctx, "", nil)
	assert.NilError(t, err)
	base := cacheIface
	ctx = ContextWithCache(ctx, base)
	srv := cacheTestServer(t, base)
	ctx = srvToContext(ctx, srv)

	child1Call := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType((&cacheTestObject{}).Type()),
		Field: "child1",
	}
	child1Detached := cacheTestObjectResult(t, srv, child1Call, 1, nil)
	child1Any, err := base.AttachResult(ctx, "test-session", srv, child1Detached)
	assert.NilError(t, err)
	child1 := child1Any.(ObjectResult[*cacheTestObject])

	child2Call := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType((&cacheTestObject{}).Type()),
		Field: "child2",
	}
	child2Detached := cacheTestObjectResult(t, srv, child2Call, 2, nil)
	child2Any, err := base.AttachResult(ctx, "test-session", srv, child2Detached)
	assert.NilError(t, err)
	child2 := child2Any.(ObjectResult[*cacheTestObject])

	arr := ObjectResultArray[*cacheTestObject]{child1, child2}
	parentCall := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType(arr.Type()),
		Field: "items",
	}
	detachedParent, err := NewResultForCall(arr, parentCall)
	assert.NilError(t, err)
	attachedParentAny, err := base.AttachResult(ctx, "test-session", srv, detachedParent)
	assert.NilError(t, err)
	attachedParent := attachedParentAny.(Result[Typed])
	parentShared := attachedParent.cacheSharedResult()
	assert.Assert(t, parentShared != nil)

	nthReq := &CallRequest{
		ResultCall: parentCall.fork(),
	}
	nthReq.Type = nthReq.Type.Elem.clone()
	nthReq.Receiver = &ResultCallRef{ResultID: uint64(parentShared.id), shared: parentShared}
	nthReq.Nth = 1

	_, hitBefore, err := base.lookupCallRequest(ctx, "test-session", srv, nthReq)
	assert.NilError(t, err)
	assert.Assert(t, !hitBefore)

	nthAny, err := attachedParent.NthValue(ctx, 1)
	assert.NilError(t, err)
	nth := nthAny.(ObjectResult[*cacheTestObject])
	assert.Assert(t, nth.HitCache())
	assert.Assert(t, nth.cacheSharedResult() == child1.cacheSharedResult())

	nthCall, err := nth.ResultCall()
	assert.NilError(t, err)
	assert.Equal(t, "child1", nthCall.Field)

	_, hitAfter, err := base.lookupCallRequest(ctx, "test-session", srv, nthReq)
	assert.NilError(t, err)
	assert.Assert(t, !hitAfter)
}

func TestNthValueAttachedDynamicResultArrayReturnsCanonicalChild(t *testing.T) {
	t.Parallel()
	ctx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(ctx, "", nil)
	assert.NilError(t, err)
	base := cacheIface
	ctx = ContextWithCache(ctx, base)
	ctx = srvToContext(ctx, cacheTestServer(t, base))

	child1Call := cacheTestIntCall("result-array-child-1")
	child1Any, err := base.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: child1Call}, ValueFunc(cacheTestIntResult(child1Call, 11)))
	assert.NilError(t, err)
	child1 := child1Any.(Result[Typed])

	child2Call := cacheTestIntCall("result-array-child-2")
	child2Any, err := base.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: child2Call}, ValueFunc(cacheTestIntResult(child2Call, 22)))
	assert.NilError(t, err)

	arr := DynamicResultArrayOutput{
		Elem:   NewInt(0),
		Values: []AnyResult{child1Any, child2Any},
	}
	parentCall := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType(arr.Type()),
		Field: "items",
	}
	detachedParent, err := NewResultForCall(arr, parentCall)
	assert.NilError(t, err)
	attachedParentAny, err := base.AttachResult(ctx, "test-session", noopTypeResolver{}, detachedParent)
	assert.NilError(t, err)
	attachedParent := attachedParentAny.(Result[Typed])
	parentShared := attachedParent.cacheSharedResult()
	assert.Assert(t, parentShared != nil)

	nthReq := &CallRequest{
		ResultCall: parentCall.fork(),
	}
	nthReq.Type = nthReq.Type.Elem.clone()
	nthReq.Receiver = &ResultCallRef{ResultID: uint64(parentShared.id), shared: parentShared}
	nthReq.Nth = 1

	_, hitBefore, err := base.lookupCallRequest(ctx, "test-session", noopTypeResolver{}, nthReq)
	assert.NilError(t, err)
	assert.Assert(t, !hitBefore)

	nth, err := attachedParent.NthValue(ctx, 1)
	assert.NilError(t, err)
	assert.Assert(t, nth.HitCache())
	assert.Assert(t, nth.cacheSharedResult() == child1.cacheSharedResult())
	assert.Equal(t, 11, cacheTestUnwrapInt(t, nth))

	nthCall, err := nth.ResultCall()
	assert.NilError(t, err)
	assert.Equal(t, "result-array-child-1", nthCall.Field)

	_, hitAfter, err := base.lookupCallRequest(ctx, "test-session", noopTypeResolver{}, nthReq)
	assert.NilError(t, err)
	assert.Assert(t, !hitAfter)
}

func TestNullableDerefUsesSameSharedResult(t *testing.T) {
	t.Parallel()
	ctx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(ctx, "", nil)
	assert.NilError(t, err)
	base := cacheIface
	ctx = ContextWithCache(ctx, base)
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

	attached, err := base.AttachResult(ctx, "test-session", srv, detached)
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

func TestNullableWrappedUsesSameSharedResult(t *testing.T) {
	t.Parallel()
	ctx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(ctx, "", nil)
	assert.NilError(t, err)
	base := cacheIface
	ctx = ContextWithCache(ctx, base)
	srv := cacheTestServer(t, base)
	ctx = srvToContext(ctx, srv)

	call := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType((&cacheTestObject{}).Type()),
		Field: "object",
	}
	detached := cacheTestObjectResult(t, srv, call, 17, nil)
	attached, err := base.AttachResult(ctx, "test-session", srv, detached)
	assert.NilError(t, err)
	assert.Assert(t, attached != nil)
	attachedShared := attached.cacheSharedResult()
	assert.Assert(t, attachedShared != nil)
	assert.Assert(t, attachedShared.id != 0)

	wrapped := attached.NullableWrapped()
	assert.Assert(t, wrapped != nil)
	assert.Assert(t, wrapped.cacheSharedResult() == attachedShared)
	assert.Assert(t, !wrapped.Type().NonNull)

	wrappedNullable, ok := wrapped.Unwrap().(DynamicNullable)
	assert.Assert(t, ok)
	assert.Assert(t, wrappedNullable.Valid)
	_, ok = UnwrapAs[*cacheTestObject](wrappedNullable.Value)
	assert.Assert(t, ok)

	derefWrapped, ok := wrapped.DerefValue()
	assert.Assert(t, ok)
	assert.Assert(t, derefWrapped != nil)
	assert.Assert(t, derefWrapped.cacheSharedResult() == attachedShared)
	assert.Assert(t, derefWrapped.Type().NonNull)
	_, ok = UnwrapAs[*cacheTestObject](derefWrapped.Unwrap())
	assert.Assert(t, ok)

	id, err := wrapped.ID()
	assert.NilError(t, err)
	assert.Assert(t, id != nil)
	assert.Assert(t, !id.Type().ToAST().NonNull)

	loaded, err := srv.LoadType(ctx, id)
	assert.NilError(t, err)
	assert.Assert(t, loaded != nil)
	assert.Assert(t, loaded.cacheSharedResult() == attachedShared)
	assert.Assert(t, !loaded.Type().NonNull)

	loadedNullable, ok := loaded.Unwrap().(DynamicNullable)
	assert.Assert(t, ok)
	assert.Assert(t, loadedNullable.Valid)
	_, ok = UnwrapAs[*cacheTestObject](loadedNullable.Value)
	assert.Assert(t, ok)
}

func TestNullableDerefCacheHitsReconstructObjectView(t *testing.T) {
	t.Parallel()
	ctx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(ctx, "", nil)
	assert.NilError(t, err)
	base := cacheIface
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

	first, err := base.GetOrInitCall(ctx, "test-session", srv, newReq(), func(context.Context) (AnyResult, error) {
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

	second, err := base.GetOrInitCall(ctx, "test-session", srv, newReq(), func(context.Context) (AnyResult, error) {
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
