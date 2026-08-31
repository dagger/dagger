package core

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	bkcache "github.com/dagger/dagger/engine/snapshots"
)

// containerPartsTestBaseOp is a refined test op standing in for a
// pending base container (in real chains: an exec): a cheap metadata
// body and a separate fs body, so tests can observe which of a parent's
// groups a child demand actually forces.
type containerPartsTestBaseOp struct {
	LazyState

	metaRuns atomic.Int32
	fsRuns   atomic.Int32

	workdir     string
	env         []string
	volatileEnv []string
	// mountTargets are read-only directory mounts whose sources this op
	// fills, each in its own group. mountRuns counts per target.
	mountTargets []string
	mountRunsMu  sync.Mutex
	mountRuns    map[string]int
	// mountBodyHook, when set, runs inside each mount group's body before
	// it returns (used to rendezvous concurrent group completions).
	mountBodyHook func(target string)
	// fsBodyHook, when set, runs inside the fs group's body before it
	// returns.
	fsBodyHook func()
}

func (op *containerPartsTestBaseOp) mountRunsFor(target string) int {
	op.mountRunsMu.Lock()
	defer op.mountRunsMu.Unlock()
	return op.mountRuns[target]
}

var _ LazyContainerParts = (*containerPartsTestBaseOp)(nil)

func (op *containerPartsTestBaseOp) Evaluate(ctx context.Context, ctr *Container) error {
	return ctr.evaluateAllLazyGroups(ctx, op)
}

func (op *containerPartsTestBaseOp) AttachDependencies(context.Context, func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	return nil, nil
}

func (op *containerPartsTestBaseOp) EncodePersisted(context.Context, dagql.PersistedObjectCache) (json.RawMessage, error) {
	return nil, nil
}

func (op *containerPartsTestBaseOp) ContainerLazyGroups(_ context.Context, ctr *Container, parts []dagql.PartKey) ([]dagql.LazyGroupKey, error) {
	return templateAContainerGroups(ctr, parts)
}

func (op *containerPartsTestBaseOp) EvaluateContainerGroup(ctx context.Context, ctr *Container, group dagql.LazyGroupKey) error {
	switch group {
	case ContainerLazyGroupMetadata:
		return op.LazyState.EvaluateGroup(ctx, "test.base", group, func(context.Context) error {
			op.metaRuns.Add(1)
			ctr.Config.WorkingDir = op.workdir
			ctr.Config.Env = append([]string(nil), op.env...)
			ctr.VolatileEnv = append([]string(nil), op.volatileEnv...)
			return nil
		})
	case containerDelegationGroup(ContainerPartFS):
		return op.LazyState.EvaluateGroup(ctx, "test.base", group, func(context.Context) error {
			op.fsRuns.Add(1)
			dir := &Directory{
				Dir:      new(LazyAccessor[string, *Directory]),
				Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]),
			}
			dir.Dir.SetValue("/")
			ctr.ensureFSAccessor().SetValue(dir)
			if op.fsBodyHook != nil {
				op.fsBodyHook()
			}
			return nil
		})
	default:
		for _, target := range op.mountTargets {
			if group != containerDelegationGroup(ContainerPartMount(target)) {
				continue
			}
			return op.LazyState.EvaluateGroup(ctx, "test.base", group, func(context.Context) error {
				op.mountRunsMu.Lock()
				if op.mountRuns == nil {
					op.mountRuns = make(map[string]int)
				}
				op.mountRuns[target]++
				op.mountRunsMu.Unlock()
				dir := &Directory{
					Dir:      new(LazyAccessor[string, *Directory]),
					Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]),
				}
				dir.Dir.SetValue("/")
				mnt := ctr.mountAt(target)
				if mnt == nil {
					return fmt.Errorf("test base op: no mount at %q", target)
				}
				mnt.DirectorySource.SetValue(dir)
				if op.mountBodyHook != nil {
					op.mountBodyHook(target)
				}
				return nil
			})
		}
		// execMeta: nothing ever ran, nothing to fill.
		return op.LazyState.EvaluateGroup(ctx, "test.base", group, nil)
	}
}

func newContainerPartsTestCtx(t *testing.T) (context.Context, *dagql.Cache, *dagql.Server, string) {
	t.Helper()
	ctx := engine.ContextWithClientMetadata(t.Context(), &engine.ClientMetadata{
		ClientID:  "container-parts-test-client",
		SessionID: "container-parts-test-session",
	})
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, cache.CloseDiscardingPersistence())
	})
	ctx = dagql.ContextWithCache(ctx, cache)
	srv := newCoreDagqlServerForTest(t, &Query{Server: &mockServer{}})
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*Container]{}))
	return ctx, cache, srv, "container-parts-test-session"
}

func attachContainerPartsTestResult(
	t *testing.T,
	ctx context.Context,
	cache *dagql.Cache,
	srv *dagql.Server,
	sessionID, op string,
	ctr *Container,
) dagql.ObjectResult[*Container] {
	t.Helper()
	frame := &dagql.ResultCall{
		Kind:        dagql.ResultCallKindSynthetic,
		SyntheticOp: op,
		Type:        dagql.NewResultCallType((&Container{}).Type()),
	}
	resAny, err := cache.GetOrInitCall(ctx, sessionID, srv, &dagql.CallRequest{ResultCall: frame}, func(context.Context) (dagql.AnyResult, error) {
		return dagql.NewObjectResultForCall(ctr, srv, frame)
	})
	require.NoError(t, err)
	return resAny.(dagql.ObjectResult[*Container])
}

// A metadata read on a pending template-A chain settles metadata through
// the chain and leaves every snapshot group pending: the commit-6
// headline at the core layer.
func TestContainerMetadataChainLeavesSnapshotGroupsPending(t *testing.T) {
	t.Parallel()
	ctx, cache, srv, sessionID := newContainerPartsTestCtx(t)

	baseOp := &containerPartsTestBaseOp{
		LazyState: NewLazyState(),
		workdir:   "/base",
		env:       []string{"FOO=bar"},
	}
	base := &Container{
		FS:           new(LazyAccessor[*Directory, *Container]),
		MetaSnapshot: new(LazyAccessor[bkcache.ImmutableRef, *Container]),
		Lazy:         baseOp,
	}
	baseRes := attachContainerPartsTestResult(t, ctx, cache, srv, sessionID, "parts-test-base", base)

	child := &Container{
		FS:           new(LazyAccessor[*Directory, *Container]),
		MetaSnapshot: new(LazyAccessor[bkcache.ImmutableRef, *Container]),
	}
	child.Lazy = &ContainerWithWorkdirLazy{
		LazyState: NewLazyState(),
		Parent:    baseRes,
		Path:      "/child",
	}
	childRes := attachContainerPartsTestResult(t, ctx, cache, srv, sessionID, "parts-test-child", child)

	require.NoError(t, cache.EvaluateParts(ctx, childRes, ContainerPartMetadata))

	// Metadata settled through the chain; the child's edit applied on the
	// base's settled fields.
	require.Equal(t, "/child", child.Config.WorkingDir)
	require.Equal(t, []string{"FOO=bar"}, child.Config.Env)
	require.Equal(t, int32(1), baseOp.metaRuns.Load())

	// No snapshot work anywhere: the base's fs body never ran, the
	// child's fs accessor is untouched, and both results stay pending.
	require.Equal(t, int32(0), baseOp.fsRuns.Load())
	_, childFSSet := child.FS.Peek()
	require.False(t, childFSSet)
	require.True(t, dagql.HasPendingLazyEvaluation(childRes))
	require.True(t, dagql.HasPendingLazyEvaluation(baseRes))

	// A repeated metadata read re-runs nothing.
	require.NoError(t, cache.EvaluateParts(ctx, childRes, ContainerPartMetadata))
	require.Equal(t, int32(1), baseOp.metaRuns.Load())

	// Demanding the child's fs part forces exactly the base's fs group
	// and fills the child's accessor by delegation.
	require.NoError(t, cache.EvaluateParts(ctx, childRes, ContainerPartFS))
	require.Equal(t, int32(1), baseOp.fsRuns.Load())
	childFS, childFSSet := child.FS.Peek()
	require.True(t, childFSSet)
	require.NotNil(t, childFS)
	childFSDir, ok := childFS.Dir.Peek()
	require.True(t, ok)
	require.Equal(t, "/", childFSDir)

	// Full evaluation consumes the remaining exec-meta delegation and
	// clears the ops.
	require.NoError(t, cache.Evaluate(ctx, childRes))
	require.False(t, dagql.HasPendingLazyEvaluation(childRes))
	require.Nil(t, child.Lazy)
	require.Equal(t, int32(1), baseOp.metaRuns.Load())
	require.Equal(t, int32(1), baseOp.fsRuns.Load())
}

// Direct object-side evaluation (Container.Evaluate, the path
// metaFileContents uses) runs a refined op's remaining groups and
// coexists with cache-side per-group state.
func TestContainerDirectEvaluateRunsRefinedGroups(t *testing.T) {
	t.Parallel()
	ctx, cache, srv, sessionID := newContainerPartsTestCtx(t)

	baseOp := &containerPartsTestBaseOp{
		LazyState: NewLazyState(),
		workdir:   "/base",
	}
	base := &Container{
		FS:           new(LazyAccessor[*Directory, *Container]),
		MetaSnapshot: new(LazyAccessor[bkcache.ImmutableRef, *Container]),
		Lazy:         baseOp,
	}
	baseRes := attachContainerPartsTestResult(t, ctx, cache, srv, sessionID, "parts-test-direct-base", base)

	child := &Container{
		FS:           new(LazyAccessor[*Directory, *Container]),
		MetaSnapshot: new(LazyAccessor[bkcache.ImmutableRef, *Container]),
	}
	child.Lazy = &ContainerWithUserLazy{
		LazyState: NewLazyState(),
		Parent:    baseRes,
		Name:      "guest",
	}
	attachContainerPartsTestResult(t, ctx, cache, srv, sessionID, "parts-test-direct-child", child)

	require.NoError(t, child.Evaluate(ctx))
	require.Equal(t, "guest", child.Config.User)
	require.Equal(t, "/base", child.Config.WorkingDir)
	require.Nil(t, child.Lazy)
	require.Equal(t, int32(1), baseOp.metaRuns.Load())
	require.Equal(t, int32(1), baseOp.fsRuns.Load())
}

// containerPartsTestUnrefinedWriterOp stands in for an unrefined
// snapshot writer (the withDirectory family): the schema shell keeps the
// cloned pre-copy fs accessor from construction time, and the
// whole-result body later replaces fs with the op's output. It
// implements only Lazy[*Container], never LazyContainerParts.
type containerPartsTestUnrefinedWriterOp struct {
	LazyState
	parent dagql.ObjectResult[*Container]
	newDir string
	runs   atomic.Int32
	// preClearHook, when set, runs inside the body right before the op
	// consumes itself (used to rendezvous readers with the inline clear).
	preClearHook func()
}

func (op *containerPartsTestUnrefinedWriterOp) Evaluate(ctx context.Context, ctr *Container) error {
	return op.LazyState.Evaluate(ctx, "test.unrefinedWriter", func(context.Context) error {
		op.runs.Add(1)
		dir := &Directory{
			Dir:      new(LazyAccessor[string, *Directory]),
			Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]),
		}
		dir.Dir.SetValue(op.newDir)
		ctr.ensureFSAccessor().SetValue(dir)
		if op.preClearHook != nil {
			op.preClearHook()
		}
		ctr.consumeLazyOp()
		return nil
	})
}

func (op *containerPartsTestUnrefinedWriterOp) AttachDependencies(_ context.Context, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	if op.parent.Self() == nil {
		return nil, nil
	}
	parent, err := attach(op.parent)
	if err != nil {
		return nil, err
	}
	op.parent = parent.(dagql.ObjectResult[*Container])
	return []dagql.AnyResult{parent}, nil
}

func (op *containerPartsTestUnrefinedWriterOp) EncodePersisted(context.Context, dagql.PersistedObjectCache) (json.RawMessage, error) {
	return nil, nil
}

// A construction-time cloned accessor is NOT the parent part's final
// value when an unrefined writer sits between: A (fs set to /old) ->
// P = unrefined writer replacing fs with /new (shell keeps the cloned
// /old pre-copy) -> C = refined metadata op (shell clones P's /old
// pre-copy). Demanding C's fs must serve the writer's output, so
// delegation must always evaluate the parent part and copy - a set
// destination accessor proves nothing about provenance.
func TestContainerDelegationOverwritesStalePreCopiedAccessor(t *testing.T) {
	t.Parallel()
	ctx, cache, srv, sessionID := newContainerPartsTestCtx(t)

	oldDir := &Directory{
		Dir:      new(LazyAccessor[string, *Directory]),
		Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]),
	}
	oldDir.Dir.SetValue("/old")
	base := &Container{
		FS:           new(LazyAccessor[*Directory, *Container]),
		MetaSnapshot: new(LazyAccessor[bkcache.ImmutableRef, *Container]),
	}
	base.FS.SetValue(oldDir)
	baseRes := attachContainerPartsTestResult(t, ctx, cache, srv, sessionID, "stale-precopy-base", base)

	writerFS, err := CloneContainerDirectoryAccessor(ctx, base.FS)
	require.NoError(t, err)
	writer := &Container{
		FS:           writerFS,
		MetaSnapshot: new(LazyAccessor[bkcache.ImmutableRef, *Container]),
	}
	writerOp := &containerPartsTestUnrefinedWriterOp{
		LazyState: NewLazyState(),
		parent:    baseRes,
		newDir:    "/new",
	}
	writer.Lazy = writerOp
	writerRes := attachContainerPartsTestResult(t, ctx, cache, srv, sessionID, "stale-precopy-writer", writer)

	childFS, err := CloneContainerDirectoryAccessor(ctx, writer.FS)
	require.NoError(t, err)
	child := &Container{
		FS:           childFS,
		MetaSnapshot: new(LazyAccessor[bkcache.ImmutableRef, *Container]),
	}
	child.Lazy = &ContainerWithEnvVariableLazy{
		LazyState: NewLazyState(),
		Parent:    writerRes,
		Name:      "K",
		Value:     "v",
	}
	childRes := attachContainerPartsTestResult(t, ctx, cache, srv, sessionID, "stale-precopy-child", child)

	// Sanity: the child's shell really does carry the stale pre-copy.
	preFS, preSet := child.FS.Peek()
	require.True(t, preSet)
	preDir, _ := preFS.Dir.Peek()
	require.Equal(t, "/old", preDir)

	require.NoError(t, cache.EvaluateParts(ctx, childRes, ContainerPartFS))
	require.Equal(t, int32(1), writerOp.runs.Load())
	gotFS, gotSet := child.FS.Peek()
	require.True(t, gotSet)
	gotDir, ok := gotFS.Dir.Peek()
	require.True(t, ok)
	require.Equal(t, "/new", gotDir)
}

// Two sibling groups finishing concurrently both observe full
// consumption and both clear container.Lazy; the clear must be
// serialized under the op's LazyMu (write/write on the interface word
// otherwise, which the race detector flags).
func TestContainerConcurrentGroupCompletionClearsLazyOnce(t *testing.T) {
	t.Parallel()
	ctx, cache, srv, sessionID := newContainerPartsTestCtx(t)

	var rendezvous sync.WaitGroup
	rendezvous.Add(2)
	baseOp := &containerPartsTestBaseOp{
		LazyState:    NewLazyState(),
		workdir:      "/base",
		mountTargets: []string{"/a", "/b"},
		mountBodyHook: func(string) {
			rendezvous.Done()
			rendezvous.Wait()
		},
	}
	base := &Container{
		FS:           new(LazyAccessor[*Directory, *Container]),
		MetaSnapshot: new(LazyAccessor[bkcache.ImmutableRef, *Container]),
		Mounts: ContainerMounts{
			{Target: "/a", Readonly: true, DirectorySource: new(LazyAccessor[*Directory, *Container])},
			{Target: "/b", Readonly: true, DirectorySource: new(LazyAccessor[*Directory, *Container])},
		},
		Lazy: baseOp,
	}
	baseRes := attachContainerPartsTestResult(t, ctx, cache, srv, sessionID, "concurrent-clear-base", base)

	// Consume every group except the two mounts.
	require.NoError(t, cache.EvaluateParts(ctx, baseRes, ContainerPartMetadata))
	require.NoError(t, cache.EvaluateParts(ctx, baseRes, ContainerPartFS))
	require.NoError(t, cache.EvaluateParts(ctx, baseRes, ContainerPartExecMeta))

	// The last two groups finish together: their bodies rendezvous, so
	// both completions race the all-consumed check and the Lazy clear.
	errA := make(chan error, 1)
	errB := make(chan error, 1)
	go func() { errA <- cache.EvaluateParts(ctx, baseRes, ContainerPartMount("/a")) }()
	go func() { errB <- cache.EvaluateParts(ctx, baseRes, ContainerPartMount("/b")) }()
	require.NoError(t, <-errA)
	require.NoError(t, <-errB)

	require.Nil(t, base.Lazy)
	require.False(t, dagql.HasPendingLazyEvaluation(baseRes))
}

// The resolution-phase read (ResolveLazyEvalGroups) and the direct
// narrow force (evaluatePartsDirect) read the op pointer before any
// group state is consulted, so no attempt-retirement ordering covers
// them; they must share a synchronization point with the refined
// clear. Two readers hammer both paths while the op's final group
// completes and clears - red under -race without the shared lock.
func TestContainerRoutingReadsRaceRefinedClear(t *testing.T) {
	t.Parallel()
	ctx, cache, srv, sessionID := newContainerPartsTestCtx(t)

	clearImminent := make(chan struct{})
	baseOp := &containerPartsTestBaseOp{
		LazyState: NewLazyState(),
		workdir:   "/base",
		fsBodyHook: func() {
			close(clearImminent)
			// Hold the body open briefly so the readers below overlap
			// the window between body return and attempt retirement.
			for range 2000 {
				runtime.Gosched()
			}
		},
	}
	base := &Container{
		FS:           new(LazyAccessor[*Directory, *Container]),
		MetaSnapshot: new(LazyAccessor[bkcache.ImmutableRef, *Container]),
		Lazy:         baseOp,
	}
	baseRes := attachContainerPartsTestResult(t, ctx, cache, srv, sessionID, "routing-race-base", base)

	// Consume everything except fs, so the fs completion is the clear.
	require.NoError(t, cache.EvaluateParts(ctx, baseRes, ContainerPartMetadata))
	require.NoError(t, cache.EvaluateParts(ctx, baseRes, ContainerPartExecMeta))

	done := make(chan struct{})
	finalErr := make(chan error, 1)
	go func() {
		defer close(done)
		finalErr <- cache.EvaluateParts(ctx, baseRes, ContainerPartFS)
	}()

	<-clearImminent
	resolveErr := make(chan error, 1)
	go func() {
		// The cache resolver path: ResolveLazyEvalGroups' pointer read.
		for {
			select {
			case <-done:
				resolveErr <- nil
				return
			default:
			}
			if err := cache.EvaluateParts(ctx, baseRes, ContainerPartMetadata); err != nil {
				resolveErr <- err
				return
			}
		}
	}()
	directErr := make(chan error, 1)
	go func() {
		// The direct narrow-force path: evaluatePartsDirect's pointer read.
		for {
			select {
			case <-done:
				directErr <- nil
				return
			default:
			}
			if err := base.evaluatePartsDirect(ctx, ContainerPartMetadata); err != nil {
				directErr <- err
				return
			}
		}
	}()

	require.NoError(t, <-finalErr)
	require.NoError(t, <-resolveErr)
	require.NoError(t, <-directErr)
	require.Nil(t, base.Lazy)
}

// The routing reads must also be ordered against UNREFINED ops' clears -
// the dominant everyday writer (every from(image) body ends with one).
// Two clients reading metadata of the same pending unrefined container
// while its whole-result body finishes is routine.
func TestContainerRoutingReadsRaceUnrefinedClear(t *testing.T) {
	t.Parallel()
	ctx, cache, srv, sessionID := newContainerPartsTestCtx(t)

	clearImminent := make(chan struct{})
	op := &containerPartsTestUnrefinedWriterOp{
		LazyState: NewLazyState(),
		newDir:    "/made",
		preClearHook: func() {
			close(clearImminent)
			for range 2000 {
				runtime.Gosched()
			}
		},
	}
	base := &Container{
		FS:           new(LazyAccessor[*Directory, *Container]),
		MetaSnapshot: new(LazyAccessor[bkcache.ImmutableRef, *Container]),
		Lazy:         op,
	}
	baseRes := attachContainerPartsTestResult(t, ctx, cache, srv, sessionID, "unrefined-clear-race-base", base)

	done := make(chan struct{})
	finalErr := make(chan error, 1)
	go func() {
		defer close(done)
		finalErr <- cache.Evaluate(ctx, baseRes)
	}()

	<-clearImminent
	resolveErr := make(chan error, 1)
	go func() {
		// The cache resolver path's routing read.
		for {
			select {
			case <-done:
				resolveErr <- nil
				return
			default:
			}
			if err := cache.EvaluateParts(ctx, baseRes, ContainerPartMetadata); err != nil {
				resolveErr <- err
				return
			}
		}
	}()
	directErr := make(chan error, 1)
	go func() {
		// The direct narrow-force path's routing read.
		for {
			select {
			case <-done:
				directErr <- nil
				return
			default:
			}
			if err := base.evaluatePartsDirect(ctx, ContainerPartMetadata); err != nil {
				directErr <- err
				return
			}
		}
	}()

	require.NoError(t, <-finalErr)
	require.NoError(t, <-resolveErr)
	require.NoError(t, <-directErr)
	require.Nil(t, base.Lazy)
	require.Equal(t, int32(1), op.runs.Load())
}

// HasPendingLazyEvaluation's fallback (LazyEvalFunc's op-pointer read)
// must be ordered against the direct-path clear: when every group was
// consumed through the direct narrow force, the shared result carries no
// cache-side lazy state, so the fallback read is reached on every call
// (the cloneContainerForSchemaChild path) while evaluatePartsDirect's
// final-group completion clears the op.
func TestContainerHasPendingFallbackRacesDirectClear(t *testing.T) {
	t.Parallel()
	ctx, cache, srv, sessionID := newContainerPartsTestCtx(t)
	_ = cache

	clearImminent := make(chan struct{})
	baseOp := &containerPartsTestBaseOp{
		LazyState: NewLazyState(),
		workdir:   "/base",
		fsBodyHook: func() {
			close(clearImminent)
			for range 2000 {
				runtime.Gosched()
			}
		},
	}
	base := &Container{
		FS:           new(LazyAccessor[*Directory, *Container]),
		MetaSnapshot: new(LazyAccessor[bkcache.ImmutableRef, *Container]),
		Lazy:         baseOp,
	}
	baseRes := attachContainerPartsTestResult(t, ctx, cache, srv, sessionID, "haspending-direct-race-base", base)

	// Consume everything except fs strictly through the direct path, so
	// no cache-side group state exists and HasPendingLazyEvaluation
	// always reaches the value fallback.
	require.NoError(t, base.evaluatePartsDirect(ctx, ContainerPartMetadata))
	require.NoError(t, base.evaluatePartsDirect(ctx, ContainerPartExecMeta))

	done := make(chan struct{})
	finalErr := make(chan error, 1)
	go func() {
		defer close(done)
		finalErr <- base.evaluatePartsDirect(ctx, ContainerPartFS)
	}()

	<-clearImminent
	pendingDone := make(chan struct{})
	go func() {
		defer close(pendingDone)
		for {
			select {
			case <-done:
				return
			default:
			}
			dagql.HasPendingLazyEvaluation(baseRes)
		}
	}()

	require.NoError(t, <-finalErr)
	<-pendingDone
	require.Nil(t, base.Lazy)
	require.False(t, dagql.HasPendingLazyEvaluation(baseRes))
}
