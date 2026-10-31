package snapshots_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/dagger/dagger/engine/remotecache/pack"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

// packProvider serves the blobs of its pack only through the pack: reading
// a member alone fails. Other blobs come from the inner provider.
type packProvider struct {
	content.InfoReaderProvider
	id      digest.Digest
	data    []byte
	members map[digest.Digest]bool
	opens   atomic.Int64
	// gate, when set, holds every OpenPack until it is closed.
	gate chan struct{}
	// fail, when set, fails every OpenPack.
	fail error
}

// newPackProvider packs the given layers' blobs, read from inner, in order.
func newPackProvider(t *testing.T, inner content.InfoReaderProvider, layers ...ocispecs.Descriptor) *packProvider {
	t.Helper()
	p := &packProvider{InfoReaderProvider: inner, members: map[digest.Digest]bool{}}
	var members []pack.Member
	var blobs [][]byte
	for _, desc := range layers {
		data, err := content.ReadBlob(context.Background(), inner, desc)
		require.NoError(t, err)
		members = append(members, pack.Member{Digest: desc.Digest, Size: desc.Size})
		blobs = append(blobs, data)
		p.members[desc.Digest] = true
	}
	header, err := pack.Header(members)
	require.NoError(t, err)
	p.id, err = pack.ID(members)
	require.NoError(t, err)
	p.data = append(p.data, header...)
	for _, blob := range blobs {
		p.data = append(p.data, blob...)
	}
	return p
}

func (p *packProvider) ReaderAt(ctx context.Context, desc ocispecs.Descriptor) (content.ReaderAt, error) {
	if p.members[desc.Digest] {
		return nil, errors.New("a packed blob read alone")
	}
	return p.InfoReaderProvider.ReaderAt(ctx, desc)
}

func (p *packProvider) PackOf(blob digest.Digest) (digest.Digest, bool) {
	return p.id, p.members[blob]
}

func (p *packProvider) OpenPack(ctx context.Context, blob digest.Digest) (io.ReadCloser, error) {
	p.opens.Add(1)
	if p.gate != nil {
		select {
		case <-p.gate:
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		}
	}
	if p.fail != nil {
		return nil, p.fail
	}
	return io.NopCloser(bytes.NewReader(p.data)), nil
}

// A chain whose layers are stored in one pack imports from one read of the
// pack.
func TestImportChainFromPack(t *testing.T) {
	producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
	a, _ := producer.Build(t, nil, "a.txt", "lower")
	ab, _ := producer.Build(t, a, "b.txt", "upper")
	chain := exportChain(t, ab)
	provider := newPackProvider(t, chain.Provider, chain.Layers[0].Descriptor, chain.Layers[1].Descriptor)
	ref := importChain(t, consumer, supplied(chain, provider))
	require.EqualValues(t, 1, provider.opens.Load(), "the pack is read once for both layers")
	testutil.CheckFile(t, ref, "a.txt", "lower")
	testutil.CheckFile(t, ref, "b.txt", "upper")
}

// Two imports that need members of the same pack at once read it once.
func TestImportChainsJoinPack(t *testing.T) {
	producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
	a, _ := producer.Build(t, nil, "a.txt", "first")
	b, _ := producer.Build(t, nil, "b.txt", "second")
	chainA, chainB := exportChain(t, a), exportChain(t, b)
	gate := make(chan struct{})
	providerA := newPackProvider(t, chainA.Provider, chainA.Layers[0].Descriptor, chainB.Layers[0].Descriptor)
	providerA.gate = gate
	// The same pack, through another install's provider.
	providerB := newPackProvider(t, chainA.Provider, chainA.Layers[0].Descriptor, chainB.Layers[0].Descriptor)
	providerB.InfoReaderProvider = chainB.Provider
	providerB.gate = gate

	type result struct {
		ref bkcache.ImmutableRef
		err error
	}
	first, second := make(chan result, 1), make(chan result, 1)
	go func() {
		ref, err := consumer.Manager.ImportChain(context.Background(), supplied(chainA, providerA))
		first <- result{ref, err}
	}()
	require.Eventually(t, func() bool { return providerA.opens.Load() == 1 }, 10*time.Second, 10*time.Millisecond)
	go func() {
		ref, err := consumer.Manager.ImportChain(context.Background(), supplied(chainB, providerB))
		second <- result{ref, err}
	}()
	time.Sleep(100 * time.Millisecond)
	close(gate)
	r1, r2 := <-first, <-second
	require.NoError(t, r1.err)
	require.NoError(t, r2.err)
	defer r1.ref.Release(context.Background())
	defer r2.ref.Release(context.Background())
	require.EqualValues(t, 1, providerA.opens.Load()+providerB.opens.Load(), "the pack is read once")
	testutil.CheckFile(t, r1.ref, "a.txt", "first")
	testutil.CheckFile(t, r2.ref, "b.txt", "second")
}

// A pack's members that no import was waiting for stay in the content store
// after the pack is read, so a later import of one needs no read at all.
func TestImportChainPackHoldsSiblings(t *testing.T) {
	producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
	a, _ := producer.Build(t, nil, "a.txt", "first")
	b, _ := producer.Build(t, nil, "b.txt", "second")
	chainA, chainB := exportChain(t, a), exportChain(t, b)
	provider := newPackProvider(t, chainA.Provider, chainA.Layers[0].Descriptor, chainB.Layers[0].Descriptor)
	importChain(t, consumer, supplied(chainA, provider))

	later := newPackProvider(t, chainA.Provider, chainA.Layers[0].Descriptor, chainB.Layers[0].Descriptor)
	later.InfoReaderProvider = chainB.Provider
	later.fail = errors.New("the pack is read again")
	ref := importChain(t, consumer, supplied(chainB, later))
	require.Zero(t, later.opens.Load(), "the sibling was held from the first read")
	testutil.CheckFile(t, ref, "b.txt", "second")
}

// A pack whose header doesn't match its ID fails the import as a content
// failure of the layer that needed it.
func TestImportChainFromPackWithWrongID(t *testing.T) {
	producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
	a, _ := producer.Build(t, nil, "a.txt", "first")
	chain := exportChain(t, a)
	provider := newPackProvider(t, chain.Provider, chain.Layers[0].Descriptor)
	provider.id = digest.FromString("another pack")
	_, err := consumer.Manager.ImportChain(context.Background(), supplied(chain, provider))
	var contentErr *bkcache.ChainContentError
	require.ErrorAs(t, err, &contentErr)
	require.Equal(t, chain.Layers[0].Descriptor.Digest, contentErr.Layer)
	require.Equal(t, "provider", contentErr.Stage)
	require.EqualValues(t, 1, provider.opens.Load(), "the pack was read, and its header refused")
}

// A pack whose header is right but one of whose members is corrupt fails the
// layer that needs that member as a content failure, after the members
// before it were written.
func TestImportChainFromPackWithCorruptMember(t *testing.T) {
	producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
	a, _ := producer.Build(t, nil, "a.txt", "lower")
	ab, _ := producer.Build(t, a, "b.txt", "upper")
	chain := exportChain(t, ab)
	provider := newPackProvider(t, chain.Provider, chain.Layers[0].Descriptor, chain.Layers[1].Descriptor)
	// Flip the pack's last byte, inside the second member.
	provider.data[len(provider.data)-1] ^= 0xff
	_, err := consumer.Manager.ImportChain(context.Background(), supplied(chain, provider))
	var contentErr *bkcache.ChainContentError
	require.ErrorAs(t, err, &contentErr)
	require.Equal(t, chain.Layers[1].Descriptor.Digest, contentErr.Layer)
	require.Equal(t, "copy", contentErr.Stage)
	require.Positive(t, provider.opens.Load())
}

// An import that joined another import's read of a pack, which failed for
// that import's provider, reads the pack once more with its own.
func TestImportChainsJoinPackRetriesOwnProvider(t *testing.T) {
	producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
	a, _ := producer.Build(t, nil, "a.txt", "first")
	b, _ := producer.Build(t, nil, "b.txt", "second")
	chainA, chainB := exportChain(t, a), exportChain(t, b)
	gate := make(chan struct{})
	providerA := newPackProvider(t, chainA.Provider, chainA.Layers[0].Descriptor, chainB.Layers[0].Descriptor)
	providerA.gate, providerA.fail = gate, errors.New("expired address")
	providerB := newPackProvider(t, chainA.Provider, chainA.Layers[0].Descriptor, chainB.Layers[0].Descriptor)
	providerB.InfoReaderProvider = chainB.Provider

	first := make(chan error, 1)
	go func() {
		ref, err := consumer.Manager.ImportChain(context.Background(), supplied(chainA, providerA))
		if err == nil {
			_ = ref.Release(context.Background())
		}
		first <- err
	}()
	require.Eventually(t, func() bool { return providerA.opens.Load() == 1 }, 10*time.Second, 10*time.Millisecond)
	second := make(chan error, 1)
	var ref bkcache.ImmutableRef
	go func() {
		var err error
		ref, err = consumer.Manager.ImportChain(context.Background(), supplied(chainB, providerB))
		second <- err
	}()
	// The second import has joined the first one's read by now.
	time.Sleep(100 * time.Millisecond)
	close(gate)
	var contentErr *bkcache.ChainContentError
	require.ErrorAs(t, <-first, &contentErr, "the first import's own provider failed")
	require.NoError(t, <-second)
	defer ref.Release(context.Background())
	require.EqualValues(t, 1, providerB.opens.Load(), "the second import read the pack with its own provider")
	testutil.CheckFile(t, ref, "b.txt", "second")
}
