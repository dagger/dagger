package core

import (
	"context"
	"encoding/json"
	"fmt"
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

	workdir string
	env     []string
	// mountTargets are read-only directory mounts whose sources this op
	// fills, each in its own group. mountRuns counts per target.
	mountTargets []string
	mountRunsMu  sync.Mutex
	mountRuns    map[string]int
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
