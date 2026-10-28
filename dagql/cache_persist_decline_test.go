package dagql

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// A value its call declined to persist keeps the mark across a restart. Kept
// only as the dependency of a persisted entry, it is saved and restored with
// that entry; a persistable hit on it after the restart leaves it without a
// retention edge of its own, so it goes when its dependent's edge goes.
func TestCacheDeclinedValueStaysUnpersistedAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	ctx, c, srv := persistedListTestCache(t, path)
	const field = "declined-contents"
	read := func(ctx context.Context, c *Cache, srv *Server, session string) AnyResult {
		t.Helper()
		frame := &ResultCall{Kind: ResultCallKindField, Field: field, Type: NewResultCallType(String("").Type())}
		res, err := c.GetOrInitCall(ctx, session, srv, &CallRequest{ResultCall: frame, IsPersistable: true}, func(ctx context.Context) (AnyResult, error) {
			DeclinePersistence(ctx)
			return NewResultForCall(String("large"), frame)
		})
		require.NoError(t, err)
		return res
	}
	retained := func(c *Cache, id sharedResultID) bool {
		c.egraphMu.RLock()
		defer c.egraphMu.RUnlock()
		_, ok := c.persistedEdgesByResult[id]
		return ok
	}

	s1 := currentEntryTestSession(ctx, c, "session-1")
	declined := read(s1, c, srv, "session-1")
	require.False(t, declined.HitCache())
	declinedID := declined.cacheSharedResult().id
	parent := currentEntryTestPublish(t, s1, c, srv, "session-1", "declined-parent", String("parent"))
	parentID := parent.cacheSharedResult().id
	require.NoError(t, c.AddExplicitDependency(s1, parent, declined, "declined-dependency"))
	require.False(t, retained(c, declinedID), "a declined value has no retention edge")
	require.True(t, retained(c, parentID))
	require.NoError(t, c.ReleaseSession(s1, "session-1"))
	require.NoError(t, c.WaitSessionRelease(s1, "session-1"))
	require.NoError(t, c.Close(ctx))

	ctx, c, srv = persistedListTestCache(t, path)
	s2 := currentEntryTestSession(ctx, c, "session-2")
	hit := read(s2, c, srv, "session-2")
	require.True(t, hit.HitCache(), "the dependency is restored")
	require.Equal(t, declinedID, hit.cacheSharedResult().id)
	require.Equal(t, String("large"), hit.Unwrap())
	require.False(t, retained(c, declinedID), "a persistable hit after the restart doesn't persist it")
	require.NoError(t, c.ReleaseSession(s2, "session-2"))
	require.NoError(t, c.WaitSessionRelease(s2, "session-2"))

	_, removed, err := c.removePersistedEdge(ctx, parentID)
	require.NoError(t, err)
	require.True(t, removed)
	c.egraphMu.RLock()
	_, live := c.resultsByID[declinedID]
	c.egraphMu.RUnlock()
	require.False(t, live, "it goes with its dependent's retention edge")
}
