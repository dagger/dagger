package dagql

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A writer that comes while a save walks the store gets the cache's lock
// while the walk goes on, not once it has ended, and so does a lookup that
// comes after the writer, which Go's lock would otherwise queue behind it.
// The walk is slowed by 2 ms an entry, so a walk under one hold keeps both
// out for about a second.
func TestCachePersistenceSaveLetsWritersInWhileItWalks(t *testing.T) {
	t.Parallel()
	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, filepath.Join(t.TempDir(), "cache.db"), nil, nil)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, c.Close(context.Background()))
	}()
	call := func(session, field string) {
		t.Helper()
		key := cacheTestIntCall(field)
		_, err := c.GetOrInitCall(ctx, session, noopTypeResolver{}, &CallRequest{
			ResultCall:    key,
			IsPersistable: true,
		}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(key, len(field)), nil
		})
		require.NoError(t, err)
	}

	const entries = 500
	for i := range entries {
		call("test-session", fmt.Sprintf("lets-writers-in-%d", i))
	}
	cacheTestReleaseSession(t, c, ctx)

	var read atomic.Int64
	walking := make(chan struct{})
	var once sync.Once
	c.testPersistRead = func() {
		read.Add(1)
		once.Do(func() { close(walking) })
		time.Sleep(2 * time.Millisecond)
	}
	saved := make(chan error, 1)
	go func() { saved <- c.persistCurrentState(ctx) }()
	<-walking

	type done struct {
		waited time.Duration
		read   int64
	}
	writer := make(chan done, 1)
	start := time.Now()
	go func() {
		call("writer-session", "lets-writers-in-writer")
		writer <- done{time.Since(start), read.Load()}
	}()
	// The lookup comes once the writer waits for the lock, or has had it.
	for !c.egraphMu.Waiting() && len(writer) == 0 {
		time.Sleep(100 * time.Microsecond)
	}
	lookup := make(chan done, 1)
	lookupStart := time.Now()
	go func() {
		call("lookup-session", "lets-writers-in-0")
		lookup <- done{time.Since(lookupStart), read.Load()}
	}()

	w, l := <-writer, <-lookup
	require.NoError(t, <-saved)
	t.Logf("writer waited %v, the walk having read %d entries; the lookup %v, at %d", w.waited, w.read, l.waited, l.read)
	require.Less(t, w.read, int64(entries/2), "the writer got the lock only after the walk")
	require.Less(t, l.read, int64(entries/2), "the lookup got the lock only after the walk")
	require.Less(t, w.waited, 250*time.Millisecond)
	require.Less(t, l.waited, 250*time.Millisecond)
}

// A Cloud's save that reads the store in pieces saves the store as it was at
// its end, whatever the service wrote while it read: new roots, a stored
// part, a prune and a collection of an engine's holdings, made while the
// save had let the lock go, after it had read every unit. The store it
// leaves is what a full save of the cache as the save returns would write.
func TestCloudSaveReadsWhatIsWrittenWhileItReads(t *testing.T) {
	t.Parallel()
	ctx, cloud := storedPersistenceTestCloud(t, filepath.Join(t.TempDir(), "cloud.db"))
	defer func() { require.NoError(t, cloud.Close(ctx)) }()
	cloud.closeStoredBodies()
	storedPersistenceTestRootAndDep(t, ctx, cloud, "before-first")
	_, second := storedPersistenceTestRootAndDep(t, ctx, cloud, "before-second")
	storedPersistenceTestRootAndDep(t, ctx, cloud, "before-third")
	require.NoError(t, cloud.Checkpoint(ctx))

	writes := []func(){
		func() { storedPersistenceTestRootAndDep(t, ctx, cloud, "during-new") },
		func() {
			stored, _, err := cloud.SetStoredPart(ctx, second, storedPruneTestPart("snapshot", map[string]int64{"during-layer": 10}), 0)
			require.NoError(t, err)
			require.True(t, stored)
		},
		func() {
			_, err := cloud.PruneMetadataEstimate(ctx, 2, 1)
			require.NoError(t, err)
		},
		func() {
			_, _, err := cloud.CollectRemoteHoldings(ctx, cloud.ReleaseRemoteCache(ctx, "cache-a").Candidates, nil)
			require.NoError(t, err)
		},
		func() { storedPersistenceTestRootAndDep(t, ctx, cloud, "during-last") },
	}
	cloud.egraphMu.RLock()
	entries := len(cloud.resultsByID)
	cloud.egraphMu.RUnlock()
	wrote := 0
	cloud.readPieceSize = 1
	cloud.testPersistWalked = func() {
		for _, write := range writes {
			write()
			wrote++
		}
	}
	require.NoError(t, cloud.Checkpoint(ctx))
	cloud.readPieceSize, cloud.testPersistWalked = 0, nil
	require.Equal(t, len(writes), wrote, "every write ran while the save read")
	require.Greater(t, cloud.LastSaveRead().Holds, entries, "the save read in pieces")
	require.NoError(t, checkPersistFullSave(ctx, cloud, persistTestFullSave(t, cloud)))
}

// persistTestFullSave is a full save of the cache as it is now, read under
// one hold, as an empty database would take it.
func persistTestFullSave(t *testing.T, c *Cache) *persistStateSnapshot {
	t.Helper()
	c.egraphMu.RLock()
	full := newPersistRead(&persistKept{}, c.persistSeed)
	full.prints = nil
	err := c.walkStoreLocked(full, nil)
	next, writes := sharedResultID(c.nextSharedResultID.Load()), c.writes
	c.egraphMu.RUnlock()
	require.NoError(t, err)
	snapshot, err := full.assemble(next, writes)
	require.NoError(t, err)
	return &snapshot
}

// A content digest a span teaches an entry whose class already holds that
// digest, after a save has read the class, gives the class a content label
// and no join. The save writes the class's label as the cache holds it at
// its end.
func TestCloudSaveReadsAContentLabelTaughtAfterItReadTheClass(t *testing.T) {
	t.Parallel()
	ctx, cloud := storedPersistenceTestCloud(t, filepath.Join(t.TempDir(), "cloud.db"))
	defer func() { require.NoError(t, cloud.Close(ctx)) }()
	cloud.closeStoredBodies()
	bundle, aNumber := storedContentTestRoot(t, "label-root")
	number := storedPersistenceTestMerge(t, ctx, cloud, bundle)[0]
	require.NoError(t, cloud.Checkpoint(ctx))
	entry := storedBundleTestEntry(cloud, number)
	cloud.egraphMu.RLock()
	require.NotEmpty(t, entry.recipeKeys)
	recipe := entry.recipeKeys[0]
	cloud.egraphMu.RUnlock()

	taught := false
	cloud.testPersistWalked = func() {
		// The recipe is in the entry's class already: no join.
		storedContentTestTeach(t, ctx, cloud, aNumber, recipe)
		taught = true
	}
	require.NoError(t, cloud.Checkpoint(ctx))
	cloud.testPersistWalked = nil
	require.True(t, taught, "the span taught the entry while the save read")
	require.NoError(t, checkPersistFullSave(ctx, cloud, persistTestFullSave(t, cloud)))
}

// The body worker gives an entry's frame a taught content digest, and then
// records it given in a later hold of the lock. When that later hold comes
// while a save reads, after the save read the entry, the save writes the
// entry's taught digests as given.
func TestCloudSaveReadsTheTaughtDigestsTheBodyWorkerGives(t *testing.T) {
	t.Parallel()
	content := testDigest("content-given-while-saving")
	ctx, cloud := storedPersistenceTestCloud(t, filepath.Join(t.TempDir(), "cloud.db"))
	defer func() { require.NoError(t, cloud.Close(ctx)) }()
	bundle, aNumber := storedContentTestRoot(t, "given-root")
	storedPersistenceTestMerge(t, ctx, cloud, bundle)
	require.NoError(t, cloud.Checkpoint(ctx))

	given, proceed, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var held, freed, finished sync.Once
	release := func() { freed.Do(func() { close(proceed) }) }
	t.Cleanup(release)
	cloud.testTaughtContentGiven = func() {
		held.Do(func() {
			close(given)
			<-proceed
		})
	}
	cloud.testTaughtContent = func(int) { finished.Do(func() { close(done) }) }
	storedContentTestTeach(t, ctx, cloud, aNumber, content)
	// The worker has given the frame the digest and not yet recorded it.
	<-given

	recorded := false
	cloud.testPersistWalked = func() {
		release()
		<-done
		recorded = true
	}
	require.NoError(t, cloud.Checkpoint(ctx))
	cloud.testPersistWalked = nil
	require.True(t, recorded, "the worker recorded the digest given while the save read")
	require.NoError(t, checkPersistFullSave(ctx, cloud, persistTestFullSave(t, cloud)))
}
