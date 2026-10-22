package snapshots_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	digest "github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

// layeredChain builds n layers in store, each adding one file, and returns
// the export of the top one's chain.
func layeredChain(t *testing.T, store *testutil.Store, name string, n int) *bkcache.ExportChain {
	t.Helper()
	var top bkcache.ImmutableRef
	for i := range n {
		top, _ = store.Build(t, top, fmt.Sprintf("%s-%02d.txt", name, i), fmt.Sprintf("%s %d", name, i))
	}
	return exportChain(t, top)
}

// getGauge records how many GETs are in flight at once, at most.
type getGauge struct {
	inflight, most atomic.Int64
}

func (g *getGauge) enter() {
	n := g.inflight.Add(1)
	for {
		most := g.most.Load()
		if n <= most || g.most.CompareAndSwap(most, n) {
			return
		}
	}
}

// A chain's blobs download at once: every one of its layers' GETs is in
// flight together, which a provider that answers none until all have
// arrived checks. The layers still apply in order.
func TestImportChainDownloadsLayersAtOnce(t *testing.T) {
	const layers = 10
	producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
	chain := layeredChain(t, producer, "layer", layers)
	gauge := &getGauge{}
	all := make(chan struct{})
	var arrived atomic.Int64
	provider := &testutil.Provider{InfoReaderProvider: chain.Provider}
	provider.BeforeRead = func(ctx context.Context, _ ocispecs.Descriptor) error {
		gauge.enter()
		defer gauge.inflight.Add(-1)
		if arrived.Add(1) == layers {
			close(all)
		}
		select {
		case <-all:
			return nil
		case <-time.After(30 * time.Second):
			return errors.New("the chain's GETs were not all in flight at once")
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
	var mu sync.Mutex
	var applied []digest.Digest
	consumer.BeforeApply = func(_ context.Context, desc ocispecs.Descriptor) error {
		mu.Lock()
		defer mu.Unlock()
		applied = append(applied, desc.Digest)
		return nil
	}
	start := time.Now()
	ref := importChain(t, consumer, supplied(chain, provider))
	t.Logf("%d layers: import %s, at most %d GETs at once", layers, time.Since(start), gauge.most.Load())
	require.EqualValues(t, layers, gauge.most.Load(), "every layer's GET in flight at once")
	require.EqualValues(t, layers, provider.Reads.Load(), "each blob is read once")
	want := make([]digest.Digest, 0, layers)
	for _, layer := range chain.Layers {
		want = append(want, layer.Descriptor.Digest)
	}
	require.Equal(t, want, applied, "layers apply in order")
	testutil.CheckFile(t, ref, "layer-00.txt", "layer 0")
	testutil.CheckFile(t, ref, "layer-09.txt", "layer 9")
}

// An import that fails part-way stops every download it started ahead and
// leaves no lease or partial content behind.
func TestImportChainFailureReleasesEveryDownload(t *testing.T) {
	const layers, failing = 8, 3
	const later = layers - failing - 1
	producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
	chain := layeredChain(t, producer, "layer", layers)
	index := map[digest.Digest]int{}
	for i, layer := range chain.Layers {
		index[layer.Descriptor.Digest] = i
	}
	errLayer := errors.New("injected layer read failure")
	var entered, stopped atomic.Int64
	downloading := make(chan struct{}, layers)
	provider := &testutil.Provider{InfoReaderProvider: chain.Provider}
	provider.BeforeRead = func(ctx context.Context, desc ocispecs.Descriptor) error {
		switch i := index[desc.Digest]; {
		case i < failing:
			return nil
		case i == failing:
			// Fail once every later layer's blob is downloading.
			timeout := time.After(5 * time.Second)
			for range later {
				select {
				case <-downloading:
				case <-timeout:
					return errLayer
				}
			}
			return errLayer
		default:
			entered.Add(1)
			downloading <- struct{}{}
			<-ctx.Done()
			stopped.Add(1)
			return context.Cause(ctx)
		}
	}
	ref, err := consumer.Manager.ImportChain(context.Background(), supplied(chain, provider))
	require.Nil(t, ref)
	require.ErrorIs(t, err, errLayer)
	require.EqualValues(t, later, entered.Load(), "every later layer's blob was downloading")
	require.Equal(t, entered.Load(), stopped.Load(), "the failed import stopped every download it held")
	assertNoTemporaryResources(t, consumer)
}

// A chain whose first layers the engine imported already downloads only the
// rest: no blob of that local prefix, even with those blobs gone from the
// content store, as when an owner keeps the snapshots and not their blobs.
func TestImportChainDownloadsNothingForItsLocalPrefix(t *testing.T) {
	ctx := context.Background()
	const layers, local = 6, 3
	producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
	var top, prefixTop bkcache.ImmutableRef
	for i := range layers {
		top, _ = producer.Build(t, top, fmt.Sprintf("layer-%02d.txt", i), fmt.Sprintf("layer %d", i))
		if i == local-1 {
			prefixTop = top
		}
	}
	prefix, chain := exportChain(t, prefixTop), exportChain(t, top)
	imported, err := consumer.Manager.ImportChain(ctx, prefix)
	require.NoError(t, err)
	kept := imported.SnapshotID()
	keep := flatLeaseNamingOnly(t, consumer, "prefix-owner", kept)
	t.Cleanup(func() { require.NoError(t, consumer.Leases.Delete(context.Background(), keep)) })
	require.NoError(t, imported.Release(ctx))
	consumer.GC(t)
	require.True(t, snapshotPresent(t, consumer, kept), "the prefix's snapshots stay")
	for _, layer := range prefix.Layers {
		require.False(t, blobPresent(t, consumer, layer.Descriptor.Digest), "and their blobs are gone")
	}
	reads := map[digest.Digest]*atomic.Int64{}
	for _, layer := range chain.Layers {
		reads[layer.Descriptor.Digest] = &atomic.Int64{}
	}
	provider := &testutil.Provider{InfoReaderProvider: chain.Provider}
	provider.BeforeRead = func(_ context.Context, desc ocispecs.Descriptor) error {
		reads[desc.Digest].Add(1)
		return nil
	}
	var timing bkcache.ImportTiming
	ref := importChain(t, consumer, &bkcache.ExportChain{Layers: chain.Layers, Provider: provider, Timing: &timing})
	// Started ahead, whatever their reads' schedule: the layers after the
	// first one missing, and none of the prefix.
	require.EqualValues(t, layers-local-1, timing.Ahead, "downloads started ahead")
	for i, layer := range chain.Layers {
		want := int64(1)
		if i < local {
			want = 0
		}
		require.Equal(t, want, reads[layer.Descriptor.Digest].Load(), "GETs of layer %d", i)
	}
	testutil.CheckFile(t, ref, "layer-00.txt", "layer 0")
	testutil.CheckFile(t, ref, "layer-05.txt", "layer 5")
}

// The engine bounds the downloads started ahead across its imports. Three
// chains want more than that at once. With every GET held, the GETs in
// flight settle at the bound plus each chain's importing layer, which takes
// no slot; once released, every import finishes.
func TestImportChainAheadDownloadsShareAnEngineBound(t *testing.T) {
	const chains, layers = 3, 18
	// The engine's bound on ahead downloads (maxAheadDownloads), plus the
	// layer each chain is importing.
	const bound = 32 + chains
	ctx := context.Background()
	producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
	gauge := &getGauge{}
	release := make(chan struct{})
	var released sync.Once
	supply := make([]*bkcache.ExportChain, 0, chains)
	for c := range chains {
		chain := layeredChain(t, producer, fmt.Sprintf("chain%d", c), layers)
		provider := &testutil.Provider{InfoReaderProvider: chain.Provider}
		provider.BeforeRead = func(ctx context.Context, _ ocispecs.Descriptor) error {
			gauge.enter()
			defer gauge.inflight.Add(-1)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return context.Cause(ctx)
			}
		}
		supply = append(supply, supplied(chain, provider))
	}
	var imports sync.WaitGroup
	// Imports still held when the test fails end before their chains' and
	// the stores' resources go.
	t.Cleanup(func() {
		released.Do(func() { close(release) })
		imports.Wait()
	})
	errs := make(chan error, chains)
	for _, chain := range supply {
		imports.Add(1)
		go func() {
			defer imports.Done()
			ref, err := consumer.Manager.ImportChain(ctx, chain)
			if err == nil {
				err = ref.Release(ctx)
			}
			errs <- err
		}()
	}
	require.Eventually(t, func() bool { return gauge.inflight.Load() == bound }, 30*time.Second, 10*time.Millisecond,
		"the GETs in flight reach the bound plus the importing layers")
	time.Sleep(200 * time.Millisecond)
	require.EqualValues(t, bound, gauge.inflight.Load(), "and stay there")
	released.Do(func() { close(release) })
	timeout := time.After(30 * time.Second)
	for range chains {
		select {
		case err := <-errs:
			require.NoError(t, err)
		case <-timeout:
			t.Fatal("an import never finished")
		}
	}
	require.EqualValues(t, bound, gauge.most.Load(), "the engine's bound held throughout")
}
