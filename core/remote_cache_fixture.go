package core

import "github.com/dagger/dagger/dagql"

const RemoteCacheFixtureRootEnv = "_DAGGER_TEST_REMOTE_CACHE_FIXTURE_ROOT"

// RemoteCacheFixturePersistence is diagnostic state stored in the gated test
// fixture directory, independently of the cache that a reset can discard.
type RemoteCacheFixturePersistence struct {
	PersistenceResetReason    dagql.CachePersistenceResetReason `json:"persistenceResetReason"`
	LocalCacheResetReason     string                            `json:"localCacheResetReason"`
	RemovedPersistedRootCount int                               `json:"removedPersistedRootCount"`
	DiskPrunedResults         []string                          `json:"diskPrunedResults,omitempty"`
}
