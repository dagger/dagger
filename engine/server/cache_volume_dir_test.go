package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine/cachevolumedir"
	"github.com/dagger/dagger/engine/config"
	"github.com/dagger/dagger/internal/buildkit/util/disk"
)

func TestValidateCacheVolumeDir(t *testing.T) {
	root := t.TempDir()
	srv := &Server{workerRootDir: filepath.Join(root, "worker")}

	_, err := srv.validateCacheVolumeDir("relative/dir")
	require.Error(t, err, "must be absolute")

	_, err = srv.validateCacheVolumeDir(filepath.Join(root, "worker", "cache-volumes"))
	require.Error(t, err, "must not be inside the worker state a reset removes")
	_, err = srv.validateCacheVolumeDir(filepath.Join(root, "worker"))
	require.Error(t, err)

	dir, err := srv.validateCacheVolumeDir(filepath.Join(root, "cache-volumes") + "/")
	require.NoError(t, err, "inside the engine root but outside worker/ is fine")
	require.Equal(t, filepath.Join(root, "cache-volumes"), dir)

	_, err = srv.validateCacheVolumeDir(filepath.Join(root, "workers"))
	require.NoError(t, err, "a sibling sharing the prefix isn't inside")

	// A symlink into worker/, with a leaf that doesn't exist yet.
	require.NoError(t, os.MkdirAll(filepath.Join(root, "worker"), 0o700))
	require.NoError(t, os.Symlink(filepath.Join(root, "worker"), filepath.Join(root, "link")))
	_, err = srv.validateCacheVolumeDir(filepath.Join(root, "link", "new-cache"))
	require.Error(t, err, "resolved through the symlink, it's inside worker/")
}

func TestCloseCacheVolumeDirKeepsOwnershipWhilePinned(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "cache-volumes")
	srv := &Server{workerRootDir: filepath.Join(root, "worker")}
	require.NoError(t, srv.initCacheVolumeDir(config.CacheVolumesConfig{Dir: dir}))
	pin, err := srv.cacheVolumeDir.Acquire(t.Context(), cachevolumedir.Identity{Key: "k"}, true, nil)
	require.NoError(t, err)

	require.NoError(t, srv.closeCacheVolumeDir(), "a stop with operations still mounting volumes isn't an error")
	_, err = cachevolumedir.Open(dir, cachevolumedir.Options{})
	require.ErrorIs(t, err, cachevolumedir.ErrOwned, "the directory stays owned until they finish")

	pin.Release()
	require.NoError(t, srv.closeCacheVolumeDir())
	require.True(t, srv.cacheVolumeDir.Closed())
	other, err := cachevolumedir.Open(dir, cachevolumedir.Options{})
	require.NoError(t, err, "given up once nothing uses it")
	require.NoError(t, other.Close())
}

func TestCacheVolumeDirPressure(t *testing.T) {
	root := t.TempDir()
	srv := &Server{workerRootDir: filepath.Join(root, "worker"), localCacheGCEnabled: true}
	require.False(t, srv.cacheVolumeDirPressure(t.Context()), "no directory, no pressure")

	require.NoError(t, srv.initCacheVolumeDir(config.CacheVolumesConfig{Dir: filepath.Join(root, "cache-volumes")}))
	t.Cleanup(func() { _ = srv.closeCacheVolumeDir() })
	// The default limits want 20% of the filesystem free: 19e9 of 100e9, as
	// the engine rounds percentages.
	dstat := disk.DiskStat{Total: 100e9, Available: 19e9}
	srv.cacheVolumeDirDiskStat = func(string) (disk.DiskStat, error) { return dstat, nil }
	require.False(t, srv.cacheVolumeDirPressure(t.Context()))

	// Less free than that: pressure, which the GC monitor acts on.
	dstat.Available = 19e9 - 1
	require.True(t, srv.cacheVolumeDirPressure(t.Context()))
	require.True(t, srv.localCachePressureGCNeeded(t.Context()))

	// With a capacity, the limit is the directory's use, which GC passes
	// check; the filesystem's free space isn't.
	srv.cacheVolumesCfg.CapacityBytes = 100e9
	require.False(t, srv.cacheVolumeDirPressure(t.Context()))
	srv.cacheVolumesCfg.CapacityBytes = 0

	srv.localCacheGCEnabled = false
	require.False(t, srv.cacheVolumeDirPressure(t.Context()), "not with GC disabled")
}

func TestInitCacheVolumeDirFallsBackWhenOwned(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "cache-volumes")
	holder, err := cachevolumedir.Open(dir, cachevolumedir.Options{})
	require.NoError(t, err)

	srv := &Server{workerRootDir: filepath.Join(root, "worker")}
	require.NoError(t, srv.initCacheVolumeDir(config.CacheVolumesConfig{Dir: dir}), "an owned directory doesn't stop the engine")
	require.Nil(t, srv.cacheVolumeDir, "cache volumes stay snapshots")

	require.NoError(t, holder.Close())
	require.NoError(t, srv.initCacheVolumeDir(config.CacheVolumesConfig{Dir: dir}))
	require.NotNil(t, srv.cacheVolumeDir)
	require.NoError(t, srv.closeCacheVolumeDir())
	require.True(t, srv.cacheVolumeDir.Closed())

	require.NoError(t, srv.initCacheVolumeDir(config.CacheVolumesConfig{}), "off when unset")
	require.Nil(t, srv.cacheVolumeDir)
}

func TestCacheVolumeDirPolicy(t *testing.T) {
	root := t.TempDir()
	srv := &Server{workerRootDir: filepath.Join(root, "worker")}
	require.NoError(t, srv.initCacheVolumeDir(config.CacheVolumesConfig{Dir: filepath.Join(root, "cache-volumes")}))
	t.Cleanup(func() { _ = srv.closeCacheVolumeDir() })
	var statted string
	srv.cacheVolumeDirDiskStat = func(dir string) (disk.DiskStat, error) {
		statted = dir
		return disk.DiskStat{Total: 200e9, Available: 50e9}, nil
	}

	// Unset capacity: the engine's default limits for the directory's own
	// filesystem, with percentages rounded as the engine rounds them.
	policy, err := srv.cacheVolumeDirPolicy(false)
	require.NoError(t, err)
	require.Equal(t, srv.cacheVolumeDir.Dir(), statted)
	require.Equal(t, cachevolumedir.Policy{
		ReservedSpace:    10e9,
		MaxUsedSpace:     140e9,
		MinFreeSpace:     38e9,
		CurrentFreeSpace: 50e9,
	}, policy)

	// A capacity: 80% of it, whatever the filesystem says.
	srv.cacheVolumesCfg.CapacityBytes = 100e9
	policy, err = srv.cacheVolumeDirPolicy(true)
	require.NoError(t, err)
	require.Equal(t, cachevolumedir.Policy{
		MaxUsedSpace:     80e9,
		CurrentFreeSpace: 50e9,
		All:              true,
	}, policy)

	// The real filesystem, unstubbed.
	srv.cacheVolumeDirDiskStat = nil
	srv.cacheVolumesCfg.CapacityBytes = 0
	policy, err = srv.cacheVolumeDirPolicy(false)
	require.NoError(t, err)
	require.Positive(t, policy.MaxUsedSpace)
}

// The GC monitor checks pressure outside gcmu, concurrently with a graceful
// stop closing the directory. Run with -race: the check must neither race the
// close nor use the store after it.
func TestCacheVolumeDirPressureDuringClose(t *testing.T) {
	root := t.TempDir()
	srv := &Server{workerRootDir: filepath.Join(root, "worker"), localCacheGCEnabled: true}
	require.NoError(t, srv.initCacheVolumeDir(config.CacheVolumesConfig{Dir: filepath.Join(root, "cache-volumes")}))

	stop := make(chan struct{})
	checked := make(chan struct{})
	go func() {
		defer close(checked)
		for {
			select {
			case <-stop:
				return
			default:
				srv.cacheVolumeDirPressure(context.Background())
			}
		}
	}()
	require.NoError(t, srv.closeCacheVolumeDir())
	close(stop)
	<-checked
	require.False(t, srv.cacheVolumeDirPressure(t.Context()), "a closed directory has no pressure to act on")
}
