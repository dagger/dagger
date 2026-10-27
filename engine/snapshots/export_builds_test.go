package snapshots_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/semaphore"
)

// buildsConfig builds up to workers layers of an export at once, under gate
// when it is set.
func buildsConfig(workers int, gate *semaphore.Weighted) config.RefConfig {
	cfg := transferConfig
	cfg.Builds = config.LayerBuilds{Workers: workers, Gate: gate}
	return cfg
}

// trackDiffs makes each diff wait, up to wait, until want diffs have run at
// once, and returns the most that did.
func trackDiffs(store *testutil.Store, want int64, wait time.Duration) *atomic.Int64 {
	var running, peak atomic.Int64
	store.BeforeDiff = func(ctx context.Context) error {
		n := running.Add(1)
		defer running.Add(-1)
		for {
			if p := peak.Load(); n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		deadline := time.Now().Add(wait)
		for peak.Load() < want && time.Now().Before(deadline) {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Millisecond):
			}
		}
		return nil
	}
	return &peak
}

func exportChains(t *testing.T, refs []bkcache.ImmutableRef, cfg config.RefConfig) []*bkcache.ExportChain {
	t.Helper()
	return exportChainsWork(t, refs, nil, cfg)
}

func exportChainsWork(t *testing.T, refs []bkcache.ImmutableRef, work *bkcache.ChainWork, cfg config.RefConfig) []*bkcache.ExportChain {
	t.Helper()
	chains, err := bkcache.ExportChains(context.Background(), refs, work, cfg)
	require.NoError(t, err)
	for _, chain := range chains {
		t.Cleanup(func() { require.NoError(t, chain.Release(context.Background())) })
	}
	return chains
}

// transferPins are the live leases of chain exports' and imports' pins.
func transferPins(t *testing.T, store *testutil.Store) map[string]bool {
	t.Helper()
	live, err := store.Leases.List(context.Background())
	require.NoError(t, err)
	ids := map[string]bool{}
	for _, lease := range live {
		if lease.Labels["dagger.io/snapshot-transfer"] == "true" {
			ids[lease.ID] = true
		}
	}
	return ids
}

// A chain's missing layers build at once, up to the export's workers, and
// the chain still lists them root first.
func TestExportChainBuildsLayersConcurrently(t *testing.T) {
	store, consumer := testutil.NewStore(t), testutil.NewStore(t)
	a, _ := store.Build(t, nil, "a.txt", "parallel a")
	ab, _ := store.Build(t, a, "b.txt", "parallel b")
	abc, _ := store.Build(t, ab, "c.txt", "parallel c")
	peak := trackDiffs(store, 3, 5*time.Second)
	chain := exportChains(t, []bkcache.ImmutableRef{abc}, buildsConfig(3, nil))[0]
	require.EqualValues(t, 3, peak.Load(), "the chain's three layers built at once")
	require.Len(t, chain.Layers, 3)
	require.Equal(t, 3, chain.Stats.LayersDiffed)
	require.Equal(t, 1, chain.Prefixes[a.SnapshotID()])
	require.Equal(t, 2, chain.Prefixes[ab.SnapshotID()])
	require.Equal(t, 3, chain.Prefixes[abc.SnapshotID()])
	imported := importChain(t, consumer, chain)
	testutil.CheckFile(t, imported, "a.txt", "parallel a")
	testutil.CheckFile(t, imported, "b.txt", "parallel b")
	testutil.CheckFile(t, imported, "c.txt", "parallel c")
}

// The workers bound how many of an export's layers build at once.
func TestExportChainsWorkersBound(t *testing.T) {
	store := testutil.NewStore(t)
	var refs []bkcache.ImmutableRef
	for _, name := range []string{"w.txt", "x.txt", "y.txt", "z.txt"} {
		ref, _ := store.Build(t, nil, name, "bounded "+name)
		refs = append(refs, ref)
	}
	peak := trackDiffs(store, 3, 300*time.Millisecond)
	chains := exportChains(t, refs, buildsConfig(2, nil))
	require.EqualValues(t, 2, peak.Load())
	require.Len(t, chains, 4)
	require.EqualValues(t, 4, store.Diffs.Load())
}

// Chains of one export that share a prefix build it once. Its first chain
// counts it diffed and the others reused, as exporting them one after
// another would, and each lists its layers in order.
func TestExportChainsSharedPrefix(t *testing.T) {
	store, consumer := testutil.NewStore(t), testutil.NewStore(t)
	a, _ := store.Build(t, nil, "a.txt", "shared prefix")
	ab, _ := store.Build(t, a, "b.txt", "first suffix")
	ac, _ := store.Build(t, a, "c.txt", "second suffix")
	peak := trackDiffs(store, 3, 5*time.Second)
	chains := exportChains(t, []bkcache.ImmutableRef{ab, ac}, buildsConfig(4, nil))
	require.EqualValues(t, 3, peak.Load(), "the prefix and both suffixes built at once")
	require.EqualValues(t, 3, store.Diffs.Load(), "the shared prefix was diffed once")
	first, second := chains[0], chains[1]
	require.Equal(t, bkcache.ExportStats{Layers: 2, LayersDiffed: 2}, statCounts(first.Stats))
	require.Equal(t, bkcache.ExportStats{Layers: 2, LayersDiffed: 1, LayersReused: 1}, statCounts(second.Stats))
	require.Equal(t, first.Layers[0].Descriptor, second.Layers[0].Descriptor)
	require.NotEqual(t, first.Layers[1].Descriptor.Digest, second.Layers[1].Descriptor.Digest)
	require.Equal(t, 1, second.Prefixes[a.SnapshotID()])
	require.Equal(t, 2, second.Prefixes[ac.SnapshotID()])
	testutil.CheckFile(t, importChain(t, consumer, first), "b.txt", "first suffix")
	imported := importChain(t, consumer, second)
	testutil.CheckFile(t, imported, "a.txt", "shared prefix")
	testutil.CheckFile(t, imported, "c.txt", "second suffix")
}

// Each layer builds within the work of the first chain that has it, so
// what its build records lands under that chain.
func TestExportChainsBuildUnderChainWork(t *testing.T) {
	store := testutil.NewStore(t)
	a, _ := store.Build(t, nil, "a.txt", "shared root")
	ab, _ := store.Build(t, a, "b.txt", "first suffix")
	ac, _ := store.Build(t, a, "c.txt", "second suffix")
	type chainKey struct{}
	var mu sync.Mutex
	builtUnder := map[int]int{}
	store.BeforeDiff = func(ctx context.Context) error {
		i, ok := ctx.Value(chainKey{}).(int)
		if !ok {
			return errors.New("a build ran outside its chain's work")
		}
		mu.Lock()
		defer mu.Unlock()
		builtUnder[i]++
		return nil
	}
	ended := map[int]int64{}
	work := &bkcache.ChainWork{
		Begin: func(ctx context.Context, i int) context.Context {
			return context.WithValue(ctx, chainKey{}, i)
		},
		End: func(i int, blobBytes int64, err error) {
			assert.NoError(t, err)
			mu.Lock()
			defer mu.Unlock()
			ended[i] = blobBytes
		},
	}
	ctx := context.Background()
	chains, err := bkcache.ExportChains(ctx, []bkcache.ImmutableRef{ab, ac}, work, buildsConfig(4, nil))
	require.NoError(t, err)
	for _, chain := range chains {
		require.NoError(t, chain.Release(ctx))
	}
	require.Equal(t, map[int]int{0: 2, 1: 1}, builtUnder)
	require.Len(t, ended, 2)
	require.Equal(t, chains[0].Stats.BlobBytes, ended[0])
	require.Equal(t, chains[1].Stats.BlobBytes, ended[1])
}

// A chain's work ends with its own builds: a chain whose blob already
// exists ends while another chain of the export is still diffing, so a
// profile doesn't charge it with the other's diff.
func TestExportChainsWorkEndsWithOwnBuilds(t *testing.T) {
	store := testutil.NewStore(t)
	reused, _ := store.Build(t, nil, "reused.txt", "already exported")
	exportChains(t, []bkcache.ImmutableRef{reused}, buildsConfig(1, nil))
	fresh, _ := store.Build(t, nil, "fresh.txt", "built now")
	reusedEnded := make(chan struct{})
	store.BeforeDiff = func(ctx context.Context) error {
		select {
		case <-reusedEnded:
			return nil
		case <-time.After(5 * time.Second):
			return errors.New("the reused chain's work stayed open through the other chain's diff")
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	var mu sync.Mutex
	ended := map[int]int64{}
	work := &bkcache.ChainWork{
		Begin: func(ctx context.Context, _ int) context.Context { return ctx },
		End: func(i int, blobBytes int64, err error) {
			assert.NoError(t, err)
			mu.Lock()
			ended[i] = blobBytes
			mu.Unlock()
			if i == 1 {
				close(reusedEnded)
			}
		},
	}
	chains := exportChainsWork(t, []bkcache.ImmutableRef{fresh, reused}, work, buildsConfig(2, nil))
	require.Equal(t, 1, chains[0].Stats.LayersDiffed)
	require.Equal(t, 1, chains[1].Stats.LayersReused)
	require.Positive(t, ended[0])
	require.Zero(t, ended[1])
}

// statCounts keeps a chain's counts, without its sizes and times.
func statCounts(stats bkcache.ExportStats) bkcache.ExportStats {
	return bkcache.ExportStats{Layers: stats.Layers, LayersDiffed: stats.LayersDiffed, LayersReused: stats.LayersReused}
}

// zstd diffs through the walking differ rather than the manager's; its
// layers build at once too, and import back.
func TestExportChainsZstd(t *testing.T) {
	store, consumer := testutil.NewStore(t), testutil.NewStore(t)
	a, _ := store.Build(t, nil, "a.txt", "zstd prefix")
	ab, _ := store.Build(t, a, "b.txt", "zstd suffix")
	ac, _ := store.Build(t, a, "c.txt", "zstd other")
	cfg := buildsConfig(4, semaphore.NewWeighted(4))
	cfg.Compression = compression.New(compression.Zstd)
	chains := exportChains(t, []bkcache.ImmutableRef{ab, ac}, cfg)
	require.Equal(t, 3, chains[0].Stats.LayersDiffed+chains[1].Stats.LayersDiffed)
	for _, chain := range chains {
		for _, layer := range chain.Layers {
			require.Equal(t, compression.Zstd.MediaType(), layer.Descriptor.MediaType)
		}
	}
	imported := importChain(t, consumer, chains[1])
	testutil.CheckFile(t, imported, "a.txt", "zstd prefix")
	testutil.CheckFile(t, imported, "c.txt", "zstd other")
}

// A gate shared by exports bounds their builds together, and each export
// counts its wait for it.
func TestExportChainsGateBoundsExports(t *testing.T) {
	store := testutil.NewStore(t)
	a, _ := store.Build(t, nil, "a.txt", "gated a")
	ab, _ := store.Build(t, a, "b.txt", "gated b")
	x, _ := store.Build(t, nil, "x.txt", "gated x")
	xy, _ := store.Build(t, x, "y.txt", "gated y")
	peak := trackDiffs(store, 2, 200*time.Millisecond)
	gate := semaphore.NewWeighted(1)
	results := make(chan []*bkcache.ExportChain, 2)
	for _, ref := range []bkcache.ImmutableRef{ab, xy} {
		go func() {
			chains, err := bkcache.ExportChains(context.Background(), []bkcache.ImmutableRef{ref}, nil, buildsConfig(2, gate))
			if !assertNoError(t, err) {
				chains = nil
			}
			results <- chains
		}()
	}
	var wait time.Duration
	for range 2 {
		chains := <-results
		require.Len(t, chains, 1)
		t.Cleanup(func() { require.NoError(t, chains[0].Release(context.Background())) })
		require.Equal(t, 2, chains[0].Stats.LayersDiffed)
		wait += chains[0].Stats.BuildWait
	}
	require.EqualValues(t, 1, peak.Load(), "one build at a time across both exports")
	require.Positive(t, wait)
}

func assertNoError(t *testing.T, err error) bool {
	t.Helper()
	if err != nil {
		t.Errorf("export: %v", err)
		return false
	}
	return true
}

// A failed build fails the export, cancels the builds beside it and
// releases the chains' pins.
func TestExportChainsBuildFailure(t *testing.T) {
	store := testutil.NewStore(t)
	a, _ := store.Build(t, nil, "a.txt", "failing a")
	ab, _ := store.Build(t, a, "b.txt", "failing b")
	before := transferPins(t, store)
	injected := errors.New("injected diff failure")
	var calls atomic.Int64
	var siblingCanceled atomic.Bool
	store.BeforeDiff = func(ctx context.Context) error {
		if calls.Add(1) == 1 {
			// Let the other build start before failing.
			deadline := time.Now().Add(5 * time.Second)
			for calls.Load() < 2 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			return injected
		}
		select {
		case <-ctx.Done():
			siblingCanceled.Store(true)
			return ctx.Err()
		case <-time.After(10 * time.Second):
			return errors.New("sibling build not canceled")
		}
	}
	_, err := bkcache.ExportChains(context.Background(), []bkcache.ImmutableRef{ab}, nil, buildsConfig(2, nil))
	require.ErrorIs(t, err, injected)
	require.EqualValues(t, 2, calls.Load())
	require.True(t, siblingCanceled.Load(), "the failure canceled the build beside it")
	require.Equal(t, before, transferPins(t, store))
}

// An export waiting for the gate stops when its context is canceled, having
// built nothing and kept no pin.
func TestExportChainsCanceledGateWait(t *testing.T) {
	store := testutil.NewStore(t)
	a, _ := store.Build(t, nil, "a.txt", "waiting a")
	before := transferPins(t, store)
	gate := semaphore.NewWeighted(1)
	require.True(t, gate.TryAcquire(1))
	defer gate.Release(1)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := bkcache.ExportChains(ctx, []bkcache.ImmutableRef{a}, nil, buildsConfig(2, gate))
		done <- err
	}()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.DeadlineExceeded)
	case <-time.After(5 * time.Second):
		t.Fatal("export kept waiting for the gate after its context ended")
	}
	require.Zero(t, store.Diffs.Load())
	require.Equal(t, before, transferPins(t, store))
}
