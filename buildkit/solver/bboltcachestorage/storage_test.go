package bboltcachestorage

import (
	"path/filepath"
	"testing"

	"github.com/moby/buildkit/solver"
	"github.com/moby/buildkit/solver/testutil"
	"github.com/stretchr/testify/require"
)

func TestBoltCacheStorage(t *testing.T) {
	testutil.RunCacheStorageTests(t, func() solver.CacheKeyStorage {
		tmpDir := t.TempDir()

		st, err := NewStore(filepath.Join(tmpDir, "cache.db"))
		require.NoError(t, err)
		t.Cleanup(func() {
			require.NoError(t, st.db.Close())
		})

		return st
	})
}
