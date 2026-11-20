package dagql

import (
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

func TestSchemaModuleSelectionFallback(t *testing.T) {
	ctx, c, srv := transferTestCache(t)
	lower := persistedListTestResult(t, ctx, c, srv, "foreign", String("foreign"))
	lower.cacheSharedResult().imported = true
	exact := persistedListTestResult(t, ctx, c, srv, "native", String("native"))
	for _, value := range []AnyResult{lower, exact} {
		_, err := value.WithContentDigestAny(ctx, digest.FromString("implementation"))
		require.NoError(t, err)
	}
	recorded := uint64(exact.cacheSharedResult().id)
	got, err := c.LoadResultByResultIDForSchema(ctx, "test-session", srv, recorded, nil)
	require.NoError(t, err)
	require.Same(t, exact.cacheSharedResult(), got.cacheSharedResult())
	c.egraphMu.Lock()
	exact.cacheSharedResult().expiresAtUnix = time.Now().Add(-time.Hour).Unix()
	c.egraphMu.Unlock()
	got, err = c.LoadResultByResultIDForSchema(ctx, "test-session", srv, recorded, nil)
	require.NoError(t, err)
	require.Same(t, lower.cacheSharedResult(), got.cacheSharedResult())
	// An installed Module outlives expiry of the ordinary cache entry.
	got, err = c.LoadResultByResultIDForSchema(ctx, "test-session", srv, uint64(lower.cacheSharedResult().id), []SchemaModuleCandidate{{ModuleResultID: recorded, ScopedResultID: recorded}})
	require.NoError(t, err)
	require.Same(t, exact.cacheSharedResult(), got.cacheSharedResult())
	_, err = c.LoadResultByResultIDForSchema(ctx, "test-session", srv, 999999, nil)
	require.ErrorContains(t, err, "missing shared result")
	c.testBeforeServeRequirementRecheck = func(row *sharedResult) {
		c.egraphMu.Lock()
		row.sessionResourceHandle = "late-socket"
		_, err := c.recomputeRequiredSessionResourcesLocked(row)
		c.egraphMu.Unlock()
		require.NoError(t, err)
	}
	_, err = c.LoadResultByResultIDForSchema(ctx, "test-session", srv, recorded, nil)
	require.ErrorContains(t, err, "session resources")
	c.testBeforeServeRequirementRecheck = nil
	require.NoError(t, c.ReleaseSession(ctx, "test-session"))
	_, err = c.LoadResultByResultIDForSchema(ctx, "test-session", srv, recorded, nil)
	require.ErrorIs(t, err, ErrCacheSessionReleased)
}

func TestSchemaModuleSelectionSkipsInaccessibleInstalled(t *testing.T) {
	for _, inaccessible := range []string{"operational", "scoped"} {
		t.Run(inaccessible, func(t *testing.T) {
			ctx, c, srv := transferTestCache(t)
			lower := persistedListTestResult(t, ctx, c, srv, "lower", String("lower"))
			recorded := persistedListTestResult(t, ctx, c, srv, "recorded", String("recorded"))
			operational := persistedListTestResult(t, ctx, c, srv, "operational", String("operational"))
			scoped := persistedListTestResult(t, ctx, c, srv, "scoped", String("scoped"))
			for _, value := range []AnyResult{lower, recorded, scoped} {
				_, err := value.WithContentDigestAny(ctx, digest.FromString("implementation"))
				require.NoError(t, err)
			}
			c.egraphMu.Lock()
			blocked := operational.cacheSharedResult()
			if inaccessible == "scoped" {
				blocked = scoped.cacheSharedResult()
			}
			blocked.sessionResourceHandle = "unbound-socket"
			_, err := c.recomputeRequiredSessionResourcesLocked(blocked)
			c.egraphMu.Unlock()
			require.NoError(t, err)
			candidates := []SchemaModuleCandidate{{ModuleResultID: uint64(operational.cacheSharedResult().id), ScopedResultID: uint64(scoped.cacheSharedResult().id)}}
			id := uint64(recorded.cacheSharedResult().id)
			got, err := c.LoadResultByResultIDForSchema(ctx, "test-session", srv, id, candidates)
			require.NoError(t, err)
			require.Same(t, recorded.cacheSharedResult(), got.cacheSharedResult())
			c.egraphMu.Lock()
			recorded.cacheSharedResult().expiresAtUnix = time.Now().Add(-time.Hour).Unix()
			c.egraphMu.Unlock()
			got, err = c.LoadResultByResultIDForSchema(ctx, "test-session", srv, id, candidates)
			require.NoError(t, err)
			require.Same(t, lower.cacheSharedResult(), got.cacheSharedResult())
		})
	}
}
