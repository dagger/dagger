package snapshots_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/stretchr/testify/require"
)

// A layer of many directories applies apart, into a fresh snapshot, with
// next to no lookups of what its paths are: in a snapshot with nothing
// below it, a path exists only if the layer made it, and a directory the
// layer made resolves as its parent did. 410 directories, three deep, are
// made with fewer lookups than a quarter of a lookup each. Not parallel: it
// counts the engine applier's lookups.
func TestImportChainOverlayDirectoriesTakeNoLookups(t *testing.T) {
	var entries []tarEntry
	dirs := 0
	for a := range 10 {
		entries = append(entries, tarEntry{name: fmt.Sprintf("a%d/", a), mode: 0o755})
		dirs++
		for b := range 10 {
			entries = append(entries, tarEntry{name: fmt.Sprintf("a%d/b%d/", a, b), mode: 0o750})
			dirs++
			for c := range 3 {
				entries = append(entries, tarEntry{name: fmt.Sprintf("a%d/b%d/c%d/", a, b, c), mode: 0o700})
				dirs++
			}
		}
	}
	producer, consumer := testutil.NewStore(t), testutil.NewOverlayStore(t)
	consumer.UseEngineApplier(t)
	chain := tarChain(t, producer, entries)
	lstats, resolutions, restore := bkcache.CountLayerLookups()
	ref := importChain(t, consumer, chain)
	restore()
	t.Logf("%d directories: %d Lstats, %d resolutions", dirs, lstats.Load(), resolutions.Load())
	require.Less(t, lstats.Load()+resolutions.Load(), int64(dirs/4), "fewer lookups than a quarter of a lookup a directory")
	tree := testutil.OverlayTree(t, ref)
	require.Equal(t, "dir 755", tree["a0"])
	require.Equal(t, "dir 750", tree["a9/b9"])
	require.Equal(t, "dir 700", tree["a9/b9/c2"])
	require.Len(t, tree, dirs)
}

// A directory a layer made, then replaced by a symlink to a path outside
// it, sends a later entry under its name through the symlink, kept inside
// the snapshot, as before: what the layer knew of the directory ends when
// the directory is replaced.
func TestImportChainOverlayDirectoryReplacedBySymlink(t *testing.T) {
	t.Parallel()
	escape := filepath.Join(t.TempDir(), "escape")
	producer, consumer := testutil.NewStore(t), testutil.NewOverlayStore(t)
	consumer.UseEngineApplier(t)
	ref := importChain(t, consumer, tarChain(t, producer, []tarEntry{
		{name: "d/", mode: 0o755},
		{name: "d/x", mode: 0o644, body: "x"},
		{name: "d", link: "/target"},
		{name: "d/y", mode: 0o644, body: "y"},
		{name: "e/", mode: 0o755},
		{name: "e", link: "../../../../../../../../.." + escape},
		{name: "e/z", mode: 0o644, body: "z"},
	}))
	tree := testutil.OverlayTree(t, ref)
	require.Equal(t, "symlink /target", tree["d"])
	require.Equal(t, "file 644 y", tree["target/y"])
	require.NotContains(t, tree, "d/x")
	require.Equal(t, "file 644 z", tree[filepath.Join(escape[1:], "z")], "the relative symlink resolves inside the snapshot")
	_, err := os.Lstat(escape)
	require.True(t, os.IsNotExist(err), "nothing is written outside the snapshot")
}

// Applied onto a lower layer in place, not apart, an entry under a symlink
// the lower layer holds goes through the symlink, inside the snapshot.
func TestImportChainSymlinkFromALowerLayer(t *testing.T) {
	t.Parallel()
	producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
	consumer.UseEngineApplier(t)
	ref := importChain(t, consumer, tarChain(t, producer,
		[]tarEntry{{name: "l", link: "/elsewhere"}},
		[]tarEntry{{name: "l/z", mode: 0o644, body: "z"}},
	))
	root := testutil.Root(t, ref)
	target, err := os.Readlink(filepath.Join(root, "l"))
	require.NoError(t, err)
	require.Equal(t, "/elsewhere", target)
	data, err := os.ReadFile(filepath.Join(root, "elsewhere", "z"))
	require.NoError(t, err)
	require.Equal(t, "z", string(data))
}

// A whiteout of a directory the same layer made, and then the directory
// made again, leaves only the second directory's contents, applied in place
// into a fresh snapshot; apart, onto overlayfs, the whiteout can't take the
// place of the directory and the apply fails, as before.
func TestImportChainWhiteoutOfADirectoryTheLayerMade(t *testing.T) {
	t.Parallel()
	layer := []tarEntry{
		{name: "w/", mode: 0o755},
		{name: "w/f", mode: 0o644, body: "f"},
		{name: ".wh.w"},
		{name: "w/", mode: 0o755},
		{name: "w/g", mode: 0o644, body: "g"},
	}
	t.Run("in place", func(t *testing.T) {
		t.Parallel()
		producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
		consumer.UseEngineApplier(t)
		ref := importChain(t, consumer, tarChain(t, producer, layer))
		root := testutil.Root(t, ref)
		_, err := os.Lstat(filepath.Join(root, "w", "f"))
		require.True(t, os.IsNotExist(err), "the whiteout removed the first directory's file")
		data, err := os.ReadFile(filepath.Join(root, "w", "g"))
		require.NoError(t, err)
		require.Equal(t, "g", string(data))
	})
	t.Run("apart", func(t *testing.T) {
		t.Parallel()
		producer, consumer := testutil.NewStore(t), testutil.NewOverlayStore(t)
		if err := consumer.CanMakeWhiteouts(); err != nil {
			t.Skipf("the store can't hold overlay whiteouts, which are device nodes: %v", err)
		}
		consumer.UseEngineApplier(t)
		_, err := consumer.Manager.ImportChain(t.Context(), supplied(tarChain(t, producer, layer), producer.Content))
		require.Error(t, err)
	})
}

// Paths that climb with .. stay inside the snapshot.
func TestImportChainOverlayDotDotStaysInside(t *testing.T) {
	t.Parallel()
	producer, consumer := testutil.NewStore(t), testutil.NewOverlayStore(t)
	consumer.UseEngineApplier(t)
	ref := importChain(t, consumer, tarChain(t, producer, []tarEntry{
		{name: "../escape", mode: 0o644, body: "e"},
		{name: "a/../../b", mode: 0o644, body: "b"},
		{name: "x/", mode: 0o755},
		{name: "x/../y", mode: 0o644, body: "y"},
		{name: "x/sub/", mode: 0o755},
		{name: "x/sub/../../../z", mode: 0o644, body: "z"},
	}))
	tree := testutil.OverlayTree(t, ref)
	require.Equal(t, "file 644 e", tree["escape"])
	require.Equal(t, "file 644 b", tree["b"])
	require.Equal(t, "file 644 y", tree["y"])
	require.Equal(t, "file 644 z", tree["z"])
	require.Equal(t, "dir 755", tree["x/sub"])
}

// Applied apart, a whiteout puts a device node where the layer made
// nothing; a directory of that name later in the layer replaces it. A path
// the layer didn't make can exist once it has converted a whiteout.
func TestImportChainOverlayDirectoryAfterAWhiteoutOfIt(t *testing.T) {
	t.Parallel()
	producer, consumer := testutil.NewStore(t), testutil.NewOverlayStore(t)
	if err := consumer.CanMakeWhiteouts(); err != nil {
		t.Skipf("the store can't hold overlay whiteouts, which are device nodes: %v", err)
	}
	consumer.UseEngineApplier(t)
	ref := importChain(t, consumer, tarChain(t, producer, []tarEntry{
		{name: ".wh.q"},
		{name: "q/", mode: 0o755},
		{name: "q/f", mode: 0o644, body: "f"},
	}))
	tree := testutil.OverlayTree(t, ref)
	require.Equal(t, "dir 755", tree["q"])
	require.Equal(t, "file 644 f", tree["q/f"])
}
