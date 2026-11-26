package dagql

import (
	"context"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/snapshots"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// storedSizesAloneTest is a Cloud whose entries form a closure graph drawn
// at random: entries over others and over receivers, calls kept on some
// entries that take a receiver as their input, parts that share layers,
// some blobs stored in one pack by some parts and in another, or none, by
// others, and a recipient, cache-b, that holds some entries.
type storedSizesAloneTest struct {
	ctx   context.Context
	cloud *Cache
	// entries are the Cloud's numbers for the graph's entries and its
	// receivers.
	entries []uint64
}

// aloneTestReceiver is a receiver's call as client makes it: unlike
// receiverTestFrame's, each client's has content of its own, so each is an
// entry and a class of its own on the Cloud, and a call kept on one is
// placed by that receiver alone.
func aloneTestReceiver(client string) *ResultCall {
	frame := callRecordTestFrame("receiver")
	frame.ImplicitInputs = []*ResultCallArg{{Name: "cachePerClient", Value: &ResultCallLiteral{Kind: ResultCallLiteralKindString, StringValue: client}}}
	return frame
}

func newStoredSizesAloneTest(t *testing.T, seed uint64) *storedSizesAloneTest {
	t.Helper()
	rng := rand.New(rand.NewPCG(seed, 1))
	actx, a, asrv := transferTestCache(t)
	var receivers []AnyResult
	for i := range 3 {
		frame := aloneTestReceiver(fmt.Sprint("client-", i))
		receiver, err := a.GetOrInitCall(actx, "test-session", asrv, &CallRequest{ResultCall: frame, IsPersistable: true},
			func(context.Context) (AnyResult, error) {
				return NewResultForCall(&transferTestValue{Text: fmt.Sprint("receiver ", i)}, frame)
			})
		require.NoError(t, err)
		receivers = append(receivers, receiver)
	}
	type keep struct {
		frame *ResultCall
		inner AnyResult
	}
	var entries []AnyResult
	var kept []keep
	for i := range 40 {
		entry := persistedListTestResult(t, actx, a, asrv, fmt.Sprint("alone-", i),
			&transferTestValue{Text: fmt.Sprint(i, " ", strings.Repeat("v", rng.IntN(4<<10)))})
		for range rng.IntN(3) {
			if len(entries) > 0 {
				transferTestDependency(a, actx, entry, entries[rng.IntN(len(entries))])
			}
		}
		if rng.IntN(4) == 0 {
			transferTestDependency(a, actx, entry, receivers[rng.IntN(len(receivers))])
		}
		if rng.IntN(3) == 0 {
			frame := callRecordTestFrame(fmt.Sprint("call-", i))
			frame.Receiver = &ResultCallRef{Call: aloneTestReceiver(fmt.Sprint("client-", rng.IntN(len(receivers))))}
			got, err := a.GetOrInitCall(actx, "test-session", asrv, &CallRequest{ResultCall: frame, IsPersistable: true},
				func(context.Context) (AnyResult, error) { return entry, nil })
			require.NoError(t, err)
			require.Equal(t, entry.cacheSharedResult().id, got.cacheSharedResult().id)
			kept = append(kept, keep{frame: frame, inner: entry})
		}
		entries = append(entries, entry)
	}
	all := slices.Concat(entries, receivers)
	bundle := exportTestBundle(t, actx, a, all...)
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	reply, err := cloud.MergeValues(ctx, "cache-a", bundle)
	require.NoError(t, err)
	byProducer := map[uint64]uint64{}
	for _, value := range reply.Values {
		for _, sent := range bundle.Values {
			if sent.Ordinal == value.Ordinal {
				byProducer[sent.SenderNumber] = value.Number
			}
		}
	}
	test := &storedSizesAloneTest{ctx: ctx, cloud: cloud}
	for _, res := range all {
		number := byProducer[uint64(res.cacheSharedResult().id)]
		require.NotZero(t, number)
		test.entries = append(test.entries, number)
	}
	for _, k := range kept {
		call := &callRecordTest{ctx: actx, producer: a, frame: k.frame, inner: k.inner, cloud: cloud}
		_, err := cloud.AttachRemoteHolding(ctx, HolderKey{Cache: "cache-a", Number: uint64(k.inner.cacheSharedResult().id)}, call.span(t, true, 0))
		require.NoError(t, err)
	}
	// Parts over a pool of layers, half of them stored in packs; a third of
	// the parts store the blobs in other packs, or in none.
	var pool []snapshots.ExportLayer
	packs, others := map[digest.Digest]digest.Digest{}, map[digest.Digest]digest.Digest{}
	for i := range 12 {
		layer := storedLayersTestLayer(fmt.Sprint("alone-layer-", seed, "-", i), "the pool's")
		pool = append(pool, layer)
		if i%2 == 0 {
			packs[layer.Descriptor.Digest] = testDigest(fmt.Sprint("alone-pack-", seed, "-", i%4))
		}
		if i%3 == 0 {
			others[layer.Descriptor.Digest] = testDigest(fmt.Sprint("alone-other-pack-", seed, "-", i%2))
		}
	}
	for i, number := range test.entries {
		if rng.IntN(5) < 3 {
			from := rng.IntN(len(pool) - 2)
			chain := slices.Clone(pool[from : from+1+rng.IntN(len(pool)-from-1)])
			in := packs
			if rng.IntN(3) == 0 {
				in = others
			}
			stored, _, err := cloud.SetStoredPart(ctx, number, storedLayersTestPart(fmt.Sprint("part-", i%3), chain, in), 0)
			require.NoError(t, err)
			require.True(t, stored)
		}
		if rng.IntN(5) == 0 {
			heldTestHold(t, ctx, cloud, "cache-b", uint64(1000+i), number, 0)
		}
	}
	return test
}

// For each root, StoredSizesAlone answers what StoredSizes answers for the
// root alone, on graphs drawn at random with held rows, shared layers, blobs
// in more than one pack and calls kept on entries whose receivers some
// roots' closures have and others add: a root the cache lacks, and a root
// asked twice, included.
func TestStoredSizesAloneAreEachRootsStoredSizes(t *testing.T) {
	t.Parallel()
	for seed := range uint64(8) {
		t.Run(fmt.Sprint("seed ", seed), func(t *testing.T) {
			t.Parallel()
			test := newStoredSizesAloneTest(t, seed)
			now := time.Now().Unix()
			roots := slices.Clone(test.entries)
			rand.New(rand.NewPCG(seed, 2)).Shuffle(len(roots), func(i, j int) { roots[i], roots[j] = roots[j], roots[i] })
			roots = append(roots[:min(len(roots), StoredSizesAloneMax-2)], 9_999_999, roots[0])
			var added, without, heldRows int
			for _, held := range []*HeldBy{nil, {Cache: "cache-b"}} {
				got, err := test.cloud.StoredSizesAlone(test.ctx, roots, now, held)
				require.NoError(t, err)
				require.Len(t, got, len(roots))
				for i, root := range roots {
					want, err := test.cloud.StoredSizes(test.ctx, []uint64{root}, now, held)
					require.NoError(t, err)
					require.Equal(t, want[0], got[i], "root %d, the %dth, held by %v", root, i, held)
					closure, err := test.cloud.StoredClosure(test.ctx, []uint64{root}, now, held)
					require.NoError(t, err)
					if len(closure.CallInputs) > 0 {
						added++
					} else {
						without++
					}
					for _, entry := range closure.Entries {
						if entry.Held {
							heldRows++
						}
					}
				}
			}
			require.Positive(t, added, "some roots' kept calls add their inputs")
			require.Positive(t, without, "and some roots add none")
			require.Positive(t, heldRows, "some roots carry held rows")
		})
	}
}

// Roots that share their closure are walked once, not once a root: here 16
// roots over one chain of 64 entries take about the steps one walk of them
// takes, where pricing each alone takes one walk each.
func TestStoredSizesAloneWalkSharedClosuresOnce(t *testing.T) {
	t.Parallel()
	actx, a, asrv := transferTestCache(t)
	var below AnyResult
	for i := range 64 {
		dep := persistedListTestResult(t, actx, a, asrv, fmt.Sprint("chain-", i), &transferTestValue{Text: fmt.Sprint("chain ", i)})
		if below != nil {
			transferTestDependency(a, actx, dep, below)
		}
		below = dep
	}
	var tops []AnyResult
	for i := range 16 {
		top := persistedListTestResult(t, actx, a, asrv, fmt.Sprint("top-", i), &transferTestValue{Text: fmt.Sprint("top ", i)})
		transferTestDependency(a, actx, top, below)
		tops = append(tops, top)
	}
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	roots := storedPersistenceTestMerge(t, ctx, cloud, exportTestBundle(t, actx, a, tops...))
	require.Len(t, roots, 16)
	now := time.Now().Unix()
	// Each step its own hold: the holds count the steps.
	cloud.readPieceSize = 1
	var mu sync.Mutex
	holds := 0
	cloud.testStoredSizesHold = func(time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		holds++
	}
	count := func(f func()) int {
		mu.Lock()
		holds = 0
		mu.Unlock()
		f()
		mu.Lock()
		defer mu.Unlock()
		return holds
	}
	var each []StoredSize
	eachHolds := count(func() {
		for _, root := range roots {
			size, err := cloud.StoredSizes(ctx, []uint64{root}, now, nil)
			require.NoError(t, err)
			each = append(each, size[0])
		}
	})
	var alone []StoredSize
	aloneHolds := count(func() {
		var err error
		alone, err = cloud.StoredSizesAlone(ctx, roots, now, nil)
		require.NoError(t, err)
	})
	require.Equal(t, each, alone)
	require.Less(t, 4*aloneHolds, eachHolds, "one walk, %d steps, against %d for one walk a root", aloneHolds, eachHolds)
}

// A root whose closure has a blob in two packs is allowed an address for
// each part that names the blob in another pack than the first its walk
// reaches, which depends on the order its walk reaches them in: here R's
// two parts name the blob in pack B, and P's, which R's walk reaches first,
// in pack A, three addresses in all. A root's addresses are counted in the
// order its own walk reaches its parts, as StoredSizes counts them, whatever
// order the walk of all the roots reached them in; P alone has one pack,
// and one address.
func TestStoredSizesAloneCountABlobInTwoPacksInEachRootsOrder(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, p := storedBundleTestCloud(t, "root")
	blob := storedLayersTestLayer("two-packs", "in two packs")
	in := func(pack string) map[digest.Digest]digest.Digest {
		return map[digest.Digest]digest.Digest{blob.Descriptor.Digest: testDigest(pack)}
	}
	for number, parts := range map[uint64]map[string]string{p: {"a": "pack-a"}, r: {"b": "pack-b", "c": "pack-b"}} {
		for part, pack := range parts {
			stored, _, err := cloud.SetStoredPart(ctx, number, storedLayersTestPart(part, []snapshots.ExportLayer{blob}, in(pack)), 0)
			require.NoError(t, err)
			require.True(t, stored)
		}
	}
	now := time.Now().Unix()
	// R's price allows for three addresses beyond its parts and layers.
	own, err := cloud.StoredSizes(ctx, []uint64{r}, now, nil)
	require.NoError(t, err)
	wire := int64(0)
	layers := map[*storedLayer]bool{}
	cloud.egraphMu.RLock()
	for _, number := range []uint64{r, p} {
		for _, part := range cloud.resultsByID[sharedResultID(number)].storedParts {
			wire += part.wire
			for _, layer := range part.layers {
				if !layers[layer] {
					layers[layer] = true
					wire += layer.wire
				}
			}
		}
	}
	cloud.egraphMu.RUnlock()
	require.Equal(t, int64(3)*storedBlobAddressAllowance, own[0].OutputBytes-wire, "A's address shared, B's on b and on c")
	got, err := cloud.StoredSizesAlone(ctx, []uint64{r, p}, now, nil)
	require.NoError(t, err)
	for i, root := range []uint64{r, p} {
		want, err := cloud.StoredSizes(ctx, []uint64{root}, now, nil)
		require.NoError(t, err)
		require.Equal(t, want[0], got[i], "root %d", root)
	}
}

// StoredSizesAlone marks which roots reach which entries on its copy of
// them, without egraphMu: a step for each entry of each root's closure,
// which for roots over one closure is that closure once a root. A writer
// waits for one piece of its reads, never for its marks. Here each mark
// takes the write lock, which it gets only while the walk holds no piece.
// The Cloud's body worker is stopped before the merge: it reads the merged
// records and installs their compressed bodies under egraphMu, and could
// hold it as the walk marks.
func TestStoredSizesAloneMarkRootsWithoutTheLock(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, p := storedBundleTestCloudIn(t, "root", true)
	marks := 0
	cloud.testStoredSizesAloneMarks = func() {
		require.True(t, cloud.egraphMu.TryLock(), "the walk holds no piece of egraphMu as it marks")
		cloud.egraphMu.Unlock()
		marks++
	}
	_, err := cloud.StoredSizesAlone(ctx, []uint64{r, p}, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.Positive(t, marks)
}
