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

	_, err := frame.deriveRecipeDigest(nil)
	require.ErrorContains(t, err, `args: failed to write argument "bad" to hash`)

	_, err = frame.deriveContentPreferredDigest(nil)
	require.ErrorContains(t, err, `args: failed to write argument "bad" to hash`)

	_, _, err = frame.selfDigestAndInputRefs(nil)
	require.ErrorContains(t, err, `result call frame "broken" args: failed to write argument "bad" to hash`)
}

func TestResultCallSelfDigestAndInputRefsPreserveInputKinds(t *testing.T) {
	t.Parallel()

	receiver := cacheTestIntCall("receiver")
	resultInput := cacheTestIntCall("result-input")
	moduleInput := cacheTestIntCall("module")
	digested := digest.FromString("digested-input")

	frame := &ResultCall{
		Kind:     ResultCallKindField,
		Type:     NewResultCallType(Int(0).Type()),
		Field:    "field",
		Receiver: &ResultCallRef{Call: receiver},
		Module: &ResultCallModule{
			ResultRef: &ResultCallRef{Call: moduleInput},
			Name:      "mod",
			Ref:       "ref",
			Pin:       "pin",
		},
		Args: []*ResultCallArg{
			{
				Name:  "child",
				Value: &ResultCallLiteral{Kind: ResultCallLiteralKindResultRef, ResultRef: &ResultCallRef{Call: resultInput}},
			},
			{
				Name: "opaque",
				Value: &ResultCallLiteral{
					Kind:                 ResultCallLiteralKindDigestedString,
					DigestedStringValue:  "payload",
					DigestedStringDigest: digested,
				},
			},
		},
	}

	_, refs, err := frame.selfDigestAndInputRefs(nil)
	require.NoError(t, err)
	require.Len(t, refs, 4)
	require.Same(t, receiver, refs[0].Result.Call)
	require.Same(t, resultInput, refs[1].Result.Call)
	require.Equal(t, digested, refs[2].Digest)
	require.Nil(t, refs[2].Result)
	require.Same(t, moduleInput, refs[3].Result.Call)
}

func TestResultCallModuleMetadataDoesNotAffectStructuralIdentity(t *testing.T) {
	t.Parallel()

	module := cacheTestIntCall("module")
	frameA := cacheTestIntCall("field")
	frameA.Module = &ResultCallModule{
		ResultRef: &ResultCallRef{Call: module},
		Name:      "mod-a",
		Ref:       "ref-a",
		Pin:       "pin-a",
	}
	frameB := cacheTestIntCall("field")
	frameB.Module = &ResultCallModule{
		ResultRef: &ResultCallRef{Call: module},
		Name:      "mod-b",
		Ref:       "ref-b",
		Pin:       "pin-b",
	}

	digestA, err := frameA.deriveRecipeDigest(nil)
	require.NoError(t, err)
	digestB, err := frameB.deriveRecipeDigest(nil)
	require.NoError(t, err)
	require.Equal(t, digestA, digestB)

	selfA, refsA, err := frameA.selfDigestAndInputRefs(nil)
	require.NoError(t, err)
	selfB, refsB, err := frameB.selfDigestAndInputRefs(nil)
	require.NoError(t, err)
	require.Equal(t, selfA, selfB)
	require.Len(t, refsA, 1)
	require.Len(t, refsB, 1)

	inputA, err := refsA[0].inputDigest(nil)
	require.NoError(t, err)
	inputB, err := refsB[0].inputDigest(nil)
	require.NoError(t, err)
	require.Equal(t, inputA, inputB)
}

func TestResultCallModuleIdentityIsStructuralInputOnly(t *testing.T) {
	t.Parallel()

	moduleA := cacheTestIntCall("module-a")
	moduleB := cacheTestIntCall("module-b")
	idNoModule := cacheTestIntCall("field")
	idWithModuleA := cacheTestIntCall("field")
	idWithModuleA.Module = &ResultCallModule{
		ResultRef: &ResultCallRef{Call: moduleA},
		Name:      "mod",
		Ref:       "ref",
		Pin:       "pin",
	}
	idWithModuleB := cacheTestIntCall("field")
	idWithModuleB.Module = &ResultCallModule{
		ResultRef: &ResultCallRef{Call: moduleB},
		Name:      "mod",
		Ref:       "ref",
		Pin:       "pin",
	}

	digestNoModule, err := idNoModule.deriveRecipeDigest(nil)
	require.NoError(t, err)
	digestModuleA, err := idWithModuleA.deriveRecipeDigest(nil)
	require.NoError(t, err)
	digestModuleB, err := idWithModuleB.deriveRecipeDigest(nil)
	require.NoError(t, err)
	require.NotEqual(t, digestNoModule, digestModuleA)
	require.NotEqual(t, digestModuleA, digestModuleB)

	selfNoModule, refsNoModule, err := idNoModule.selfDigestAndInputRefs(nil)
	require.NoError(t, err)
	selfModuleA, refsModuleA, err := idWithModuleA.selfDigestAndInputRefs(nil)
	require.NoError(t, err)
	selfModuleB, refsModuleB, err := idWithModuleB.selfDigestAndInputRefs(nil)
	require.NoError(t, err)
	require.Equal(t, selfNoModule, selfModuleA)
	require.Equal(t, selfNoModule, selfModuleB)
	require.Len(t, refsModuleA, len(refsNoModule)+1)
	require.Len(t, refsModuleB, len(refsModuleA))

	moduleADigest, err := moduleA.deriveRecipeDigest(nil)
	require.NoError(t, err)
	moduleBDigest, err := moduleB.deriveRecipeDigest(nil)
	require.NoError(t, err)
	inputA, err := refsModuleA[len(refsModuleA)-1].inputDigest(nil)
	require.NoError(t, err)
	inputB, err := refsModuleB[len(refsModuleB)-1].inputDigest(nil)
	require.NoError(t, err)
	require.Equal(t, moduleADigest, inputA)
	require.Equal(t, moduleBDigest, inputB)
	require.NotEqual(t, inputA, inputB)
}

func TestResultCallSelfDigestUsesRecipeDigestsForResultInputs(t *testing.T) {
	t.Parallel()

	recvA := cacheTestIntCall("receiver", call.ExtraDigest{
		Digest: digest.FromString("aux-a"),
		Label:  "aux",
	})
	recvB := cacheTestIntCall("receiver", call.ExtraDigest{
		Digest: digest.FromString("aux-b"),
		Label:  "aux",
	})
	childA := cacheTestIntCall("child")
	childA.Receiver = &ResultCallRef{Call: recvA}
	childB := cacheTestIntCall("child")
	childB.Receiver = &ResultCallRef{Call: recvB}

	selfA, refsA, err := childA.selfDigestAndInputRefs(nil)
	require.NoError(t, err)
	selfB, refsB, err := childB.selfDigestAndInputRefs(nil)
	require.NoError(t, err)
	require.Equal(t, selfA, selfB)
	require.Len(t, refsA, 1)
	require.Len(t, refsB, 1)

	inputA, err := refsA[0].inputDigest(nil)
	require.NoError(t, err)
	inputB, err := refsB[0].inputDigest(nil)
	require.NoError(t, err)
	require.Equal(t, inputA, inputB)
}

func TestResultCallDigestedStringUsesAttachedDigestForStructuralIdentity(t *testing.T) {
	t.Parallel()

	execMDDigest := digest.FromString("execmd-identity")
	frameA := cacheTestIntCall("withExec")
	frameA.Args = []*ResultCallArg{{
		Name: "execMD",
		Value: &ResultCallLiteral{
			Kind:                 ResultCallLiteralKindDigestedString,
			DigestedStringValue:  `{"clientID":"a","execID":"1"}`,
			DigestedStringDigest: execMDDigest,
		},
	}}
	frameB := cacheTestIntCall("withExec")
	frameB.Args = []*ResultCallArg{{
		Name: "execMD",
		Value: &ResultCallLiteral{
			Kind:                 ResultCallLiteralKindDigestedString,
			DigestedStringValue:  `{"clientID":"b","execID":"2"}`,
			DigestedStringDigest: execMDDigest,
		},
	}}

	selfA, refsA, err := frameA.selfDigestAndInputRefs(nil)
	require.NoError(t, err)
	selfB, refsB, err := frameB.selfDigestAndInputRefs(nil)
	require.NoError(t, err)
	require.Equal(t, selfA, selfB)
	require.Len(t, refsA, 1)
	require.Len(t, refsB, 1)
	require.Equal(t, execMDDigest, refsA[0].Digest)
	require.Equal(t, execMDDigest, refsB[0].Digest)
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

	c := &Cache{}
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

	withContent, err := base.WithContentDigest(t.Context(), digest.FromString("detached-content"))
	require.NoError(t, err)
	require.NotSame(t, base.shared.resultCall, withContent.shared.resultCall)
	require.Empty(t, base.shared.resultCall.ContentDigest())
	require.Equal(t, digest.FromString("detached-content"), withContent.shared.resultCall.ContentDigest())
}

func TestResultCallRefReceiverUsesSharedFastPath(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	ctx = ContextWithCache(ctx, cacheIface)
	c := cacheIface

	reqCall := cacheTestIntCall("shared-fast-path")
	res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: reqCall}, func(context.Context) (AnyResult, error) {
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
	receiver, err := frame.ReceiverCall(ctx)
	require.NoError(t, err)
	require.NotNil(t, receiver)
	require.Equal(t, reqCall.Field, receiver.Field)

	cacheTestReleaseSession(t, cacheIface, ctx)
}

func TestResultCallRefContentPreferredDigestUsesLatestSharedFrame(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	ctx = ContextWithCache(ctx, cacheIface)
	c := cacheIface

	reqCall := cacheTestIntCall("shared-content-digest")
	res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: reqCall}, func(context.Context) (AnyResult, error) {
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

	cacheTestReleaseSession(t, cacheIface, ctx)
}

func TestResultCallRefRecipeIDUsesLatestSharedFrame(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	ctx = ContextWithCache(ctx, cacheIface)
	c := cacheIface

	reqCall := cacheTestIntCall("shared-recipe-id")
	res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: reqCall}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(reqCall, 19), nil
	})
	require.NoError(t, err)

	shared := res.cacheSharedResult()
	require.NotNil(t, shared)
	require.NotZero(t, shared.id)

	contentDigest := digest.FromString("shared-fast-path-recipe-id")
	require.NoError(t, c.TeachContentDigest(ctx, res, contentDigest))

	ref := &ResultCallRef{ResultID: uint64(shared.id), shared: shared}
	caller := &ResultCall{}
	id, err := caller.resolveRefRecipeID(ctx, c, ref, map[sharedResultID]struct{}{})
	require.NoError(t, err)
	require.Equal(t, contentDigest, id.ContentDigest())

	cacheTestReleaseSession(t, cacheIface, ctx)
}

func TestResultCallRefSharedFastPathDoesNotSurviveRemoval(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	ctx = ContextWithCache(ctx, cacheIface)
	c := cacheIface

	reqCall := cacheTestIntCall("shared-fast-path-removal")
	res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: reqCall}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(reqCall, 23), nil
	})
	require.NoError(t, err)

	shared := res.cacheSharedResult()
	require.NotNil(t, shared)
	require.NotZero(t, shared.id)

	ref := &ResultCallRef{ResultID: uint64(shared.id), shared: shared}
	cacheTestReleaseSession(t, cacheIface, ctx)
	require.Nil(t, shared.loadResultCall())

	frame := &ResultCall{
		Kind:     ResultCallKindField,
		Type:     NewResultCallType(Int(0).Type()),
		Field:    "child",
		Receiver: ref,
	}
	_, err = frame.ReceiverCall(ctx)
	require.ErrorContains(t, err, "missing result call frame")
}

func TestResultCallRefResultIDFallbackStillWorks(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	ctx = ContextWithCache(ctx, cacheIface)
	c := cacheIface

	reqCall := cacheTestIntCall("shared-fallback")
	res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: reqCall}, func(context.Context) (AnyResult, error) {
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
	}
	receiver, err := frame.ReceiverCall(ctx)
	require.NoError(t, err)
	require.NotNil(t, receiver)
	require.Equal(t, reqCall.Field, receiver.Field)

	cacheTestReleaseSession(t, cacheIface, ctx)
}
