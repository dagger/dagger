package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/engine/wcprof"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

// chainsContentSource serves a blob from whichever exported chain holds it.
type chainsContentSource []content.InfoReaderProvider

func (chainsContentSource) Available(dagql.PersistedPartOffer, time.Time) bool { return true }

func (s chainsContentSource) Provider(context.Context, dagql.PersistedPartOffer, *dagql.PartDemandState) content.InfoReaderProvider {
	return s
}

func (s chainsContentSource) Info(ctx context.Context, dgst digest.Digest) (content.Info, error) {
	var errs []error
	for _, p := range s {
		info, err := p.Info(ctx, dgst)
		if err == nil {
			return info, nil
		}
		errs = append(errs, err)
	}
	return content.Info{}, errors.Join(errs...)
}

func (s chainsContentSource) ReaderAt(ctx context.Context, desc ocispec.Descriptor) (content.ReaderAt, error) {
	var errs []error
	for _, p := range s {
		if _, err := p.Info(ctx, desc.Digest); err != nil {
			errs = append(errs, err)
			continue
		}
		return p.ReaderAt(ctx, desc)
	}
	return nil, errors.Join(errs...)
}

// mountChain is n chained withMountedDirectory layers over a base, as
// dagger/go's module proxy builds: the i-th mounts "/in/i", a directory made
// by its own operation that holds "m<i>.txt".
type mountChain struct {
	top     dagql.ObjectResult[*Container]
	targets []string
	// outputs are the content an engine that built the chain exports with
	// it: each layer's rootfs. The mounted directories aren't exported, as
	// the module proxy's weren't on the fill that ran away, so another
	// engine makes each of them again.
	outputs []dagql.SelectedValueOutput
}

func attachDirectoryChild(t *testing.T, ctx context.Context, cache *dagql.Cache, srv *dagql.Server, session, field string, parent dagql.ObjectResult[*Directory], value *Directory) dagql.ObjectResult[*Directory] {
	t.Helper()
	id, err := cache.PersistedResultID(parent)
	require.NoError(t, err)
	frame := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: field, Receiver: &dagql.ResultCallRef{ResultID: id}, Type: dagql.NewResultCallType(value.Type())}
	result, err := cache.GetOrInitCall(ctx, session, srv, &dagql.CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (dagql.AnyResult, error) { return dagql.NewObjectResultForCall(value, srv, frame) })
	require.NoError(t, err)
	return result.(dagql.ObjectResult[*Directory])
}

func buildMountChain(t *testing.T, ctx context.Context, cache *dagql.Cache, srv *dagql.Server, store *testutil.Store, session string, n int) mountChain {
	t.Helper()
	platform := Platform{OS: "linux", Architecture: "amd64"}
	fsRef, _ := store.Build(t, nil, "base.txt", "base bytes")
	base := NewContainer(platform)
	base.FS.setValue(partTestDirectory(fsRef, "/"))
	layer := attachTransferObject(t, ctx, cache, srv, session, "mountChainBase", base)
	chain := mountChain{targets: make([]string, n)}
	scratch := attachTransferObject(t, ctx, cache, srv, session, "mountChainScratch", &Directory{Dir: new(LazyAccessor[string, *Directory]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]), Platform: platform, Lazy: &DirectoryScratchLazy{LazyState: NewLazyState()}})
	for i := range n {
		chain.targets[i] = fmt.Sprintf("/in/%d", i)
		made := &Directory{Dir: new(LazyAccessor[string, *Directory]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]), Platform: platform,
			Lazy: &DirectoryWithNewFileLazy{LazyState: NewLazyState(), Parent: scratch, Dest: fmt.Sprintf("m%d.txt", i), Content: []byte(fmt.Sprintf("module %d", i)), Permissions: 0o640}}
		source := attachDirectoryChild(t, ctx, cache, srv, session, fmt.Sprintf("mountChainFile%d", i), scratch, made)
		child := NewContainer(platform)
		for _, mnt := range layer.Self().Mounts {
			child.Mounts = append(child.Mounts, ContainerMount{Target: mnt.Target, Readonly: mnt.Readonly, DirectorySource: new(LazyAccessor[*Directory, *Container])})
		}
		child.Mounts = child.Mounts.With(ContainerMount{Target: chain.targets[i], Readonly: true, DirectorySource: new(LazyAccessor[*Directory, *Container])})
		child.Lazy = &ContainerWithMountedDirectoryLazy{LazyState: NewLazyState(), Parent: layer, Source: source, Target: chain.targets[i], Readonly: true}
		layer = attachDelegationChild(t, ctx, cache, srv, session, "withMountedDirectory", layer, child)
		chain.outputs = append(chain.outputs, dagql.SelectedValueOutput{Result: layer, Address: dagql.PersistedPartAddress{Part: ContainerPartFS}})
	}
	chain.top = layer
	return chain
}

func (chain mountChain) parts() []dagql.PartKey {
	parts := []dagql.PartKey{ContainerPartFS}
	for _, target := range chain.targets {
		parts = append(parts, ContainerPartMount(target))
	}
	return parts
}

// mountChainCost is what demanding every mount of a chain's top cost, from
// the native profile of the demand: the lazy tasks that ran, and the demand
// steps that each capture a record (a probe captures the demanded entry's;
// a source selection captures the entry's and its parent's; a decision
// captures the entry's and decodes it).
type mountChainCost struct {
	tasks, probes, selects, decisions int
	// tasksByKind counts the lazy tasks by their key's kind: acquire,
	// obtain, lazy, or a value's own group.
	tasksByKind map[string]int
	// keys counts the lazy tasks by result and key: a key run more than once
	// is a task retried.
	keys    map[string]int
	elapsed time.Duration
}

func (cost mountChainCost) captures() int {
	return cost.probes + 2*cost.selects + cost.decisions
}

// demandMountChain demands every mount of top at once, as an exec over it
// does, checks each mount's content, and returns what the demand cost.
func demandMountChain(t *testing.T, ctx context.Context, cache *dagql.Cache, top dagql.ObjectResult[*Container], targets []string, parts []dagql.PartKey) mountChainCost {
	t.Helper()
	cost := mountChainCost{tasksByKind: map[string]int{}, keys: map[string]int{}}
	wcprof.EnsureRecorder()
	ctx, root := wcprof.BeginOp(wcprof.ContextWithProfiling(ctx), wcprof.OpKindSessionPhase, "test.mountChain", wcprof.OpOpts{})
	started := time.Now()
	require.NoError(t, cache.EvaluateParts(ctx, top, parts...))
	cost.elapsed = time.Since(started)
	root.End(wcprof.OutcomeOK)

	for i, target := range targets {
		mnt := top.Self().mountAt(target)
		require.NotNil(t, mnt, target)
		dir, ok := mnt.DirectorySource.Peek()
		require.True(t, ok, target)
		snapshot, ok := dir.Snapshot.Peek()
		require.True(t, ok, target)
		data, err := os.ReadFile(filepath.Join(testutil.Root(t, snapshot), fmt.Sprintf("m%d.txt", i)))
		require.NoError(t, err, target)
		require.Equal(t, fmt.Sprintf("module %d", i), string(data), target)
	}

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
		for id = parents[id]; id != 0; id = parents[id] {
			if id == root.ID() {
				return true
			}
		}
		return false
	}
	for _, ev := range events {
		if ev.Type != "op" || !under(ev.OpID) {
			continue
		}
		switch {
		case ev.OpKind == "lazy":
			cost.tasks++
			kind, _, found := strings.Cut(header.Strings[ev.IdentID], ":")
			if !found {
				kind = "group"
			}
			cost.tasksByKind[kind]++
			cost.keys[fmt.Sprintf("%d/%s", ev.ResultID, header.Strings[ev.IdentID])]++
		case header.Strings[ev.ClassID] == "part.demand.probe":
			cost.probes++
		case header.Strings[ev.ClassID] == "part.demand.select":
			cost.selects++
		case header.Strings[ev.ClassID] == "part.demand.decide":
			cost.decisions++
		}
	}
	return cost
}

// importedMountChainCost builds the chain on one cache and evaluates it
// there, exports it with its content, and merges it into a second cache, as
// an engine with the remote cache does, which then demands every mount.
func importedMountChainCost(t *testing.T, n int) mountChainCost {
	t.Helper()
	aStore, bStore := testutil.NewStore(t), testutil.NewStore(t)
	actx, a, asrv := transferCache(t, aStore, "", "a")
	bctx, b, bsrv := transferCache(t, bStore, "", "b")
	chain := buildMountChain(t, actx, a, asrv, aStore, "a", n)
	require.NoError(t, a.EvaluateParts(actx, chain.top, chain.parts()...))

	var cost mountChainCost
	selection := dagql.ValueSelection{Roots: []dagql.AnyResult{chain.top}, Outputs: chain.outputs}
	require.NoError(t, a.WithExportedValues(actx, selection, config.RefConfig{Compression: compression.New(compression.Uncompressed)}, func(_ context.Context, exported *dagql.ExportedValues) error {
		var source chainsContentSource
		for _, entry := range exported.Chains.Entries {
			source = append(source, entry.Provider)
		}
		b.SetPartContentSource(source)
		reply, err := b.MergeValues(bctx, dagql.CloudCacheID, exported.Bundle)
		require.NoError(t, err)
		loaded, err := b.LoadResultByResultID(bctx, "", bsrv, reply.Imported()[0].ResultID)
		require.NoError(t, err)
		top := loaded.(dagql.ObjectResult[*Container])
		require.NotNil(t, top.Self().acquiredOutput.Load(), "the merged top decodes from its record alone, as on the engine that ran away")
		cost = demandMountChain(t, bctx, b, top, chain.targets, chain.parts())
		return nil
	}))
	repeats := map[int]int{}
	for _, runs := range cost.keys {
		repeats[runs]++
	}
	t.Logf("merged chain of %d mounts: %d lazy tasks %v over %d keys (keys by runs %v), %d record captures (%d probes, %d selections, %d decisions), %s",
		n, cost.tasks, cost.tasksByKind, len(cost.keys), repeats, cost.captures(), cost.probes, cost.selects, cost.decisions, cost.elapsed)
	return cost
}

// restoredMountChainCost builds the chain on a cache that persists, leaves
// every mount pending, and restarts the cache from its own persisted state,
// as an engine without the remote cache restarts, which then demands every
// mount.
func restoredMountChainCost(t *testing.T, n int) mountChainCost {
	t.Helper()
	store := testutil.NewStore(t)
	path := filepath.Join(t.TempDir(), "cache.db")
	ctx, cache, srv := transferCache(t, store, path, "before")
	chain := buildMountChain(t, ctx, cache, srv, store, "before", n)
	require.NoError(t, cache.EvaluateParts(ctx, chain.top, ContainerPartMetadata))
	id, err := cache.PersistedResultID(chain.top)
	require.NoError(t, err)
	require.NoError(t, cache.ReleaseSession(ctx, "before"))
	require.NoError(t, cache.Close(ctx))
	store.Reload(t)

	ctx, cache, srv = transferCache(t, store, path, "after")
	require.Equal(t, dagql.CachePersistenceResetNone, cache.PersistenceResetReason())
	loaded, err := cache.LoadResultByResultID(ctx, "", srv, id)
	require.NoError(t, err)
	top := loaded.(dagql.ObjectResult[*Container])
	cost := demandMountChain(t, ctx, cache, top, chain.targets, chain.parts())
	t.Logf("restored chain of %d mounts: %d lazy tasks %v, %d record captures (%d probes, %d selections, %d decisions), %s",
		n, cost.tasks, cost.tasksByKind, cost.captures(), cost.probes, cost.selects, cost.decisions, cost.elapsed)
	return cost
}

// requireMountChainLinear checks that demanding the chain's mounts costs
// about n, not n²: a bounded number of tasks and record captures per mount,
// and at most eight times the work for four times the mounts, where n would
// be about four and n² sixteen; scheduling moves the counts somewhat. Before the walk went through merged layers and a row's
// source scans took turns, 64 merged mounts took 48,654 tasks and 69,669
// captures, 47 times those of 16.
func requireMountChainLinear(t *testing.T, small, large mountChainCost, n int) {
	t.Helper()
	require.Less(t, large.tasks, 12*n, "lazy tasks grow with n, not n²")
	require.Less(t, large.captures(), 48*n, "record captures grow with n, not n²")
	require.LessOrEqual(t, large.tasks, 8*small.tasks)
	require.LessOrEqual(t, large.captures(), 8*small.captures())
}

// An exec over a chain of n mounts merged from another engine demands every
// mount of the chain's top at once. Each mount must come from the layer that
// mounted it, as it does natively (TestContainerMountedFileChainCopiesFromWriter),
// and not be copied through every layer above it: through n layers that is
// n²/2 tasks, each capturing a record that grows with the chain. On a whole
// pull request's fill, a 571-mount module proxy merged that way kept an
// engine from serving anything for 24 minutes.
func TestImportedMountChainCopiesFromWriter(t *testing.T) {
	t.Parallel()
	requireMountChainLinear(t, importedMountChainCost(t, 16), importedMountChainCost(t, 64), 64)
}

// The same chain restored from the cache's own persisted state, with the
// remote cache off, copies each mount from the layer that mounted it too.
func TestRestoredMountChainCopiesFromWriter(t *testing.T) {
	t.Parallel()
	requireMountChainLinear(t, restoredMountChainCost(t, 16), restoredMountChainCost(t, 64), 64)
}

// A container merged from another engine's record passes a part through to
// its receiver only where a demand of the part routes it to the part's own
// copy step. An operation that makes every part at once, such as an import,
// passes nothing through: walking past it would copy the parts it replaced.
func TestTransferredContainerPassesThroughWhatItsOperationCopies(t *testing.T) {
	t.Parallel()
	view := func(lazyJSON string) *containerAcquiredOutput {
		return &containerAcquiredOutput{Payload: persistedContainerPayload{
			Metadata: persistedContainerMetadata{Consumed: true, Value: persistedContainerMetadataValue{
				Mounts: []persistedContainerMountPayload{{Target: "/a", Kind: persistedContainerMountKindDirectory}, {Target: "/b", Kind: persistedContainerMountKindDirectory}},
			}},
			Parts: map[dagql.PartKey]persistedContainerPart{
				ContainerPartFS:          {Kind: containerPartPending},
				ContainerPartExecMeta:    {Kind: containerPartAbsent},
				ContainerPartMount("/a"): {Kind: containerPartPending},
				ContainerPartMount("/b"): {Kind: containerPartPending},
			},
			LazyJSON: []byte(lazyJSON),
		}}
	}
	call := func(field string) *dagql.ResultCall {
		return &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: field, Receiver: &dagql.ResultCallRef{ResultID: 1}}
	}
	for _, tc := range []struct {
		field, lazyJSON string
		passes          map[dagql.PartKey]bool
	}{
		{"import", `{}`, map[dagql.PartKey]bool{ContainerPartFS: false, ContainerPartMount("/a"): false, ContainerPartMount("/b"): false}},
		{"_builtinContainer", `{}`, map[dagql.PartKey]bool{ContainerPartFS: false, ContainerPartMount("/a"): false}},
		{"withEnvVariable", `{}`, map[dagql.PartKey]bool{ContainerPartFS: true, ContainerPartMount("/a"): true, ContainerPartMount("/b"): true}},
		{"withMountedDirectory", `{"target":"/b"}`, map[dagql.PartKey]bool{ContainerPartFS: true, ContainerPartMount("/a"): true, ContainerPartMount("/b"): false}},
	} {
		v := view(tc.lazyJSON)
		for part, want := range tc.passes {
			require.Equal(t, want, v.passesThrough(call(tc.field), part), "%s passes %s through", tc.field, part)
		}
		require.False(t, view("").passesThrough(call(tc.field), ContainerPartFS), "%s without its recorded operation", tc.field)
	}
}
