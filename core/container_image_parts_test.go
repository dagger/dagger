package core

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

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
