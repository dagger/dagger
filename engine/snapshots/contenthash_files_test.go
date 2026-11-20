package snapshots

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// breakDir replaces files' directory with a file, so writing a tree fails.
func breakDir(t *testing.T, files *contentHashFiles) {
	t.Helper()
	require.NoError(t, os.RemoveAll(files.dir))
	require.NoError(t, os.WriteFile(files.dir, nil, 0o600))
}

// failWrites makes files' writes fail while fail is set.
func failWrites(files *contentHashFiles) *atomic.Bool {
	var fail atomic.Bool
	files.failWrite = func() error {
		if fail.Load() {
			return errors.New("disk on fire")
		}
		return nil
	}
	return &fail
}

// treePlaces returns where id's tree is: in a file, in memory, or both (a
// file left to delete beside the tree in memory).
func treePlaces(files *contentHashFiles, id string) (onDisk, inMemory bool) {
	files.mu.Lock()
	defer files.mu.Unlock()
	_, onDisk = files.ids[id]
	_, inMemory = files.mem[id]
	return onDisk, inMemory
}

// A new process's manager starts without the last one's trees, the
// checkpoint loading the ones it keeps, in a fresh directory of its own: it
// deletes nothing it did not write, leaving what is under its root to the
// caller, which sweeps it.
func TestContentHashFilesStartInFreshDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "contenthash")
	first, err := newContentHashFiles(root)
	require.NoError(t, err)
	require.NoError(t, first.store("a", []byte("tree a"), nil))

	second, err := newContentHashFiles(root)
	require.NoError(t, err)
	require.NotEqual(t, first.dir, second.dir)
	require.Equal(t, root, filepath.Dir(second.dir))
	_, ok, err := second.read("a", nil)
	require.NoError(t, err)
	require.False(t, ok)
	entries, err := os.ReadDir(second.dir)
	require.NoError(t, err)
	require.Empty(t, entries)
	require.FileExists(t, first.path("a"), "the manager deletes nothing it did not write")
}

// A tree that can't be written at load is kept in memory, rather than failing
// the load, and with it the whole persisted cache, or being lost to a rescan
// that gives an imported snapshot other digests.
func TestContentHashFilesLoadKeepsUnwritableTreesInMemory(t *testing.T) {
	s := newMetadataStore()
	files, err := newContentHashFiles(filepath.Join(t.TempDir(), "contenthash"))
	require.NoError(t, err)
	s.hashFiles = files
	cm := &snapshotManager{
		metadataStore:       s,
		records:             map[string]*cacheRecord{},
		snapshotOwnerLeases: map[string]map[string]struct{}{"a": {"owner": {}}},
	}
	breakDir(t, files)

	require.NoError(t, cm.LoadPersistentMetadata(PersistentMetadataRows{
		ContentHashes: []SnapshotContentHashRow{{SnapshotID: "a", Data: []byte("tree a")}},
	}))
	onDisk, inMemory := treePlaces(files, "a")
	require.False(t, onDisk)
	require.True(t, inMemory)
	require.Equal(t, []SnapshotContentHashRow{{SnapshotID: "a", Data: []byte("tree a")}},
		cm.PersistentMetadataRows().ContentHashes, "the next checkpoint keeps it")

	md := s.getOrCreate("a")
	dt, err := md.GetExternal(ContentHashMetadataKey)
	require.NoError(t, err)
	require.Equal(t, []byte("tree a"), dt)
}

// A tree that can't be written when saved is kept in memory too, rather than
// failing the save (and with it a host sync) or being lost, and replaces the
// older tree on disk; the next save that can write moves it to disk.
func TestContentHashFilesSaveKeepsUnwritableTreesInMemory(t *testing.T) {
	s := newMetadataStore()
	files, err := newContentHashFiles(filepath.Join(t.TempDir(), "contenthash"))
	require.NoError(t, err)
	s.hashFiles = files
	fail := failWrites(files)
	cm := &snapshotManager{
		metadataStore:       s,
		records:             map[string]*cacheRecord{},
		snapshotOwnerLeases: map[string]map[string]struct{}{"a": {"owner": {}}},
	}
	md := s.getOrCreate("a")
	require.NoError(t, md.SetExternal(ContentHashMetadataKey, []byte("tree 1")))
	onDisk, inMemory := treePlaces(files, "a")
	require.True(t, onDisk)
	require.False(t, inMemory)

	fail.Store(true)
	require.NoError(t, md.SetExternal(ContentHashMetadataKey, []byte("tree 2")))
	onDisk, inMemory = treePlaces(files, "a")
	require.False(t, onDisk, "the older tree on disk is dropped")
	require.True(t, inMemory)
	require.NoFileExists(t, files.path("a"))
	dt, err := md.GetExternal(ContentHashMetadataKey)
	require.NoError(t, err)
	require.Equal(t, []byte("tree 2"), dt)
	require.Equal(t, []SnapshotContentHashRow{{SnapshotID: "a", Data: []byte("tree 2")}},
		cm.PersistentMetadataRows().ContentHashes, "the next checkpoint keeps it")

	fail.Store(false)
	require.NoError(t, md.SetExternal(ContentHashMetadataKey, []byte("tree 3")))
	onDisk, inMemory = treePlaces(files, "a")
	require.True(t, onDisk)
	require.False(t, inMemory, "the tree on disk replaces the one in memory")
	dt, err = md.GetExternal(ContentHashMetadataKey)
	require.NoError(t, err)
	require.Equal(t, []byte("tree 3"), dt)

	// A checkpoint that listed the tree on disk, and reads it after a save
	// moved it to memory, finds it there.
	rows, hashIDs := cm.persistentMetadataRows()
	require.Equal(t, []string{"a"}, hashIDs)
	fail.Store(true)
	require.NoError(t, md.SetExternal(ContentHashMetadataKey, []byte("tree 4")))
	require.Equal(t, []SnapshotContentHashRow{{SnapshotID: "a", Data: []byte("tree 4")}},
		cm.readContentHashRows(rows, hashIDs).ContentHashes)
}

// Saves that land while a lookup reads a tree's file don't make it miss the
// tree: the lookup opened the file under the lock saves publish under, and
// reads the tree it opened. Two failed and two successful saves within one
// read made the lookup that looked for the file by name again report it
// missing.
func TestContentHashFilesSavesDuringRead(t *testing.T) {
	s := newMetadataStore()
	files, err := newContentHashFiles(filepath.Join(t.TempDir(), "contenthash"))
	require.NoError(t, err)
	s.hashFiles = files
	fail := failWrites(files)
	md := s.getOrCreate("a")
	require.NoError(t, md.SetExternal(ContentHashMetadataKey, []byte("tree 1")))

	saves := 0
	files.afterOpen = func() {
		files.afterOpen = nil
		for i, failed := range []bool{true, false, true, false} {
			fail.Store(failed)
			saves++
			require.NoError(t, md.SetExternal(ContentHashMetadataKey, []byte{'t', byte('2' + i)}))
		}
	}
	dt, err := md.GetExternal(ContentHashMetadataKey)
	require.NoError(t, err)
	require.Equal(t, 4, saves)
	require.Equal(t, []byte("tree 1"), dt, "the tree opened")

	dt, err = md.GetExternal(ContentHashMetadataKey)
	require.NoError(t, err)
	require.Equal(t, []byte("t5"), dt, "the last save")
}

// Concurrent saves of one tree, some of which can't write it, and lookups of
// it: every lookup finds a tree, and once the saves are done, the tree is
// kept in exactly one place.
func TestContentHashFilesConcurrentSavesAndLookups(t *testing.T) {
	s := newMetadataStore()
	files, err := newContentHashFiles(filepath.Join(t.TempDir(), "contenthash"))
	require.NoError(t, err)
	s.hashFiles = files
	md := s.getOrCreate("a")
	require.NoError(t, md.SetExternal(ContentHashMetadataKey, []byte("tree")))

	var wg sync.WaitGroup
	done := make(chan struct{})
	for range 4 {
		wg.Go(func() {
			for {
				select {
				case <-done:
					return
				default:
				}
				dt, err := md.GetExternal(ContentHashMetadataKey)
				if !assert.NoError(t, err) {
					return
				}
				assert.Equal(t, []byte("tree"), dt)
			}
		})
	}
	// Every other write fails.
	var writes atomic.Int64
	files.failWrite = func() error {
		if writes.Add(1)%2 == 0 {
			return errors.New("disk on fire")
		}
		return nil
	}
	var saves sync.WaitGroup
	for range 4 {
		saves.Go(func() {
			for range 50 {
				assert.NoError(t, md.SetExternal(ContentHashMetadataKey, []byte("tree")))
			}
		})
	}
	saves.Wait()
	close(done)
	wg.Wait()

	onDisk, inMemory := treePlaces(files, "a")
	require.NotEqual(t, onDisk, inMemory, "the tree is kept in exactly one place")
}

// A registered tree whose file is missing was deleted by something other than
// the manager, which unregisters a tree when it deletes its file: reading it
// fails, rather than reporting no tree, which would make an imported
// snapshot's digests come from a rescan. A tree the manager removed is no
// tree.
func TestContentHashFilesMissingFileIsAnError(t *testing.T) {
	s := newMetadataStore()
	files, err := newContentHashFiles(filepath.Join(t.TempDir(), "contenthash"))
	require.NoError(t, err)
	s.hashFiles = files
	md := s.getOrCreate("a")
	require.NoError(t, md.SetExternal(ContentHashMetadataKey, []byte("tree")))

	require.NoError(t, os.Remove(files.path("a")))
	_, _, err = files.read("a", nil)
	require.ErrorContains(t, err, "missing")
	_, err = md.GetExternal(ContentHashMetadataKey)
	require.ErrorContains(t, err, "missing")
	require.NotErrorIs(t, err, ErrExternalNotFound)

	files.remove("a", nil)
	_, ok, err := files.read("a", nil)
	require.NoError(t, err)
	require.False(t, ok)
	_, err = md.GetExternal(ContentHashMetadataKey)
	require.ErrorIs(t, err, ErrExternalNotFound)
}

// Dropping a tree leaves its file to purge, which runs outside the callers'
// locks. A file that can't be deleted is kept for the next purge rather than
// left on disk with nothing to delete it, and is not a tree: metadata made
// again for the same snapshot, as a mutable snapshot's is, has none.
func TestContentHashFilesPurgeKeepsUndeletedFiles(t *testing.T) {
	s := newMetadataStore()
	files, err := newContentHashFiles(filepath.Join(t.TempDir(), "contenthash"))
	require.NoError(t, err)
	s.hashFiles = files
	md := s.getOrCreate("a")
	require.NoError(t, md.SetExternal(ContentHashMetadataKey, []byte("tree")))

	s.forget("a")
	require.Empty(t, files.list())
	require.FileExists(t, files.path("a"), "left to purge")
	s.purgeContentHashes()
	require.NoFileExists(t, files.path("a"))
	require.Empty(t, files.orphans)

	md = s.getOrCreate("a")
	require.NoError(t, md.SetExternal(ContentHashMetadataKey, []byte("tree")))
	// A non-empty directory can't be unlinked.
	require.NoError(t, os.Remove(files.path("a")))
	require.NoError(t, os.MkdirAll(filepath.Join(files.path("a"), "busy"), 0o700))
	s.clear("a")
	require.Contains(t, files.orphans, "a", "kept for the next purge")
	require.Empty(t, files.list())
	_, err = md.GetExternal(ContentHashMetadataKey)
	require.ErrorIs(t, err, ErrExternalNotFound, "the dropped metadata has no tree")
	_, err = s.getOrCreate("a").GetExternal(ContentHashMetadataKey)
	require.ErrorIs(t, err, ErrExternalNotFound, "new metadata has no tree")

	require.NoError(t, os.Remove(filepath.Join(files.path("a"), "busy")))
	s.purgeContentHashes()
	require.Empty(t, files.orphans)
	require.NoFileExists(t, files.path("a"))
}

// A save renames its tree over a file left to purge, which then leaves it.
func TestContentHashFilesSaveReplacesFileToPurge(t *testing.T) {
	s := newMetadataStore()
	files, err := newContentHashFiles(filepath.Join(t.TempDir(), "contenthash"))
	require.NoError(t, err)
	s.hashFiles = files
	require.NoError(t, s.getOrCreate("a").SetExternal(ContentHashMetadataKey, []byte("old")))
	s.forget("a")

	md := s.getOrCreate("a")
	require.NoError(t, md.SetExternal(ContentHashMetadataKey, []byte("new")))
	s.purgeContentHashes()
	dt, err := md.GetExternal(ContentHashMetadataKey)
	require.NoError(t, err)
	require.Equal(t, []byte("new"), dt)
}

// A tree saved after its metadata was dropped, as a background save can be,
// is discarded rather than left on disk, or in memory, with nothing to remove
// it.
func TestContentHashFilesDiscardWritesForDroppedMetadata(t *testing.T) {
	s := newMetadataStore()
	files, err := newContentHashFiles(t.TempDir())
	require.NoError(t, err)
	s.hashFiles = files
	fail := failWrites(files)
	md := s.getOrCreate("snap")

	require.NoError(t, md.SetExternal(ContentHashMetadataKey, []byte("tree")))
	dt, err := md.GetExternal(ContentHashMetadataKey)
	require.NoError(t, err)
	require.Equal(t, []byte("tree"), dt)
	s.mu.RLock()
	require.Empty(t, md.external, "the tree is not held in memory")
	s.mu.RUnlock()

	s.clear("snap")
	_, err = md.GetExternal(ContentHashMetadataKey)
	require.Error(t, err)
	require.NoError(t, md.SetExternal(ContentHashMetadataKey, []byte("late tree")))
	_, err = md.GetExternal(ContentHashMetadataKey)
	require.Error(t, err)
	entries, err := os.ReadDir(files.dir)
	require.NoError(t, err)
	require.Empty(t, entries)

	// Nor kept in memory when it can't be written.
	fail.Store(true)
	require.NoError(t, md.SetExternal(ContentHashMetadataKey, []byte("late tree")))
	require.Empty(t, files.list())
	s.mu.RLock()
	require.Empty(t, md.external)
	s.mu.RUnlock()
	fail.Store(false)

	// Other external keys stay in memory.
	other := s.getOrCreate("other")
	require.NoError(t, other.SetExternal("other.key", []byte("value")))
	dt, err = other.GetExternal("other.key")
	require.NoError(t, err)
	require.Equal(t, []byte("value"), dt)

	// After the store closes, saves are discarded too.
	require.NoError(t, s.close())
	require.NoError(t, other.SetExternal(ContentHashMetadataKey, []byte("tree")))
	entries, err = os.ReadDir(files.dir)
	require.NoError(t, err)
	require.Empty(t, entries)
}
