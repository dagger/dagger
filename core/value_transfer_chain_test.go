package core

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
	"github.com/stretchr/testify/require"
)

type transferObservedSnapshots struct {
	bkcache.SnapshotManager
	mu     sync.Mutex
	opens  []string
	failOn string
}

func (m *transferObservedSnapshots) GetBySnapshotID(ctx context.Context, id string, opts ...bkcache.RefOption) (bkcache.ImmutableRef, error) {
	m.mu.Lock()
	m.opens = append(m.opens, id)
	failed := m.failOn == id
	m.mu.Unlock()
	if failed {
		return nil, fmt.Errorf("injected snapshot open failure")
	}
	return m.SnapshotManager.GetBySnapshotID(ctx, id, opts...)
}

func TestValueTransferPartsContainerMount(t *testing.T) {
	producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
	sibling, _ := producer.Build(t, nil, "sibling.txt", "unselected bytes")
	selected, _ := producer.Build(t, nil, "selected.txt", "mount bytes")
	observed := &transferObservedSnapshots{SnapshotManager: producer.Manager}
	producer.Manager = observed
	ctx, cache, srv := transferCache(t, producer, filepath.Join(t.TempDir(), "a.db"), "a")
	platform := Platform{OS: "linux", Architecture: "amd64"}
	newDir := func(ref bkcache.ImmutableRef) *Directory {
		dir := &Directory{Dir: new(LazyAccessor[string, *Directory]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]), Platform: platform}
		dir.SetPath("/")
		dir.SetSnapshot(ref)
		return dir
	}
	ctr := NewContainer(platform)
	ctr.FS.setValue(newDir(sibling))
	mount := new(LazyAccessor[*Directory, *Container])
	mount.setValue(newDir(selected))
	ctr.Mounts = ContainerMounts{{Target: "/selected", DirectorySource: mount}}
	result := attachTransferObject(t, ctx, cache, srv, "a", "mountContainer", ctr)
	observed.opens = nil
	selection := dagql.ValueSelection{Roots: []dagql.AnyResult{result}, Outputs: []dagql.SelectedValueOutput{{Result: result, Address: dagql.PersistedPartAddress{Part: "mount:/selected"}}}}
	require.NoError(t, cache.WithExportedValues(ctx, selection, config.RefConfig{Compression: compression.New(compression.Uncompressed)}, func(ctx context.Context, values *dagql.ExportedValues) error {
		require.Len(t, values.Chains.Entries, 1)
		chain := values.Chains.Entries[0]
		ref, err := consumer.Manager.ImportChain(ctx, &bkcache.ExportChain{Layers: chain.Layers, Provider: chain.Provider})
		require.NoError(t, err)
		defer ref.Release(context.WithoutCancel(ctx))
		testutil.CheckFile(t, ref, "selected.txt", "mount bytes")
		return nil
	}))
	require.Equal(t, []string{selected.SnapshotID()}, observed.opens, "unselected filesystem sibling must not open")
	observed.failOn = selected.SnapshotID()
	err := cache.WithExportedValues(ctx, selection, config.RefConfig{}, func(context.Context, *dagql.ExportedValues) error {
		t.Fatal("broken completed link delivered")
		return nil
	})
	require.ErrorContains(t, err, "injected snapshot open failure")
}
