package core

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
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
	// The nested view is built already evaluated: Directory.directory and
	// Directory.file evaluate through a read-only mount, so the real view is
	// native TestHostInputs' to prove. What is exported for a file deep inside
	// a larger snapshot is the same either way.
	file := &File{File: new(LazyAccessor[string, *File]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *File]), Platform: Platform{OS: "linux", Architecture: "amd64"}}
	file.SetPath("/visible/value.txt")
	file.SetSnapshot(tree)
	fileResult := attachTransferObject(t, ctx, cache, srv, "a", "nestedFile", file)
	require.NoError(t, cache.Evaluate(ctx, fileResult))
	observed.opens = nil
	var bundle dagql.ValueBundle
	var borrowed *dagql.SelectedChains
	err := cache.WithExportedValues(ctx, dagql.ValueSelection{Roots: []dagql.AnyResult{fileResult}, Outputs: []dagql.SelectedValueOutput{{Result: fileResult, Address: dagql.PersistedPartAddress{Part: "snapshot"}}}}, config.RefConfig{Compression: compression.New(compression.Uncompressed)}, func(ctx context.Context, values *dagql.ExportedValues) error {
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
	importedReply, err := b.MergeValues(bctx, dagql.CloudCacheID, bundle)
	imported := importedReply.Imported()
	require.NoError(t, err)
	loaded, err := b.LoadResultByResultID(bctx, "b", bsrv, imported[0].ResultID)
	require.NoError(t, err)
	pending := loaded.(dagql.ObjectResult[*File])
	require.Equal(t, "/visible/value.txt", mustTransferPath(t, bctx, pending))
	require.NoError(t, b.Evaluate(bctx, pending))
	contents := demandedFileContents(t, bctx, pending)
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

// Spike 2 of the engine-service protocol design (6.2), kept as a test of
// merge. A three-container chain's leaf is exported with only its fs: the
// bundle carries the three records and the leaf's whole chain, opening only
// the leaf's snapshot. Merged into B, the parent's fs is pending with no route
// (these fixtures record no operation). A second bundle offering the parent's
// own fs lands on the same parent entry, and the parent installs from its own
// offer: from no layer reads once the leaf's chain is local, and from its two
// layers otherwise.
func TestValueMergeChainSecondBundleLandsOnTheParent(t *testing.T) {
	for _, readLeaf := range []bool{true, false} {
		name := "leaf read first"
		if !readLeaf {
			name = "leaf not read"
		}
		t.Run(name, func(t *testing.T) {
			aStore, bStore := testutil.NewStore(t), testutil.NewStore(t)
			s0, _ := aStore.Build(t, nil, "base.txt", "base bytes")
			s1, _ := aStore.Build(t, s0, "one.txt", "step one")
			s2, _ := aStore.Build(t, s1, "two.txt", "step two")
			observed := &transferObservedSnapshots{SnapshotManager: aStore.Manager}
			aStore.Manager = observed
			actx, a, asrv := transferCache(t, aStore, "", "a")
			platform := Platform{OS: "linux", Architecture: "amd64"}
			base := NewContainer(platform)
			base.FS.setValue(partTestDirectory(s0, "/"))
			pBase := attachTransferObject(t, actx, a, asrv, "a", "chainBase", base)
			c1 := NewContainer(platform)
			c1.FS.setValue(partTestDirectory(s1, "/"))
			p1 := attachDelegationChild(t, actx, a, asrv, "a", "chainStep", pBase, c1)
			c2 := NewContainer(platform)
			c2.FS.setValue(partTestDirectory(s2, "/"))
			p2 := attachDelegationChild(t, actx, a, asrv, "a", "chainStep", p1, c2)
			uncompressed := config.RefConfig{Compression: compression.New(compression.Uncompressed)}
			bctx, b, bsrv := transferCache(t, bStore, "", "b")

			observed.opens = nil
			var parentID uint64
			var parent dagql.AnyResult
			leafSelection := dagql.ValueSelection{Roots: []dagql.AnyResult{p2}, Outputs: []dagql.SelectedValueOutput{{Result: p2, Address: dagql.PersistedPartAddress{Part: "fs"}}}}
			require.NoError(t, a.WithExportedValues(actx, leafSelection, uncompressed, func(_ context.Context, ex *dagql.ExportedValues) error {
				require.Len(t, ex.Bundle.Values, 3, "the records of the whole chain travel")
				require.Len(t, ex.Chains.Entries, 1, "one selected part, one chain")
				require.Len(t, ex.Chains.Entries[0].Layers, 3, "the leaf's chain is whole")
				require.Equal(t, []string{s2.SnapshotID()}, observed.opens, "only the leaf's snapshot is opened")
				require.Len(t, ex.Bundle.Outputs, 1)

				leafProvider := &testutil.Provider{InfoReaderProvider: ex.Chains.Entries[0].Provider}
				b.SetPartContentSource(partTestContentSource{leafProvider})
				reply, err := b.MergeValues(bctx, dagql.CloudCacheID, ex.Bundle)
				require.NoError(t, err)
				leafID := reply.Imported()[0].ResultID
				leafFrame, err := b.ResultCallByResultID(bctx, "", leafID)
				require.NoError(t, err)
				parentID = leafFrame.Receiver.ResultID
				if readLeaf {
					leaf, err := b.LoadResultByResultID(bctx, "", bsrv, leafID)
					require.NoError(t, err)
					require.NoError(t, b.EvaluateParts(bctx, leaf, ContainerPartFS))
					require.Equal(t, int64(3), leafProvider.Reads.Load(), "the leaf's chain")
					dir, ok := leaf.(dagql.ObjectResult[*Container]).Self().FS.Peek()
					require.True(t, ok)
					snap, ok := dir.Snapshot.Peek()
					require.True(t, ok)
					testutil.CheckFile(t, snap, "two.txt", "step two")
					testutil.CheckFile(t, snap, "base.txt", "base bytes")
				}
				parent, err = b.LoadResultByResultID(bctx, "", bsrv, parentID)
				require.NoError(t, err)
				reads := leafProvider.Reads.Load()
				require.ErrorIs(t, b.EvaluateParts(bctx, parent, ContainerPartFS), dagql.ErrUnavailablePart, "the parent's fs is pending")
				require.Equal(t, reads, leafProvider.Reads.Load(), "no bytes fetched for the parent")
				return nil
			}))

			observed.opens = nil
			parentSelection := dagql.ValueSelection{Roots: []dagql.AnyResult{p1}, Outputs: []dagql.SelectedValueOutput{{Result: p1, Address: dagql.PersistedPartAddress{Part: "fs"}}}}
			require.NoError(t, a.WithExportedValues(actx, parentSelection, uncompressed, func(_ context.Context, ex *dagql.ExportedValues) error {
				require.Equal(t, []string{s1.SnapshotID()}, observed.opens)
				parentProvider := &testutil.Provider{InfoReaderProvider: ex.Chains.Entries[0].Provider}
				b.SetPartContentSource(partTestContentSource{parentProvider})
				reply, err := b.MergeValues(bctx, dagql.CloudCacheID, ex.Bundle)
				require.NoError(t, err)
				require.Equal(t, parentID, reply.Imported()[0].ResultID, "the second bundle lands on the same parent entry")
				require.Len(t, reply.Values[len(reply.Values)-1].OfferedParts, 1, "with an offer of its fs")
				require.NoError(t, b.EvaluateParts(bctx, parent, ContainerPartFS))
				want := int64(2)
				if readLeaf {
					want = 0
				}
				require.Equal(t, want, parentProvider.Reads.Load(), "layer reads for the parent's own offer")
				dir, ok := parent.(dagql.ObjectResult[*Container]).Self().FS.Peek()
				require.True(t, ok)
				snap, ok := dir.Snapshot.Peek()
				require.True(t, ok)
				testutil.CheckFile(t, snap, "one.txt", "step one")
				testutil.CheckFile(t, snap, "base.txt", "base bytes")
				return nil
			}))
		})
	}
}
