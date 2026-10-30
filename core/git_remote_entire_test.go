package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gofrs/flock"
	"github.com/stretchr/testify/require"
)

// git-remote-entire keeps its replica cache at $XDG_CACHE_HOME/entire/nodes.json
// and serializes read-modify-write with gofrs/flock on nodes.json.lock.
func TestTrimEntireNodesCache(t *testing.T) {
	const limit = 16
	setup := func(t *testing.T, size int) (cacheHome, cache string) {
		cacheHome = t.TempDir()
		cache = filepath.Join(cacheHome, "entire", "nodes.json")
		require.NoError(t, os.MkdirAll(filepath.Dir(cache), 0o700))
		require.NoError(t, os.WriteFile(cache, make([]byte, size), 0o600))
		return cacheHome, cache
	}

	t.Run("removes a cache over the limit and releases the lock", func(t *testing.T) {
		cacheHome, cache := setup(t, limit+1)
		require.NoError(t, trimEntireNodesCache(cacheHome, limit))
		require.NoFileExists(t, cache)

		helper := flock.New(cache + ".lock")
		locked, err := helper.TryLock()
		require.NoError(t, err)
		require.True(t, locked, "the helper must be able to take its lock afterwards")
		require.NoError(t, helper.Unlock())
	})

	t.Run("keeps a cache within the limit", func(t *testing.T) {
		cacheHome, cache := setup(t, limit)
		require.NoError(t, trimEntireNodesCache(cacheHome, limit))
		require.FileExists(t, cache)
	})

	t.Run("leaves a cache a helper is updating", func(t *testing.T) {
		cacheHome, cache := setup(t, limit+1)
		helper := flock.New(cache + ".lock")
		require.NoError(t, helper.Lock())
		t.Cleanup(func() { _ = helper.Unlock() })

		require.NoError(t, trimEntireNodesCache(cacheHome, limit))
		require.FileExists(t, cache)
	})

	t.Run("accepts a cache the helper has not created", func(t *testing.T) {
		require.NoError(t, trimEntireNodesCache(t.TempDir(), limit))
	})
}
