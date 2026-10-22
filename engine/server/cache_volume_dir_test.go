package server

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine/cachevolumedir"
	"github.com/dagger/dagger/engine/config"
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
	require.Nil(t, srv.cacheVolumeDir)

	require.NoError(t, srv.initCacheVolumeDir(config.CacheVolumesConfig{}), "off when unset")
	require.Nil(t, srv.cacheVolumeDir)
}

func TestCacheVolumeDirPolicy(t *testing.T) {
	root := t.TempDir()
	srv := &Server{workerRootDir: filepath.Join(root, "worker")}
	require.NoError(t, srv.initCacheVolumeDir(config.CacheVolumesConfig{Dir: filepath.Join(root, "cache-volumes")}))
	t.Cleanup(func() { _ = srv.closeCacheVolumeDir() })

	policy, err := srv.cacheVolumeDirPolicy(false)
	require.NoError(t, err)
	require.Positive(t, policy.MaxUsedSpace, "unset space defaults from the directory's filesystem")
	require.False(t, policy.All)

	srv.cacheVolumesCfg.MaxUsedSpace = config.DiskSpace{Bytes: 80e9}
	srv.cacheVolumesCfg.KeepDuration.Duration = 3600e9
	policy, err = srv.cacheVolumeDirPolicy(true)
	require.NoError(t, err)
	require.EqualValues(t, 80e9, policy.MaxUsedSpace)
	require.EqualValues(t, 3600e9, policy.KeepDuration)
	require.True(t, policy.All)
}
