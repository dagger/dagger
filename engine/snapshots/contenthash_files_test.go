package snapshots

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestContentHashFilesStartEmpty(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "contenthash")
	files, err := newContentHashFiles(dir)
	require.NoError(t, err)
	require.NoError(t, files.write("a", []byte("tree a"), nil))

	// A new process's manager starts without the last one's trees: the
	// checkpoint loads the ones it keeps.
	files, err = newContentHashFiles(dir)
	require.NoError(t, err)
	_, ok, err := files.read("a", nil)
	require.NoError(t, err)
	require.False(t, ok)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries)
}

// A tree that can't be written at load is left out, as one that can't be
// read at a checkpoint is, rather than failing the load and with it the
// whole persisted cache.
func TestContentHashFilesLoadSkipsUnwritableTrees(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "contenthash")
	s := newMetadataStore()
	files, err := newContentHashFiles(dir)
	require.NoError(t, err)
	s.hashFiles = files
	cm := &snapshotManager{metadataStore: s, records: map[string]*cacheRecord{}}
	require.NoError(t, os.RemoveAll(dir))
	require.NoError(t, os.WriteFile(dir, nil, 0o600))

	require.NoError(t, cm.LoadPersistentMetadata(PersistentMetadataRows{
		ContentHashes: []SnapshotContentHashRow{{SnapshotID: "a", Data: []byte("tree a")}},
	}))
	require.Empty(t, files.list())
}

// A tree saved after its metadata was dropped, as a background save can be,
// is discarded rather than left on disk with nothing to remove it.
func TestContentHashFilesDiscardWritesForDroppedMetadata(t *testing.T) {
	dir := t.TempDir()
	s := newMetadataStore()
	files, err := newContentHashFiles(dir)
	require.NoError(t, err)
	s.hashFiles = files
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
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries)

	// Other external keys stay in memory.
	other := s.getOrCreate("other")
	require.NoError(t, other.SetExternal("other.key", []byte("value")))
	dt, err = other.GetExternal("other.key")
	require.NoError(t, err)
	require.Equal(t, []byte("value"), dt)

	// After the store closes, saves are discarded too.
	require.NoError(t, s.close())
	require.NoError(t, other.SetExternal(ContentHashMetadataKey, []byte("tree")))
	entries, err = os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries)
}
