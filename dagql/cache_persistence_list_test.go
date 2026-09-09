package dagql

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func persistedListTestCache(t *testing.T, path string) (context.Context, *Cache, *Server) {
	t.Helper()
	ctx := cacheTestContext(t.Context())
	cache, err := NewCache(ctx, path, nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.CloseDiscardingPersistence()) })
	srv := newDagqlServerForTest(t, &persistCodecRoot{})
	return srvToContext(ContextWithCache(ctx, cache), srv), cache, srv
}

func persistedListTestResult(t *testing.T, ctx context.Context, cache *Cache, srv *Server, field string, value Typed) AnyResult {
	t.Helper()
	frame := &ResultCall{Kind: ResultCallKindField, Field: field, Type: NewResultCallType(value.Type())}
	res, err := cache.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (AnyResult, error) {
		return NewResultForCall(value, frame)
	})
	require.NoError(t, err)
	return res
}

func persistedListTestEncoding(t *testing.T, ctx context.Context, cache *Cache, res AnyResult) []byte {
	t.Helper()
	encoding, err := DefaultPersistedSelfCodec.EncodeResult(ctx, cache, res)
	require.NoError(t, err)
	data, err := json.Marshal(encoding.Envelope)
	require.NoError(t, err)
	return data
}

func TestPersistedScalarListSurvivesRepeatedRestore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	ctx, cache, srv := persistedListTestCache(t, path)
	child := persistedListTestResult(t, ctx, cache, srv, "scalar", String("saved"))
	childID, err := cache.PersistedResultID(child)
	require.NoError(t, err)
	list := persistedListTestResult(t, ctx, cache, srv, "list", DynamicResultArrayOutput{Elem: String(""), Values: []AnyResult{child}})
	listID, err := cache.PersistedResultID(list)
	require.NoError(t, err)
	original := persistedListTestEncoding(t, ctx, cache, list)
	for cycle := range 2 {
		require.NoError(t, cache.ReleaseSession(ctx, "test-session"))
		require.NoError(t, cache.Close(ctx))
		ctx, cache, srv = persistedListTestCache(t, path)
		state := cache.resultsByID[sharedResultID(listID)].loadPayloadState()
		require.False(t, state.hasValue, "referenced scalar lists defer at boot")
		require.NotNil(t, state.persistedEnvelope)
		list, err = cache.LoadResultByResultID(ctx, "test-session", srv, listID)
		require.NoError(t, err)
		restoredChild := list.Unwrap().(DynamicResultArrayOutput).Values[0]
		require.Equal(t, sharedResultID(childID), restoredChild.cacheSharedResult().id, "cycle %d preserves the child row", cycle)
		direct, err := cache.LoadResultByResultID(ctx, "", srv, childID)
		require.NoError(t, err)
		require.Same(t, direct.cacheSharedResult(), restoredChild.cacheSharedResult())
		require.Equal(t, Typed(String("saved")), restoredChild.Unwrap())
		require.Equal(t, original, persistedListTestEncoding(t, ctx, cache, list))
		cache.egraphMu.RLock()
		_, childSessionEdge := cache.sessionResultIDsBySession["test-session"][sharedResultID(childID)]
		cache.egraphMu.RUnlock()
		require.False(t, childSessionEdge, "decode and empty-session loads add no session edge")
	}
}

func TestPersistedNestedListSurvivesRepeatedRestore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	ctx, cache, srv := persistedListTestCache(t, path)
	child := persistedListTestResult(t, ctx, cache, srv, "nested-scalar", Int(7))
	inner := persistedListTestResult(t, ctx, cache, srv, "inner", DynamicResultArrayOutput{Elem: Int(0), Values: []AnyResult{child}})
	// This raw list keeps both its nested list and scalar values inline.
	raw := persistedListTestResult(t, ctx, cache, srv, "raw", Array[Array[Int]]{{8, 9}, {}})
	outer := persistedListTestResult(t, ctx, cache, srv, "outer", DynamicResultArrayOutput{Elem: inner.Unwrap(), Values: []AnyResult{inner, raw}})
	id, err := cache.PersistedResultID(outer)
	require.NoError(t, err)
	original := persistedListTestEncoding(t, ctx, cache, outer)
	for range 2 {
		require.NoError(t, cache.ReleaseSession(ctx, "test-session"))
		require.NoError(t, cache.Close(ctx))
		ctx, cache, srv = persistedListTestCache(t, path)
		outer, err = cache.LoadResultByResultID(ctx, "test-session", srv, id)
		require.NoError(t, err)
		require.Equal(t, original, persistedListTestEncoding(t, ctx, cache, outer))
		items := outer.Unwrap().(DynamicResultArrayOutput).Values
		value := items[0].Unwrap().(DynamicResultArrayOutput).Values[0]
		require.Equal(t, Typed(Int(7)), value.Unwrap())
		require.NotZero(t, value.cacheSharedResult().id)
		inline := items[1].Unwrap().(DynamicResultArrayOutput).Values[0]
		require.Zero(t, inline.cacheSharedResult().id)
		leaf := inline.Unwrap().(DynamicResultArrayOutput).Values[1]
		require.Zero(t, leaf.cacheSharedResult().id)
		require.Equal(t, Typed(Int(9)), leaf.Unwrap())
	}
}

func TestPersistedListDefersReferencedChildrenWithoutServer(t *testing.T) {
	ctx, _, _ := persistedListTestCache(t, "")
	frame := &ResultCall{Kind: ResultCallKindField, Field: "list", Type: NewResultCallType(Array[Int]{}.Type())}
	env := PersistedResultEnvelope{Kind: persistedResultKindList, ElemTypeName: "Int", Items: []PersistedResultEnvelope{
		{Kind: persistedResultKindScalar, TypeName: "Int", ResultID: 999, ScalarJSON: json.RawMessage(`7`)},
	}}
	for _, ctx := range []context.Context{t.Context(), ctx} {
		_, err := DefaultPersistedSelfCodec.DecodeResult(ctx, nil, 0, frame, env)
		require.ErrorContains(t, err, "referenced result requires a dagql server")
	}
}

func TestPersistedListPrepassUsesChildCall(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	ctx, cache, srv := persistedListTestCache(t, path)
	srv.InstallObject(NewClass(srv, ClassOpts[*persistCodecObj]{}))
	child := persistedListTestResult(t, ctx, cache, srv, "child-schema", &persistCodecObj{Name: "child"})
	list := persistedListTestResult(t, ctx, cache, srv, "parent-list", DynamicResultArrayOutput{Elem: &persistCodecObj{}, Values: []AnyResult{child}})
	id, err := cache.PersistedResultID(list)
	require.NoError(t, err)
	require.NoError(t, cache.ReleaseSession(ctx, "test-session"))
	require.NoError(t, cache.Close(ctx))
	ctx, cache, srv = persistedListTestCache(t, path)
	resolved := newDagqlServerForTest(t, &persistCodecRoot{})
	resolved.InstallObject(NewClass(resolved, ClassOpts[*persistCodecObj]{}))
	var frames []string
	srv.SetResultServerForCall(func(_ context.Context, frame *ResultCall) (*Server, error) {
		frames = append(frames, frame.Field)
		if frame.Field != "child-schema" {
			return nil, fmt.Errorf("schema reconstruction used %q instead of child call", frame.Field)
		}
		return resolved, nil
	})
	list, err = cache.LoadResultByResultID(ctx, "test-session", srv, id)
	require.NoError(t, err)
	require.NotEmpty(t, frames)
	for _, frame := range frames {
		require.Equal(t, "child-schema", frame)
	}
	value := list.Unwrap().(DynamicResultArrayOutput).Values[0]
	require.Equal(t, "child", value.Unwrap().(*persistCodecObj).Name)
}

func TestPersistedSelfCodecInlineListForms(t *testing.T) {
	ctx := setupPersistCodecTest(t)
	for _, value := range []Typed{
		Array[Int]{}, Array[String]{"one", "two"},
		DynamicResultArrayOutput{Elem: Int(0), Values: []AnyResult{nil}},
		Array[Array[Int]]{{1}, {}},
	} {
		t.Run(value.Type().String(), func(t *testing.T) {
			frame := &ResultCall{Kind: ResultCallKindField, Field: "inline", Type: NewResultCallType(value.Type())}
			res, err := NewResultForCall(value, frame)
			require.NoError(t, err)
			encoding, err := DefaultPersistedSelfCodec.EncodeResult(ctx, nil, res)
			require.NoError(t, err)
			decoded, err := DefaultPersistedSelfCodec.DecodeResult(ctx, nil, 0, frame, encoding.Envelope)
			require.NoError(t, err)
			encodedAgain, err := DefaultPersistedSelfCodec.EncodeResult(ctx, nil, decoded)
			require.NoError(t, err)
			require.Equal(t, encoding.Envelope, encodedAgain.Envelope)
		})
	}
}
