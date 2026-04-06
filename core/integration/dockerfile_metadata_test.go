package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/config"
	"github.com/dagger/testctx"
	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

func (DockerfileSuite) TestDockerBuildContainerMetadata(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := c.Directory().WithNewFile("Dockerfile", `FROM `+alpineImage+`
WORKDIR /final
USER root
ENV RESULT=success
LABEL com.example.suite=dockerfile
EXPOSE 8090
ENTRYPOINT ["cat"]
CMD ["/final/out.txt"]
RUN echo ok >/final/out.txt
`).DockerBuild()

	workdir, err := ctr.Workdir(ctx)
	require.NoError(t, err)
	require.Equal(t, "/final", workdir)

	user, err := ctr.User(ctx)
	require.NoError(t, err)
	require.Equal(t, "root", user)

	env, err := ctr.EnvVariable(ctx, "RESULT")
	require.NoError(t, err)
	require.Equal(t, "success", env)

	label, err := ctr.Label(ctx, "com.example.suite")
	require.NoError(t, err)
	require.Equal(t, "dockerfile", label)

	entrypoint, err := ctr.Entrypoint(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"cat"}, entrypoint)

	defaultArgs, err := ctr.DefaultArgs(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"/final/out.txt"}, defaultArgs)

	ports, err := ctr.ExposedPorts(ctx)
	require.NoError(t, err)
	portSet := map[string]bool{}
	for _, p := range ports {
		n, err := p.Port(ctx)
		require.NoError(t, err)
		proto, err := p.Protocol(ctx)
		require.NoError(t, err)
		portSet[fmt.Sprintf("%d/%s", n, proto)] = true
	}
	require.True(t, portSet["8090/TCP"])
}

func (DockerfileSuite) TestDockerBuildExportConfig(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := c.Directory().WithNewFile("Dockerfile", `FROM `+alpineImage+`
RUN echo hi > /out.txt
HEALTHCHECK --interval=21s --timeout=4s --start-period=9s --start-interval=2s --retries=5 CMD ["sh","-c","test -f /out.txt"]
ONBUILD RUN echo child-build
SHELL ["/bin/ash","-eo","pipefail","-c"]
VOLUME ["/cache","/data"]
STOPSIGNAL SIGQUIT
CMD ["cat", "/out.txt"]
`).DockerBuild()

	out, err := ctr.WithExec(nil).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "hi", strings.TrimSpace(out))

	imagePath := filepath.Join(t.TempDir(), "dockerfile-export-config.tar")
	actualPath, err := ctr.Export(ctx, imagePath)
	require.NoError(t, err)
	require.Equal(t, imagePath, actualPath)

	dockerManifestBytes := readTarFile(t, imagePath, "manifest.json")
	var dockerManifest []struct {
		Config string
	}
	require.NoError(t, json.Unmarshal(dockerManifestBytes, &dockerManifest))
	require.Len(t, dockerManifest, 1)

	configBytes := readTarFile(t, imagePath, dockerManifest[0].Config)
	var gotImage dockerspec.DockerOCIImage
	require.NoError(t, json.Unmarshal(configBytes, &gotImage))

	require.Equal(t, []string{"CMD", "sh", "-c", "test -f /out.txt"}, gotImage.Config.Healthcheck.Test)
	require.Equal(t, 21*time.Second, gotImage.Config.Healthcheck.Interval)
	require.Equal(t, 4*time.Second, gotImage.Config.Healthcheck.Timeout)
	require.Equal(t, 9*time.Second, gotImage.Config.Healthcheck.StartPeriod)
	require.Equal(t, 2*time.Second, gotImage.Config.Healthcheck.StartInterval)
	require.Equal(t, 5, gotImage.Config.Healthcheck.Retries)
	require.Equal(t, []string{"RUN echo child-build"}, gotImage.Config.OnBuild)
	require.Equal(t, []string{"/bin/ash", "-eo", "pipefail", "-c"}, gotImage.Config.Shell)
	require.Equal(t, map[string]struct{}{"/cache": {}, "/data": {}}, gotImage.Config.Volumes)
	require.Equal(t, "SIGQUIT", gotImage.Config.StopSignal)
}

func (DockerfileSuite) TestDockerBuildSecurityPolicy(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	f := false
	engine := devEngineContainer(c, engineWithConfig(ctx, t, func(ctx context.Context, t *testctx.T, cfg config.Config) config.Config {
		cfg.Security = &config.Security{
			InsecureRootCapabilities: &f,
		}
		return cfg
	}))
	engineSvc, err := c.Host().Tunnel(devEngineContainerAsService(engine)).Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = engineSvc.Stop(ctx)
	})

	endpoint, err := engineSvc.Endpoint(ctx, dagger.ServiceEndpointOpts{Scheme: "tcp"})
	require.NoError(t, err)

	c2, err := dagger.Connect(
		ctx,
		dagger.WithRunnerHost(endpoint),
		dagger.WithLogOutput(io.Discard),
		dagger.WithSkipWorkspaceModules(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c2.Close() })

	_, err = c2.Directory().WithNewFile("Dockerfile", `FROM `+alpineImage+`
RUN --network=host sh -c 'echo denied > /status'
CMD ["cat", "/status"]
`).DockerBuild().Sync(ctx)
	require.Error(t, err)
	require.ErrorContains(t, err, "network.host is not allowed")
}
