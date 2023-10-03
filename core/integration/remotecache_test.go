package core

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/moby/buildkit/identity"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func getDevEngineForRemoteCache(ctx context.Context, c *dagger.Client, cache *dagger.Service, cacheName, cacheEnv string, index uint8) (*dagger.Service, string, error) {
	id := identity.NewID()
	networkCIDR := fmt.Sprintf("10.%d.0.0/16", 100+index)
	devEngineSvc := devEngineContainer(c).
		WithServiceBinding(cacheName, cache).
		WithExposedPort(1234, dagger.ContainerWithExposedPortOpts{Protocol: dagger.Tcp}).
		WithEnvVariable("ENGINE_ID", id).
		WithMountedCache("/var/lib/dagger", c.CacheVolume("dagger-dev-engine-state-"+identity.NewID())).
		WithExec([]string{
			"--network-name", fmt.Sprintf("remotecache%d", index),
			"--network-cidr", networkCIDR,
		}, dagger.ContainerWithExecOpts{
			InsecureRootCapabilities: true,
		}).
		Service()

	endpoint, err := devEngineSvc.Endpoint(ctx, dagger.ServiceEndpointOpts{
		Port:   1234,
		Scheme: "tcp",
	})

	return devEngineSvc, endpoint, err
}

func TestRemoteCacheRegistry(t *testing.T) {
	c, ctx := connect(t)

	registry := c.Pipeline("registry").Container().From("registry:2").
		WithMountedCache("/var/lib/registry/", c.CacheVolume("remote-cache-registry-"+identity.NewID())).
		WithExposedPort(5000, dagger.ContainerWithExposedPortOpts{Protocol: dagger.Tcp}).
		Service()

	cacheEnv := "type=registry,ref=registry:5000/test-cache,mode=max"

	devEngineA, endpointA, err := getDevEngineForRemoteCache(ctx, c, registry, "registry", cacheEnv, 0)
	require.NoError(t, err)

	// This loads the dagger-cli binary from the host into the container, that was set up by
	// internal/mage/engine.go:test. This is used to communicate with the dev engine.
	daggerCli := daggerCliFile(t, c)

	cliBinPath := "/.dagger-cli"

	outputA, err := c.Container().From(alpineImage).
		WithServiceBinding("dev-engine", devEngineA).
		WithMountedFile(cliBinPath, daggerCli).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliBinPath).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpointA).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CACHE_CONFIG", cacheEnv).
		WithNewFile("/.dagger-query.txt", dagger.ContainerWithNewFileOpts{
			Contents: `{
				container {
					from(address: "` + alpineImage + `") {
						withExec(args: ["sh", "-c", "head -c 128 /dev/random | sha256sum"]) {
							stdout
						}
					}
				}
			}`,
		}).
		WithExec([]string{
			"sh", "-c", cliBinPath + ` query --doc .dagger-query.txt`,
		}).Stdout(ctx)
	require.NoError(t, err)
	shaA := strings.TrimSpace(gjson.Get(outputA, "container.from.withExec.stdout").String())
	require.NotEmpty(t, shaA, "shaA is empty")

	devEngineB, endpointB, err := getDevEngineForRemoteCache(ctx, c, registry, "registry", cacheEnv, 1)
	require.NoError(t, err)

	outputB, err := c.Container().From(alpineImage).
		WithServiceBinding("dev-engine", devEngineB).
		WithMountedFile(cliBinPath, daggerCli).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliBinPath).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpointB).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CACHE_CONFIG", cacheEnv).
		WithNewFile("/.dagger-query.txt", dagger.ContainerWithNewFileOpts{
			Contents: `{
				container {
					from(address: "` + alpineImage + `") {
						withExec(args: ["sh", "-c", "head -c 128 /dev/random | sha256sum"]) {
							stdout
						}
					}
				}
			}`,
		}).
		WithExec([]string{
			"sh", "-c", cliBinPath + " query --doc .dagger-query.txt",
		}).Stdout(ctx)
	require.NoError(t, err)
	shaB := strings.TrimSpace(gjson.Get(outputB, "container.from.withExec.stdout").String())
	require.NotEmpty(t, shaB, "shaB is empty")

	require.Equal(t, shaA, shaB)
}

func TestRemoteCacheS3(t *testing.T) {
	t.Run("buildkit s3 caching", func(t *testing.T) {
		c, ctx := connect(t)

		bucket := "dagger-test-remote-cache-s3-" + identity.NewID()

		s3 := c.Pipeline("s3").Container().From("minio/minio").
			WithMountedCache("/data", c.CacheVolume("minio-cache")).
			WithExposedPort(9000, dagger.ContainerWithExposedPortOpts{Protocol: dagger.Tcp}).
			WithExec([]string{"server", "/data"}).
			Service()

		s3Endpoint, err := s3.Endpoint(ctx, dagger.ServiceEndpointOpts{Port: 9000, Scheme: "http"})
		require.NoError(t, err)

		minioStdout, err := c.Container().From("minio/mc").
			WithServiceBinding("s3", s3).
			WithEntrypoint([]string{"sh"}).
			WithExec([]string{"-c", "mc alias set minio http://s3:9000 minioadmin minioadmin && mc mb minio/" + bucket}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, minioStdout, "Bucket created successfully")

		s3Env := "type=s3,mode=max,endpoint_url=" + s3Endpoint + ",access_key_id=minioadmin,secret_access_key=minioadmin,region=mars,use_path_style=true,bucket=" + bucket

		devEngineA, endpointA, err := getDevEngineForRemoteCache(ctx, c, s3, "s3", s3Env, 0)
		require.NoError(t, err)

		cliBinPath := "/.dagger-cli"
		// This loads the dagger-cli binary from the host into the container, that was set up by
		// internal/mage/engine.go:test. This is used to communicate with the dev engine.
		daggerCli := c.Host().Directory("/dagger-dev/", dagger.HostDirectoryOpts{Include: []string{"dagger"}}).File("dagger")

		outputA, err := c.Container().From(alpineImage).
			WithServiceBinding("dev-engine", devEngineA).
			WithMountedFile(cliBinPath, daggerCli).
			WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliBinPath).
			WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpointA).
			WithEnvVariable("_EXPERIMENTAL_DAGGER_CACHE_CONFIG", s3Env).
			WithNewFile("/.dagger-query.txt", dagger.ContainerWithNewFileOpts{
				Contents: `{
						container {
							from(address: "` + alpineImage + `") {
								withExec(args: ["sh", "-c", "head -c 128 /dev/random | sha256sum"]) {
									stdout
								}
							}
						}
					}`,
			}).
			WithExec([]string{
				"sh", "-c", cliBinPath + ` query --doc .dagger-query.txt`,
			}).Stdout(ctx)
		require.NoError(t, err)
		shaA := strings.TrimSpace(gjson.Get(outputA, "container.from.withExec.stdout").String())
		require.NotEmpty(t, shaA, "shaA is empty")

		devEngineB, endpointB, err := getDevEngineForRemoteCache(ctx, c, s3, "s3", s3Env, 1)
		require.NoError(t, err)

		outputB, err := c.Container().From(alpineImage).
			WithServiceBinding("dev-engine", devEngineB).
			WithMountedFile(cliBinPath, daggerCli).
			WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliBinPath).
			WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpointB).
			WithEnvVariable("_EXPERIMENTAL_DAGGER_CACHE_CONFIG", s3Env).
			WithNewFile("/.dagger-query.txt", dagger.ContainerWithNewFileOpts{
				Contents: `{
						container {
							from(address: "` + alpineImage + `") {
								withExec(args: ["sh", "-c", "head -c 128 /dev/random | sha256sum"]) {
									stdout
								}
							}
						}
					}`,
			}).
			WithExec([]string{
				"sh", "-c", cliBinPath + " query --doc .dagger-query.txt",
			}).Stdout(ctx)
		require.NoError(t, err)
		shaB := strings.TrimSpace(gjson.Get(outputB, "container.from.withExec.stdout").String())
		require.NotEmpty(t, shaB, "shaB is empty")

		require.Equal(t, shaA, shaB)
	})
}
