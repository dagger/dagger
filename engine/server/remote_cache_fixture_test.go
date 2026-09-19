package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

func TestRemoteCacheFixturePruneReport(t *testing.T) {
	for _, mode := range []string{"disk", "metadata"} {
		t.Run(mode, func(t *testing.T) {
			path := t.TempDir()
			t.Setenv(core.RemoteCacheFixtureRootEnv, path)
			cache := newGCTestCache(t)
			ctx := addGCTestPersistable(t, cache, "fixture", "fixtureRoot", dagql.NewInt(1))
			require.NoError(t, cache.ReleaseSession(ctx, "fixture"))
			srv := &Server{rootDir: t.TempDir(), engineCache: cache}
			if mode == "disk" {
				srv.workerGCPolicies = []dagqlCachePrunePolicy{{All: true}}
			} else {
				srv.localCacheGCEnabled = true
				srv.dagqlCacheMaxEstimatedBytes = cache.MetadataEstimate().EstimatedBytes - 1
				srv.dagqlCacheTargetEstimatedBytes = 1
			}
			require.NoError(t, srv.gcLocked(ctx, localCacheGCScheduled))
			require.Zero(t, cache.EntryStats().RetainedCalls)
			// Another server instance reads the report kept outside cache state.
			next := &Server{}
			require.NoError(t, next.updateRemoteCacheFixturePersistence(func(report *core.RemoteCacheFixturePersistence) {
				require.Equal(t, 1, report.RemovedPersistedRootCount)
				report.PersistenceResetReason = dagql.CachePersistenceResetUncleanShutdown
				report.LocalCacheResetReason = "dagql_unclean_shutdown"
			}))
			raw, err := os.ReadFile(filepath.Join(path, "persistence.json"))
			require.NoError(t, err)
			var report core.RemoteCacheFixturePersistence
			require.NoError(t, json.Unmarshal(raw, &report))
			require.Equal(t, 1, report.RemovedPersistedRootCount)
			require.Equal(t, dagql.CachePersistenceResetUncleanShutdown, report.PersistenceResetReason)
			require.Equal(t, "dagql_unclean_shutdown", report.LocalCacheResetReason)
			if mode == "disk" {
				require.Equal(t, []string{"dagql.result.1"}, report.DiskPrunedResults)
			}
		})
	}
}
