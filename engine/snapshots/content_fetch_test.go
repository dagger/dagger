package snapshots_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

// gatedProvider counts the reads of each blob and holds the reads of one
// blob until released.
type gatedProvider struct {
	*testutil.Provider
	reads map[string]*atomic.Int64
}

func newGatedProvider(chain *bkcache.ExportChain, gated string, started chan<- struct{}, release <-chan struct{}) *gatedProvider {
	p := &gatedProvider{Provider: &testutil.Provider{InfoReaderProvider: chain.Provider}, reads: map[string]*atomic.Int64{}}
	for _, layer := range chain.Layers {
		p.reads[layer.Descriptor.Digest.String()] = &atomic.Int64{}
	}
	p.BeforeRead = func(ctx context.Context, desc ocispecs.Descriptor) error {
		if n := p.reads[desc.Digest.String()]; n != nil {
			n.Add(1)
		}
		if desc.Digest.String() != gated {
			return nil
		}
		if started != nil {
			started <- struct{}{}
		}
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
	return p
}

// A chain import fetches the next layer's blob while the layer before it
// applies.
func TestImportChainFetchesNextLayerAhead(t *testing.T) {
	producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
	a, _ := producer.Build(t, nil, "a.txt", "lower")
	ab, _ := producer.Build(t, a, "b.txt", "upper")
	chain := exportChain(t, ab)
	upper := chain.Layers[1].Descriptor
	upperRead := make(chan struct{}, 1)
	provider := &testutil.Provider{InfoReaderProvider: chain.Provider}
	provider.BeforeRead = func(_ context.Context, desc ocispecs.Descriptor) error {
		if desc.Digest == upper.Digest {
			upperRead <- struct{}{}
		}
		return nil
	}
	var ahead atomic.Bool
	consumer.BeforeApply = func(_ context.Context, desc ocispecs.Descriptor) error {
		if desc.Digest == chain.Layers[0].Descriptor.Digest {
			select {
			case <-upperRead:
				ahead.Store(true)
			case <-time.After(5 * time.Second):
			}
		}
		return nil
	}
	ref := importChain(t, consumer, supplied(chain, provider))
	require.True(t, ahead.Load(), "the upper layer's blob was read while the lower layer applied")
	require.EqualValues(t, 2, provider.Reads.Load(), "each blob is read once")
	testutil.CheckFile(t, ref, "b.txt", "upper")
}

// Two imports that need the same blob at once, on different parents, read it
// once: the second joins the first one's download.
func TestImportChainJoinsSharedBlob(t *testing.T) {
	producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
	a, _ := producer.Build(t, nil, "a.txt", "shared")
	b, _ := producer.Build(t, nil, "b.txt", "other base")
	chainA, chainB := exportChain(t, a), exportChain(t, b)
	shared := chainA.Layers[0].Descriptor
	// The shared blob on top of another base: a different parent, so the
	// per-layer import lock doesn't serialize the two imports.
	onB := &bkcache.ExportChain{Layers: append(append([]bkcache.ExportLayer{}, chainB.Layers...), chainA.Layers...)}
	started, release := make(chan struct{}, 4), make(chan struct{})
	providerA := newGatedProvider(chainA, shared.Digest.String(), started, release)
	// Both chains come from the producer's store, which serves every blob.
	providerB := newGatedProvider(&bkcache.ExportChain{Layers: onB.Layers, Provider: chainA.Provider}, shared.Digest.String(), started, release)
	onB.Provider = providerB

	type result struct {
		ref bkcache.ImmutableRef
		err error
	}
	first, second := make(chan result, 1), make(chan result, 1)
	go func() {
		ref, err := consumer.Manager.ImportChain(context.Background(), supplied(chainA, providerA))
		first <- result{ref, err}
	}()
	<-started
	go func() {
		ref, err := consumer.Manager.ImportChain(context.Background(), onB)
		second <- result{ref, err}
	}()
	// The second import has applied its base, and so reached the shared
	// blob, once it applies a layer.
	require.Eventually(t, func() bool { return consumer.Applies.Load() >= 1 }, 10*time.Second, 10*time.Millisecond)
	time.Sleep(100 * time.Millisecond)
	close(release)
	r1, r2 := <-first, <-second
	require.NoError(t, r1.err)
	require.NoError(t, r2.err)
	defer r1.ref.Release(context.Background())
	defer r2.ref.Release(context.Background())
	reads := providerA.reads[shared.Digest.String()].Load() + providerB.reads[shared.Digest.String()].Load()
	require.EqualValues(t, 1, reads, "the shared blob is downloaded once")
	testutil.CheckFile(t, r1.ref, "a.txt", "shared")
	testutil.CheckFile(t, r2.ref, "a.txt", "shared")
	testutil.CheckFile(t, r2.ref, "b.txt", "other base")
}

// The import's timing counts the blob fetched ahead, and a layer whose blob
// came from another import's download as joined; a layer that joins its own
// import's fetch ahead isn't.
func TestImportChainTimingCountsAheadAndJoined(t *testing.T) {
	producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
	a, _ := producer.Build(t, nil, "a.txt", "lower")
	ab, _ := producer.Build(t, a, "b.txt", "upper")
	chain := exportChain(t, ab)
	var timing bkcache.ImportTiming
	importChain(t, consumer, &bkcache.ExportChain{Layers: chain.Layers, Provider: chain.Provider, Timing: &timing})
	require.Equal(t, 1, timing.Ahead)
	require.Zero(t, timing.Joined)
}

// joinedImports starts the import of chainA, whose shared blob's read waits
// for release, then the import of the same blob on another base, and
// returns once the second import has reached the shared blob.
func joinedImports(t *testing.T, ctxA, ctxB context.Context) (consumer *testutil.Store, providers []*gatedProvider, shared string, release chan struct{}, first, second chan importResult, timingB *bkcache.ImportTiming) {
	t.Helper()
	producer := testutil.NewStore(t)
	consumer = testutil.NewStore(t)
	a, _ := producer.Build(t, nil, "a.txt", "shared")
	b, _ := producer.Build(t, nil, "b.txt", "other base")
	chainA, chainB := exportChain(t, a), exportChain(t, b)
	shared = chainA.Layers[0].Descriptor.Digest.String()
	layers := append(append([]bkcache.ExportLayer{}, chainB.Layers...), chainA.Layers...)
	started := make(chan struct{}, 4)
	release = make(chan struct{})
	providerA := newGatedProvider(chainA, shared, started, release)
	providerB := newGatedProvider(&bkcache.ExportChain{Layers: layers, Provider: chainA.Provider}, shared, started, release)
	timingB = &bkcache.ImportTiming{}
	first, second = make(chan importResult, 1), make(chan importResult, 1)
	go func() {
		ref, err := consumer.Manager.ImportChain(ctxA, supplied(chainA, providerA))
		first <- importResult{ref, err}
	}()
	<-started
	go func() {
		ref, err := consumer.Manager.ImportChain(ctxB, &bkcache.ExportChain{Layers: layers, Provider: providerB, Timing: timingB})
		second <- importResult{ref, err}
	}()
	require.Eventually(t, func() bool { return consumer.Applies.Load() >= 1 }, 10*time.Second, 10*time.Millisecond)
	time.Sleep(100 * time.Millisecond)
	return consumer, []*gatedProvider{providerA, providerB}, shared, release, first, second, timingB
}

type importResult struct {
	ref bkcache.ImmutableRef
	err error
}

// The import that started a shared download can be canceled while another
// waits for it: the download goes on for the other, and the blob is read
// once.
func TestImportChainJoinedDownloadOutlivesItsStarter(t *testing.T) {
	ctxA, cancelA := context.WithCancel(context.Background())
	defer cancelA()
	_, providers, shared, release, first, second, timingB := joinedImports(t, ctxA, context.Background())
	cancelA()
	r1 := <-first
	require.ErrorIs(t, r1.err, context.Canceled)
	close(release)
	r2 := <-second
	require.NoError(t, r2.err)
	defer r2.ref.Release(context.Background())
	require.EqualValues(t, 1, providers[0].reads[shared].Load()+providers[1].reads[shared].Load())
	testutil.CheckFile(t, r2.ref, "a.txt", "shared")
	require.Equal(t, 1, timingB.Joined)
}

// A download no import waits for anymore is canceled, and leaves no lease
// or partial content.
func TestImportChainAbandonedDownload(t *testing.T) {
	producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
	a, _ := producer.Build(t, nil, "a.txt", "abandoned")
	chain := exportChain(t, a)
	started := make(chan struct{}, 1)
	provider := newGatedProvider(chain, chain.Layers[0].Descriptor.Digest.String(), started, make(chan struct{}))
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := consumer.Manager.ImportChain(ctx, supplied(chain, provider))
		result <- err
	}()
	<-started
	cancel()
	require.ErrorIs(t, <-result, context.Canceled)
	assertNoTemporaryResources(t, consumer)
}

// A layer that fails to apply while the next layer's blob downloads ahead
// cancels that download before the import returns, leaving nothing behind.
func TestImportChainFailureCancelsFetchAhead(t *testing.T) {
	producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
	a, _ := producer.Build(t, nil, "a.txt", "lower")
	ab, _ := producer.Build(t, a, "b.txt", "upper")
	chain := exportChain(t, ab)
	upperRead := make(chan struct{}, 1)
	provider := newGatedProvider(chain, chain.Layers[1].Descriptor.Digest.String(), upperRead, make(chan struct{}))
	failure := errors.New("apply failed")
	consumer.BeforeApply = func(_ context.Context, desc ocispecs.Descriptor) error {
		if desc.Digest == chain.Layers[0].Descriptor.Digest {
			<-upperRead
			return failure
		}
		return nil
	}
	_, err := consumer.Manager.ImportChain(context.Background(), supplied(chain, provider))
	require.ErrorIs(t, err, failure)
	owners, err := consumer.Leases.List(context.Background())
	require.NoError(t, err)
	require.Empty(t, owners, "no lease of the import or of its download remains")
	writes, err := consumer.Content.ListStatuses(context.Background())
	require.NoError(t, err)
	require.Empty(t, writes, "the download ahead left no partial content")
	// The failed layer's mutable snapshot goes at the next collection, as
	// after any failed apply.
	consumer.GC(t)
	assertNoTemporaryResources(t, consumer)
}

// failingGatedProvider serves a chain's blobs, but its reads of one blob
// wait for release, then fail.
func failingGatedProvider(chain *bkcache.ExportChain, gated string, started chan<- struct{}, release <-chan struct{}) *gatedProvider {
	p := newGatedProvider(chain, gated, started, release)
	gate := p.BeforeRead
	p.BeforeRead = func(ctx context.Context, desc ocispecs.Descriptor) error {
		if err := gate(ctx, desc); err != nil {
			return err
		}
		if desc.Digest.String() == gated {
			return errors.New("this import's source failed")
		}
		return nil
	}
	return p
}

// Imports waiting on a download that failed fail with it, as a content
// failure, however healthy their own sources: nobody downloads the blob
// again.
func TestImportChainJoinersFailWithTheDownload(t *testing.T) {
	producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
	shared, _ := producer.Build(t, nil, "a.txt", "shared")
	b1, _ := producer.Build(t, nil, "b1.txt", "first base")
	b2, _ := producer.Build(t, nil, "b2.txt", "second base")
	chainA := exportChain(t, shared)
	digestA := chainA.Layers[0].Descriptor.Digest.String()
	started, release := make(chan struct{}, 8), make(chan struct{})
	failing := failingGatedProvider(chainA, digestA, started, release)
	var joiners []*gatedProvider
	results := make(chan importResult, 3)
	go func() {
		ref, err := consumer.Manager.ImportChain(context.Background(), supplied(chainA, failing))
		results <- importResult{ref, err}
	}()
	<-started
	for _, base := range []bkcache.ImmutableRef{b1, b2} {
		layers := append(append([]bkcache.ExportLayer{}, exportChain(t, base).Layers...), chainA.Layers...)
		provider := newGatedProvider(&bkcache.ExportChain{Layers: layers, Provider: chainA.Provider}, "", nil, nil)
		joiners = append(joiners, provider)
		go func() {
			ref, err := consumer.Manager.ImportChain(context.Background(), &bkcache.ExportChain{Layers: layers, Provider: provider})
			results <- importResult{ref, err}
		}()
	}
	// Both joiners have applied their base, and so reached the shared blob.
	require.Eventually(t, func() bool { return consumer.Applies.Load() >= 2 }, 10*time.Second, 10*time.Millisecond)
	time.Sleep(100 * time.Millisecond)
	close(release)
	for range 3 {
		r := <-results
		if r.err == nil {
			r.ref.Release(context.Background())
		}
		var contentErr *bkcache.ChainContentError
		require.ErrorAs(t, r.err, &contentErr, "every import fails as content")
		require.Equal(t, digestA, contentErr.Layer.String())
	}
	require.EqualValues(t, 1, failing.reads[digestA].Load())
	require.Zero(t, joiners[0].reads[digestA].Load()+joiners[1].reads[digestA].Load(), "no import downloads the blob again")
}

// An import that joins another import's download started ahead, which
// fails, fails with it, and so does the import that started it: the blob is
// read once.
func TestImportChainJoinerOfAFailedFetchAheadFails(t *testing.T) {
	producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
	base, _ := producer.Build(t, nil, "base.txt", "base")
	shared, _ := producer.Build(t, nil, "a.txt", "shared")
	chainBase, chainShared := exportChain(t, base), exportChain(t, shared)
	digestShared := chainShared.Layers[0].Descriptor.Digest.String()
	layers := append(append([]bkcache.ExportLayer{}, chainBase.Layers...), chainShared.Layers...)
	started, release := make(chan struct{}, 4), make(chan struct{})
	failing := failingGatedProvider(&bkcache.ExportChain{Layers: layers, Provider: chainShared.Provider}, digestShared, started, release)
	// Hold the base's apply until the other import has seen the shared
	// blob's download fail, so this import reaches that layer only then.
	baseApplied := make(chan struct{})
	consumer.BeforeApply = func(_ context.Context, desc ocispecs.Descriptor) error {
		if desc.Digest == chainBase.Layers[0].Descriptor.Digest {
			<-baseApplied
		}
		return nil
	}
	first := make(chan importResult, 1)
	go func() {
		ref, err := consumer.Manager.ImportChain(context.Background(), &bkcache.ExportChain{Layers: layers, Provider: failing})
		first <- importResult{ref, err}
	}()
	// The shared blob's download started ahead, while the base applies.
	<-started
	healthy := newGatedProvider(chainShared, "", nil, nil)
	second := make(chan importResult, 1)
	go func() {
		ref, err := consumer.Manager.ImportChain(context.Background(), supplied(chainShared, healthy))
		second <- importResult{ref, err}
	}()
	time.Sleep(100 * time.Millisecond)
	close(release)
	r2 := <-second
	var contentErr *bkcache.ChainContentError
	require.ErrorAs(t, r2.err, &contentErr, "the joiner fails with the download it joined")
	close(baseApplied)
	r1 := <-first
	require.ErrorAs(t, r1.err, &contentErr)
	require.EqualValues(t, 1, failing.reads[digestShared].Load(), "the failed source is read once")
	require.Zero(t, healthy.reads[digestShared].Load(), "the joiner doesn't download the blob itself")
}
