package core

import (
	"bytes"
	"context"
	"os"
	"path/filepath"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	bolt "go.etcd.io/bbolt"
)

func (EngineSuite) TestCorruptBoltDBStartupRecovery(ctx context.Context, t *testctx.T) {
	for _, metadataPath := range []string{"worker/containerdmeta.db", "worker/snapshots/metadata.db"} {
		t.Run(metadataPath, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			fixture := filepath.Join(t.TempDir(), "corrupt.db")
			writeCorruptEngineMetadata(t, fixture)
			ctr := devEngineContainer(c).
				WithMountedFile("/tmp/corrupt.db", c.Host().File(fixture)).
				WithEnvVariable("METADATA_PATH", "/var/lib/dagger/"+metadataPath).
				WithEnvVariable("_EXPERIMENTAL_DAGGER_METRICS_ADDR", "0.0.0.0:9090").
				WithExposedPort(9090).
				WithExec([]string{"sh", "-ec", `
mkdir -p "$(dirname "$METADATA_PATH")"
cp /tmp/corrupt.db "$METADATA_PATH"
printf old > /var/lib/dagger/worker/old-snapshot
printf old > /var/lib/dagger/dagql-cache.db
`})
			devEngine := devEngineContainerAsService(ctr)
			client := engineClientContainer(ctx, t, c, devEngine)
			out, err := client.WithExec([]string{"dagger", "query"}, dagger.ContainerWithExecOpts{
				Stdin: `{ container { from(address: "` + alpineImage + `") { withExec(args: ["echo", "recovered"]) { stdout } } } }`,
			}).Stdout(ctx)
			require.NoError(t, err)
			require.Contains(t, out, "recovered")

			// The restarted engine must report the reset, even though it was
			// performed by its supervisor rather than Server.initLocalCacheState.
			_, err = client.WithExec([]string{"sh", "-ec", `
for attempt in 1 2 3 4 5; do
  wget -qO- http://dev-engine:9090/metrics | grep -q '^dagger_local_cache_corrupt_db_reset 1$' && exit 0
  sleep 1
done
exit 1
`}).Sync(ctx)
			require.NoError(t, err)
		})
	}
}

func writeCorruptEngineMetadata(t *testctx.T, path string) {
	t.Helper()
	db, err := bolt.Open(path, 0600, &bolt.Options{NoFreelistSync: true})
	require.NoError(t, err)
	require.NoError(t, db.Update(func(tx *bolt.Tx) error {
		bucket, err := tx.CreateBucket([]byte("test"))
		if err != nil {
			return err
		}
		return bucket.Put([]byte("key"), bytes.Repeat([]byte("value"), 2000))
	}))
	var offset int64
	require.NoError(t, db.View(func(tx *bolt.Tx) error {
		offset = int64(tx.Bucket([]byte("test")).Root()) * int64(db.Info().PageSize)
		return nil
	}))
	require.NoError(t, db.Close())
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	require.NoError(t, err)
	_, err = file.WriteAt(make([]byte, 8), offset)
	require.NoError(t, err)
	require.NoError(t, file.Close())
}
