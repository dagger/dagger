package server

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/containerd/containerd/v2/core/content"
	localcontentstore "github.com/containerd/containerd/v2/plugins/content/local"
	"github.com/dagger/dagger/engine/config"
	bkconfig "github.com/dagger/dagger/internal/buildkit/cmd/buildkitd/config"
	"github.com/stretchr/testify/require"
)

// The snapshot manager takes the builtin image store at construction, so
// the store must be open before the local cache state is initialized: the
// production order in NewServer, enforced by initLocalCacheStateOnce.
func TestLocalCacheStateTakesTheBuiltinStore(t *testing.T) {
	t.Parallel()
	newServer := func(t *testing.T) *Server {
		t.Helper()
		root := t.TempDir()
		srv := &Server{rootDir: root}
		srv.workerRootDir = filepath.Join(root, "worker")
		srv.snapshotterRootDir = filepath.Join(srv.workerRootDir, "snapshots")
		srv.snapshotterDBPath = filepath.Join(srv.snapshotterRootDir, "metadata.db")
		srv.contentStoreRootDir = filepath.Join(srv.workerRootDir, "content")
		srv.containerdMetaDBPath = filepath.Join(srv.workerRootDir, "containerdmeta.db")
		srv.workerCacheMetaDBPath = filepath.Join(srv.workerRootDir, "metadata_v2.db")
		srv.buildkitMountPoolDir = filepath.Join(srv.workerRootDir, "cachemounts")
		srv.executorRootDir = filepath.Join(srv.workerRootDir, "executor")
		return srv
	}

	t.Run("wired", func(t *testing.T) {
		t.Parallel()
		srv := newServer(t)
		builtin, err := localcontentstore.NewStore(t.TempDir())
		require.NoError(t, err)
		srv.builtinContentStore = builtin
		reset, err := srv.initLocalCacheStateOnce(context.Background(), config.Config{}, bkconfig.OCIConfig{})
		require.NoError(t, err)
		require.Equal(t, localCacheStateResetNone, reset)
		t.Cleanup(func() { require.NoError(t, srv.closeLocalCacheStateForReset()) })
		manager, ok := srv.workerCache.(interface {
			BuiltinContent() content.InfoReaderProvider
		})
		require.True(t, ok, "the worker cache exposes its builtin store")
		require.Same(t, builtin, manager.BuiltinContent(), "the manager holds the server's builtin store")
	})
	t.Run("refused without the store", func(t *testing.T) {
		t.Parallel()
		srv := newServer(t)
		_, err := srv.initLocalCacheStateOnce(context.Background(), config.Config{}, bkconfig.OCIConfig{})
		require.ErrorContains(t, err, "builtin content store must be opened before")
	})
}
