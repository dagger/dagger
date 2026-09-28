package dagql

// The cache's identity across the engine processes that open it: the random
// ID its persistence database keeps, the generation counting the opens, the
// identity of a database an open wiped, and result numbers a restart never
// reuses.

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"

	"gotest.tools/v3/assert"

	persistdb "github.com/dagger/dagger/dagql/persistdb"
)

// A clean restart keeps the identity and counts the next generation.
func TestCacheIdentityAcrossCleanRestart(t *testing.T) {
	t.Parallel()
	ctx := cacheTestContext(t.Context())
	path := filepath.Join(t.TempDir(), "cache.db")

	var first CacheIdentity
	for generation := uint64(1); generation <= 3; generation++ {
		c, err := NewCache(ctx, path, nil, nil)
		assert.NilError(t, err)
		identity := c.Identity()
		if generation == 1 {
			first = identity
			assert.Assert(t, identity.ID != "")
			assert.Assert(t, !c.OpenedExisting(), "a new database")
		} else {
			assert.Equal(t, first.ID, identity.ID, "the identity survives a clean restart")
			assert.Assert(t, c.OpenedExisting())
		}
		assert.Equal(t, generation, identity.Generation)
		assert.Equal(t, first.ID+"/"+strconv.FormatUint(generation, 10), identity.String())
		assert.Equal(t, "", c.WipedCacheID())
		assert.NilError(t, c.Close(context.Background()))
	}
}

// A cache without a persistence database has no identity.
func TestCacheIdentityInMemory(t *testing.T) {
	t.Parallel()
	c, err := NewCache(cacheTestContext(t.Context()), "", nil, nil)
	assert.NilError(t, err)
	t.Cleanup(func() { assert.NilError(t, c.CloseDiscardingPersistence()) })
	assert.Equal(t, CacheIdentity{}, c.Identity())
}

// Each way an open wipes its database gives the cache a new identity at
// generation 1 and names the wiped database's identity, including the import
// failure, which rebuilds the cache after the wipe. The next open is an
// ordinary restart of the new identity.
func TestCacheIdentityNamesWipedDatabase(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		reason CachePersistenceResetReason
		// spoil leaves the database, closed, in the state the reason names.
		spoil func(t *testing.T, ctx context.Context, path string)
	}{
		{
			name:   "unclean shutdown",
			reason: CachePersistenceResetUncleanShutdown,
			spoil: func(t *testing.T, ctx context.Context, path string) {
				setCacheTestMeta(t, ctx, path, persistdb.MetaKeyCleanShutdown, "0")
			},
		},
		{
			name:   "schema mismatch",
			reason: CachePersistenceResetSchemaMismatch,
			spoil: func(t *testing.T, ctx context.Context, path string) {
				setCacheTestMeta(t, ctx, path, persistdb.MetaKeySchemaVersion, "0")
			},
		},
		{
			name:   "import failure",
			reason: CachePersistenceResetImportFailure,
			spoil: func(t *testing.T, ctx context.Context, path string) {
				db, q, err := prepareCacheDBs(ctx, path)
				assert.NilError(t, err)
				_, err = db.Exec(`UPDATE results SET self_payload = x'7B6E6F742D6A736F6E'`)
				assert.NilError(t, err)
				assert.NilError(t, closeCacheDBs(db, q))
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := cacheTestContext(t.Context())
			path := filepath.Join(t.TempDir(), "cache.db")

			c, err := NewCache(ctx, path, nil, nil)
			assert.NilError(t, err)
			wiped := c.Identity()
			frame := cacheTestIntCall("identity-wipe-" + tc.name)
			_, err = c.GetOrInitCall(ctx, "s", noopTypeResolver{}, &CallRequest{ResultCall: frame, IsPersistable: true}, ValueFunc(cacheTestIntResult(frame, 1)))
			assert.NilError(t, err)
			assert.NilError(t, c.ReleaseSession(ctx, "s"))
			assert.NilError(t, c.Close(context.Background()))
			tc.spoil(t, ctx, path)

			c, err = NewCache(ctx, path, nil, nil)
			assert.NilError(t, err)
			assert.Equal(t, tc.reason, c.PersistenceResetReason())
			identity := c.Identity()
			assert.Assert(t, identity.ID != "" && identity.ID != wiped.ID, "a wiped database gets a new identity")
			assert.Equal(t, uint64(1), identity.Generation)
			assert.Assert(t, !c.OpenedExisting())
			assert.Equal(t, wiped.ID, c.WipedCacheID(), "the open names the identity it wiped")
			assert.NilError(t, c.Close(context.Background()))

			c, err = NewCache(ctx, path, nil, nil)
			assert.NilError(t, err)
			assert.Equal(t, CacheIdentity{ID: identity.ID, Generation: 2}, c.Identity())
			assert.Equal(t, "", c.WipedCacheID())
			assert.NilError(t, c.Close(context.Background()))
		})
	}
}

func setCacheTestMeta(t *testing.T, ctx context.Context, path, key, value string) {
	t.Helper()
	db, q, err := prepareCacheDBs(ctx, path)
	assert.NilError(t, err)
	assert.NilError(t, q.UpsertMeta(ctx, key, value))
	assert.NilError(t, closeCacheDBs(db, q))
}

// A restored cache allocates past every number its previous process
// allocated, saved or not, so a number names one entry for the life of the
// identity.
func TestCacheResultNumbersNotReusedAcrossRestart(t *testing.T) {
	t.Parallel()
	ctx := cacheTestContext(t.Context())
	path := filepath.Join(t.TempDir(), "cache.db")

	c, err := NewCache(ctx, path, nil, nil)
	assert.NilError(t, err)
	kept := cacheTestIntCall("numbers-kept")
	_, err = c.GetOrInitCall(ctx, "s", noopTypeResolver{}, &CallRequest{ResultCall: kept, IsPersistable: true}, ValueFunc(cacheTestIntResult(kept, 1)))
	assert.NilError(t, err)
	var highest sharedResultID
	for _, field := range []string{"numbers-dropped-a", "numbers-dropped-b"} {
		frame := cacheTestIntCall(field)
		res, err := c.GetOrInitCall(ctx, "s", noopTypeResolver{}, &CallRequest{ResultCall: frame}, ValueFunc(cacheTestIntResult(frame, 2)))
		assert.NilError(t, err)
		highest = res.cacheSharedResult().id
	}
	assert.NilError(t, c.ReleaseSession(ctx, "s"))
	assert.Equal(t, 1, c.Size(), "only the retained result is saved")
	assert.NilError(t, c.Close(context.Background()))

	c, err = NewCache(ctx, path, nil, nil)
	assert.NilError(t, err)
	t.Cleanup(func() { assert.NilError(t, c.Close(context.Background())) })
	assert.Equal(t, 1, c.Size())
	frame := cacheTestIntCall("numbers-after-restart")
	res, err := c.GetOrInitCall(ctx, "s", noopTypeResolver{}, &CallRequest{ResultCall: frame}, ValueFunc(cacheTestIntResult(frame, 3)))
	assert.NilError(t, err)
	assert.Assert(t, res.cacheSharedResult().id > highest, "number %d reuses one the previous process allocated (up to %d)", res.cacheSharedResult().id, highest)
	assert.NilError(t, c.ReleaseSession(ctx, "s"))
}
