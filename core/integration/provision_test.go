package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/testctx"
	"github.com/moby/buildkit/identity"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"golang.org/x/mod/semver"
)

type ProvisionSuite struct{}

func TestProvision(t *testing.T) {
	testctx.Run(testCtx, t, ProvisionSuite{}, Middleware()...)
}

func (ProvisionSuite) TestDockerDriver(ctx context.Context, t *testctx.T) {
	t.Run("default image", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		dockerc := dockerSetup(ctx, t, "provisioner", c, "", nil)
		dockerc = dockerc.WithMountedFile("/bin/dagger", daggerCliFile(t, c))

		out, err := dockerc.
			WithExec([]string{"dagger", "query"}, dagger.ContainerWithExecOpts{Stdin: "{version}"}).Stdout(ctx)
		require.NoError(t, err)

		version := gjson.Get(out, "version").String()
		require.True(t, semver.IsValid(version))
	})

	t.Run("specified image", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		dockerc := dockerSetup(ctx, t, "provisioner", c, "", nil)
		dockerc = dockerc.WithMountedFile("/bin/dagger", daggerCliFile(t, c))

		version := "v0.14.0"
		out, err := dockerc.
			WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", "docker-image://registry.dagger.io/engine:"+version).
			WithExec([]string{"dagger", "query"}, dagger.ContainerWithExecOpts{Stdin: "{version}"}).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"version":"`+version+`"}`, out)
	})

	t.Run("current image", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		dockerc := dockerSetup(ctx, t, "provisioner", c, "", nil)
		dockerc = dockerc.WithMountedFile("/bin/dagger", daggerCliFile(t, c))
		dockerc, err := dockerLoadEngine(ctx, c, dockerc, "registry.dagger.io/engine:dev")
		require.NoError(t, err)

		out, err := dockerc.
			WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", "docker-image://registry.dagger.io/engine:dev").
			WithExec([]string{"dagger", "query"}, dagger.ContainerWithExecOpts{Stdin: "{version}"}).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"version":"`+engine.Version+`"}`, out)
	})
}

func (ProvisionSuite) TestDockerDriverConfig(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	configContents := `{"gc":{"reservedSpace": 1000, "maxUsedSpace": 2000, "minFreeSpace": 3000}}`
	middleware := func(ctr *dagger.Container) *dagger.Container {
		// this mounts the file into both the client+server containers
		return ctr.WithNewFile("/root/.config/dagger/engine.json", configContents)
	}

	dockerc := dockerSetup(ctx, t, "provisioner", c, "", middleware)
	dockerc, err := dockerLoadEngine(ctx, c, dockerc, "registry.dagger.io/engine:dev")
	require.NoError(t, err)
	dockerc = dockerc.
		WithMountedFile("/bin/dagger", daggerCliFile(t, c)).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", "docker-image://registry.dagger.io/engine:dev")

	// check that the config was used by the engine
	out, err := dockerc.WithExec([]string{"dagger", "query"}, dagger.ContainerWithExecOpts{Stdin: "{engine{localCache{reservedSpace,maxUsedSpace,minFreeSpace}}}"}).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"engine": {"localCache": {"reservedSpace": 1000, "maxUsedSpace": 2000, "minFreeSpace": 3000}}}`, out)

	// also, just for good measure, check that the file was propagated to the right place
	ctrid, err := dockerc.WithExec([]string{"docker", "ps", "-n1", "--format={{.ID}}"}).Stdout(ctx)
	require.NoError(t, err)
	ctrid = strings.TrimSpace(ctrid)
	require.NotEmpty(t, ctrid)
	result, err := dockerc.WithExec([]string{"docker", "exec", ctrid, "cat", "/etc/dagger/engine.json"}).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, configContents, result)
}

func (ProvisionSuite) TestDockerDriverGarbageCollectEngines(ctx context.Context, t *testctx.T) {
	dockerPs := func(ctx context.Context, t *testctx.T, dockerc *dagger.Container) []string {
		out, err := dockerc.
			WithEnvVariable("CACHEBUSTER", identity.NewID()).
			WithExec([]string{"docker", "ps", "-q"}).
			Stdout(ctx)
		require.NoError(t, err)
		out = strings.TrimSpace(out)
		if out == "" {
			return []string{}
		}
		return strings.Split(out, "\n")
	}

	t.Run("cleanup", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		dockerc := dockerSetup(ctx, t, "provisioner", c, "", nil)
		dockerc = dockerc.WithMountedFile("/bin/dagger", daggerCliFile(t, c))

		require.Len(t, dockerPs(ctx, t, dockerc), 0)

		version := "v0.13.0"
		out, err := dockerc.
			WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", "docker-image://registry.dagger.io/engine:"+version).
			WithExec([]string{"dagger", "query"}, dagger.ContainerWithExecOpts{Stdin: "{version}"}).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"version":"`+version+`"}`, out)

		require.Len(t, dockerPs(ctx, t, dockerc), 1)

		version = "v0.14.0"
		out, err = dockerc.
			WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", "docker-image://registry.dagger.io/engine:"+version).
			WithExec([]string{"dagger", "query"}, dagger.ContainerWithExecOpts{Stdin: "{version}"}).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"version":"`+version+`"}`, out)

		require.Len(t, dockerPs(ctx, t, dockerc), 1)
	})

	t.Run("no cleanup", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		dockerc := dockerSetup(ctx, t, "provisioner", c, "", nil)
		dockerc = dockerc.WithMountedFile("/bin/dagger", daggerCliFile(t, c))
		dockerc = dockerc.WithEnvVariable("DAGGER_LEAVE_OLD_ENGINE", "true")

		require.Len(t, dockerPs(ctx, t, dockerc), 0)

		version := "v0.13.0"
		out, err := dockerc.
			WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", "docker-image://registry.dagger.io/engine:"+version).
			WithExec([]string{"dagger", "query"}, dagger.ContainerWithExecOpts{Stdin: "{version}"}).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"version":"`+version+`"}`, out)

		require.Len(t, dockerPs(ctx, t, dockerc), 1)

		version = "v0.14.0"
		out, err = dockerc.
			WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", "docker-image://registry.dagger.io/engine:"+version).
			WithExec([]string{"dagger", "query"}, dagger.ContainerWithExecOpts{Stdin: "{version}"}).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"version":"`+version+`"}`, out)

		require.Len(t, dockerPs(ctx, t, dockerc), 2)
	})
}

func dockerSetup(ctx context.Context, t *testctx.T, name string, dag *dagger.Client, dockerVersion string, f func(*dagger.Container) *dagger.Container) *dagger.Container {
	if f == nil {
		f = func(ctr *dagger.Container) *dagger.Container {
			return ctr
		}
	}

	dockerdTag := "dind"
	dockercTag := "cli"
	if dockerVersion != "" {
		dockerdTag = dockerVersion + "-" + dockerdTag
		dockercTag = dockerVersion + "-" + dockercTag
	}

	port := 4000
	dockerd := dag.Container().From("docker:"+dockerdTag).
		With(f).
		WithMountedCache("/var/lib/docker", dag.CacheVolume(name+"-"+dockerVersion+"-docker-lib"), dagger.ContainerWithMountedCacheOpts{
			Sharing: dagger.CacheSharingModePrivate,
		}).
		WithExposedPort(port).
		AsService(
			dagger.ContainerAsServiceOpts{
				Args: []string{
					"dockerd",
					"--host=tcp://0.0.0.0:" + strconv.Itoa(port),
					"--tls=false",
				},
				InsecureRootCapabilities: true,
			},
		)
	dockerd, err := dockerd.Start(ctx)
	require.NoError(t, err)

	dockerHost, err := dockerd.Endpoint(ctx, dagger.ServiceEndpointOpts{
		Scheme: "tcp",
	})
	require.NoError(t, err)

	dockerc := dag.Container().From("docker:"+dockercTag).
		With(f).
		With(mountDockerConfig(dag)).
		WithServiceBinding("docker", dockerd).
		WithEnvVariable("DOCKER_HOST", dockerHost).
		WithEnvVariable("CACHEBUSTER", identity.NewID())

	t.Cleanup(func() {
		_, err := dockerc.WithExec([]string{"sh", "-c", "docker rm -f $(docker ps -aq); docker system prune --force --all --volumes; true"}).Sync(ctx)
		require.NoError(t, err)

		_, err = dockerd.Stop(ctx)
		require.NoError(t, err)
	})

	return dockerc
}

func dockerLoadEngine(ctx context.Context, dag *dagger.Client, ctr *dagger.Container, engineTag string) (*dagger.Container, error) {
	var tarPath string
	if v, ok := os.LookupEnv("_DAGGER_TESTS_ENGINE_TAR"); ok {
		tarPath = v
	} else {
		tarPath = "./bin/engine.tar"
	}
	out, err := ctr.
		WithMountedFile("engine.tar", dag.Host().File(tarPath)).
		WithExec([]string{"docker", "image", "load", "-i", "engine.tar"}).
		Stdout(ctx)
	if err != nil {
		return nil, err
	}
	_, imageID, ok := strings.Cut(out, "Loaded image ID: sha256:")
	if !ok {
		_, imageID, ok = strings.Cut(out, "Loaded image: sha256:") // podman
		if !ok {
			return nil, fmt.Errorf("unexpected output from docker load: %s", out)
		}
	}
	imageID = strings.TrimSpace(imageID)
	_, err = ctr.WithExec([]string{"docker", "tag", imageID, engineTag}).Sync(ctx)
	if err != nil {
		return nil, err
	}

	return ctr, nil
}

// mountDockerConfig is a helper for mounting the host's docker config if it exists
func mountDockerConfig(dag *dagger.Client) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		home, err := os.UserHomeDir()
		if err != nil {
			return ctr
		}
		content, err := os.ReadFile(filepath.Join(home, ".docker/config.json"))
		if err != nil {
			return ctr
		}

		return ctr.WithMountedSecret(
			"/root/.docker/config.json",
			dag.SetSecret("docker-config-"+identity.NewID(), string(content)),
		)
	}
}
