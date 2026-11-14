package dagql

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
	"time"

	engineslog "github.com/dagger/dagger/engine/slog"
	"github.com/stretchr/testify/require"
)

// Prune names the calls of the entries it removes, in its report and its
// log, as the usage listing names them before the prune: a removed entry's
// call with its receiver's type, also when the same pass removes the
// receiver. It works the labels out for those entries only.
//
// It replaces the default logger to read the log, so it runs alone.
func TestCachePruneNamesTheCallsItRemoves(t *testing.T) {
	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, c.CloseDiscardingPersistence()) })

	parentFrame := cacheTestIntCall("prune-label-parent")
	parent, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: parentFrame, IsPersistable: true}, func(context.Context) (AnyResult, error) {
		return cacheTestSizedIntResult(parentFrame, 1, 50, "snapshot://prune-label-parent", nil), nil
	})
	require.NoError(t, err)
	childFrame := &ResultCall{Kind: ResultCallKindField, Type: NewResultCallType(Int(0).Type()), Field: "prune-label-child", Receiver: &ResultCallRef{ResultID: uint64(parent.cacheSharedResult().id)}}
	child, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: childFrame, IsPersistable: true}, func(context.Context) (AnyResult, error) {
		return cacheTestSizedIntResult(childFrame, 2, 50, "snapshot://prune-label-child", nil), nil
	})
	require.NoError(t, err)
	keptFrame := cacheTestIntCall("prune-label-kept")
	_, err = c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: keptFrame, IsPersistable: true}, func(context.Context) (AnyResult, error) {
		return cacheTestSizedIntResult(keptFrame, 3, 50, "snapshot://prune-label-kept", nil), nil
	})
	require.NoError(t, err)
	cacheTestReleaseSession(t, c, ctx)

	before := map[string]string{}
	for _, entry := range c.UsageEntriesAll(ctx) {
		before[entry.ID] = entry.DagqlCall
	}
	parentID, childID := cacheTestSharedResultEntryID(parent), cacheTestSharedResultEntryID(child)
	require.Equal(t, "Query.prune-label-parent", before[parentID])
	require.Equal(t, "Int.prune-label-child", before[childID])

	old := time.Now().Add(-2 * time.Hour).UnixNano()
	c.egraphMu.Lock()
	for _, res := range []*sharedResult{parent.cacheSharedResult(), child.cacheSharedResult()} {
		res.lastUsedAtUnixNano = old
		res.createdAtUnixNano = old
	}
	c.egraphMu.Unlock()

	var logs bytes.Buffer
	prev := engineslog.Default()
	engineslog.SetDefault(engineslog.New(slog.NewTextHandler(&logs, nil)))
	report, err := c.Prune(ctx, []CachePrunePolicy{{All: true, KeepDuration: time.Hour}})
	engineslog.SetDefault(prev)
	require.NoError(t, err)

	got := map[string]string{}
	for _, entry := range report.Entries {
		got[entry.ID] = entry.DagqlCall
	}
	require.Equal(t, map[string]string{parentID: before[parentID], childID: before[childID]}, got, "the report names the removed entries' calls as before")
	c.egraphMu.RLock()
	_, parentLeft := c.resultsByID[parent.cacheSharedResult().id]
	c.egraphMu.RUnlock()
	require.False(t, parentLeft, "the pass removed the child's receiver too")
	for _, label := range []string{before[parentID], before[childID]} {
		require.Contains(t, logs.String(), "call="+label, "the log names %s", label)
	}
}
