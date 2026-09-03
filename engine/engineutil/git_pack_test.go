package engineutil

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGitPackSpool(t *testing.T) {
	spool, err := newGitPackSpool("dagger-git-pack-test-*", 8)
	require.NoError(t, err)
	t.Cleanup(func() { _ = spool.remove() })

	require.NoError(t, spool.write([]byte("pack")))
	require.NoError(t, spool.write([]byte("data")))
	path, err := spool.finish()
	require.NoError(t, err)
	require.FileExists(t, path)

	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, []byte("packdata"), contents)
}

func TestGitPackSpoolRejectsAggregateOverflow(t *testing.T) {
	spool, err := newGitPackSpool("dagger-git-pack-limit-test-*", 5)
	require.NoError(t, err)
	t.Cleanup(func() { _ = spool.remove() })

	require.NoError(t, spool.write([]byte("1234")))
	err = spool.write([]byte("56"))
	require.ErrorContains(t, err, "exceeds limit 5")
	require.Equal(t, int64(4), spool.size)
}

func TestGitPackCloseRemovesOwnedFiles(t *testing.T) {
	bundle, err := os.CreateTemp("", "dagger-checkout-pack-close-test-*")
	require.NoError(t, err)
	require.NoError(t, bundle.Close())
	checkout := &GitCheckoutPack{BundlePath: bundle.Name()}
	require.NoError(t, checkout.Close())
	require.NoFileExists(t, bundle.Name())
	require.NoError(t, checkout.Close())

	patch, err := os.CreateTemp("", "dagger-uncommitted-pack-close-test-*")
	require.NoError(t, err)
	require.NoError(t, patch.Close())
	uncommitted := &GitUncommittedPack{PatchPath: patch.Name()}
	require.NoError(t, uncommitted.Close())
	require.NoFileExists(t, patch.Name())
	require.NoError(t, uncommitted.Close())
}
