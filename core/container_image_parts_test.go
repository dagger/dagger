package core

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

type containerPartsTestFromImageOp struct {
	*ContainerFromImageRefLazy
}

func (op *containerPartsTestFromImageOp) EvaluateContainerGroup(ctx context.Context, ctr *Container, group dagql.LazyGroupKey) error {
	if group != ContainerLazyGroupWrite {
		return op.ContainerFromImageRefLazy.EvaluateContainerGroup(ctx, ctr, group)
	}
	return op.LazyState.EvaluateGroup(ctx, "test.containerFrom", group, func(context.Context) error {
		dir := &Directory{
			Dir:      new(LazyAccessor[string, *Directory]),
			Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]),
		}
		dir.Dir.SetValue("/")
		ctr.ensureFSAccessor().SetValue(dir)
		return nil
	})
}

func TestContainerFromSuccessfulFSConsumesFinalDelegations(t *testing.T) {
	t.Parallel()
	ctx, cache, srv, sessionID := newContainerPartsTestCtx(t)

	parent := &Container{
		FS:           new(LazyAccessor[*Directory, *Container]),
		MetaSnapshot: new(LazyAccessor[bkcache.ImmutableRef, *Container]),
	}
	parentRes := attachContainerPartsTestResult(t, ctx, cache, srv, sessionID, "from-final-parent", parent)
	lazy := &containerPartsTestFromImageOp{ContainerFromImageRefLazy: &ContainerFromImageRefLazy{
		LazyState: NewLazyState(),
		Parent:    parentRes,
	}}
	child := &Container{
		FS:           new(LazyAccessor[*Directory, *Container]),
		MetaSnapshot: new(LazyAccessor[bkcache.ImmutableRef, *Container]),
		Lazy:         lazy,
	}
	childRes := attachContainerPartsTestResult(t, ctx, cache, srv, sessionID, "from-final-child", child)

	require.NoError(t, cache.EvaluateParts(ctx, childRes, ContainerPartFS))
	require.Nil(t, child.lazyOpForRouting())
	require.False(t, dagql.HasPendingLazyEvaluation(childRes))
	_, parentFSSet := parent.FS.Peek()
	require.False(t, parentFSSet)
	_, parentExecMetaSet := parent.MetaSnapshot.Peek()
	require.False(t, parentExecMetaSet)
}

func TestContainerFromMetadataDoesNotPullImage(t *testing.T) {
	t.Parallel()
	ctx, cache, srv, sessionID := newContainerPartsTestCtx(t)
	const imageRef = "registry.example/image@sha256:0000000000000000000000000000000000000000000000000000000000000000"

	parent := &Container{
		Config: dockerspec.DockerOCIImageConfig{
			ImageConfig: specs.ImageConfig{WorkingDir: "/parent"},
		},
		FS:           new(LazyAccessor[*Directory, *Container]),
		MetaSnapshot: new(LazyAccessor[bkcache.ImmutableRef, *Container]),
	}
	parentRes := attachContainerPartsTestResult(t, ctx, cache, srv, sessionID, "from-parts-parent", parent)

	config := dockerspec.DockerOCIImageConfig{
		ImageConfig: specs.ImageConfig{
			WorkingDir: "/image",
			Env:        []string{"FOO=bar"},
		},
	}
	platform := Platform(specs.Platform{OS: "linux", Architecture: "amd64"})
	child := &Container{
		Config: dockerspec.DockerOCIImageConfig{
			ImageConfig: specs.ImageConfig{WorkingDir: "/stale"},
		},
		FS:           new(LazyAccessor[*Directory, *Container]),
		MetaSnapshot: new(LazyAccessor[bkcache.ImmutableRef, *Container]),
		Lazy: &ContainerFromImageRefLazy{
			LazyState:    NewLazyState(),
			Parent:       parentRes,
			CanonicalRef: imageRef,
			Config:       CloneContainerImageConfig(config),
			ImageRef:     imageRef,
			Platform:     platform,
		},
	}
	childRes := attachContainerPartsTestResult(t, ctx, cache, srv, sessionID, "from-parts-child", child)

	require.NoError(t, cache.EvaluateParts(ctx, childRes, ContainerPartMetadata))
	require.Equal(t, config, child.Config)
	require.Equal(t, imageRef, child.ImageRef)
	require.Equal(t, platform, child.Platform)
	require.True(t, dagql.HasPendingLazyEvaluation(childRes))

	err := cache.EvaluateParts(ctx, childRes, ContainerPartFS)
	require.Error(t, err)
	require.Equal(t, config, child.Config)
	require.Equal(t, imageRef, child.ImageRef)
	require.Equal(t, platform, child.Platform)
}

func TestContainerFromDelegatesSnapshotParts(t *testing.T) {
	metaParent := &cacheVolumeTestImmutableRef{id: "meta-parent", snapshotID: "meta"}
	metaChild := &cacheVolumeTestImmutableRef{id: "meta-child", snapshotID: "meta"}
	mountParent := &cacheVolumeTestImmutableRef{id: "mount-parent", snapshotID: "mount"}
	mountChild := &cacheVolumeTestImmutableRef{id: "mount-child", snapshotID: "mount"}
	snapshotManager := &cacheVolumeTestSnapshotManager{
		immutableBySnapshotID: map[string]bkcache.ImmutableRef{
			"meta":  metaChild,
			"mount": mountChild,
		},
	}
	queryServer := &cacheVolumeTestQueryServer{
		mockServer:   &mockServer{},
		cacheManager: snapshotManager,
	}
	ctx, cache, srv, sessionID := newContainerPartsTestCtxWithQueryServer(t, queryServer)
	ctx = ContextWithQuery(ctx, &Query{Server: queryServer})

	baseOp := &containerPartsTestBaseOp{
		LazyState:      NewLazyState(),
		workdir:        "/",
		mountTargets:   []string{"/parent-mount"},
		metaSnapshot:   metaParent,
		mountSnapshots: map[string]bkcache.ImmutableRef{"/parent-mount": mountParent},
	}
	parent := &Container{
		FS:           new(LazyAccessor[*Directory, *Container]),
		MetaSnapshot: new(LazyAccessor[bkcache.ImmutableRef, *Container]),
		Mounts: ContainerMounts{{
			Target:          "/parent-mount",
			DirectorySource: new(LazyAccessor[*Directory, *Container]),
		}},
		Lazy: baseOp,
	}
	parentRes := attachContainerPartsTestResult(t, ctx, cache, srv, sessionID, "from-delegation-parent", parent)
	child := &Container{
		FS:           new(LazyAccessor[*Directory, *Container]),
		MetaSnapshot: new(LazyAccessor[bkcache.ImmutableRef, *Container]),
		Lazy: &ContainerFromImageRefLazy{
			LazyState: NewLazyState(),
			Parent:    parentRes,
			Config:    dockerspec.DockerOCIImageConfig{},
			Platform:  Platform(specs.Platform{OS: "linux", Architecture: "amd64"}),
		},
	}
	childRes := attachContainerPartsTestResult(t, ctx, cache, srv, sessionID, "from-delegation-child", child)

	require.NoError(t, cache.EvaluateParts(ctx, childRes, ContainerPartExecMeta))
	metaSnapshot, ok := child.MetaSnapshot.Peek()
	require.True(t, ok)
	require.Same(t, metaChild, metaSnapshot)
	require.Equal(t, int32(1), baseOp.execMetaRuns.Load())
	require.Equal(t, int32(0), baseOp.fsRuns.Load())

	require.NoError(t, cache.EvaluateParts(ctx, childRes, ContainerPartMount("/parent-mount")))
	mount := child.mountAt("/parent-mount")
	require.NotNil(t, mount)
	mountedDir, ok := mount.DirectorySource.Peek()
	require.True(t, ok)
	mountedSnapshot, ok := mountedDir.Snapshot.Peek()
	require.True(t, ok)
	require.Same(t, mountChild, mountedSnapshot)
	require.Equal(t, 1, baseOp.mountRunsFor("/parent-mount"))
	require.Equal(t, int32(0), baseOp.fsRuns.Load())
	_, fsSet := child.FS.Peek()
	require.False(t, fsSet)
	require.True(t, dagql.HasPendingLazyEvaluation(childRes))
}
