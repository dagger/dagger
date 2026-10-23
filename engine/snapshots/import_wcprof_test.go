package snapshots_test

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/leases"
	cerrdefs "github.com/containerd/errdefs"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/engine/wcprof"
	digest "github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

// profiledEvent is a dumped event with its strings resolved.
type profiledEvent struct {
	wcprof.DumpEvent
	class, ident string
}

// profiledUnder returns the dumped events recorded under the op root: its
// descendant ops, and the waits and links from them.
func profiledUnder(t *testing.T, root uint64) []profiledEvent {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, wcprof.Active().WriteDump(&buf, false))
	header, events, err := wcprof.ReadDump(&buf)
	require.NoError(t, err)
	parents := map[uint64]uint64{}
	for _, ev := range events {
		if ev.Type == "op" {
			parents[ev.OpID] = ev.ParentID
		}
	}
	under := func(id uint64) bool {
		for ; id != 0; id = parents[id] {
			if id == root {
				return true
			}
		}
		return false
	}
	var out []profiledEvent
	for _, ev := range events {
		id := ev.ParentID
		if ev.Type == "op" {
			id = ev.OpID
		}
		if ev.OpID == root || !under(id) {
			continue
		}
		out = append(out, profiledEvent{DumpEvent: ev, class: header.Strings[ev.ClassID], ident: header.Strings[ev.IdentID]})
	}
	return out
}

func profiledOp(ctx context.Context, class string) (context.Context, *wcprof.Op) {
	wcprof.EnsureRecorder()
	return wcprof.BeginOp(wcprof.ContextWithProfiling(ctx), wcprof.OpKindRemoteCache, class, wcprof.OpOpts{})
}

// importPhase is a profiled import phase: its class and the blob it is for.
type importPhase struct{ class, ident string }

// importPhases returns the import phases recorded under root by digest and
// class, checking each is root's child and ended ok, and counts the lock
// waits among them.
func importPhases(t *testing.T, root uint64) (map[importPhase]profiledEvent, []importPhase, int) {
	t.Helper()
	byPhase := map[importPhase]profiledEvent{}
	var ops []importPhase
	var lockWaits int
	for _, ev := range profiledUnder(t, root) {
		switch ev.Type {
		case "op":
			p := importPhase{ev.class, ev.ident}
			ops = append(ops, p)
			byPhase[p] = ev
			require.Equal(t, root, ev.ParentID, "%s is a child of the import's op", ev.class)
			require.Equal(t, "ok", ev.Outcome, ev.class)
		case "wait":
			require.Equal(t, "lock", ev.Reason)
			require.Equal(t, "snapshots.importLayer", ev.ident)
			require.LessOrEqual(t, ev.StartNS, ev.EndNS)
			lockWaits++
		}
	}
	return byPhase, ops, lockWaits
}

// A profiled chain import records, for each layer it needs bytes for, the
// fetch of its blob and its apply, with the blob's digest, and its wait for
// the layer's import lock. A fetch is the time the import still waited for
// the blob: its own download, still running, moves the blob's bytes; a
// download started ahead that ended before leaves the blob present, which
// moves none. A layer whose snapshot is reused is neither fetched nor
// applied.
func TestImportChainProfiled(t *testing.T) {
	for _, tc := range []struct {
		name string
		// aheadDone holds the lower layer's apply until the upper layer's
		// download, started ahead, has stored its blob; otherwise the upper
		// layer's read waits until its fetch has begun.
		aheadDone bool
		upper     string
	}{
		{name: "download running", upper: "download"},
		{name: "download done ahead", aheadDone: true, upper: "present"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
			a, _ := producer.Build(t, nil, "a.txt", "profiled prefix")
			ab, _ := producer.Build(t, a, "b.txt", "profiled suffix")
			chain := exportChain(t, ab)
			require.Len(t, chain.Layers, 2)
			lower, upper := chain.Layers[0].Descriptor, chain.Layers[1].Descriptor
			provider := &testutil.Provider{InfoReaderProvider: chain.Provider}
			if tc.aheadDone {
				consumer.BeforeApply = func(ctx context.Context, desc ocispecs.Descriptor) error {
					if desc.Digest != lower.Digest {
						return nil
					}
					deadline := time.After(time.Minute)
					for {
						if _, err := consumer.Content.Info(ctx, upper.Digest); err == nil {
							return nil
						}
						select {
						case <-deadline:
							return errors.New("the upper layer's download, started ahead, never stored its blob")
						case <-time.After(5 * time.Millisecond):
						}
					}
				}
			} else {
				// The upper layer's fetch links its blob to the import's lease,
				// finds it missing, then looks in the builtin image store
				// before it waits for the download: the read waits for that.
				fetching := make(chan struct{})
				var linked atomic.Bool
				var once sync.Once
				consumer.WithBuiltin(t, builtinLookups(func(dgst digest.Digest) {
					if dgst == upper.Digest && linked.Load() {
						once.Do(func() { close(fetching) })
					}
				}))
				consumer.BeforeAdd = func(_ context.Context, _ leases.Lease, r leases.Resource) error {
					if r.Type == "content" && r.ID == upper.Digest.String() {
						linked.Store(true)
					}
					return nil
				}
				provider.BeforeRead = func(ctx context.Context, desc ocispecs.Descriptor) error {
					if desc.Digest != upper.Digest {
						return nil
					}
					select {
					case <-fetching:
						return nil
					case <-time.After(time.Minute):
						return errors.New("the upper layer's fetch never began")
					case <-ctx.Done():
						return context.Cause(ctx)
					}
				}
			}

			ctx, root := profiledOp(context.Background(), "test.import")
			for range 2 {
				ref, err := consumer.Manager.ImportChain(ctx, supplied(chain, provider))
				require.NoError(t, err)
				require.NoError(t, ref.Release(context.Background()))
			}
			root.End(wcprof.OutcomeOK)

			byPhase, ops, lockWaits := importPhases(t, root.ID())
			source := map[string]string{lower.Digest.String(): "download", upper.Digest.String(): tc.upper}
			var want []importPhase
			for _, layer := range chain.Layers {
				dgst := layer.Descriptor.Digest.String()
				want = append(want, importPhase{"snapshot.import.fetch[" + source[dgst] + "]", dgst}, importPhase{"snapshot.import.apply", dgst})
			}
			require.ElementsMatch(t, want, ops, "the second import reuses both layers")
			require.Equal(t, 4, lockWaits, "one lock wait per layer per import")
			for _, layer := range chain.Layers {
				dgst := layer.Descriptor.Digest.String()
				fetch := byPhase[importPhase{"snapshot.import.fetch[" + source[dgst] + "]", dgst}]
				apply := byPhase[importPhase{"snapshot.import.apply", dgst}]
				if source[dgst] == "download" {
					require.Equal(t, layer.Descriptor.Size, fetch.Bytes, "the import's own download moves the blob")
					require.Equal(t, "external", fetch.WorkType)
				} else {
					require.Zero(t, fetch.Bytes, "a blob present moves nothing")
					require.Equal(t, "engine", fetch.WorkType)
				}
				require.Equal(t, layer.Descriptor.Size, apply.Bytes)
				require.Equal(t, "engine", apply.WorkType)
				require.LessOrEqual(t, fetch.EndNS, apply.StartNS, "a layer is fetched before it is applied")
			}
			require.EqualValues(t, 2, provider.Reads.Load(), "each blob is read once")
		})
	}
}

// Two profiled imports that need one blob at once, on different parents,
// record its bytes once: the import whose download read the blob moved
// them, and the other, which joined that download, waited on the network
// and moved none.
func TestImportChainProfiledJoin(t *testing.T) {
	producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
	a, _ := producer.Build(t, nil, "a.txt", "shared")
	b, _ := producer.Build(t, nil, "b.txt", "other base")
	chainA, chainB := exportChain(t, a), exportChain(t, b)
	shared := chainA.Layers[0].Descriptor
	onB := &bkcache.ExportChain{Layers: append(append([]bkcache.ExportLayer{}, chainB.Layers...), chainA.Layers...)}
	// The other base is imported already, so the second import fetches
	// nothing ahead: it joins the download for the shared layer itself, once
	// it found the blob missing.
	importChain(t, consumer, chainB)
	started, release := make(chan struct{}, 4), make(chan struct{})
	var released sync.Once
	t.Cleanup(func() { released.Do(func() { close(release) }) })
	providerA := newGatedProvider(chainA, shared.Digest.String(), started, release)
	providerB := newGatedProvider(&bkcache.ExportChain{Layers: onB.Layers, Provider: chainA.Provider}, shared.Digest.String(), started, release)
	onB.Provider = providerB

	ctx, root := profiledOp(context.Background(), "test.import")
	errs := make(chan error, 2)
	var imports sync.WaitGroup
	run := func(chain *bkcache.ExportChain) {
		imports.Add(1)
		go func() {
			defer imports.Done()
			ref, err := consumer.Manager.ImportChain(ctx, chain)
			if err == nil {
				err = ref.Release(context.Background())
			}
			errs <- err
		}()
	}
	t.Cleanup(func() {
		released.Do(func() { close(release) })
		imports.Wait()
	})
	run(supplied(chainA, providerA))
	waitFor(t, started, "the first import's download of the shared blob")
	run(onB)
	require.Eventually(t, func() bool { return bkcache.FetchHolders(consumer.Manager, shared.Digest) == 2 }, 10*time.Second, 5*time.Millisecond,
		"the second import joined the first one's download")
	released.Do(func() { close(release) })
	for range 2 {
		require.NoError(t, waitFor(t, errs, "an import"))
	}
	root.End(wcprof.OutcomeOK)

	reads := providerA.reads[shared.Digest.String()].Load() + providerB.reads[shared.Digest.String()].Load()
	require.EqualValues(t, 1, reads, "the shared blob is read once")
	var moved int64
	sources := map[string]int{}
	for _, ev := range profiledUnder(t, root.ID()) {
		if ev.Type != "op" || ev.ident != shared.Digest.String() || ev.class == "snapshot.import.apply" {
			continue
		}
		moved += ev.Bytes
		sources[ev.class]++
		if ev.class == "snapshot.import.fetch[joined]" {
			require.Zero(t, ev.Bytes, "a joined download moves nothing for the import that joined it")
			require.Equal(t, "external", ev.WorkType, "a joined download waits on the network")
		}
	}
	require.Equal(t, shared.Size, moved, "the shared blob's bytes are moved once")
	require.Equal(t, map[string]int{"snapshot.import.fetch[download]": 1, "snapshot.import.fetch[joined]": 1}, sources)
}

// A profiled import whose blob another writer stores after the import
// found it missing, and before its download writes it, reads nothing and
// records the blob as present, moving no bytes.
func TestImportChainProfiledStoredMeanwhile(t *testing.T) {
	producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
	a, _ := producer.Build(t, nil, "a.txt", "stored meanwhile")
	chain := exportChain(t, a)
	desc := chain.Layers[0].Descriptor
	data, err := content.ReadBlob(context.Background(), chain.Provider, desc)
	require.NoError(t, err)
	// Armed once the import found the blob missing; the next transfer pin,
	// the download's, is created just before it opens its writer.
	var armed, stored atomic.Bool
	consumer.BeforeAdd = func(_ context.Context, _ leases.Lease, r leases.Resource) error {
		if r.Type == "content" && r.ID == desc.Digest.String() {
			armed.Store(true)
		}
		return nil
	}
	consumer.AfterCreate = func(l leases.Lease) {
		if l.Labels["containerd.io/gc.flat"] == "" || !armed.Load() || !stored.CompareAndSwap(false, true) {
			return
		}
		require.NoError(t, content.WriteBlob(leases.WithLease(context.Background(), l.ID), consumer.Content, "meanwhile-"+desc.Digest.Encoded(), bytes.NewReader(data), desc))
	}
	provider := &testutil.Provider{InfoReaderProvider: chain.Provider}

	ctx, root := profiledOp(context.Background(), "test.import")
	ref, err := consumer.Manager.ImportChain(ctx, &bkcache.ExportChain{Layers: chain.Layers, Provider: provider})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, ref.Release(context.Background())) })
	root.End(wcprof.OutcomeOK)
	require.True(t, stored.Load(), "the blob was stored meanwhile")

	byPhase, ops, _ := importPhases(t, root.ID())
	dgst := desc.Digest.String()
	require.ElementsMatch(t, []importPhase{{"snapshot.import.fetch[present]", dgst}, {"snapshot.import.apply", dgst}}, ops)
	require.Zero(t, byPhase[importPhase{"snapshot.import.fetch[present]", dgst}].Bytes, "nothing was moved")
	require.Zero(t, provider.Reads.Load(), "nothing was read")
	testutil.CheckFile(t, ref, "a.txt", "stored meanwhile")
}

// builtinLookups is a builtin image store that holds nothing, and reports
// each digest looked up in it.
type builtinLookups func(digest.Digest)

func (b builtinLookups) Info(_ context.Context, dgst digest.Digest) (content.Info, error) {
	b(dgst)
	return content.Info{}, cerrdefs.ErrNotFound
}

func (b builtinLookups) ReaderAt(context.Context, ocispecs.Descriptor) (content.ReaderAt, error) {
	return nil, cerrdefs.ErrNotFound
}
