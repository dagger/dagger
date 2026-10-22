package snapshots_test

import (
	"context"
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
