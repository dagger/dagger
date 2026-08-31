package schema

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/distribution/reference"
	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

func TestShouldSelectLatestImageRelease(t *testing.T) {
	t.Parallel()

	bare, err := reference.ParseNormalizedNamed("alpine")
	require.NoError(t, err)
	explicitLatest, err := reference.ParseNormalizedNamed("alpine:latest")
	require.NoError(t, err)

	for _, tc := range []struct {
		name    string
		version string
		ref     reference.Named
		want    bool
	}{
		{name: "legacy bare address", version: "v1.0.0-beta.9", ref: bare},
		{name: "locking without latest release", version: workspaceLockingVersion, ref: bare},
		{name: "new bare address", version: workspace.LatestReleaseVersion, ref: bare, want: true},
		{name: "explicit latest", version: workspace.LatestReleaseVersion, ref: explicitLatest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := dagql.ContextWithCall(t.Context(), &dagql.ResultCall{
				View: call.View(tc.version),
			})
			require.Equal(t, tc.want, shouldSelectLatestImageRelease(
				ctx,
				tc.ref,
			))
		})
	}
}

func TestContainerDefaultPlatformCacheIdentity(t *testing.T) {
	ctx := context.Background()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, cache.Close(context.Background()))
	})
	ctx = dagql.ContextWithCache(ctx, cache)
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
		ClientID:  "container-platform-client",
		SessionID: "container-platform-session",
	})

	amd64 := core.Platform{OS: "linux", Architecture: "amd64"}
	arm64 := core.Platform{OS: "linux", Architecture: "arm64"}
	srv := &currentTypeDefsTestServer{platform: amd64}
	root := core.NewRoot(srv)
	coreSchemaBase, err := NewCoreSchemaBase(ctx, srv)
	require.NoError(t, err)
	dag, err := coreSchemaBase.Fork(ctx, root, "")
	require.NoError(t, err)
	srv.dag = dag

	selectContainer := func(platform dagql.Input) dagql.ObjectResult[*core.Container] {
		t.Helper()
		selector := dagql.Selector{Field: "container"}
		if platform != nil {
			selector.Args = []dagql.NamedInput{{Name: "platform", Value: platform}}
		}
		var ctr dagql.ObjectResult[*core.Container]
		require.NoError(t, dag.Select(ctx, dag.Root(), &ctr, selector))
		return ctr
	}
	recipeDigest := func(ctr dagql.ObjectResult[*core.Container]) string {
		t.Helper()
		id, err := ctr.RecipeID(ctx)
		require.NoError(t, err)
		return id.Digest().String()
	}

	omittedAMD64 := selectContainer(nil)
	require.Equal(t, amd64, omittedAMD64.Self().Platform)

	explicitAMD64 := selectContainer(dagql.Opt(amd64))
	require.Equal(t, recipeDigest(omittedAMD64), recipeDigest(explicitAMD64))

	srv.platform = arm64
	omittedARM64 := selectContainer(nil)
	require.Equal(t, arm64, omittedARM64.Self().Platform)
	require.NotEqual(t, recipeDigest(omittedAMD64), recipeDigest(omittedARM64))

	explicitAMD64OnARM64 := selectContainer(dagql.Opt(amd64))
	require.Equal(t, amd64, explicitAMD64OnARM64.Self().Platform)
	require.Equal(t, recipeDigest(explicitAMD64), recipeDigest(explicitAMD64OnARM64))

	nullOnARM64 := selectContainer(dagql.NoOpt[core.Platform]())
	require.Equal(t, arm64, nullOnARM64.Self().Platform)
	require.Equal(t, recipeDigest(omittedARM64), recipeDigest(nullOnARM64))
}

func TestCloneContainerForSchemaChildDisablesFromContentDigest(t *testing.T) {
	t.Parallel()

	dag, err := dagql.NewServer(t.Context(), &core.Query{})
	require.NoError(t, err)
	dag.InstallObject(dagql.NewClass(dag, dagql.ClassOpts[*core.Container]{Typed: &core.Container{}}))

	parent, err := dagql.NewObjectResultForCall(core.NewContainer(core.Platform{}), dag, &dagql.ResultCall{
		Kind:        dagql.ResultCallKindSynthetic,
		SyntheticOp: "scratch-container",
		Type:        dagql.NewResultCallType((&core.Container{}).Type()),
	})
	require.NoError(t, err)
	require.True(t, parent.Self().CanUseFromContentDigest())

	child, _, err := cloneContainerForSchemaChild(t.Context(), parent)
	require.NoError(t, err)
	require.False(t, child.CanUseFromContentDigest())
}

func TestWithImageConfigMetadataMutatesContainerConfig(t *testing.T) {
	t.Parallel()

	s := &containerSchema{}
	ctx := context.Background()

	healthcheck := dockerspec.HealthcheckConfig{
		Test:          []string{"CMD-SHELL", "test -f /out.txt"},
		Interval:      21 * time.Second,
		Timeout:       4 * time.Second,
		StartPeriod:   9 * time.Second,
		StartInterval: 2 * time.Second,
		Retries:       5,
	}
	healthcheckJSON, err := json.Marshal(&healthcheck)
	require.NoError(t, err)

	parent := &core.Container{
		Config: dockerspec.DockerOCIImageConfig{
			ImageConfig: ocispecs.ImageConfig{
				Volumes: map[string]struct{}{"/old": {}},
			},
			DockerOCIImageConfigExt: dockerspec.DockerOCIImageConfigExt{
				OnBuild: []string{"RUN old"},
				Shell:   []string{"/bin/sh", "-c"},
			},
		},
	}

	updated, err := s.withImageConfigMetadata(ctx, parent, containerWithImageConfigMetadataArgs{
		Healthcheck: string(healthcheckJSON),
		OnBuild: dagql.Opt(
			dagql.ArrayInput[dagql.String]{
				dagql.NewString("RUN child-build"),
			},
		),
		Shell: dagql.Opt(
			dagql.ArrayInput[dagql.String]{
				dagql.NewString("/bin/ash"),
				dagql.NewString("-eo"),
				dagql.NewString("pipefail"),
				dagql.NewString("-c"),
			},
		),
		Volumes: dagql.Opt(
			dagql.ArrayInput[dagql.String]{
				dagql.NewString("/cache"),
				dagql.NewString("/data"),
				dagql.NewString("/cache"),
			},
		),
		StopSignal: "SIGQUIT",
	})
	require.NoError(t, err)
	require.NotNil(t, updated)
	require.Same(t, parent, updated)

	require.NotNil(t, parent.Config.Healthcheck)
	require.Equal(t, healthcheck, *parent.Config.Healthcheck)
	require.Equal(t, []string{"RUN child-build"}, parent.Config.OnBuild)
	require.Equal(t, []string{"/bin/ash", "-eo", "pipefail", "-c"}, parent.Config.Shell)
	require.Equal(t, map[string]struct{}{"/cache": {}, "/data": {}}, parent.Config.Volumes)
	require.Equal(t, "SIGQUIT", parent.Config.StopSignal)
}

func TestWithImageConfigMetadataRejectsInvalidHealthcheckJSON(t *testing.T) {
	t.Parallel()

	s := &containerSchema{}
	ctx := context.Background()

	_, err := s.withImageConfigMetadata(ctx, &core.Container{}, containerWithImageConfigMetadataArgs{
		Healthcheck: "{this is not json}",
	})
	require.Error(t, err)
	require.ErrorContains(t, err, "failed to decode healthcheck metadata")
}
