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

func getDevEngine(ctx context.Context, c *dagger.Client, cache *dagger.Container, cacheName, cacheEnv string, index uint8) (devEngine *dagger.Container, endpoint string, err error) {
	id := identity.NewID()
	networkCIDR := fmt.Sprintf("10.%d.0.0/16", 100+index)
	// This loads the engine.tar file from the host into the container, that was set up by
	// internal/mage/engine.go:test. This is used to spin up additional dev engines.
	devEngineTar := c.Host().Directory("/dagger-dev/", dagger.HostDirectoryOpts{Include: []string{"engine.tar"}}).File("engine.tar")

	devEngine = c.Container().Import(devEngineTar)
	entrypoint, err := devEngine.File("/usr/local/bin/dagger-entrypoint.sh").Contents(ctx)
	if err != nil {
		return nil, "", err
	}
	entrypoint = strings.ReplaceAll(entrypoint, "10.88.0.0/16", networkCIDR)
	entrypoint = strings.ReplaceAll(entrypoint, "dagger-dev", fmt.Sprintf("remote-cache-%d", index))

	devEngine = devEngine.WithNewFile("/usr/local/bin/dagger-entrypoint.sh", dagger.ContainerWithNewFileOpts{
		Contents: entrypoint,
	})

	devEngine = devEngine.
		WithServiceBinding(cacheName, cache).
		WithExposedPort(1234, dagger.ContainerWithExposedPortOpts{Protocol: dagger.Tcp}).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CACHE_CONFIG", cacheEnv).
		WithEnvVariable("ENGINE_ID", id).
		WithMountedCache("/var/lib/dagger", c.CacheVolume("dagger-dev-engine-state-"+id)).
		WithExec(nil, dagger.ContainerWithExecOpts{
			InsecureRootCapabilities:      true,
			ExperimentalPrivilegedNesting: true,
		})

	endpoint, err = devEngine.Endpoint(ctx, dagger.ContainerEndpointOpts{Port: 1234, Scheme: "tcp"})

	return devEngine, endpoint, err
}

func TestRemoteCacheRegistry(t *testing.T) {
	c, ctx := connect(t)
	defer c.Close()

	registry := c.Pipeline("registry").Container().From("registry:2").
		WithExposedPort(5000, dagger.ContainerWithExposedPortOpts{Protocol: dagger.Tcp}).
		WithExec(nil)

	devEngine, endpoint, err := getDevEngine(ctx, c, registry, "registry", "type=registry,ref=registry:5000/test-cache,mode=max", 0)
	require.NoError(t, err)

	// This loads the dagger-cli binary from the host into the container, that was set up by
	// internal/mage/engine.go:test. This is used to communicate with the dev engine.
	daggerCli := c.Host().Directory("/dagger-dev/", dagger.HostDirectoryOpts{Include: []string{"dagger"}}).File("dagger")

	cliBinPath := "/.dagger-cli"

	outputA, err := c.Container().From("alpine:3.17").
		WithServiceBinding("dev-engine", devEngine).
		WithMountedFile(cliBinPath, daggerCli).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliBinPath).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpoint).
		WithNewFile("/.dagger-query.txt", dagger.ContainerWithNewFileOpts{
			Contents: `{ 
				container { 
					from(address: "alpine:3.17") { 
						withExec(args: ["sh", "-c", "head -c 128 /dev/random | sha256sum"]) { 
							stdout 
						} 
					} 
				} 
			}`}).
		WithExec([]string{
			"sh", "-c", cliBinPath + ` query --doc .dagger-query.txt`,
		}).Stdout(ctx)
	require.NoError(t, err)
	shaA := strings.TrimSpace(gjson.Get(outputA, "container.from.exec.stdout").String())

	devEngine, endpoint, err = getDevEngine(ctx, c, registry, "registry", "type=registry,ref=registry:5000/test-cache,mode=max", 1)
	require.NoError(t, err)

	outputB, err := c.Container().From("alpine:3.17").
		WithServiceBinding("dev-engine", devEngine).
		WithMountedFile(cliBinPath, daggerCli).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliBinPath).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpoint).
		WithNewFile("/.dagger-query.txt", dagger.ContainerWithNewFileOpts{
			Contents: `{ 
				container { 
					from(address: "alpine:3.17") { 
						withExec(args: ["sh", "-c", "head -c 128 /dev/random | sha256sum"]) { 
							stdout 
						} 
					} 
				} 
			}`}).
		WithExec([]string{
			"sh", "-c", cliBinPath + " query --doc .dagger-query.txt",
		}).Stdout(ctx)
	require.NoError(t, err)
	shaB := strings.TrimSpace(gjson.Get(outputB, "container.from.exec.stdout").String())

	require.Equal(t, shaA, shaB)
}

func TestRemoteCacheS3(t *testing.T) {
	t.Run("buildkit s3 caching", func(t *testing.T) {
		c, ctx := connect(t)
		defer c.Close()

		bucket := "dagger-test-remote-cache-s3-" + identity.NewID()

		s3 := c.Pipeline("s3").Container().From("minio/minio").
			WithMountedCache("/data", c.CacheVolume("minio-cache")).
			WithExposedPort(9000, dagger.ContainerWithExposedPortOpts{Protocol: dagger.Tcp}).
			WithExec([]string{"server", "/data"})

		s3Endpoint, err := s3.Endpoint(ctx, dagger.ContainerEndpointOpts{Port: 9000, Scheme: "http"})
		require.NoError(t, err)

		minioStdout, err := c.Container().From("minio/mc").
			WithServiceBinding("s3", s3).
			WithEntrypoint([]string{"sh"}).
			WithExec([]string{"-c", "mc alias set minio http://s3:9000 minioadmin minioadmin && mc mb minio/" + bucket}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, minioStdout, "Bucket created successfully")

		s3Env := "type=s3,mode=max,endpoint_url=" + s3Endpoint + ",access_key_id=minioadmin,secret_access_key=minioadmin,region=mars,use_path_style=true,bucket=" + bucket

		devEngine, endpoint, err := getDevEngine(ctx, c, s3, "s3", s3Env, 0)
		require.NoError(t, err)

		cliBinPath := "/.dagger-cli"
		// This loads the dagger-cli binary from the host into the container, that was set up by
		// internal/mage/engine.go:test. This is used to communicate with the dev engine.
		daggerCli := c.Host().Directory("/dagger-dev/", dagger.HostDirectoryOpts{Include: []string{"dagger"}}).File("dagger")

		outputA, err := c.Container().From("alpine:3.17").
			WithServiceBinding("dev-engine", devEngine).
			WithMountedFile(cliBinPath, daggerCli).
			WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliBinPath).
			WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpoint).
			WithNewFile("/.dagger-query.txt", dagger.ContainerWithNewFileOpts{
				Contents: `{ 
						container { 
							from(address: "alpine:3.17") { 
								withExec(args: ["sh", "-c", "head -c 128 /dev/random | sha256sum"]) { 
									stdout 
								} 
							} 
						} 
					}`}).
			WithExec([]string{
				"sh", "-c", cliBinPath + ` query --doc .dagger-query.txt`,
			}).Stdout(ctx)
		require.NoError(t, err)
		shaA := strings.TrimSpace(gjson.Get(outputA, "container.from.exec.stdout").String())

		devEngine, endpoint, err = getDevEngine(ctx, c, s3, "s3", s3Env, 1)
		require.NoError(t, err)

		outputB, err := c.Container().From("alpine:3.17").
			WithServiceBinding("dev-engine", devEngine).
			WithMountedFile(cliBinPath, daggerCli).
			WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliBinPath).
			WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpoint).
			WithNewFile("/.dagger-query.txt", dagger.ContainerWithNewFileOpts{
				Contents: `{ 
						container { 
							from(address: "alpine:3.17") { 
								withExec(args: ["sh", "-c", "head -c 128 /dev/random | sha256sum"]) { 
									stdout 
								} 
							} 
						} 
					}`}).
			WithExec([]string{
				"sh", "-c", cliBinPath + " query --doc .dagger-query.txt",
			}).Stdout(ctx)
		require.NoError(t, err)
		shaB := strings.TrimSpace(gjson.Get(outputB, "container.from.exec.stdout").String())

		require.Equal(t, shaA, shaB)
	})
}
