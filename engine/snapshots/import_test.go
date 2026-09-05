package snapshots_test

import (
	"bytes"
	"context"

	"errors"
	"fmt"
	"github.com/dagger/dagger/internal/buildkit/client"
	digest "github.com/opencontainers/go-digest"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/leases"
	ctdsnapshots "github.com/containerd/containerd/v2/core/snapshots"
	cerrdefs "github.com/containerd/errdefs"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

var transferConfig = config.RefConfig{Compression: compression.New(compression.Uncompressed)}

func exportChain(t *testing.T, ref bkcache.ImmutableRef) *bkcache.ExportChain {
	t.Helper()
	chain, err := ref.ExportChain(context.Background(), transferConfig)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, chain.Release(context.Background())) })
	return chain
}

func importChain(t *testing.T, store *testutil.Store, chain *bkcache.ExportChain) bkcache.ImmutableRef {
	t.Helper()
	ref, err := store.Manager.ImportChain(context.Background(), chain)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, ref.Release(context.Background())) })
	return ref
}

func supplied(chain *bkcache.ExportChain, provider content.InfoReaderProvider) *bkcache.ExportChain {
	return &bkcache.ExportChain{Layers: chain.Layers, Provider: provider}
}

func TestImportChainLocalStores(t *testing.T) {
	ctx := context.Background()
	producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
	a, _ := producer.Build(t, nil, "a.txt", "prefix bytes")
	ab, _ := producer.Build(t, a, "dir/b.txt", "suffix bytes")
	chainA, chainAB := exportChain(t, a), exportChain(t, ab)
	require.Len(t, chainA.Layers, 1)
	require.Len(t, chainAB.Layers, 2)
	provider := &testutil.Provider{InfoReaderProvider: chainAB.Provider}
	first := importChain(t, consumer, supplied(chainA, provider))
	require.EqualValues(t, 1, provider.Reads.Load())
	require.EqualValues(t, 1, consumer.Applies.Load())
	second := importChain(t, consumer, supplied(chainAB, provider))
	require.EqualValues(t, 2, provider.Reads.Load())
	require.EqualValues(t, 2, consumer.Applies.Load())
	third := importChain(t, consumer, supplied(chainA, provider))
	require.Equal(t, first.SnapshotID(), third.SnapshotID())
	require.EqualValues(t, 2, provider.Reads.Load())
	require.EqualValues(t, 2, consumer.Applies.Load())
	testutil.CheckFile(t, first, "a.txt", "prefix bytes")
	testutil.CheckFile(t, second, "a.txt", "prefix bytes")
	testutil.CheckFile(t, second, "dir/b.txt", "suffix bytes")
	info, err := consumer.Snapshots.Stat(ctx, second.SnapshotID())
	require.NoError(t, err)
	require.Equal(t, first.SnapshotID(), info.Parent)
	t.Logf("A, A+B, A: source reads=%d apply calls=%d", provider.Reads.Load(), consumer.Applies.Load())
}

func TestImportChainSameManager(t *testing.T) {
	for _, root := range []string{"absent", "scratch", "empty"} {
		t.Run(root, func(t *testing.T) {
			ctx := context.Background()
			store := testutil.NewStore(t)
			var parent bkcache.ImmutableRef
			if root == "scratch" {
				var err error
				parent, err = store.Manager.Scratch(ctx)
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, parent.Release(ctx)) })
			} else if root == "empty" {
				parent, _ = store.Build(t, nil, "", "")
			}
			a, _ := store.Build(t, parent, "a.txt", "same manager")
			chain := exportChain(t, a)
			require.Len(t, chain.Layers, 1)
			provider := &testutil.Provider{InfoReaderProvider: chain.Provider}
			ref := importChain(t, store, supplied(chain, provider))
			require.Zero(t, store.Applies.Load(), "same manager must reuse its exported snapshot")
			require.Zero(t, provider.Reads.Load())
			require.Equal(t, a.SnapshotID(), ref.SnapshotID())
			store.Reload(t)
			reloaded := importChain(t, store, supplied(chain, provider))
			require.Equal(t, a.SnapshotID(), reloaded.SnapshotID())
			testutil.CheckFile(t, reloaded, "a.txt", "same manager")
			require.Zero(t, provider.Reads.Load())
			require.Zero(t, store.Applies.Load())
			info, err := store.Snapshots.Stat(ctx, a.SnapshotID())
			require.NoError(t, err)
			if parent != nil {
				require.Equal(t, parent.SnapshotID(), info.Parent)
			}
			// Explicit compression selects and registers this descriptor only.
			encoded, err := reloaded.ExportChain(ctx, config.RefConfig{Compression: compression.New(compression.Gzip).SetForce(true)})
			require.NoError(t, err)
			defer encoded.Release(ctx)
			require.NotEqual(t, chain.Layers[0].Descriptor.Digest, encoded.Layers[0].Descriptor.Digest)
			require.Equal(t, ocispecs.MediaTypeImageLayerGzip, encoded.Layers[0].Descriptor.MediaType)
			rows := store.Manager.PersistentMetadataRows()
			require.Contains(t, rows.ImportedByBlob, bkcache.ImportedLayerBlobRow{ParentSnapshotID: "", BlobDigest: encoded.Layers[0].Descriptor.Digest, SnapshotID: a.SnapshotID()})
			encodedProvider := &testutil.Provider{InfoReaderProvider: encoded.Provider}
			importChain(t, store, supplied(encoded, encodedProvider))
			require.Zero(t, encodedProvider.Reads.Load())
			require.Zero(t, store.Applies.Load())
			var blobs int
			require.NoError(t, store.Content.Walk(ctx, func(content.Info) error { blobs++; return nil }))
			require.Equal(t, 2, blobs, "only the original and explicitly requested encoding")
			t.Logf("root=%s reload and requested gzip: source reads=0 apply calls=0 blobs=%d", root, blobs)
		})
	}
	t.Run("zero layers", func(t *testing.T) {
		store := testutil.NewStore(t)
		ref := importChain(t, store, &bkcache.ExportChain{})
		scratch, err := store.Manager.Scratch(context.Background())
		require.NoError(t, err)
		defer scratch.Release(context.Background())
		require.Equal(t, scratch.SnapshotID(), ref.SnapshotID())
	})
}

func TestImportChainConcurrentPrefix(t *testing.T) {
	producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
	a, _ := producer.Build(t, nil, "a.txt", "shared")
	ab, _ := producer.Build(t, a, "b.txt", "suffix")
	chain := exportChain(t, ab)
	provider := &testutil.Provider{InfoReaderProvider: chain.Provider}
	entered, proceed := make(chan struct{}), make(chan struct{})
	secondStarted := make(chan struct{})
	var starts atomic.Int64
	consumer.AfterCreate = func(leases.Lease) {
		if starts.Add(1) == 2 {
			close(secondStarted)
		}
	}
	var once sync.Once
	consumer.BeforeApply = func(_ context.Context, _ ocispecs.Descriptor) error {
		once.Do(func() { close(entered); <-proceed })
		return nil
	}
	type result struct {
		ref bkcache.ImmutableRef
		err error
	}
	results := make(chan result, 2)
	go func() {
		ref, err := consumer.Manager.ImportChain(context.Background(), supplied(chain, provider))
		results <- result{ref, err}
	}()
	<-entered
	go func() {
		ref, err := consumer.Manager.ImportChain(context.Background(), supplied(chain, provider))
		results <- result{ref, err}
	}()
	<-secondStarted
	close(proceed)
	first, second := <-results, <-results
	require.NoError(t, first.err)
	require.NoError(t, second.err)
	defer first.ref.Release(context.Background())
	defer second.ref.Release(context.Background())
	require.Equal(t, first.ref.SnapshotID(), second.ref.SnapshotID())
	require.EqualValues(t, 2, provider.Reads.Load())
	require.EqualValues(t, 2, consumer.Applies.Load())
	testutil.CheckFile(t, second.ref, "b.txt", "suffix")
	t.Logf("two concurrent A+B imports: source reads=%d apply calls=%d", provider.Reads.Load(), consumer.Applies.Load())
}

func TestImportChainPrunedSuffix(t *testing.T) {
	for _, keepBlob := range []bool{true, false} {
		t.Run(fmt.Sprintf("keep blob %t", keepBlob), func(t *testing.T) {
			ctx := context.Background()
			producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
			a, _ := producer.Build(t, nil, "a.txt", "retained prefix")
			ab, _ := producer.Build(t, a, "b.txt", "replaceable suffix")
			chain := exportChain(t, ab)
			provider := &testutil.Provider{InfoReaderProvider: chain.Provider}
			prefix := importChain(t, consumer, &bkcache.ExportChain{Layers: chain.Layers[:1], Provider: provider})
			both := importChain(t, consumer, supplied(chain, provider))
			blob := chain.Layers[1].Descriptor
			if keepBlob {
				owner, err := consumer.Leases.Create(ctx, leases.WithRandomID())
				require.NoError(t, err)
				defer consumer.Manager.RemoveLease(ctx, owner.ID)
				require.NoError(t, consumer.Leases.AddResource(ctx, owner, leases.Resource{Type: "content", ID: blob.Digest.String()}))
			}
			require.NoError(t, both.Release(ctx))
			consumer.GC(t)
			_, err := consumer.Snapshots.Stat(ctx, both.SnapshotID())
			require.True(t, cerrdefs.IsNotFound(err), "suffix must actually be pruned: %v", err)
			testutil.CheckFile(t, prefix, "a.txt", "retained prefix")
			_, err = consumer.Content.Info(ctx, blob.Digest)
			if keepBlob {
				require.NoError(t, err)
			} else {
				require.True(t, cerrdefs.IsNotFound(err))
			}
			retry := importChain(t, consumer, supplied(chain, provider))
			require.NotEqual(t, both.SnapshotID(), retry.SnapshotID())
			require.EqualValues(t, 3, consumer.Applies.Load())
			wantReads := int64(3)
			if keepBlob {
				wantReads = 2
			}
			require.Equal(t, wantReads, provider.Reads.Load())
			testutil.CheckFile(t, retry, "b.txt", "replaceable suffix")
			t.Logf("suffix pruned, blob retained=%t: source reads=%d apply calls=%d", keepBlob, provider.Reads.Load(), consumer.Applies.Load())
		})
	}
}

func TestImportChainFailurePrefix(t *testing.T) {
	for _, cancelSource := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel %t", cancelSource), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
			a, _ := producer.Build(t, nil, "a.txt", "committed before failure")
			ab, _ := producer.Build(t, a, "b.txt", "retry suffix")
			chain := exportChain(t, ab)
			provider := &testutil.Provider{InfoReaderProvider: chain.Provider}
			failure := &failingLayerProvider{Provider: provider, digest: chain.Layers[1].Descriptor.Digest.String()}
			if cancelSource {
				failure.cancel = cancel
			}

			ref, err := consumer.Manager.ImportChain(ctx, supplied(chain, failure))
			require.Error(t, err)
			require.Nil(t, ref)
			if cancelSource {
				require.ErrorIs(t, err, context.Canceled)
			}
			assertNoTemporaryResources(t, consumer)
			rows := consumer.Manager.PersistentMetadataRows()
			require.Len(t, rows.ImportedByDiff, 1)
			prefixID := rows.ImportedByDiff[0].SnapshotID
			retry := importChain(t, consumer, supplied(chain, provider))
			info, err := consumer.Snapshots.Stat(context.Background(), retry.SnapshotID())
			require.NoError(t, err)
			require.Equal(t, prefixID, info.Parent)
			require.EqualValues(t, 3, provider.Reads.Load())
			require.EqualValues(t, 2, consumer.Applies.Load())
			testutil.CheckFile(t, retry, "a.txt", "committed before failure")
			testutil.CheckFile(t, retry, "b.txt", "retry suffix")
		})
	}
}

func assertNoTemporaryResources(t *testing.T, store *testutil.Store) {
	t.Helper()
	ctx := context.Background()
	owners, err := store.Leases.List(ctx)
	require.NoError(t, err)
	require.Empty(t, owners)
	writes, err := store.Content.ListStatuses(ctx)
	require.NoError(t, err)
	require.Empty(t, writes)
	require.NoError(t, store.Snapshots.Walk(ctx, func(_ context.Context, info ctdsnapshots.Info) error {
		require.Equal(t, ctdsnapshots.KindCommitted, info.Kind)
		return nil
	}))
}

func TestExportChainConcurrentCancellation(t *testing.T) {
	ctx := context.Background()
	store := testutil.NewStore(t)
	a, owner := store.Build(t, nil, "a.txt", "surviving provider")
	entered := make(chan struct{})
	var attempts atomic.Int64
	store.BeforeDiff = func(ctx context.Context) error {
		if attempts.Add(1) == 1 {
			close(entered)
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	}
	secondPinned := make(chan struct{})
	var pinOwners sync.Map
	var attachments atomic.Int64
	store.AfterAdd = func(_ context.Context, lease leases.Lease, resource leases.Resource) {
		if resource != (leases.Resource{Type: "snapshots/native", ID: a.SnapshotID()}) {
			return
		}
		if _, loaded := pinOwners.LoadOrStore(lease.ID, true); !loaded && attachments.Add(1) == 2 {
			close(secondPinned)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	first := make(chan error, 1)
	go func() { _, err := a.ExportChain(canceled, transferConfig); first <- err }()
	<-entered
	type result struct {
		chain *bkcache.ExportChain
		err   error
	}
	second := make(chan result, 1)
	go func() { chain, err := a.ExportChain(ctx, transferConfig); second <- result{chain, err} }()
	<-secondPinned
	cancel()
	require.ErrorIs(t, <-first, context.Canceled)
	survivor := <-second
	require.NoError(t, survivor.err)
	defer survivor.chain.Release(ctx)
	require.NoError(t, store.Manager.RemoveLease(ctx, owner))
	require.NoError(t, a.Release(ctx))
	store.GC(t)
	data, err := content.ReadBlob(ctx, survivor.chain.Provider, survivor.chain.Layers[0].Descriptor)
	require.NoError(t, err)
	require.NotEmpty(t, data)
	testutil.CheckFile(t, importChain(t, store, survivor.chain), "a.txt", "surviving provider")
	require.EqualValues(t, 2, attempts.Load())
}

func TestImportChainCandidateLostBeforePin(t *testing.T) {
	ctx := context.Background()
	producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
	a, _ := producer.Build(t, nil, "a.txt", "independent owner")
	ab, _ := producer.Build(t, a, "b.txt", "lost candidate")
	chain := exportChain(t, ab)
	provider := &testutil.Provider{InfoReaderProvider: chain.Provider}
	prefix := importChain(t, consumer, &bkcache.ExportChain{Layers: chain.Layers[:1], Provider: provider})
	both := importChain(t, consumer, supplied(chain, provider))
	const priorOwner = "prior-suffix-owner"
	require.NoError(t, consumer.Manager.AttachLease(ctx, priorOwner, both.SnapshotID()))
	require.NoError(t, both.Release(ctx))
	var lost atomic.Bool
	consumer.BeforeAdd = func(ctx context.Context, _ leases.Lease, resource leases.Resource) error {
		if resource.Type != "snapshots/native" || resource.ID != both.SnapshotID() || !lost.CompareAndSwap(false, true) {
			return nil
		}
		// RemoveLease deletes from containerd before taking the manager metadata
		// lock. Reproduce that external metadata boundary during attachment.
		require.NoError(t, consumer.Leases.Delete(ctx, leases.Lease{ID: priorOwner}))
		consumer.GC(t)
		_, err := consumer.Snapshots.Stat(ctx, both.SnapshotID())
		require.True(t, cerrdefs.IsNotFound(err))
		return nil
	}
	retry := importChain(t, consumer, supplied(chain, provider))
	require.True(t, lost.Load())
	require.NotEqual(t, both.SnapshotID(), retry.SnapshotID())
	require.EqualValues(t, 3, provider.Reads.Load())
	require.EqualValues(t, 3, consumer.Applies.Load())
	testutil.CheckFile(t, prefix, "a.txt", "independent owner")
	testutil.CheckFile(t, retry, "b.txt", "lost candidate")
	require.NoError(t, consumer.Manager.RemoveLease(ctx, priorOwner))
}

func TestImportChainSnapshotAndBlobPins(t *testing.T) {
	t.Run("snapshot reuse after prior owner release", func(t *testing.T) {
		ctx := context.Background()
		producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
		a, _ := producer.Build(t, nil, "a.txt", "retained")
		ab, _ := producer.Build(t, a, "b.txt", "retained suffix")
		chain := exportChain(t, ab)
		provider := &testutil.Provider{InfoReaderProvider: chain.Provider}
		first := importChain(t, consumer, supplied(chain, provider))
		second := importChain(t, consumer, supplied(chain, provider))
		require.NoError(t, first.Release(ctx))
		consumer.GC(t)
		assertChainResources(t, consumer, second.SnapshotID(), chain)
		testutil.CheckFile(t, second, "a.txt", "retained")
		testutil.CheckFile(t, second, "b.txt", "retained suffix")
		require.EqualValues(t, 2, provider.Reads.Load())
		require.EqualValues(t, 2, consumer.Applies.Load())
	})
	t.Run("blob reuse before apply", func(t *testing.T) {
		ctx := context.Background()
		producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
		a, _ := producer.Build(t, nil, "a.txt", "leased bytes")
		chain := exportChain(t, a)
		owner, err := consumer.Leases.Create(ctx, leases.WithRandomID())
		require.NoError(t, err)
		desc := chain.Layers[0].Descriptor
		reader, err := chain.Provider.ReaderAt(ctx, desc)
		require.NoError(t, err)
		require.NoError(t, content.WriteBlob(leases.WithLease(ctx, owner.ID), consumer.Content, "fixture-copy", content.NewReader(reader), desc))
		require.NoError(t, reader.Close())
		consumer.BeforeApply = func(ctx context.Context, _ ocispecs.Descriptor) error {
			require.NoError(t, consumer.Leases.Delete(ctx, owner))
			consumer.GC(t)
			_, err := consumer.Content.Info(ctx, desc.Digest)
			require.NoError(t, err)
			return nil
		}
		provider := &testutil.Provider{InfoReaderProvider: chain.Provider}
		ref := importChain(t, consumer, supplied(chain, provider))
		require.Zero(t, provider.Reads.Load())
		require.EqualValues(t, 1, consumer.Applies.Load())
		testutil.CheckFile(t, ref, "a.txt", "leased bytes")
	})
}

func TestExportChainExistingBlobPins(t *testing.T) {
	ctx := context.Background()
	store := testutil.NewStore(t)
	a, ownerA := store.Build(t, nil, "a.txt", "producer prefix")
	ab, ownerB := store.Build(t, a, "b.txt", "producer suffix")
	first := exportChain(t, ab)
	require.NoError(t, first.Release(ctx))
	store.GC(t)
	assertChainResources(t, store, ab.SnapshotID(), first)
	second := exportChain(t, ab)
	require.EqualValues(t, 2, store.Diffs.Load(), "second export uses existing blobs")
	require.NoError(t, store.Manager.RemoveLease(ctx, ownerA))
	require.NoError(t, store.Manager.RemoveLease(ctx, ownerB))
	require.NoError(t, a.Release(ctx))
	require.NoError(t, ab.Release(ctx))
	store.GC(t)
	assertChainResources(t, store, ab.SnapshotID(), second)
	for _, layer := range second.Layers {
		data, err := content.ReadBlob(ctx, second.Provider, layer.Descriptor)
		require.NoError(t, err)
		require.NotEmpty(t, data)
	}
	require.NoError(t, second.Release(ctx))
	store.GC(t)
	owners, err := store.Leases.List(ctx)
	require.NoError(t, err)
	require.Empty(t, owners)
	_, err = store.Snapshots.Stat(ctx, ab.SnapshotID())
	require.True(t, cerrdefs.IsNotFound(err))
}

func assertChainResources(t *testing.T, store *testutil.Store, snapshotID string, chain *bkcache.ExportChain) {
	t.Helper()
	ctx := context.Background()
	owners, err := store.Leases.List(ctx)
	require.NoError(t, err)
	var found bool
	for _, owner := range owners {
		resources, err := store.Leases.ListResources(ctx, owner)
		require.NoError(t, err)
		ownsTip := false
		for _, resource := range resources {
			if resource == (leases.Resource{Type: "snapshots/native", ID: snapshotID}) {
				ownsTip = true
			}
		}
		if !ownsTip {
			continue
		}
		found = true
		for id := snapshotID; id != ""; {
			require.Contains(t, resources, leases.Resource{Type: "snapshots/native", ID: id})
			info, err := store.Snapshots.Stat(ctx, id)
			require.NoError(t, err)
			id = info.Parent
		}
		for _, layer := range chain.Layers {
			require.Contains(t, resources, leases.Resource{Type: "content", ID: layer.Descriptor.Digest.String()})
			_, err := store.Content.Info(ctx, layer.Descriptor.Digest)
			require.NoError(t, err)
		}
	}
	require.True(t, found, "a real lease must own the chain")
}

func TestImportChainMountedFailure(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel %t", canceled), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
			a, _ := producer.Build(t, nil, "a.txt", "independent prefix")
			ab, _ := producer.Build(t, a, "b.txt", "mounted suffix")
			chain := exportChain(t, ab)
			provider := &testutil.Provider{InfoReaderProvider: chain.Provider}
			prefix := importChain(t, consumer, &bkcache.ExportChain{Layers: chain.Layers[:1], Provider: provider})
			before, err := consumer.Leases.List(ctx)
			require.NoError(t, err)
			consumer.BeforeApply = func(ctx context.Context, desc ocispecs.Descriptor) error {
				require.Equal(t, chain.Layers[1].Descriptor.Digest, desc.Digest)
				var active int
				require.NoError(t, consumer.Snapshots.Walk(ctx, func(_ context.Context, info ctdsnapshots.Info) error {
					if info.Kind == ctdsnapshots.KindActive {
						active++
					}
					return nil
				}))
				require.Equal(t, 1, active, "suffix mutable and its mounts exist at the apply boundary")
				if canceled {
					cancel()
					return context.Canceled
				}
				return errors.New("apply stopped after mount acquisition")
			}
			ref, err := consumer.Manager.ImportChain(ctx, supplied(chain, provider))
			require.Nil(t, ref)
			require.Error(t, err)
			if canceled {
				require.ErrorIs(t, err, context.Canceled)
			}
			after, err := consumer.Leases.List(context.Background())
			require.NoError(t, err)
			require.Equal(t, before, after, "only the independently retained prefix lease remains")
			writes, err := consumer.Content.ListStatuses(context.Background())
			require.NoError(t, err)
			require.Empty(t, writes)
			consumer.GC(t)
			require.NoError(t, consumer.Snapshots.Walk(context.Background(), func(_ context.Context, info ctdsnapshots.Info) error {
				require.Equal(t, ctdsnapshots.KindCommitted, info.Kind)
				return nil
			}))
			testutil.CheckFile(t, prefix, "a.txt", "independent prefix")
			consumer.BeforeApply = nil
			retry := importChain(t, consumer, supplied(chain, provider))
			info, err := consumer.Snapshots.Stat(context.Background(), retry.SnapshotID())
			require.NoError(t, err)
			require.Equal(t, prefix.SnapshotID(), info.Parent)
			require.EqualValues(t, 3, provider.Reads.Load())
			require.EqualValues(t, 3, consumer.Applies.Load(), "includes the failed mounted apply boundary")
			testutil.CheckFile(t, retry, "b.txt", "mounted suffix")
		})
	}
}

func TestImportChainSnapshotWithLostHistoricalBlob(t *testing.T) {
	ctx := context.Background()
	producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
	a, _ := producer.Build(t, nil, "a.txt", "usable snapshot")
	chain := exportChain(t, a)
	provider := &testutil.Provider{InfoReaderProvider: chain.Provider}
	first := importChain(t, consumer, supplied(chain, provider))
	owners, err := consumer.Leases.List(ctx)
	require.NoError(t, err)
	require.Len(t, owners, 1)
	var removed atomic.Bool
	consumer.BeforeAdd = func(ctx context.Context, _ leases.Lease, resource leases.Resource) error {
		if resource.Type != "content" || !removed.CompareAndSwap(false, true) {
			return nil
		}
		require.NoError(t, consumer.Leases.Delete(ctx, owners[0]))
		consumer.GC(t)
		_, err := consumer.Content.Info(ctx, chain.Layers[0].Descriptor.Digest)
		require.True(t, cerrdefs.IsNotFound(err), "blob is lost during partial attachment")
		_, err = consumer.Snapshots.Stat(ctx, first.SnapshotID())
		require.NoError(t, err)
		return nil
	}
	second := importChain(t, consumer, supplied(chain, provider))
	require.True(t, removed.Load())
	require.Equal(t, first.SnapshotID(), second.SnapshotID())
	require.NoError(t, first.Release(ctx))
	testutil.CheckFile(t, second, "a.txt", "usable snapshot")
	require.EqualValues(t, 1, provider.Reads.Load())
	require.EqualValues(t, 1, consumer.Applies.Load())
}

// Fail after copying a real prefix of the suffix blob into the content writer.
type failingLayerProvider struct {
	*testutil.Provider
	digest string
	cancel context.CancelFunc
}

func (p *failingLayerProvider) ReaderAt(ctx context.Context, desc ocispecs.Descriptor) (content.ReaderAt, error) {
	reader, err := p.Provider.ReaderAt(ctx, desc)
	if err != nil || desc.Digest.String() != p.digest {
		return reader, err
	}
	return &failingLayerReader{ReaderAt: reader, cancel: p.cancel}, nil
}

type failingLayerReader struct {
	content.ReaderAt
	cancel context.CancelFunc
}

func (r *failingLayerReader) ReadAt(p []byte, off int64) (int, error) {
	if len(p) > 128 {
		p = p[:128]
	}
	n, err := r.ReaderAt.ReadAt(p, off)
	if err != nil && err != io.EOF {
		return n, err
	}
	if r.cancel != nil {
		r.cancel()
		return n, context.Canceled
	}
	return n, errors.New("source failed after partial bytes")
}

func TestImportImageSharesChainReuse(t *testing.T) {
	ctx := context.Background()
	producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
	a, _ := producer.Build(t, nil, "a.txt", "image prefix")
	ab, _ := producer.Build(t, a, "b.txt", "image suffix")
	chain := exportChain(t, ab)
	resolverOwner, err := consumer.Leases.Create(ctx, leases.WithRandomID())
	require.NoError(t, err)
	fetchCtx := leases.WithLease(ctx, resolverOwner.ID)
	layers := make([]ocispecs.Descriptor, 0, len(chain.Layers))
	for _, layer := range chain.Layers {
		data, err := content.ReadBlob(ctx, chain.Provider, layer.Descriptor)
		require.NoError(t, err)
		require.NoError(t, content.WriteBlob(fetchCtx, consumer.Content, layer.Descriptor.Digest.String(), bytes.NewReader(data), layer.Descriptor))
		layers = append(layers, layer.Descriptor)
	}
	metadata := func(value, mediaType string) ocispecs.Descriptor {
		desc := ocispecs.Descriptor{Digest: digest.FromString(value), Size: int64(len(value)), MediaType: mediaType}
		require.NoError(t, content.WriteBlob(fetchCtx, consumer.Content, desc.Digest.String(), bytes.NewBufferString(value), desc))
		return desc
	}
	manifest := metadata(`{"schemaVersion":2}`, ocispecs.MediaTypeImageManifest)
	imageConfig := metadata(`{"architecture":"amd64","os":"linux"}`, ocispecs.MediaTypeImageConfig)
	provider := &testutil.Provider{InfoReaderProvider: chain.Provider}
	prefix := importChain(t, consumer, &bkcache.ExportChain{Layers: chain.Layers[:1], Provider: provider})
	image, err := consumer.Manager.ImportImage(ctx, &bkcache.ImportedImage{Ref: "local/test", Layers: layers, ManifestDesc: manifest, ConfigDesc: imageConfig}, bkcache.ImportImageOpts{ImageRef: "local/test", RecordType: client.UsageRecordTypeRegular})
	require.NoError(t, err)
	defer image.Release(ctx)
	generic := importChain(t, consumer, supplied(chain, provider))
	require.Equal(t, image.SnapshotID(), generic.SnapshotID())
	require.NoError(t, consumer.Leases.Delete(ctx, resolverOwner))
	require.NoError(t, prefix.Release(ctx))
	require.NoError(t, generic.Release(ctx))
	consumer.GC(t)
	for _, desc := range []ocispecs.Descriptor{manifest, imageConfig} {
		_, err := consumer.Content.Info(ctx, desc.Digest)
		require.NoError(t, err)
	}
	testutil.CheckFile(t, image, "b.txt", "image suffix")
	require.EqualValues(t, 2, consumer.Applies.Load())
	require.Zero(t, provider.Reads.Load())
	record, ok, err := consumer.Manager.SnapshotRecordMetadata(ctx, image.SnapshotID())
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, client.UsageRecordTypeRegular, record.RecordType)
}

func TestExportChainCanceledWaiter(t *testing.T) {
	ctx := context.Background()
	store := testutil.NewStore(t)
	a, owner := store.Build(t, nil, "a.txt", "active export")
	entered, proceed := make(chan struct{}), make(chan struct{})
	store.BeforeDiff = func(context.Context) error { close(entered); <-proceed; return nil }
	secondPinned := make(chan struct{})
	var owners sync.Map
	var count atomic.Int64
	var waitingLease string
	store.AfterAdd = func(_ context.Context, lease leases.Lease, resource leases.Resource) {
		if resource != (leases.Resource{Type: "snapshots/native", ID: a.SnapshotID()}) {
			return
		}
		if _, loaded := owners.LoadOrStore(lease.ID, true); !loaded && count.Add(1) == 2 {
			waitingLease = lease.ID
			close(secondPinned)
		}
	}
	type result struct {
		chain *bkcache.ExportChain
		err   error
	}
	active := make(chan result, 1)
	go func() { chain, err := a.ExportChain(ctx, transferConfig); active <- result{chain, err} }()
	<-entered
	waiting, cancel := context.WithCancel(ctx)
	defer cancel()
	canceled := make(chan error, 1)
	go func() { _, err := a.ExportChain(waiting, transferConfig); canceled <- err }()
	<-secondPinned
	cancel()
	select {
	case err := <-canceled:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		close(proceed)
		t.Fatal("canceled waiter remained blocked by active export")
	}
	live, err := store.Leases.List(ctx)
	require.NoError(t, err)
	for _, lease := range live {
		require.NotEqual(t, waitingLease, lease.ID)
	}
	close(proceed)
	exported := <-active
	require.NoError(t, exported.err)
	defer exported.chain.Release(ctx)
	require.NoError(t, store.Manager.RemoveLease(ctx, owner))
	require.NoError(t, a.Release(ctx))
	store.GC(t)
	_, err = content.ReadBlob(ctx, exported.chain.Provider, exported.chain.Layers[0].Descriptor)
	require.NoError(t, err)
}
