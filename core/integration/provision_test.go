package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/testctx"
	"github.com/moby/buildkit/identity"
	"github.com/stretchr/testify/require"
)

func dockerService(t *testctx.T, dag *dagger.Client, dockerVersion string, f func(*dagger.Container) *dagger.Container) *dagger.Service {
	tag := "dind"
	if dockerVersion != "" {
		tag = dockerVersion + "-" + tag
	}

	if f == nil {
		f = func(ctr *dagger.Container) *dagger.Container {
			return ctr
		}
	}

	port := 4000
	dockerd := dag.Container().From("docker:"+tag).
		With(f).
		WithMountedCache("/var/lib/docker", dag.CacheVolume(t.Name()+"-"+dockerVersion+"-docker-lib"), dagger.ContainerWithMountedCacheOpts{
			Sharing: dagger.CacheSharingModePrivate,
		}).
		WithExposedPort(port).
		WithExec([]string{
			"dockerd",
			"--host=tcp://0.0.0.0:" + strconv.Itoa(port),
			"--tls=false",
		}, dagger.ContainerWithExecOpts{
			InsecureRootCapabilities: true,
		}).
		AsService()

	return dockerd
}

func dockerClient(ctx context.Context, t *testctx.T, dag *dagger.Client, dockerd *dagger.Service, dockerVersion string, f func(*dagger.Container) *dagger.Container) *dagger.Container {
	tag := "cli"
	if dockerVersion != "" {
		tag = dockerVersion + "-" + tag
	}

	if f == nil {
		f = func(ctr *dagger.Container) *dagger.Container {
			return ctr
		}
	}

	dockerHost, err := dockerd.Endpoint(ctx, dagger.ServiceEndpointOpts{
		Scheme: "tcp",
	})
	require.NoError(t, err)

	client := dag.Container().From("docker:"+tag).
		With(f).
		With(mountDockerConfig(dag)).
		WithServiceBinding("docker", dockerd).
		WithEnvVariable("DOCKER_HOST", dockerHost).
		WithEnvVariable("CACHEBUSTER", identity.NewID())

	return client
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
