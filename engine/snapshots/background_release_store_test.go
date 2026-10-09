package snapshots_test

import (
	"context"
	"strings"
	"testing"

	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/stretchr/testify/require"
)

// mountViewLeases counts the leases that hold a read-only mount's view.
func mountViewLeases(t *testing.T, store *testutil.Store) int {
	t.Helper()
	ctx := context.Background()
	all, err := store.Leases.List(ctx)
	require.NoError(t, err)
	n := 0
	for _, l := range all {
		resources, err := store.Leases.ListResources(ctx, l)
		require.NoError(t, err)
		for _, r := range resources {
			if strings.HasSuffix(r.ID, "-view") {
				n++
				break
			}
		}
	}
	return n
}

// An immutable ref releases a read-only mount in the background: the caller
// does not wait for the release, which deletes the view lease once it runs,
// and WaitForBackgroundReleases waits for it.
func TestReadOnlyMountReleasesInBackground(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := testutil.NewStore(t)
	ref, owner := buildSnapshot(t, store, nil, "a.txt", "a")

	background, ok := ref.(bkcache.BackgroundReleaser)
	require.True(t, ok, "an immutable ref releases its read-only mounts in the background")

	mounted, err := ref.Mount(ctx, true)
	require.NoError(t, err)
	_, release, err := mounted.Mount()
	require.NoError(t, err)
	require.Equal(t, 1, mountViewLeases(t, store))

	proceed, released := make(chan struct{}), make(chan struct{})
	background.ReleaseInBackground(func() error {
		<-proceed
		defer close(released)
		return release()
	})
	require.Equal(t, 1, mountViewLeases(t, store), "the release runs after the caller returns")
	close(proceed)
	<-released
	require.Zero(t, mountViewLeases(t, store))

	mounted, err = ref.Mount(ctx, true)
	require.NoError(t, err)
	_, release, err = mounted.Mount()
	require.NoError(t, err)
	background.ReleaseInBackground(release)
	waiter, ok := store.Manager.(bkcache.BackgroundReleaseWaiter)
	require.True(t, ok)
	waiter.WaitForBackgroundReleases()
	require.Zero(t, mountViewLeases(t, store), "waiting for background releases waits for this one")

	require.NoError(t, ref.Release(ctx))
	require.NoError(t, store.Manager.RemoveLease(ctx, owner))
}
