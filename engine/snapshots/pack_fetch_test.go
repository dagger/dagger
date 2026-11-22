package snapshots_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/leases"
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
	// opened, when set, sees each OpenPack's context.
	opened func(context.Context)
	// ranged serves ranges of the pack, and ranges records each
	// OpenPackRange's offset and size. Without it every OpenPackRange is
	// answered with ErrNoPackRanges, as a provider that reads none answers,
	// and the pack is read whole.
	ranged   bool
	rangesMu sync.Mutex
	ranges   [][2]int64
}

// newPackProvider packs the given layers' blobs, read from inner, in order.
func newPackProvider(t *testing.T, inner content.InfoReaderProvider, layers ...ocispecs.Descriptor) *packProvider {
	t.Helper()
	members := make([]any, len(layers))
	for i, l := range layers {
		members[i] = l
	}
	return newMixedPackProvider(t, inner, members...)
}

// newMixedPackProvider packs, in order, each member: a layer's blob read from
// inner (an ocispecs.Descriptor), or other bytes ([]byte), as another
// chain's blob that shares the pack.
func newMixedPackProvider(t *testing.T, inner content.InfoReaderProvider, in ...any) *packProvider {
	t.Helper()
	p := &packProvider{InfoReaderProvider: inner, members: map[digest.Digest]bool{}}
	var members []pack.Member
	var blobs [][]byte
	for _, x := range in {
		var data []byte
		switch v := x.(type) {
		case ocispecs.Descriptor:
			var err error
			data, err = content.ReadBlob(context.Background(), inner, v)
			require.NoError(t, err)
		case []byte:
			data = v
		}
		dgst := digest.FromBytes(data)
		members = append(members, pack.Member{Digest: dgst, Size: int64(len(data))})
		blobs = append(blobs, data)
		p.members[dgst] = true
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

func (p *packProvider) OpenPackRange(_ context.Context, _ digest.Digest, off, size int64) (io.ReadCloser, error) {
	if !p.ranged {
		return nil, bkcache.ErrNoPackRanges
	}
	p.rangesMu.Lock()
	p.ranges = append(p.ranges, [2]int64{off, size})
	p.rangesMu.Unlock()
	end := min(off+size, int64(len(p.data)))
	return io.NopCloser(bytes.NewReader(p.data[off:end])), nil
}

// rangeBytes sums the bytes the provider's range reads asked for, cut at the
// pack's end.
func (p *packProvider) rangeBytes() (n int64, reads int) {
	p.rangesMu.Lock()
	defer p.rangesMu.Unlock()
	for _, r := range p.ranges {
		n += min(r[0]+r[1], int64(len(p.data))) - r[0]
	}
	return n, len(p.ranges)
}

func (p *packProvider) PackOf(blob digest.Digest) (digest.Digest, bool) {
	return p.id, p.members[blob]
}

func (p *packProvider) OpenPack(ctx context.Context, blob digest.Digest) (io.ReadCloser, error) {
	p.opens.Add(1)
	if p.opened != nil {
		p.opened(ctx)
	}
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

// A pack can hold a blob that the engine's image already holds in place,
// such as a base layer the image's entries made. The store answers for that
// blob before the engine has recorded it, so the import records it as it is
// and reads past its bytes in the pack, and the chain imports whole.
func TestImportChainFromPackHoldingAnImageBlob(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	producer := testutil.NewStore(t)
	a, _ := producer.Build(t, nil, "a.txt", "lower, in the image")
	ab, _ := producer.Build(t, a, "b.txt", "upper")
	chain := exportChain(t, ab)
	lower := chain.Layers[0].Descriptor
	blob, err := content.ReadBlob(ctx, chain.Provider, lower)
	require.NoError(t, err)
	image := t.TempDir()
	name := filepath.Join(image, "blobs", lower.Digest.Algorithm().String(), lower.Digest.Encoded())
	require.NoError(t, os.MkdirAll(filepath.Dir(name), 0o755))
	require.NoError(t, os.WriteFile(name, blob, 0o444))

	consumer := testutil.NewStoreWithImageBlobs(t, image)
	provider := newPackProvider(t, chain.Provider, lower, chain.Layers[1].Descriptor)
	ref := importChain(t, consumer, supplied(chain, provider))
	testutil.CheckFile(t, ref, "a.txt", "lower, in the image")
	testutil.CheckFile(t, ref, "b.txt", "upper")
	info, err := consumer.Content.Info(ctx, lower.Digest)
	require.NoError(t, err, "the image's blob is recorded")
	require.Equal(t, lower.Size, info.Size)
}

// A download of a packed blob that a sibling's read already stored, as one
// that found the blob missing just before that read ended, reads nothing:
// the pack isn't read again.
func TestPackedDownloadAfterSiblingReadReadsNothing(t *testing.T) {
	producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
	a, _ := producer.Build(t, nil, "a.txt", "first")
	b, _ := producer.Build(t, nil, "b.txt", "second")
	chainA, chainB := exportChain(t, a), exportChain(t, b)
	provider := newPackProvider(t, chainA.Provider, chainA.Layers[0].Descriptor, chainB.Layers[0].Descriptor)
	importChain(t, consumer, supplied(chainA, provider))

	later := newPackProvider(t, chainA.Provider, chainA.Layers[0].Descriptor, chainB.Layers[0].Descriptor)
	require.NoError(t, bkcache.DownloadChainContent(t.Context(), consumer.Manager, chainB.Layers[0].Descriptor, later))
	require.Zero(t, later.opens.Load(), "the sibling's read stored the blob")
}

// A download of a packed blob whose sibling's read of the pack ends after
// the download found the blob missing, but before it looks for a read to
// join, reads nothing: the sibling's read stored the blob.
func TestPackedDownloadOverlappingSiblingReadReadsNothing(t *testing.T) {
	producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
	a, _ := producer.Build(t, nil, "a.txt", "first")
	b, _ := producer.Build(t, nil, "b.txt", "second")
	chainA, chainB := exportChain(t, a), exportChain(t, b)
	sibling := newPackProvider(t, chainA.Provider, chainA.Layers[0].Descriptor, chainB.Layers[0].Descriptor)
	sibling.gate = make(chan struct{})
	later := newPackProvider(t, chainA.Provider, chainA.Layers[0].Descriptor, chainB.Layers[0].Descriptor)

	// The sibling's import holds its read open; the download finds its blob
	// missing, and is held as it looks for a read to join until the
	// sibling's read has ended.
	imported := make(chan struct{})
	go func() {
		defer close(imported)
		importChain(t, consumer, supplied(chainA, sibling))
	}()
	require.Eventually(t, func() bool { return sibling.opens.Load() == 1 }, 10*time.Second, time.Millisecond)
	held := make(chan struct{})
	restore := bkcache.SetBeforeJoinPackFetch(func() {
		select {
		case <-held:
		default:
			close(held)
			close(sibling.gate)
			<-imported
		}
	})
	t.Cleanup(restore)
	require.NoError(t, bkcache.DownloadChainContent(t.Context(), consumer.Manager, chainB.Layers[0].Descriptor, later))
	require.Zero(t, later.opens.Load(), "the sibling's read stored the blob")
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
	require.Eventually(t, func() bool { return bkcache.PackWaiters(consumer.Manager, providerA.id) == 2 }, 10*time.Second, 10*time.Millisecond, "the second import joined the read")
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

// An import that joined another import's read of a pack, which failed,
// fails with it as a content failure, though its own provider is healthy: it
// doesn't read the pack again.
func TestImportChainsJoinPackFailWithTheRead(t *testing.T) {
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
	go func() {
		ref, err := consumer.Manager.ImportChain(context.Background(), supplied(chainB, providerB))
		if err == nil {
			_ = ref.Release(context.Background())
		}
		second <- err
	}()
	require.Eventually(t, func() bool { return bkcache.PackWaiters(consumer.Manager, providerA.id) == 2 }, 10*time.Second, 10*time.Millisecond, "the second import joined the first one's read")
	close(gate)
	var contentErr *bkcache.ChainContentError
	require.ErrorAs(t, <-first, &contentErr, "the first import's own provider failed")
	require.ErrorAs(t, <-second, &contentErr, "the second import fails with the read it joined")
	require.Equal(t, chainB.Layers[0].Descriptor.Digest, contentErr.Layer)
	require.Zero(t, providerB.opens.Load(), "the second import doesn't read the pack itself")
}

// A read ends before its lease can expire: its deadline, which the provider
// sees, is before the expiry containerd keeps on the lease the members are
// written under.
func TestPackReadEndsBeforeItsLease(t *testing.T) {
	producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
	a, _ := producer.Build(t, nil, "a.txt", "first")
	chain := exportChain(t, a)
	provider := newPackProvider(t, chain.Provider, chain.Layers[0].Descriptor)
	// Observed on the read's goroutine, checked on the test's.
	type observed struct {
		deadline, expires time.Time
		hasDeadline       bool
		err               error
	}
	seen := make(chan observed, 1)
	provider.opened = func(ctx context.Context) {
		var o observed
		o.deadline, o.hasDeadline = ctx.Deadline()
		id, _ := leases.FromContext(ctx)
		found, err := consumer.Leases.List(ctx, "id=="+id)
		switch {
		case err != nil:
			o.err = err
		case len(found) != 1:
			o.err = fmt.Errorf("found %d leases %q", len(found), id)
		default:
			o.expires, o.err = time.Parse(time.RFC3339, found[0].Labels["containerd.io/gc.expire"])
		}
		seen <- o
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ref, err := consumer.Manager.ImportChain(ctx, supplied(chain, provider))
	require.NoError(t, err)
	defer ref.Release(context.Background())
	o := <-seen
	require.NoError(t, o.err)
	require.True(t, o.hasDeadline, "a read has a deadline")
	require.True(t, o.deadline.Before(o.expires), "the read's deadline %s is before its lease's expiry %s", o.deadline, o.expires)
	require.Greater(t, o.expires.Sub(o.deadline), bkcache.PackLeaseTTL/4, "the waiting imports have time to link their members")
}

// A read still waiting for a slot at its deadline fails as content too, so
// the import falls back instead of failing the engine's way.
func TestPackReadCutOffWaitingForASlot(t *testing.T) {
	producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
	var busyImports sync.WaitGroup
	defer busyImports.Wait()
	gate := make(chan struct{})
	defer close(gate)
	// Fill every read slot with a pack that answers only once the test ends.
	for i := range bkcache.PackReads {
		ref, _ := producer.Build(t, nil, fmt.Sprintf("busy-%d.txt", i), fmt.Sprintf("busy %d", i))
		chain := exportChain(t, ref)
		busy := newPackProvider(t, chain.Provider, chain.Layers[0].Descriptor)
		busy.gate = gate
		busyImports.Go(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if ref, err := consumer.Manager.ImportChain(ctx, supplied(chain, busy)); err == nil {
				_ = ref.Release(context.Background())
			}
		})
		require.Eventually(t, func() bool { return busy.opens.Load() == 1 }, 10*time.Second, 10*time.Millisecond)
	}
	restore := bkcache.SetPackReadLimit(100 * time.Millisecond)
	defer restore()
	a, _ := producer.Build(t, nil, "a.txt", "first")
	chain := exportChain(t, a)
	provider := newPackProvider(t, chain.Provider, chain.Layers[0].Descriptor)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := consumer.Manager.ImportChain(ctx, supplied(chain, provider))
	var contentErr *bkcache.ChainContentError
	require.ErrorAs(t, err, &contentErr)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Zero(t, provider.opens.Load(), "the read never got a slot")
}

// A read that outlives its bound is cut off, and the import that needed it
// fails as content instead of waiting on.
func TestPackReadCutOffAtItsLimit(t *testing.T) {
	t.Cleanup(bkcache.SetPackReadLimit(100 * time.Millisecond))
	producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
	a, _ := producer.Build(t, nil, "a.txt", "first")
	chain := exportChain(t, a)
	provider := newPackProvider(t, chain.Provider, chain.Layers[0].Descriptor)
	// The pack never answers.
	provider.gate = make(chan struct{})
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := consumer.Manager.ImportChain(ctx, supplied(chain, provider))
	var contentErr *bkcache.ChainContentError
	require.ErrorAs(t, err, &contentErr)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(start), 10*time.Second, "the read was cut off at its limit")
	require.EqualValues(t, 1, provider.opens.Load())
}

// otherChainBlob is n bytes of another chain's blob, different for each seed.
func otherChainBlob(seed string, n int) []byte {
	b := bytes.Repeat([]byte(seed), n/len(seed)+1)
	return b[:n]
}

// A chain that needs a small share of a pack reads its header and then only
// its own members, in a range each where they lie apart, never the whole
// pack: the other chains' blobs between them stay unread.
func TestImportChainReadsOnlyItsMembersOfAPack(t *testing.T) {
	producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
	a, _ := producer.Build(t, nil, "a.txt", "lower")
	ab, _ := producer.Build(t, a, "b.txt", "upper")
	chain := exportChain(t, ab)
	provider := newMixedPackProvider(t, chain.Provider,
		chain.Layers[0].Descriptor, otherChainBlob("other one", 2<<20),
		chain.Layers[1].Descriptor, otherChainBlob("other two", 2<<20))
	provider.ranged = true
	ref := importChain(t, consumer, supplied(chain, provider))
	testutil.CheckFile(t, ref, "a.txt", "lower")
	testutil.CheckFile(t, ref, "b.txt", "upper")
	require.Zero(t, provider.opens.Load(), "the pack isn't read whole")
	read, reads := provider.rangeBytes()
	require.Equal(t, 3, reads, "the header, then one range for each member, 2 MiB apart")
	require.Less(t, read, int64(256<<10), "the other chains' 4 MiB stay unread")
}

// A chain that needs most of a pack reads it whole, once, after its header.
func TestImportChainReadsMostOfAPackWhole(t *testing.T) {
	producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
	a, _ := producer.Build(t, nil, "a.txt", strings.Repeat("lower ", 100<<10))
	ab, _ := producer.Build(t, a, "b.txt", "upper")
	chain := exportChain(t, ab)
	provider := newMixedPackProvider(t, chain.Provider,
		chain.Layers[0].Descriptor, otherChainBlob("other", 1<<10), chain.Layers[1].Descriptor)
	provider.ranged = true
	ref := importChain(t, consumer, supplied(chain, provider))
	testutil.CheckFile(t, ref, "a.txt", strings.Repeat("lower ", 100<<10))
	testutil.CheckFile(t, ref, "b.txt", "upper")
	require.EqualValues(t, 1, provider.opens.Load(), "read whole, once")
	_, reads := provider.rangeBytes()
	require.Equal(t, 1, reads, "only the header by range")
}

// A provider that reads no ranges has every pack read whole.
func TestImportChainReadsAPackWholeWithoutRanges(t *testing.T) {
	producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
	a, _ := producer.Build(t, nil, "a.txt", "lower")
	chain := exportChain(t, a)
	provider := newMixedPackProvider(t, chain.Provider, chain.Layers[0].Descriptor, otherChainBlob("other", 2<<20))
	ref := importChain(t, consumer, supplied(chain, provider))
	testutil.CheckFile(t, ref, "a.txt", "lower")
	require.EqualValues(t, 1, provider.opens.Load())
}

// A member a range read finds corrupt fails the import, as a whole read's
// does: nothing is applied from bytes that aren't the blob's.
func TestImportChainFromPackRangeWithCorruptMember(t *testing.T) {
	producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
	a, _ := producer.Build(t, nil, "a.txt", "lower")
	chain := exportChain(t, a)
	provider := newMixedPackProvider(t, chain.Provider, chain.Layers[0].Descriptor, otherChainBlob("other", 2<<20))
	header, err := pack.Header([]pack.Member{{Digest: chain.Layers[0].Descriptor.Digest, Size: chain.Layers[0].Descriptor.Size}, {Digest: digest.FromBytes(otherChainBlob("other", 2<<20)), Size: 2 << 20}})
	require.NoError(t, err)
	provider.data[len(header)] ^= 0xff
	provider.ranged = true
	_, err = consumer.Manager.ImportChain(context.Background(), supplied(chain, provider))
	require.Error(t, err)
	require.Zero(t, provider.opens.Load(), "the pack isn't read whole")
}

// A pack whose header doesn't hash to its ID fails a range read's import,
// as it fails a whole read's.
func TestImportChainFromPackRangeWithWrongID(t *testing.T) {
	producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
	a, _ := producer.Build(t, nil, "a.txt", "lower")
	chain := exportChain(t, a)
	provider := newMixedPackProvider(t, chain.Provider, chain.Layers[0].Descriptor, otherChainBlob("other", 2<<20))
	provider.ranged = true
	provider.id = digest.FromString("another pack")
	_, err := consumer.Manager.ImportChain(context.Background(), supplied(chain, provider))
	require.Error(t, err)
	require.Zero(t, provider.opens.Load())
}

// multiPackProvider serves a chain's blobs only through its packs: reading
// a member alone fails.
type multiPackProvider struct {
	content.InfoReaderProvider
	packOf map[digest.Digest]digest.Digest
	packs  map[digest.Digest][]byte
	// opened, when set, runs as each OpenPack starts; its error fails the
	// read.
	opened func(context.Context) error
	// ranged serves ranges of the packs; ranged, when set, runs as each
	// OpenPackRange starts, and its error fails the read. Without it every
	// OpenPackRange is answered with ErrNoPackRanges and packs are read
	// whole.
	ranged func(ctx context.Context, pack digest.Digest, off, size int64) error
}

// newMultiPackProvider packs each group of the given layers' blobs, read
// from inner, in order, as a pack of its own. With apart above zero, each
// layer's blob is followed in its pack by another chain's blob of apart
// bytes.
func newMultiPackProvider(t *testing.T, inner content.InfoReaderProvider, apart int, groups ...[]ocispecs.Descriptor) *multiPackProvider {
	t.Helper()
	p := &multiPackProvider{InfoReaderProvider: inner, packOf: map[digest.Digest]digest.Digest{}, packs: map[digest.Digest][]byte{}}
	for g, group := range groups {
		members := make([]pack.Member, 0, 2*len(group))
		var body []byte
		add := func(data []byte) {
			members = append(members, pack.Member{Digest: digest.FromBytes(data), Size: int64(len(data))})
			body = append(body, data...)
		}
		for i, desc := range group {
			data, err := content.ReadBlob(context.Background(), inner, desc)
			require.NoError(t, err)
			add(data)
			if apart > 0 {
				add(otherChainBlob(fmt.Sprintf("other %d %d ", g, i), apart))
			}
		}
		header, err := pack.Header(members)
		require.NoError(t, err)
		id, err := pack.ID(members)
		require.NoError(t, err)
		p.packs[id] = append(header, body...)
		for _, desc := range group {
			p.packOf[desc.Digest] = id
		}
	}
	return p
}

func (p *multiPackProvider) ReaderAt(ctx context.Context, desc ocispecs.Descriptor) (content.ReaderAt, error) {
	if _, ok := p.packOf[desc.Digest]; ok {
		return nil, errors.New("a packed blob read alone")
	}
	return p.InfoReaderProvider.ReaderAt(ctx, desc)
}

func (p *multiPackProvider) PackOf(blob digest.Digest) (digest.Digest, bool) {
	id, ok := p.packOf[blob]
	return id, ok
}

func (p *multiPackProvider) OpenPack(ctx context.Context, blob digest.Digest) (io.ReadCloser, error) {
	if p.opened != nil {
		if err := p.opened(ctx); err != nil {
			return nil, err
		}
	}
	return io.NopCloser(bytes.NewReader(p.packs[p.packOf[blob]])), nil
}

func (p *multiPackProvider) OpenPackRange(ctx context.Context, blob digest.Digest, off, size int64) (io.ReadCloser, error) {
	if p.ranged == nil {
		return nil, bkcache.ErrNoPackRanges
	}
	id := p.packOf[blob]
	if err := p.ranged(ctx, id, off, size); err != nil {
		return nil, err
	}
	data := p.packs[id]
	end := min(off+size, int64(len(data)))
	return io.NopCloser(bytes.NewReader(data[off:end])), nil
}

// A chain downloads ahead by reads of the blob store, not by layers: the
// layers whose blobs one pack holds are one download, its read. Three packs
// of 20 layers each, more layers than an import holds downloads ahead, are
// read at once, which a provider that answers no read until all three are
// in flight checks.
func TestImportChainReadsItsPacksAtOnce(t *testing.T) {
	t.Parallel()
	const packs, perPack = 3, 20
	producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
	chain := layeredChain(t, producer, "packed", packs*perPack)
	groups := make([][]ocispecs.Descriptor, packs)
	for i, layer := range chain.Layers {
		groups[i/perPack] = append(groups[i/perPack], layer.Descriptor)
	}
	provider := newMultiPackProvider(t, chain.Provider, 0, groups...)
	gauge := &getGauge{}
	all := make(chan struct{})
	var arrived atomic.Int64
	provider.opened = func(ctx context.Context) error {
		gauge.enter()
		defer gauge.inflight.Add(-1)
		if arrived.Add(1) == packs {
			close(all)
		}
		select {
		case <-all:
			return nil
		case <-time.After(10 * time.Second):
			return errors.New("the chain's pack reads were not all in flight at once")
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
	ref := importChain(t, consumer, supplied(chain, provider))
	require.EqualValues(t, packs, gauge.most.Load(), "every pack's read in flight at once")
	require.EqualValues(t, packs, arrived.Load(), "each pack read once")
	testutil.CheckFile(t, ref, "packed-00.txt", "packed 0")
	testutil.CheckFile(t, ref, "packed-59.txt", "packed 59")
}

// A chain reads a pack it needs little of by its own members' spans, one
// span after another: however the pack is read, it is one read of the blob
// store in flight, as the chain's downloads ahead count it. Three packs
// whose 12 layers each lie 1 MiB apart, among another chain's blobs, are
// read at once, one span of each at a time, which a provider that holds
// every span until the test lets go checks.
func TestImportChainReadsAPacksSpansOneAtATime(t *testing.T) {
	t.Parallel()
	const packs, perPack = 3, 12
	producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
	chain := layeredChain(t, producer, "spans", packs*perPack)
	groups := make([][]ocispecs.Descriptor, packs)
	for i, layer := range chain.Layers {
		groups[i/perPack] = append(groups[i/perPack], layer.Descriptor)
	}
	provider := newMultiPackProvider(t, chain.Provider, 1<<20, groups...)
	release := make(chan struct{})
	var released sync.Once
	var mu sync.Mutex
	inflight, most := map[digest.Digest]int{}, map[digest.Digest]int{}
	all := 0
	provider.ranged = func(ctx context.Context, id digest.Digest, off, _ int64) error {
		if off == 0 {
			// The pack's header.
			return nil
		}
		mu.Lock()
		inflight[id]++
		all++
		most[id] = max(most[id], inflight[id])
		mu.Unlock()
		defer func() {
			mu.Lock()
			inflight[id]--
			all--
			mu.Unlock()
		}()
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
	reading := func() (int, int) {
		mu.Lock()
		defer mu.Unlock()
		most1 := 0
		for _, n := range most {
			most1 = max(most1, n)
		}
		return all, most1
	}
	done := make(chan error, 1)
	var ref bkcache.ImmutableRef
	go func() {
		var err error
		ref, err = consumer.Manager.ImportChain(context.Background(), supplied(chain, provider))
		done <- err
	}()
	var importErr error
	finished := false
	wait := func() error {
		if !finished {
			select {
			case importErr = <-done:
				finished = true
			case <-time.After(30 * time.Second):
				return errors.New("the import never finished")
			}
		}
		return importErr
	}
	// An import still held when the test fails ends, and lets go of what
	// it imported, before the stores go.
	t.Cleanup(func() {
		released.Do(func() { close(release) })
		if wait() == nil && ref != nil {
			_ = ref.Release(context.Background())
		}
	})
	require.Eventually(t, func() bool { n, _ := reading(); return n == packs }, 30*time.Second, 10*time.Millisecond,
		"a span of every pack in flight at once")
	time.Sleep(200 * time.Millisecond)
	n, most1 := reading()
	require.Equal(t, packs, n, "and no more")
	require.Equal(t, 1, most1, "one span of a pack at a time")
	released.Do(func() { close(release) })
	require.NoError(t, wait())
	testutil.CheckFile(t, ref, "spans-00.txt", "spans 0")
	testutil.CheckFile(t, ref, "spans-35.txt", "spans 35")
}
