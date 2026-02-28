package schema

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/dagger/dagger/core"
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
		OnBuild:     []string{"RUN child-build"},
		Shell:       []string{"/bin/ash", "-eo", "pipefail", "-c"},
		Volumes:     []string{"/cache", "/data", "/cache"},
		StopSignal:  "SIGQUIT",
	})
	require.NoError(t, err)
	require.NotNil(t, updated)
	require.NotSame(t, parent, updated)

	// Ensure mutation happened on the returned container and not the original.
	require.Nil(t, parent.Config.Healthcheck)
	require.Equal(t, []string{"RUN old"}, parent.Config.OnBuild)
	require.Equal(t, []string{"/bin/sh", "-c"}, parent.Config.Shell)
	require.Equal(t, map[string]struct{}{"/old": {}}, parent.Config.Volumes)
	require.Equal(t, "", parent.Config.StopSignal)

	require.NotNil(t, updated.Config.Healthcheck)
	require.Equal(t, healthcheck, *updated.Config.Healthcheck)
	require.Equal(t, []string{"RUN child-build"}, updated.Config.OnBuild)
	require.Equal(t, []string{"/bin/ash", "-eo", "pipefail", "-c"}, updated.Config.Shell)
	require.Equal(t, map[string]struct{}{"/cache": {}, "/data": {}}, updated.Config.Volumes)
	require.Equal(t, "SIGQUIT", updated.Config.StopSignal)
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
