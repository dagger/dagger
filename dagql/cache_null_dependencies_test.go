package dagql

import (
	"context"
	"testing"

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
