package snapshots_test

import (
	"fmt"
	"math/rand/v2"
	"testing"

	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/stretchr/testify/require"
)

// benchTreeBytes is the size of a benchmark's content-hash trees: about the
// mean of a soak's (0.9 GB of trees for 36k snapshots).
const benchTreeBytes = 25 << 10

func benchTree(rng *rand.Rand, size int) []byte {
	dt := make([]byte, size)
	for i := range dt {
		dt[i] = byte(rng.Uint32())
	}
	return dt
}

// ownedTreeRows builds n owned snapshots and returns a checkpoint's rows with
// a tree for each, as a restart loads them.
func ownedTreeRows(b *testing.B, store *testutil.Store, n int) bkcache.PersistentMetadataRows {
	b.Helper()
	rng := rand.New(rand.NewPCG(1, 2))
	var rows bkcache.PersistentMetadataRows
	for range n {
		ref, _ := store.BuildWith(b, nil, nil)
		rows.ContentHashes = append(rows.ContentHashes, bkcache.SnapshotContentHashRow{
			SnapshotID: ref.SnapshotID(),
			Data:       benchTree(rng, benchTreeBytes),
		})
	}
	return rows
}

// The startup cost of the trees: loading a checkpoint's, which writes each
// to disk before the engine serves.
func BenchmarkLoadContentHashes(b *testing.B) {
	for _, n := range []int{1000, 10000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			store := testutil.NewStore(b)
			rows := ownedTreeRows(b, store, n)
			b.SetBytes(int64(n * benchTreeBytes))
			for b.Loop() {
				require.NoError(b, store.Manager.LoadPersistentMetadata(rows))
			}
		})
	}
}

// The shutdown cost of the trees: writing a checkpoint, which reads each
// back.
func BenchmarkCheckpointContentHashes(b *testing.B) {
	for _, n := range []int{1000, 10000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			store := testutil.NewStore(b)
			rows := ownedTreeRows(b, store, n)
			require.NoError(b, store.Manager.LoadPersistentMetadata(rows))
			b.SetBytes(int64(n * benchTreeBytes))
			for b.Loop() {
				got := store.Manager.PersistentMetadataRows()
				require.Len(b, got.ContentHashes, n)
			}
		})
	}
}

// The cost of one save of a tree, which contenthash makes under its cache
// context's lock.
func BenchmarkSaveContentHash(b *testing.B) {
	for _, size := range []int{4 << 10, benchTreeBytes, 1 << 20} {
		b.Run(fmt.Sprintf("%dKiB", size>>10), func(b *testing.B) {
			store := testutil.NewStore(b)
			ref, _ := store.BuildWith(b, nil, nil)
			md := ref.(bkcache.RefMetadata)
			dt := benchTree(rand.New(rand.NewPCG(1, 2)), size)
			b.SetBytes(int64(size))
			for b.Loop() {
				require.NoError(b, md.SetExternal(bkcache.ContentHashMetadataKey, dt))
			}
		})
	}
}
