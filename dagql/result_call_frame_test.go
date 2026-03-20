package dagql

import (
	"context"
	"testing"

	"github.com/dagger/dagger/dagql/call"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

func TestResultCallDigestErrorsDoNotPanic(t *testing.T) {
	t.Parallel()

	frame := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType(Int(0).Type()),
		Field: "broken",
		Args: []*ResultCallArg{{
			Name: "bad",
			Value: &ResultCallLiteral{
				Kind: ResultCallLiteralKind("bogus"),
			},
		}},
	}

	_, err := frame.RecipeDigest()
	require.ErrorContains(t, err, `args: failed to write argument "bad" to hash`)

	_, err = frame.ContentPreferredDigest()
	require.ErrorContains(t, err, `args: failed to write argument "bad" to hash`)

	_, _, err = frame.SelfDigestAndInputRefs()
	require.ErrorContains(t, err, `result call frame "broken" args: failed to write argument "bad" to hash`)
}

func TestResultCallForkClonesTopLevelMutableState(t *testing.T) {
	t.Parallel()

	sharedArg := &ResultCallArg{
		Name:  "x",
		Value: &ResultCallLiteral{Kind: ResultCallLiteralKindString, StringValue: "orig"},
	}
	frame := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType(Int(0).Type()),
		Field: "forked",
		ExtraDigests: []call.ExtraDigest{{
			Label:  call.ExtraDigestLabelContent,
			Digest: digest.FromString("orig-digest"),
		}},
		Args: []*ResultCallArg{sharedArg},
	}

	forked := frame.fork()
	require.NotSame(t, frame, forked)
	require.Same(t, frame.Args[0], forked.Args[0])

	forked.Args = append(forked.Args, &ResultCallArg{Name: "y", Value: &ResultCallLiteral{Kind: ResultCallLiteralKindString, StringValue: "new"}})
	forked.ExtraDigests[0].Digest = digest.FromString("forked-digest")

	require.Len(t, frame.Args, 1)
	require.Len(t, forked.Args, 2)
	require.Equal(t, digest.FromString("orig-digest"), frame.ExtraDigests[0].Digest)
	require.Equal(t, digest.FromString("forked-digest"), forked.ExtraDigests[0].Digest)
}

func TestCacheRecipeDigestForCallMemoizesOnOriginalFrame(t *testing.T) {
	t.Parallel()

	c := &cache{}
	frame := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType(Int(0).Type()),
		Field: "memoized",
	}

	_, err := c.RecipeDigestForCall(frame)
	require.NoError(t, err)
	require.NotEmpty(t, frame.recipeDigest)
}

func TestDetachedResultMetadataReuseAndCallMutationFork(t *testing.T) {
	t.Parallel()

	base := cacheTestDetachedResult(cacheTestIntCall("detached"), NewInt(1))

	withPostCall := base.ResultWithPostCall(func(context.Context) error { return nil })
	require.Same(t, base.shared.resultCall, withPostCall.shared.resultCall)

	withContent := base.WithContentDigest(digest.FromString("detached-content"))
	require.NotSame(t, base.shared.resultCall, withContent.shared.resultCall)
	require.Empty(t, base.shared.resultCall.ContentDigest())
	require.Equal(t, digest.FromString("detached-content"), withContent.shared.resultCall.ContentDigest())
}

func TestResultCallRefReceiverUsesSharedFastPath(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	require.NoError(t, err)
	c := cacheIface.(*cache)

	reqCall := cacheTestIntCall("shared-fast-path")
	res, err := c.GetOrInitCall(ctx, noopTypeResolver{}, &CallRequest{ResultCall: reqCall}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(reqCall, 7), nil
	})
	require.NoError(t, err)

	shared := res.cacheSharedResult()
	require.NotNil(t, shared)
	require.NotZero(t, shared.id)

	frame := &ResultCall{
		Kind:     ResultCallKindField,
		Type:     NewResultCallType(Int(0).Type()),
		Field:    "child",
		Receiver: &ResultCallRef{ResultID: uint64(shared.id), shared: shared},
	}
	receiver, err := frame.ReceiverCall()
	require.NoError(t, err)
	require.NotNil(t, receiver)
	require.Equal(t, reqCall.Field, receiver.Field)

	require.NoError(t, res.Release(ctx))
}

func TestResultCallRefContentPreferredDigestUsesLatestSharedFrame(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	require.NoError(t, err)
	c := cacheIface.(*cache)

	reqCall := cacheTestIntCall("shared-content-digest")
	res, err := c.GetOrInitCall(ctx, noopTypeResolver{}, &CallRequest{ResultCall: reqCall}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(reqCall, 13), nil
	})
	require.NoError(t, err)

	shared := res.cacheSharedResult()
	require.NotNil(t, shared)
	require.NotZero(t, shared.id)

	contentDigest := digest.FromString("shared-fast-path-content")
	require.NoError(t, c.TeachContentDigest(ctx, res, contentDigest))

	ref := &ResultCallRef{ResultID: uint64(shared.id), shared: shared}
	got, err := contentPreferredDigestForResultCallRef(c, ref, map[sharedResultID]struct{}{})
	require.NoError(t, err)
	require.Equal(t, contentDigest, got)

	require.NoError(t, res.Release(ctx))
}

func TestResultCallRefRecipeIDUsesLatestSharedFrame(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	require.NoError(t, err)
	c := cacheIface.(*cache)

	reqCall := cacheTestIntCall("shared-recipe-id")
	res, err := c.GetOrInitCall(ctx, noopTypeResolver{}, &CallRequest{ResultCall: reqCall}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(reqCall, 19), nil
	})
	require.NoError(t, err)

	shared := res.cacheSharedResult()
	require.NotNil(t, shared)
	require.NotZero(t, shared.id)

	contentDigest := digest.FromString("shared-fast-path-recipe-id")
	require.NoError(t, c.TeachContentDigest(ctx, res, contentDigest))

	ref := &ResultCallRef{ResultID: uint64(shared.id), shared: shared}
	caller := &ResultCall{cache: c}
	id, err := caller.resolveRefRecipeID(ref, map[sharedResultID]struct{}{})
	require.NoError(t, err)
	require.Equal(t, contentDigest, id.ContentDigest())

	require.NoError(t, res.Release(ctx))
}

func TestResultCallRefSharedFastPathDoesNotSurviveRemoval(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	require.NoError(t, err)
	c := cacheIface.(*cache)

	reqCall := cacheTestIntCall("shared-fast-path-removal")
	res, err := c.GetOrInitCall(ctx, noopTypeResolver{}, &CallRequest{ResultCall: reqCall}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(reqCall, 23), nil
	})
	require.NoError(t, err)

	shared := res.cacheSharedResult()
	require.NotNil(t, shared)
	require.NotZero(t, shared.id)

	ref := &ResultCallRef{ResultID: uint64(shared.id), shared: shared}
	require.NoError(t, res.Release(ctx))
	require.Nil(t, shared.loadResultCall())

	frame := &ResultCall{
		Kind:     ResultCallKindField,
		Type:     NewResultCallType(Int(0).Type()),
		Field:    "child",
		Receiver: ref,
		cache:    c,
	}
	_, err = frame.ReceiverCall()
	require.ErrorContains(t, err, "missing result call frame")
}

func TestResultCallRefResultIDFallbackStillWorks(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	require.NoError(t, err)
	c := cacheIface.(*cache)

	reqCall := cacheTestIntCall("shared-fallback")
	res, err := c.GetOrInitCall(ctx, noopTypeResolver{}, &CallRequest{ResultCall: reqCall}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(reqCall, 29), nil
	})
	require.NoError(t, err)

	shared := res.cacheSharedResult()
	require.NotNil(t, shared)
	require.NotZero(t, shared.id)

	frame := &ResultCall{
		Kind:     ResultCallKindField,
		Type:     NewResultCallType(Int(0).Type()),
		Field:    "child",
		Receiver: &ResultCallRef{ResultID: uint64(shared.id)},
		cache:    c,
	}
	receiver, err := frame.ReceiverCall()
	require.NoError(t, err)
	require.NotNil(t, receiver)
	require.Equal(t, reqCall.Field, receiver.Field)

	require.NoError(t, res.Release(ctx))
}
