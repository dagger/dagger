package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/containerd/containerd/v2/core/leases"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/stretchr/testify/require"
)

// Mounting a snapshot again, through any ref to it and read-only or not,
// shares the first mount instead of making another view of the snapshot.
func TestMountRefSharesSnapshotMounts(t *testing.T) {
	ctx := context.Background()
	store := testutil.NewStore(t)
	var views atomic.Int64
	store.AfterAdd = func(_ context.Context, _ leases.Lease, r leases.Resource) {
		if strings.HasSuffix(r.ID, "-view") {
			views.Add(1)
		}
	}
	ref, _ := store.Build(t, nil, "f", "contents")
	again, err := store.Manager.GetBySnapshotID(ctx, ref.SnapshotID())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, again.Release(context.Background())) })

	read := func(root string, _ *mount.Mount) error {
		data, err := os.ReadFile(filepath.Join(root, "f"))
		if err == nil && string(data) != "contents" {
			t.Errorf("read %q", data)
		}
		return err
	}
	require.NoError(t, MountRef(ctx, ref, read, mountRefAsReadOnly))
	require.NoError(t, MountRef(ctx, ref, read, mountRefAsReadOnly))
	require.NoError(t, MountRef(ctx, again, read, mountRefAsReadOnly))
	require.NoError(t, MountRef(ctx, again, read))
	require.EqualValues(t, 1, views.Load(), "one view of the snapshot for all four mounts")
	require.EqualValues(t, 1, store.LocalMounts.Load())
}
