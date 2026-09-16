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

func TestValueTransferPartsSelectedChain(t *testing.T) {
	producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
	prefix, _ := producer.Build(t, nil, "private.txt", "whole parent bytes")
	tree, _ := producer.Build(t, prefix, "visible/value.txt", "selected bytes")
	observed := &transferObservedSnapshots{SnapshotManager: producer.Manager}
	producer.Manager = observed
	ctx, cache, srv := transferCache(t, producer, filepath.Join(t.TempDir(), "a.db"), "a")
	root := &Directory{Dir: new(LazyAccessor[string, *Directory]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]), Platform: Platform{OS: "linux", Architecture: "amd64"}}
	root.SetPath("/")
	root.SetSnapshot(tree)
	rootResult := attachTransferObject(t, ctx, cache, srv, "a", "hostCapture", root)
	view, err := root.Subdirectory(ctx, rootResult, "visible")
	require.NoError(t, err)
	viewResult := attachTransferObject(t, ctx, cache, srv, "a", "view", view)
	require.NoError(t, cache.Evaluate(ctx, viewResult))
	file, err := view.Subfile(ctx, viewResult, "value.txt")
	require.NoError(t, err)
	fileResult := attachTransferObject(t, ctx, cache, srv, "a", "nestedFile", file)
	require.NoError(t, cache.Evaluate(ctx, fileResult))
	observed.opens = nil
	var bundle dagql.ValueBundle
	var borrowed *dagql.SelectedChains
	err = cache.WithExportedValues(ctx, dagql.ValueSelection{Roots: []dagql.AnyResult{fileResult}, Outputs: []dagql.SelectedValueOutput{{Result: fileResult, Address: dagql.PersistedPartAddress{Part: "snapshot"}}}}, config.RefConfig{Compression: compression.New(compression.Uncompressed)}, func(ctx context.Context, values *dagql.ExportedValues) error {
		bundle, borrowed = values.Bundle, values.Chains
		require.Len(t, values.Chains.Entries, 1)
		entry := values.Chains.Entries[0]
		opened, err := consumer.Manager.ImportChain(ctx, &bkcache.ExportChain{Layers: entry.Layers, Provider: entry.Provider})
		require.NoError(t, err)
		defer opened.Release(context.WithoutCancel(ctx))
		testutil.CheckFile(t, opened, "private.txt", "whole parent bytes")
		testutil.CheckFile(t, opened, "visible/value.txt", "selected bytes")
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, []string{tree.SnapshotID()}, observed.opens, "the view opens its exact parent snapshot once")
	require.NoError(t, borrowed.Release(ctx), "chain release is idempotent after the callback")
	require.Len(t, bundle.Outputs, 1)
	require.NotNil(t, bundle.Outputs[0].Owner)
	bctx, b, bsrv := transferCache(t, consumer, filepath.Join(t.TempDir(), "b.db"), "b")
	imported, err := b.ImportValues(bctx, bundle)
	require.NoError(t, err)
	loaded, err := b.LoadResultByResultID(bctx, "b", bsrv, imported[0].ResultID)
	require.NoError(t, err)
	pending := loaded.(dagql.ObjectResult[*File])
	require.Equal(t, "/visible/value.txt", mustTransferPath(t, bctx, pending))
	require.NoError(t, b.Evaluate(bctx, pending))
	contents, err := pending.Self().Contents(bctx, pending, nil, nil)
	require.NoError(t, err)
	require.Equal(t, "selected bytes", string(contents))
	require.NoError(t, b.WithExportedValues(bctx, dagql.ValueSelection{Roots: []dagql.AnyResult{pending}}, config.RefConfig{}, func(_ context.Context, forward *dagql.ExportedValues) error {
		require.Empty(t, forward.Chains.Entries)
		require.Empty(t, forward.Bundle.Outputs)
		require.Empty(t, forward.Bundle.Values[len(forward.Bundle.Values)-1].Record.Envelope.PendingOffers)
		return nil
	}))
}
func mustTransferPath(t *testing.T, ctx context.Context, file dagql.ObjectResult[*File]) string {
	t.Helper()
	path, err := file.Self().PathOrEval(ctx, file)
	require.NoError(t, err)
	return path
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
