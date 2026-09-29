package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

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
// merge, with the ancestor prefixes of 6.1. A three-container chain's leaf is
// exported with only its fs selected: the bundle carries the three records,
// the leaf's whole chain, and the parent's and base's fs as prefixes of it,
// opening only the leaf's snapshot. On B, a pipeline branching from the
// middle reads the parent's fs from its prefix offer: from no layer reads
// once the leaf's chain is local, and from its two layers otherwise. A second
// bundle of the parent's own fs lands on the same parent entry.
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
				require.Equal(t, []int{1, 2, 3}, transferOutputChainLengths(ex.Bundle), "the leaf's chain and its two prefixes")

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
				require.NoError(t, b.EvaluateParts(bctx, parent, ContainerPartFS), "the parent's fs installs from its prefix offer")
				want := int64(2)
				if readLeaf {
					want = 0
				}
				require.Equal(t, want, leafProvider.Reads.Load()-reads, "layer reads for the parent's prefix")
				dir, ok := parent.(dagql.ObjectResult[*Container]).Self().FS.Peek()
				require.True(t, ok)
				snap, ok := dir.Snapshot.Peek()
				require.True(t, ok)
				testutil.CheckFile(t, snap, "one.txt", "step one")
				testutil.CheckFile(t, snap, "base.txt", "base bytes")
				_, err = os.Stat(filepath.Join(testutil.Root(t, snap), "two.txt"))
				require.ErrorIs(t, err, os.ErrNotExist, "the parent's own snapshot, not the leaf's")
				return nil
			}))

			observed.opens = nil
			parentSelection := dagql.ValueSelection{Roots: []dagql.AnyResult{p1}, Outputs: []dagql.SelectedValueOutput{{Result: p1, Address: dagql.PersistedPartAddress{Part: "fs"}}}}
			require.NoError(t, a.WithExportedValues(actx, parentSelection, uncompressed, func(_ context.Context, ex *dagql.ExportedValues) error {
				require.Equal(t, []string{s1.SnapshotID()}, observed.opens)
				reply, err := b.MergeValues(bctx, dagql.CloudCacheID, ex.Bundle)
				require.NoError(t, err)
				require.Equal(t, parentID, reply.Imported()[0].ResultID, "the second bundle lands on the same parent entry")
				last := reply.Values[len(reply.Values)-1]
				require.Contains(t, last.Parts, dagql.PersistedPartAddress{Part: "fs"}, "whose fs is complete")
				require.Empty(t, last.OfferedParts, "and takes no offer for it")
				return nil
			}))
		})
	}
}

// transferOutputChainLengths returns the bundle's output chain lengths,
// sorted.
func transferOutputChainLengths(bundle dagql.ValueBundle) []int {
	var lengths []int
	for _, output := range bundle.Outputs {
		if output.Chain != nil {
			lengths = append(lengths, len(output.Chain.Layers))
		}
	}
	slices.Sort(lengths)
	return lengths
}

// transferOutputOrdinals returns the ordinals of the bundle's outputs with a
// chain, by their chain length.
func transferOutputOrdinals(bundle dagql.ValueBundle) map[dagql.TransferOrdinal]int {
	out := map[dagql.TransferOrdinal]int{}
	for _, output := range bundle.Outputs {
		if output.Chain != nil {
			out[output.Ordinal] = len(output.Chain.Layers)
		}
	}
	return out
}

// transferOrdinalOf returns the bundle ordinal of a sending cache's entry.
func transferOrdinalOf(t *testing.T, cache *dagql.Cache, bundle dagql.ValueBundle, res dagql.AnyResult) dagql.TransferOrdinal {
	t.Helper()
	id, err := cache.PersistedResultID(res)
	require.NoError(t, err)
	for _, value := range bundle.Values {
		if value.SenderNumber == id {
			return value.Ordinal
		}
	}
	t.Fatalf("no record of sender entry %d", id)
	return 0
}

// Exporting the tenth step of a chain of ten with its fs selected emits the
// nine steps before it as prefixes, back to the first step's single layer,
// and opens only the tenth step's snapshot.
func TestValueExportPrefixesOfAChainOfTen(t *testing.T) {
	store := testutil.NewStore(t)
	observed := &transferObservedSnapshots{SnapshotManager: store.Manager}
	store.Manager = observed
	ctx, cache, srv := transferCache(t, store, "", "a")
	platform := Platform{OS: "linux", Architecture: "amd64"}
	var snapshot bkcache.ImmutableRef
	var step dagql.ObjectResult[*Container]
	for i := range 10 {
		snapshot, _ = store.Build(t, snapshot, fmt.Sprintf("step-%d.txt", i), fmt.Sprintf("step %d", i))
		ctr := NewContainer(platform)
		ctr.FS.setValue(partTestDirectory(snapshot, "/"))
		if i == 0 {
			step = attachTransferObject(t, ctx, cache, srv, "a", "chainTenBase", ctr)
		} else {
			step = attachDelegationChild(t, ctx, cache, srv, "a", "chainTenStep", step, ctr)
		}
	}
	observed.opens = nil
	selection := dagql.ValueSelection{Roots: []dagql.AnyResult{step}, Outputs: []dagql.SelectedValueOutput{{Result: step, Address: dagql.PersistedPartAddress{Part: "fs"}}}}
	require.NoError(t, cache.WithExportedValues(ctx, selection, config.RefConfig{Compression: compression.New(compression.Uncompressed)}, func(_ context.Context, ex *dagql.ExportedValues) error {
		require.Len(t, ex.Bundle.Values, 10)
		require.Len(t, ex.Chains.Entries, 1, "only the selected part is a chain to upload")
		require.Equal(t, []string{snapshot.SnapshotID()}, observed.opens, "only the tenth step's snapshot is opened")
		require.Positive(t, ex.ChainTime, "the time opening the chain took")
		require.Equal(t, []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, transferOutputChainLengths(ex.Bundle))
		for _, output := range ex.Bundle.Outputs {
			require.Equal(t, ex.Chains.Entries[0].Layers[:len(output.Chain.Layers)], output.Chain.Layers, "each is a prefix of the selected chain")
		}
		return nil
	}))
}

// A root whose fs is its receiver's snapshot, such as a withWorkdir, is the
// selected chain's tip: the receiver gets the whole chain as its prefix
// output. A delegating child whose receiver's part the bundle offers gets no
// prefix output of its own; delegation serves it.
func TestValueExportPrefixesAndDelegation(t *testing.T) {
	store := testutil.NewStore(t)
	ctx, cache, srv := transferCache(t, store, "", "a")
	platform := Platform{OS: "linux", Architecture: "amd64"}
	s0, _ := store.Build(t, nil, "base.txt", "base bytes")
	base := NewContainer(platform)
	base.FS.setValue(partTestDirectory(s0, "/"))
	receiver := attachTransferObject(t, ctx, cache, srv, "a", "prefixReceiver", base)
	child := NewContainer(platform)
	child.FS, _ = CloneContainerDirectoryAccessor(ctx, base.FS)
	child.Config.WorkingDir = "/child"
	workdir := attachDelegationChild(t, ctx, cache, srv, "a", "withWorkdir", receiver, child)
	uncompressed := config.RefConfig{Compression: compression.New(compression.Uncompressed)}

	t.Run("the receiver of the root", func(t *testing.T) {
		selection := dagql.ValueSelection{Roots: []dagql.AnyResult{workdir}, Outputs: []dagql.SelectedValueOutput{{Result: workdir, Address: dagql.PersistedPartAddress{Part: "fs"}}}}
		require.NoError(t, cache.WithExportedValues(ctx, selection, uncompressed, func(_ context.Context, ex *dagql.ExportedValues) error {
			require.Equal(t, map[dagql.TransferOrdinal]int{
				transferOrdinalOf(t, cache, ex.Bundle, workdir):  1,
				transferOrdinalOf(t, cache, ex.Bundle, receiver): 1,
			}, transferOutputOrdinals(ex.Bundle), "the selected part, and its receiver's whole chain")
			return nil
		}))
	})
	t.Run("a delegating child", func(t *testing.T) {
		s1, _ := store.Build(t, s0, "leaf.txt", "leaf bytes")
		next := NewContainer(platform)
		next.FS.setValue(partTestDirectory(s1, "/"))
		leaf := attachDelegationChild(t, ctx, cache, srv, "a", "prefixLeaf", workdir, next)
		selection := dagql.ValueSelection{Roots: []dagql.AnyResult{leaf}, Outputs: []dagql.SelectedValueOutput{{Result: leaf, Address: dagql.PersistedPartAddress{Part: "fs"}}}}
		require.NoError(t, cache.WithExportedValues(ctx, selection, uncompressed, func(_ context.Context, ex *dagql.ExportedValues) error {
			require.Equal(t, map[dagql.TransferOrdinal]int{
				transferOrdinalOf(t, cache, ex.Bundle, leaf):     2,
				transferOrdinalOf(t, cache, ex.Bundle, receiver): 1,
			}, transferOutputOrdinals(ex.Bundle), "the withWorkdir delegates to its receiver's prefix")
			return nil
		}))
	})
}

// A part the stored record maps as absent, such as the exec metadata of a
// container built without an exec, is absent on the Cloud and never stored,
// and it stays absent when the record has expired: absence is part of the
// value's structure. The expired case sets the container's record expiry in
// the export, as the clock would, under a live root that depends on it.
func TestAvailablePartAbsentFromAStoredRecord(t *testing.T) {
	store := testutil.NewStore(t)
	ctx, a, srv := transferCache(t, store, "", "a")
	s0, _ := store.Build(t, nil, "base.txt", "base bytes")
	base := NewContainer(Platform{OS: "linux", Architecture: "amd64"})
	base.FS.setValue(partTestDirectory(s0, "/"))
	ctr := attachTransferObject(t, ctx, a, srv, "a", "noExec", base)
	child := NewContainer(base.Platform)
	child.FS, _ = CloneContainerDirectoryAccessor(ctx, base.FS)
	child.Config.WorkingDir = "/child"
	root := attachDelegationChild(t, ctx, a, srv, "a", "withWorkdir", ctr, child)
	var bundle dagql.ValueBundle
	require.NoError(t, a.WithExportedValues(ctx, dagql.ValueSelection{Roots: []dagql.AnyResult{root}}, config.RefConfig{}, func(_ context.Context, ex *dagql.ExportedValues) error {
		bundle = ex.Bundle
		return nil
	}))
	ctrOrdinal := transferOrdinalOf(t, a, bundle, ctr)

	for _, expired := range []bool{false, true} {
		t.Run(map[bool]string{false: "unexpired", true: "expired"}[expired], func(t *testing.T) {
			sent := bundle
			sent.Values = append([]dagql.TransferredValue(nil), bundle.Values...)
			if expired {
				for i := range sent.Values {
					if sent.Values[i].Ordinal == ctrOrdinal {
						sent.Values[i].ExpiresAtUnix = time.Now().Add(-time.Hour).Unix()
					}
				}
			}
			cloud, err := dagql.NewCache(ctx, "", nil, nil, dagql.WithBlobStore())
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, cloud.CloseDiscardingPersistence()) })
			reply, err := cloud.MergeValues(ctx, "cache-a", sent)
			require.NoError(t, err)
			var number uint64
			for _, value := range reply.Values {
				if value.Ordinal == ctrOrdinal {
					number = value.Number
					if expired {
						require.Positive(t, value.ExpiresAtUnix)
						require.Less(t, value.ExpiresAtUnix, time.Now().Unix(), "the stored record has expired")
					}
				}
			}
			require.NotZero(t, number)
			got, err := cloud.AvailablePart(number, dagql.PersistedPartAddress{Part: ContainerPartExecMeta})
			require.NoError(t, err)
			require.Equal(t, dagql.PartAbsent, got.State)
			require.Equal(t, number, got.Donor)
			fs, err := cloud.AvailablePart(number, dagql.PersistedPartAddress{Part: ContainerPartFS})
			require.NoError(t, err)
			require.Equal(t, dagql.PartUnavailable, fs.State, "the fs has bytes, and none are stored")
		})
	}
}
