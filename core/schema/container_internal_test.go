package schema

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

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
