package dagql

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
)

// A call that resolves to nothing (a nullable field answered with null, a
// module function returning Void) is published as a null row whose frame
// still names its receiver, its module and its arguments. Those rows are
// the null row's dependencies like any other result's: they keep the
// referenced rows alive while the null row is retained.
func TestNullResultRecordsFrameDependencies(t *testing.T) {
	t.Parallel()
	ctx := cacheTestContext(t.Context())
	cache, err := NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.CloseDiscardingPersistence()) })
	const session = "null-dependencies"

	publish := func(field string, v int) uint64 {
		t.Helper()
		call := cacheTestIntCall(field)
		res, err := cache.GetOrInitCall(ctx, session, noopTypeResolver{}, &CallRequest{ResultCall: call, IsPersistable: true}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(call, v), nil
		})
		require.NoError(t, err)
		shared := res.cacheSharedResult()
		require.NotNil(t, shared)
		return uint64(shared.id)
	}
	receiverID := publish("receiver-int", 7)
	moduleID := publish("module-int", 9)
	argumentID := publish("argument-int", 11)

	frame := &ResultCall{
		Kind:     ResultCallKindField,
		Field:    "nothing",
		Type:     NewResultCallType(&ast.Type{NamedType: "Int"}),
		Receiver: &ResultCallRef{ResultID: receiverID},
		Module:   &ResultCallModule{Name: "demo", ResultRef: &ResultCallRef{ResultID: moduleID}},
		Args:     []*ResultCallArg{{Name: "in", Value: &ResultCallLiteral{Kind: ResultCallLiteralKindResultRef, ResultRef: &ResultCallRef{ResultID: argumentID}}}},
	}
	res, err := cache.GetOrInitCall(ctx, session, noopTypeResolver{}, &CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (AnyResult, error) {
		return nil, nil
	})
	require.NoError(t, err)
	require.Nil(t, res.Unwrap(), "a null answer carries no value")
	null := res.cacheSharedResult()
	require.NotNil(t, null, "the null answer has a row")

	deps := map[uint64]bool{}
	cache.egraphMu.RLock()
	row := cache.resultsByID[null.id]
	if row != nil {
		for id := range row.deps {
			deps[uint64(id)] = true
		}
	}
	cache.egraphMu.RUnlock()
	require.NotNil(t, row)
	for name, id := range map[string]uint64{"receiver": receiverID, "module": moduleID, "argument": argumentID} {
		require.True(t, deps[id], "the null row depends on the %s its frame names", name)
	}
}

// Two sessions answer one call with nothing at once, a call whose frame
// names its inputs by result ID, as a module function returning Void does.
// The first to finish publishes the null row, whose frame is a copy of its
// request frame; the other adopts that row. Indexing the adopted row's frame
// reads its recipe digest under the graph lock, so the digest must be
// derived before the lock is taken: deriving it under the lock resolves the
// inputs through the cache's graph lock again, and the publication hangs.
func TestNullResultAdoptedByAConcurrentCall(t *testing.T) {
	t.Parallel()
	ctx := cacheTestContext(t.Context())
	cache, err := NewCache(ctx, "", nil, nil)
	require.NoError(t, err)

	publish := func(field string, v int) uint64 {
		t.Helper()
		call := cacheTestIntCall(field)
		res, err := cache.GetOrInitCall(ctx, "inputs", noopTypeResolver{}, &CallRequest{ResultCall: call, IsPersistable: true}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(call, v), nil
		})
		require.NoError(t, err)
		return uint64(res.cacheSharedResult().id)
	}
	receiverID := publish("adopted-receiver", 7)
	moduleID := publish("adopted-module", 9)
	// Each caller builds its own request frame, as each session does.
	request := func() *CallRequest {
		return &CallRequest{IsPersistable: true, ResultCall: &ResultCall{
			Kind:     ResultCallKindField,
			Field:    "nothing",
			Type:     NewResultCallType(&ast.Type{NamedType: "Int"}),
			Receiver: &ResultCallRef{ResultID: receiverID},
			Module:   &ResultCallModule{Name: "demo", ResultRef: &ResultCallRef{ResultID: moduleID}},
		}}
	}

	type outcome struct {
		res AnyResult
		err error
	}
	aStarted, aFinish := make(chan struct{}), make(chan struct{})
	aDone := make(chan outcome, 1)
	go func() {
		res, err := cache.GetOrInitCall(ctx, "session-a", noopTypeResolver{}, request(), func(context.Context) (AnyResult, error) {
			close(aStarted)
			<-aFinish
			return nil, nil
		})
		aDone <- outcome{res, err}
	}()
	<-aStarted
	b, err := cache.GetOrInitCall(ctx, "session-b", noopTypeResolver{}, request(), func(context.Context) (AnyResult, error) {
		return nil, nil
	})
	require.NoError(t, err)
	close(aFinish)

	var a outcome
	select {
	case a = <-aDone:
	case <-time.After(30 * time.Second):
		// The hung publication holds the graph lock, so nothing that takes
		// it, the cache's close included, can run after this.
		t.Fatal("the publication that adopts the null row hangs")
	}
	require.NoError(t, a.err)
	require.Equal(t, b.cacheSharedResult().id, a.res.cacheSharedResult().id, "the later publication adopts the null row")
	require.NoError(t, cache.ReleaseSession(ctx, "session-a"))
	require.NoError(t, cache.ReleaseSession(ctx, "session-b"))
	require.NoError(t, cache.ReleaseSession(ctx, "inputs"))
	require.NoError(t, cache.CloseDiscardingPersistence())
}
