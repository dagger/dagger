package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"runtime"
	"slices"
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
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*CacheVolume]{}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*Volume]{}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*Secret]{}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*Socket]{}))
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

func attachContainerPartsTestObject[T dagql.Typed](
	t *testing.T,
	ctx context.Context,
	cache *dagql.Cache,
	srv *dagql.Server,
	sessionID, op string,
	value T,
) dagql.ObjectResult[T] {
	t.Helper()
	frame := &dagql.ResultCall{
		Kind:        dagql.ResultCallKindSynthetic,
		SyntheticOp: op,
		Type:        dagql.NewResultCallType(value.Type()),
	}
	resAny, err := cache.GetOrInitCall(ctx, sessionID, srv, &dagql.CallRequest{ResultCall: frame}, func(context.Context) (dagql.AnyResult, error) {
		return dagql.NewObjectResultForCall(value, srv, frame)
	})
	require.NoError(t, err)
	return resAny.(dagql.ObjectResult[T])
}

func TestContainerMetadataOnlyMountMutationParts(t *testing.T) {
	tests := []struct {
		name   string
		apply  func(*testing.T, context.Context, *dagql.Cache, *dagql.Server, string, dagql.ObjectResult[*Container], *Container) Lazy[*Container]
		assert func(*testing.T, context.Context, *Container)
	}{
		{
			name: "mounted cache",
			apply: func(t *testing.T, ctx context.Context, cache *dagql.Cache, srv *dagql.Server, sessionID string, parent dagql.ObjectResult[*Container], child *Container) Lazy[*Container] {
				cacheVolume := &CacheVolume{snapshot: &cacheVolumeTestMutableRef{}}
				cacheRes := attachContainerPartsTestObject(t, ctx, cache, srv, sessionID, "mount-parts-cache", cacheVolume)
				_, err := child.WithMountedCache(ctx, "/cache", cacheRes)
				require.NoError(t, err)
				return &ContainerWithMountedCacheLazy{LazyState: NewLazyState(), Parent: parent, Target: "/cache", Cache: cacheRes}
			},
			assert: func(t *testing.T, _ context.Context, child *Container) {
				mnt := child.mountAt("/cache")
				require.NotNil(t, mnt)
				require.NotNil(t, mnt.CacheSource)
			},
		},
		{
			name: "mounted temp",
			apply: func(t *testing.T, ctx context.Context, _ *dagql.Cache, _ *dagql.Server, _ string, parent dagql.ObjectResult[*Container], child *Container) Lazy[*Container] {
				_, err := child.WithMountedTemp(ctx, "/tmp", 1024)
				require.NoError(t, err)
				return &ContainerWithMountedTempLazy{LazyState: NewLazyState(), Parent: parent, Target: "/tmp", Size: 1024}
			},
			assert: func(t *testing.T, _ context.Context, child *Container) {
				mnt := child.mountAt("/tmp")
				require.NotNil(t, mnt)
				require.Equal(t, 1024, mnt.TmpfsSource.Size)
			},
		},
		{
			name: "mounted volume",
			apply: func(t *testing.T, ctx context.Context, cache *dagql.Cache, srv *dagql.Server, sessionID string, parent dagql.ObjectResult[*Container], child *Container) Lazy[*Container] {
				volumeRes := attachContainerPartsTestObject(t, ctx, cache, srv, sessionID, "mount-parts-volume", &Volume{})
				_, err := child.WithMountedVolume(ctx, "/volume", volumeRes, true)
				require.NoError(t, err)
				return &ContainerWithMountedVolumeLazy{LazyState: NewLazyState(), Parent: parent, Target: "/volume", Volume: volumeRes, Readonly: true}
			},
			assert: func(t *testing.T, _ context.Context, child *Container) {
				mnt := child.mountAt("/volume")
				require.NotNil(t, mnt)
				require.NotNil(t, mnt.VolumeSource)
				require.True(t, mnt.Readonly)
			},
		},
		{
			name: "mounted secret",
			apply: func(t *testing.T, ctx context.Context, cache *dagql.Cache, srv *dagql.Server, sessionID string, parent dagql.ObjectResult[*Container], child *Container) Lazy[*Container] {
				secretRes := attachContainerPartsTestObject(t, ctx, cache, srv, sessionID, "mount-parts-secret", &Secret{NameVal: "test"})
				_, err := child.WithMountedSecret(ctx, parent, "/secret", secretRes, "", 0o400)
				require.NoError(t, err)
				return &ContainerWithMountedSecretLazy{LazyState: NewLazyState(), Parent: parent, Target: "/secret", Source: secretRes, Mode: 0o400}
			},
			assert: func(t *testing.T, _ context.Context, child *Container) {
				require.NotEmpty(t, child.Secrets)
				secret := child.Secrets[len(child.Secrets)-1]
				require.Equal(t, "/secret", secret.MountPath)
				require.Equal(t, fs.FileMode(0o400), secret.Mode)
			},
		},
		{
			name: "without mount",
			apply: func(t *testing.T, ctx context.Context, _ *dagql.Cache, _ *dagql.Server, _ string, parent dagql.ObjectResult[*Container], child *Container) Lazy[*Container] {
				_, err := child.WithoutMount(ctx, "/old")
				require.NoError(t, err)
				return &ContainerWithoutMountLazy{LazyState: NewLazyState(), Parent: parent, Target: "/old"}
			},
			assert: func(t *testing.T, ctx context.Context, child *Container) {
				require.Nil(t, child.mountAt("/old"))
				groups, err := child.Lazy.(LazyContainerParts).ContainerLazyGroups(ctx, child, nil)
				require.NoError(t, err)
				require.NotContains(t, groups, containerDelegationGroup(ContainerPartMount("/old")))
			},
		},
		{
			name: "with unix socket",
			apply: func(t *testing.T, ctx context.Context, cache *dagql.Cache, srv *dagql.Server, sessionID string, parent dagql.ObjectResult[*Container], child *Container) Lazy[*Container] {
				socketRes := attachContainerPartsTestObject(t, ctx, cache, srv, sessionID, "mount-parts-socket", &Socket{Kind: SocketKindUnixOpaque})
				_, err := child.WithUnixSocketFromParent(ctx, parent, "/socket", socketRes, "")
				require.NoError(t, err)
				return &ContainerWithUnixSocketLazy{LazyState: NewLazyState(), Parent: parent, Target: "/socket", Source: socketRes}
			},
			assert: func(t *testing.T, _ context.Context, child *Container) {
				require.Equal(t, "/socket", child.Sockets[len(child.Sockets)-1].ContainerPath)
			},
		},
		{
			name: "without unix socket",
			apply: func(t *testing.T, ctx context.Context, _ *dagql.Cache, _ *dagql.Server, _ string, parent dagql.ObjectResult[*Container], child *Container) Lazy[*Container] {
				_, err := child.WithoutUnixSocket(ctx, "/old.sock")
				require.NoError(t, err)
				return &ContainerWithoutUnixSocketLazy{LazyState: NewLazyState(), Parent: parent, Target: "/old.sock"}
			},
			assert: func(t *testing.T, _ context.Context, child *Container) {
				for _, socket := range child.Sockets {
					require.NotEqual(t, "/old.sock", socket.ContainerPath)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx, cache, srv, sessionID := newContainerPartsTestCtx(t)
			baseOp := &containerPartsTestBaseOp{
				LazyState:    NewLazyState(),
				workdir:      "/",
				mountTargets: []string{"/old"},
			}
			base := &Container{
				FS:           new(LazyAccessor[*Directory, *Container]),
				MetaSnapshot: new(LazyAccessor[bkcache.ImmutableRef, *Container]),
				Mounts: ContainerMounts{{
					Target:          "/old",
					Readonly:        true,
					DirectorySource: new(LazyAccessor[*Directory, *Container]),
				}},
				Secrets:  []ContainerSecret{{MountPath: "/old"}},
				Sockets:  []ContainerSocket{{ContainerPath: "/old.sock"}},
				ImageRef: "parent-image",
				Lazy:     baseOp,
			}
			baseRes := attachContainerPartsTestResult(t, ctx, cache, srv, sessionID, "mount-parts-base-"+test.name, base)
			clonedMounts, err := CloneContainerMounts(ctx, base.Mounts)
			require.NoError(t, err)
			child := &Container{
				FS:           new(LazyAccessor[*Directory, *Container]),
				MetaSnapshot: new(LazyAccessor[bkcache.ImmutableRef, *Container]),
				Config:       CloneContainerImageConfig(base.Config),
				Mounts:       clonedMounts,
				Secrets:      slices.Clone(base.Secrets),
				Sockets:      slices.Clone(base.Sockets),
				ImageRef:     base.ImageRef,
			}
			child.Lazy = test.apply(t, ctx, cache, srv, sessionID, baseRes, child)
			childRes := attachContainerPartsTestResult(t, ctx, cache, srv, sessionID, "mount-parts-child-"+test.name, child)

			require.NoError(t, cache.EvaluateParts(ctx, childRes, ContainerPartMetadata))
			test.assert(t, ctx, child)
			require.Empty(t, child.ImageRef)
			require.Equal(t, int32(0), baseOp.fsRuns.Load())
			require.Equal(t, 0, baseOp.mountRunsFor("/old"))
			require.True(t, dagql.HasPendingLazyEvaluation(childRes))
		})
	}
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
