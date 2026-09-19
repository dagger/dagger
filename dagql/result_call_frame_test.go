package dagql

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/call"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

func TestSchemaRecipeLookupCanceled(t *testing.T) {
	cache, err := NewCache(t.Context(), "", nil, nil)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, hit, err := cache.lookupCacheForSchemaRecipe(ctx, "test-session", noopTypeResolver{}, digest.FromString("canceled-schema"))
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, hit)
}

func TestSchemaRecipeHitPreservesRequestPolicy(t *testing.T) {
	ctx := cacheTestContext(t.Context())
	cache, err := NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	ctx = ContextWithCache(ctx, cache)
	frame := cacheTestIntCall("schema-policy")
	res, err := cache.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: frame}, ValueFunc(cacheTestIntResult(frame, 1)))
	require.NoError(t, err)
	require.Zero(t, cache.EntryStats().RetainedCalls)
	t.Cleanup(func() { _ = cache.ReleaseSession(context.Background(), "test-session") })

	evidence := &CacheDecision{}
	before := time.Now().Unix()
	hit, err := cache.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{
		ResultCall: frame, recipeOnly: true, TTL: 60, IsPersistable: true, CacheEvidence: evidence,
	}, ValueFunc(cacheTestIntResult(frame, 2)))
	require.NoError(t, err)
	require.True(t, hit.HitCache())
	require.Equal(t, res.cacheSharedResult().id, hit.cacheSharedResult().id)
	require.Equal(t, CacheHitRouteRecipe, evidence.HitRoute)
	require.Equal(t, CacheOutcomeHit, evidence.Outcome)
	cache.egraphMu.RLock()
	expiry := hit.cacheSharedResult().expiresAtUnix
	edge, persisted := cache.persistedEdgesByResult[hit.cacheSharedResult().id]
	cache.egraphMu.RUnlock()
	require.True(t, persisted)
	require.GreaterOrEqual(t, expiry, before+60)
	require.LessOrEqual(t, expiry, time.Now().Unix()+60)
	require.Equal(t, expiry, edge.expiresAtUnix)

	// A later, longer policy must not extend the conservative expiry.
	_, err = cache.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{
		ResultCall: frame, recipeOnly: true, TTL: 120, IsPersistable: true,
	}, nil)
	require.NoError(t, err)
	cache.egraphMu.RLock()
	laterExpiry := hit.cacheSharedResult().expiresAtUnix
	laterEdge := cache.persistedEdgesByResult[hit.cacheSharedResult().id]
	cache.egraphMu.RUnlock()
	require.Equal(t, expiry, laterExpiry)
	require.Equal(t, expiry, laterEdge.expiresAtUnix)
	require.NoError(t, cache.ReleaseSession(ctx, "test-session"))
	require.Equal(t, 1, cache.EntryStats().RetainedCalls)
}

func TestSchemaRecipeLoadMemoSeparatesContentHits(t *testing.T) {
	ctx := cacheTestContext(t.Context())
	cache, err := NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	ctx = ContextWithCache(ctx, cache)
	srv := cacheTestServer(t)
	content := digest.FromString("schema-equivalent-implementation")
	calls := 0
	Fields[cacheTestQuery]{
		Func("fullSchema", func(context.Context, cacheTestQuery, struct{}) (Int, error) {
			calls++
			return Int(2), nil
		}),
	}.Install(srv)
	bootstrap, err := NewResultForCall(Int(1), cacheTestIntCall("bootstrap-schema"))
	require.NoError(t, err)
	bootstrap, err = bootstrap.WithContentDigest(ctx, content)
	require.NoError(t, err)
	_, err = cache.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: cacheTestIntCall("bootstrap-schema")}, ValueFunc(bootstrap))
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.ReleaseSession(context.Background(), "test-session") })

	state := &recipeLoadState{ctx: ctx, srv: srv, cache: cache, sessionID: "test-session", loads: make(map[recipeLoadKey]*recipeLoadFuture)}
	id := call.New().Append(Int(0).Type(), "fullSchema", call.WithContentDigest(content))
	normal, err := state.load(id, false)
	require.NoError(t, err)
	require.Equal(t, Int(1), normal.Unwrap())
	strict, err := state.load(id, true)
	require.NoError(t, err)
	require.Equal(t, Int(2), strict.Unwrap())
	require.Equal(t, 1, calls)
	normal, err = state.load(id, false)
	require.NoError(t, err)
	require.Equal(t, Int(1), normal.Unwrap(), "schema loads must not replace normal content-hit futures")
}

func TestResultCallRefFromRecipeID(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	for _, id := range []*call.ID{nil, call.New(), call.NewEngineResultID(1, call.NewType(Int(0).Type()))} {
		_, err := resultCallRefFromRecipeID(ctx, id)
		require.ErrorContains(t, err, "typed recipe-form ID")
	}

	// Preserve shared inputs as a DAG, not one expanded tree per reference.
	shared := call.New().Append(Int(0).Type(), "shared")
	extra := call.ExtraDigest{Digest: digest.FromString("module-content")}
	id := shared.Append(Int(0).Type(), "module",
		call.WithArgs(call.NewArgument("input", call.NewLiteralID(shared), false)),
		call.WithExtraDigest(extra),
	)
	ref, err := resultCallRefFromRecipeID(ctx, id)
	require.NoError(t, err)
	require.Zero(t, ref.ResultID)
	require.NotNil(t, ref.Call)
	require.Nil(t, ref.shared)
	require.Same(t, ref.Call.Receiver.Call, ref.Call.Args[0].Value.ResultRef.Call)
	require.Equal(t, []call.ExtraDigest{extra}, ref.Call.ExtraDigests)

	// Reconstruction must not need a cache: schema provenance has no live IDs.
	rebuilt, err := ref.Call.recipeID(ctx, nil)
	require.NoError(t, err)
	require.Equal(t, id.Digest(), rebuilt.Digest())
	require.Equal(t, id.ExtraDigests(), rebuilt.ExtraDigests())
}

func TestResultCallRefForSchemaSnapshot(t *testing.T) {
	t.Parallel()
	ctx := cacheTestContext(t.Context())
	cache, err := NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	ctx = ContextWithCache(ctx, cache)
	res, err := NewResultForCall(Int(1), cacheTestIntCall("module-a"))
	require.NoError(t, err)

	// A failed capture must not poison the memo for a later valid context.
	_, err = ResultCallRefForSchema(context.Background(), res)
	require.Error(t, err)

	const fields = 16
	refs := make([]*ResultCallRef, fields)
	var wg sync.WaitGroup
	for i := range fields {
		wg.Go(func() {
			ref, err := ResultCallRefForSchema(ctx, res)
			if err != nil {
				t.Errorf("capture schema provenance: %v", err)
				return
			}
			refs[i] = ref
		})
	}
	wg.Wait()
	for _, ref := range refs {
		require.Same(t, refs[0], ref, "fields must share one immutable recipe DAG")
		require.Zero(t, ref.ResultID)
		require.Nil(t, ref.shared)
		require.NotNil(t, ref.recipeID)
		id, err := ref.RecipeID(ctx)
		require.NoError(t, err)
		require.Same(t, ref.recipeID, id, "schema recipe access must not copy the DAG")
	}
	original := refs[0]
	cloned := original.cloneWith(resultCallCloneMemo{})
	require.Nil(t, cloned.recipeID, "cloned calls may be edited")

	// Captures follow newly published provenance, while already-installed
	// fields keep the immutable implementation they were defined against.
	res.shared.storeResultCall(cacheTestIntCall("module-b"))
	updated, err := ResultCallRefForSchema(ctx, res)
	require.NoError(t, err)
	require.NotSame(t, original, updated)
	require.Equal(t, "module-a", original.Call.Field)
	require.Equal(t, "module-b", updated.Call.Field)
	require.NotEqual(t, original.recipeID.Digest(), updated.recipeID.Digest())

	res.shared.storeResultCall(nil)
	require.Nil(t, res.shared.schemaRef, "release must drop the snapshot memo")
	_, err = ResultCallRefForSchema(ctx, res)
	require.ErrorContains(t, err, "missing result call")
	rebuilt, err := original.Call.recipeID(ctx, nil)
	require.NoError(t, err, "installed provenance survives collection")
	require.Equal(t, original.recipeID.Digest(), rebuilt.Digest())
}

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

func TestResultCallBytesPersistAndRebuildRaw(t *testing.T) {
	t.Parallel()

	contents := []byte{0x00, 0xff, 0xfe, 'b', 'l', 'o', 'b'}
	frame := cacheTestIntCall("blob")
	frame.Args = []*ResultCallArg{{
		Name: "contents",
		Value: &ResultCallLiteral{
			Kind:       ResultCallLiteralKindBytes,
			BytesValue: bytes.Clone(contents),
		},
	}}

	payload, err := json.Marshal(frame)
	require.NoError(t, err)
	require.Contains(t, string(payload), `"kind":"bytes"`)

	var restored ResultCall
	require.NoError(t, json.Unmarshal(payload, &restored))
	require.Equal(t, contents, restored.Args[0].Value.BytesValue)

	pb, err := restored.callPB(nil)
	require.NoError(t, err)
	require.Equal(t, contents, pb.GetArgs()[0].GetValue().GetBytes())

	rebuilt, err := restored.recipeID(t.Context(), nil)
	require.NoError(t, err)
	lit, ok := rebuilt.Arg("contents").Value().(*call.LiteralBytes)
	require.True(t, ok)
	require.Equal(t, contents, lit.Value())
	require.Equal(t, pb.GetDigest(), rebuilt.Digest().String())
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
	id, err := caller.resolveRefRecipeID(ctx, c, ref, map[sharedResultID]struct{}{}, newRecipeIDMemo())
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

// TestResultCallArgPBRedactsSensitive ensures the telemetry/call protobuf
// encoder redacts args flagged sensitive (e.g. setSecret(plaintext:)), mirroring
// redactedArgForID on the call.ID path. Regression test for sensitive secret
// plaintext leaking verbatim into CLI --progress output.
func TestResultCallArgPBRedactsSensitive(t *testing.T) {
	t.Parallel()

	c := &Cache{}

	sensitive := &ResultCallArg{
		Name:        "plaintext",
		IsSensitive: true,
		Value:       &ResultCallLiteral{Kind: ResultCallLiteralKindString, StringValue: "super-secret"},
	}
	pbArg, err := resultCallArgPB(c, sensitive)
	require.NoError(t, err)
	require.Equal(t, "plaintext", pbArg.Name)
	require.Equal(t, "***", pbArg.Value.GetString_())
	require.NotContains(t, pbArg.Value.GetString_(), "super-secret")

	public := &ResultCallArg{
		Name:  "name",
		Value: &ResultCallLiteral{Kind: ResultCallLiteralKindString, StringValue: "toast"},
	}
	pbArg, err = resultCallArgPB(c, public)
	require.NoError(t, err)
	require.Equal(t, "toast", pbArg.Value.GetString_())

	pbArg, err = resultCallArgPB(c, nil)
	require.NoError(t, err)
	require.Nil(t, pbArg)
}

// TestResultCallPBRedactsSensitiveArg exercises the full ResultCall.callPB
// encoder (the path core/telemetry.go uses for the DagCall span attr) with a
// setSecret-shaped frame: the non-sensitive arg passes through while the
// sensitive plaintext is redacted.
func TestResultCallPBRedactsSensitiveArg(t *testing.T) {
	t.Parallel()

	c := &Cache{}
	frame := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType(String("").Type()),
		Field: "setSecret",
		Args: []*ResultCallArg{
			{
				Name:  "name",
				Value: &ResultCallLiteral{Kind: ResultCallLiteralKindString, StringValue: "toast"},
			},
			{
				Name:        "plaintext",
				IsSensitive: true,
				Value:       &ResultCallLiteral{Kind: ResultCallLiteralKindString, StringValue: "super-secret"},
			},
		},
	}

	pb, err := frame.callPB(c)
	require.NoError(t, err)
	require.Len(t, pb.Args, 2)

	byName := make(map[string]string, len(pb.Args))
	for _, a := range pb.Args {
		byName[a.Name] = a.Value.GetString_()
	}
	require.Equal(t, "toast", byName["name"])
	require.Equal(t, "***", byName["plaintext"])
}
