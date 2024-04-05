package core

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/moby/buildkit/identity"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"dagger.io/dagger"
)

const cliBinPath = "/.dagger-cli"

func getDevEngineForRemoteCache(ctx context.Context, c *dagger.Client, cache *dagger.Service, cacheName string, index uint8) (devEngineSvc *dagger.Service, endpoint string, err error) {
	id := identity.NewID()
	networkCIDR := fmt.Sprintf("10.%d.0.0/16", 100+index)
	devEngineSvc = devEngineContainer(c).
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
		AsService()

	endpoint, err = devEngineSvc.Endpoint(ctx, dagger.ServiceEndpointOpts{
		Port:   1234,
		Scheme: "tcp",
	})

	return devEngineSvc, endpoint, err
}

func TestRemoteCacheRegistry(t *testing.T) {
	t.Parallel()
	c, ctx := connect(t)

	registry := c.Pipeline("registry").Container().From("registry:2").
		WithMountedCache("/var/lib/registry/", c.CacheVolume("remote-cache-registry-"+identity.NewID())).
		WithExposedPort(5000, dagger.ContainerWithExposedPortOpts{Protocol: dagger.Tcp}).
		AsService()

	cacheEnv := "type=registry,ref=registry:5000/test-cache,mode=max"

	devEngineA, endpointA, err := getDevEngineForRemoteCache(ctx, c, registry, "registry", 0)
	require.NoError(t, err)

	// This loads the dagger-cli binary from the host into the container, that was set up by
	// internal/mage/engine.go:test. This is used to communicate with the dev engine.
	daggerCli := daggerCliFile(t, c)

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

	devEngineB, endpointB, err := getDevEngineForRemoteCache(ctx, c, registry, "registry", 1)
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

/*
	Regression test for https://github.com/dagger/dagger/pull/5885

Idea is to:
1. Load in a local dir, use it to force evaluation
2. Export remote cache for the load
3. Load exact same local dir in a new engine that imports the cache
4. Make sure that works and there's no errors about lazy blobs missing
The linked PR description above has more details.
*/
func TestRemoteCacheLazyBlobs(t *testing.T) {
	t.Parallel()
	c, ctx := connect(t)

	registry := c.Pipeline("registry").Container().From("registry:2").
		WithMountedCache("/var/lib/registry/", c.CacheVolume("remote-cache-registry-"+identity.NewID())).
		WithExposedPort(5000, dagger.ContainerWithExposedPortOpts{Protocol: dagger.Tcp}).
		AsService()

	cacheEnv := "type=registry,ref=registry:5000/test-cache,mode=max"

	devEngineA, endpointA, err := getDevEngineForRemoteCache(ctx, c, registry, "registry", 10)
	require.NoError(t, err)

	daggerCli := daggerCliFile(t, c)

	outputA, err := c.Container().From(alpineImage).
		WithDirectory("/foo", c.Directory().WithDirectory("bar", c.Directory().WithNewFile("baz", "blah")).WithTimestamps(0)).
		WithServiceBinding("dev-engine", devEngineA).
		WithMountedFile(cliBinPath, daggerCli).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliBinPath).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpointA).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CACHE_CONFIG", cacheEnv).
		WithNewFile("/.dagger-query.txt", dagger.ContainerWithNewFileOpts{
			Contents: `{
				host {
					directory(path: "/foo/bar") {
						entries
					}
				}
			}`,
		}).
		WithExec([]string{
			"sh", "-c", cliBinPath + ` query --doc .dagger-query.txt`,
		}).Stdout(ctx)
	require.NoErrorf(t, err, "outputA: %s", outputA)

	devEngineB, endpointB, err := getDevEngineForRemoteCache(ctx, c, registry, "registry", 11)
	require.NoError(t, err)

	outputB, err := c.Container().From(alpineImage).
		WithDirectory("/foo", c.Directory().WithDirectory("bar", c.Directory().WithNewFile("baz", "blah")).WithTimestamps(0)).
		WithServiceBinding("dev-engine", devEngineB).
		WithMountedFile(cliBinPath, daggerCli).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliBinPath).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpointB).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CACHE_CONFIG", cacheEnv).
		WithNewFile("/.dagger-query.txt", dagger.ContainerWithNewFileOpts{
			Contents: `{
				host {
					directory(path: "/foo/bar") {
						entries
					}
				}
			}`,
		}).
		WithExec([]string{
			"sh", "-c", cliBinPath + " query --doc .dagger-query.txt",
		}).Stdout(ctx)
	require.NoErrorf(t, err, "outputB: %s", outputB)
}

func TestRemoteCacheS3(t *testing.T) {
	t.Parallel()
	t.Run("buildkit s3 caching", func(t *testing.T) {
		c, ctx := connect(t)

		bucket := "dagger-test-remote-cache-s3-" + identity.NewID()

		s3 := c.Pipeline("s3").Container().From("minio/minio").
			WithMountedCache("/data", c.CacheVolume("minio-cache")).
			WithExposedPort(9000, dagger.ContainerWithExposedPortOpts{Protocol: dagger.Tcp}).
			WithExec([]string{"server", "/data"}).
			AsService()

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

		devEngineA, endpointA, err := getDevEngineForRemoteCache(ctx, c, s3, "s3", 0)
		require.NoError(t, err)

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

		devEngineB, endpointB, err := getDevEngineForRemoteCache(ctx, c, s3, "s3", 1)
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

func TestRemoteCacheRegistryMultipleConfigs(t *testing.T) {
	t.Parallel()
	c, ctx := connect(t)
	defer c.Close()

	registry := c.Pipeline("registry").Container().From("registry:2").
		WithMountedCache("/var/lib/registry/", c.CacheVolume("remote-cache-registry-"+identity.NewID())).
		WithExposedPort(5000, dagger.ContainerWithExposedPortOpts{Protocol: dagger.Tcp}).
		AsService()

	cacheConfigEnv1 := "type=registry,ref=registry:5000/test-cache:latest,mode=max"
	cacheConfigEnv2 := "type=registry,ref=registry:5000/test-cache-b:latest,mode=max"
	cacheEnv := strings.Join([]string{cacheConfigEnv1, cacheConfigEnv2}, ";")

	devEngineA, endpointA, err := getDevEngineForRemoteCache(ctx, c, registry, "registry", 20)
	require.NoError(t, err)

	// This loads the dagger-cli binary from the host into the container, that was set up by
	// internal/mage/engine.go:test. This is used to communicate with the dev engine.
	daggerCli := daggerCliFile(t, c)

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

	devEngineB, endpointB, err := getDevEngineForRemoteCache(ctx, c, registry, "registry", 21)
	require.NoError(t, err)

	outputB, err := c.Container().From(alpineImage).
		WithServiceBinding("dev-engine", devEngineB).
		WithMountedFile(cliBinPath, daggerCli).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliBinPath).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpointB).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CACHE_CONFIG", cacheConfigEnv1).
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

	devEngineC, endpointC, err := getDevEngineForRemoteCache(ctx, c, registry, "registry", 22)
	require.NoError(t, err)

	outputC, err := c.Container().From(alpineImage).
		WithServiceBinding("dev-engine", devEngineC).
		WithMountedFile(cliBinPath, daggerCli).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliBinPath).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpointC).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CACHE_CONFIG", cacheConfigEnv2).
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
	shaC := strings.TrimSpace(gjson.Get(outputC, "container.from.withExec.stdout").String())
	require.NotEmpty(t, shaC, "shaC is empty")

	require.Equal(t, shaA, shaC)
}

func TestRemoteCacheRegistrySeparateImportExport(t *testing.T) {
	t.Parallel()
	c, ctx := connect(t)
	defer c.Close()

	registry := c.Pipeline("registry").Container().From("registry:2").
		WithMountedCache("/var/lib/registry/", c.CacheVolume("remote-cache-registry-"+identity.NewID())).
		WithExposedPort(5000, dagger.ContainerWithExposedPortOpts{Protocol: dagger.Tcp}).
		AsService()

	// This loads the dagger-cli binary from the host into the container, that was set up by
	// internal/mage/engine.go:test. This is used to communicate with the dev engine.
	daggerCli := daggerCliFile(t, c)

	cacheEnvA := "type=registry,ref=registry:5000/test-cache-a:latest,mode=max"
	cacheEnvB := "type=registry,ref=registry:5000/test-cache-b:latest,mode=max"
	cacheEnvC := "type=registry,ref=registry:5000/test-cache-c:latest,mode=max"

	devEngineA, endpointA, err := getDevEngineForRemoteCache(ctx, c, registry, "registry", 0)
	require.NoError(t, err)
	outputA, err := c.Container().From(alpineImage).
		WithServiceBinding("dev-engine", devEngineA).
		WithMountedFile(cliBinPath, daggerCli).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliBinPath).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpointA).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CACHE_EXPORT_CONFIG", cacheEnvA).
		WithNewFile("/.dagger-query.txt", dagger.ContainerWithNewFileOpts{
			Contents: `{
				container {
					from(address: "` + alpineImage + `") {
						withExec(args: ["sh", "-c", "echo A >/dev/null; head -c 128 /dev/random | sha256sum"]) {
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

	devEngineB, endpointB, err := getDevEngineForRemoteCache(ctx, c, registry, "registry", 1)
	require.NoError(t, err)
	outputB, err := c.Container().From(alpineImage).
		WithServiceBinding("dev-engine", devEngineB).
		WithMountedFile(cliBinPath, daggerCli).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliBinPath).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpointB).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CACHE_EXPORT_CONFIG", cacheEnvB).
		WithNewFile("/.dagger-query.txt", dagger.ContainerWithNewFileOpts{
			Contents: `{
				container {
					from(address: "` + alpineImage + `") {
						withExec(args: ["sh", "-c", "echo B >/dev/null; head -c 128 /dev/random | sha256sum"]) {
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
	shaB := strings.TrimSpace(gjson.Get(outputB, "container.from.withExec.stdout").String())
	require.NotEmpty(t, shaB, "shaB is empty")

	devEngineC, endpointC, err := getDevEngineForRemoteCache(ctx, c, registry, "registry", 2)
	require.NoError(t, err)

	ctrC := c.Container().From(alpineImage).
		WithServiceBinding("dev-engine", devEngineC).
		WithMountedFile(cliBinPath, daggerCli).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliBinPath).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpointC).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CACHE_IMPORT_CONFIG", strings.Join([]string{cacheEnvA, cacheEnvB}, ";")).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CACHE_EXPORT_CONFIG", cacheEnvC)
	outputC, err := ctrC.WithNewFile("/.dagger-query.txt", dagger.ContainerWithNewFileOpts{
		Contents: `{
			container {
				from(address: "` + alpineImage + `") {
					outputA: withExec(args: ["sh", "-c", "echo A >/dev/null; head -c 128 /dev/random | sha256sum"]) {
						stdout
					}
					outputB: withExec(args: ["sh", "-c", "echo B >/dev/null; head -c 128 /dev/random | sha256sum"]) {
						stdout
					}
					outputC: withExec(args: ["sh", "-c", "echo C >/dev/null; head -c 128 /dev/random | sha256sum"]) {
						stdout
					}
				}
			}
		}`,
	}).WithExec([]string{
		"sh", "-c", cliBinPath + ` query --doc .dagger-query.txt`,
	}).Stdout(ctx)
	require.NoError(t, err)
	newA := strings.TrimSpace(gjson.Get(outputC, "container.from.outputA.stdout").String())
	require.Equal(t, shaA, newA)
	newB := strings.TrimSpace(gjson.Get(outputC, "container.from.outputB.stdout").String())
	require.Equal(t, shaB, newB)
	shaC := strings.TrimSpace(gjson.Get(outputC, "container.from.outputC.stdout").String())
	require.NotEmpty(t, shaC, "shaC is empty")

	devEngineD, endpointD, err := getDevEngineForRemoteCache(ctx, c, registry, "registry", 3)
	require.NoError(t, err)
	outputD, err := c.Container().From(alpineImage).
		WithServiceBinding("dev-engine", devEngineD).
		WithMountedFile(cliBinPath, daggerCli).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliBinPath).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpointD).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CACHE_IMPORT_CONFIG", cacheEnvC).
		WithNewFile("/.dagger-query.txt", dagger.ContainerWithNewFileOpts{
			Contents: `{
				container {
					from(address: "` + alpineImage + `") {
						outputC: withExec(args: ["sh", "-c", "echo C >/dev/null; head -c 128 /dev/random | sha256sum"]) {
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
	newC := strings.TrimSpace(gjson.Get(outputD, "container.from.outputC.stdout").String())
	require.Equal(t, shaC, newC)
}

// integration test for dagger/dagger#6163
func TestRemoteCacheRegistryFastCacheBlobSource(t *testing.T) {
	t.Parallel()
	c, ctx := connect(t)
	defer c.Close()

	registry := c.Pipeline("registry").Container().From("registry:2").
		WithMountedCache("/var/lib/registry/", c.CacheVolume("remote-cache-registry-"+identity.NewID())).
		WithExposedPort(5000, dagger.ContainerWithExposedPortOpts{Protocol: dagger.Tcp}).
		AsService()

	cacheConfig := "type=registry,ref=registry:5000/test-cache:latest,mode=max"

	devEngineA, endpointA, err := getDevEngineForRemoteCache(ctx, c, registry, "registry", 0)
	require.NoError(t, err)

	// This loads the dagger-cli binary from the host into the container, that was set up by
	// internal/mage/engine.go:test. This is used to communicate with the dev engine.
	daggerCli := daggerCliFile(t, c)

	dir := `{
		host {
			directory(path: "/foo") {
				id
			}
		}
	}`
	query := `{
		container {
			from(address: "` + alpineImage + `") {
				withMountedDirectory(path: "/mnt", source: "%s") {
					withExec(args: ["sh", "-c", "head -c 128 /dev/random | sha256sum"]) {
						stdout
					}
				}
			}
		}
	}`

	a := c.Container().From(alpineImage).
		WithServiceBinding("dev-engine", devEngineA).
		WithMountedFile(cliBinPath, daggerCli).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliBinPath).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpointA).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CACHE_EXPORT_CONFIG", cacheConfig).
		WithNewFile("/foo/bar")
	dirA, err := a.
		WithNewFile("/.dagger-query.txt", dagger.ContainerWithNewFileOpts{Contents: dir}).
		WithExec([]string{
			"sh", "-c", cliBinPath + ` query --doc .dagger-query.txt`,
		}).Stdout(ctx)
	require.NoError(t, err)
	dirAID := strings.TrimSpace(gjson.Get(dirA, "host.directory.id").String())
	require.NotEmpty(t, dirAID)
	outputA, err := a.
		WithNewFile("/.dagger-query.txt", dagger.ContainerWithNewFileOpts{Contents: fmt.Sprintf(query, dirAID)}).
		WithExec([]string{
			"sh", "-c", cliBinPath + ` query --doc .dagger-query.txt`,
		}).Stdout(ctx)
	require.NoError(t, err)
	shaA := strings.TrimSpace(gjson.Get(outputA, "container.from.withMountedDirectory.withExec.stdout").String())
	require.NotEmpty(t, shaA, "shaA is empty")

	devEngineB, endpointB, err := getDevEngineForRemoteCache(ctx, c, registry, "registry", 1)
	require.NoError(t, err)

	b := c.Container().From(alpineImage).
		WithServiceBinding("dev-engine", devEngineB).
		WithMountedFile(cliBinPath, daggerCli).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliBinPath).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpointB).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CACHE_IMPORT_CONFIG", cacheConfig).
		WithNewFile("/foo/bar")
	dirB, err := b.
		WithNewFile("/.dagger-query.txt", dagger.ContainerWithNewFileOpts{Contents: dir}).
		WithExec([]string{
			"sh", "-c", cliBinPath + ` query --doc .dagger-query.txt`,
		}).Stdout(ctx)
	require.NoError(t, err)
	dirBID := strings.TrimSpace(gjson.Get(dirB, "host.directory.id").String())
	require.NotEmpty(t, dirBID)
	outputB, err := b.
		WithNewFile("/.dagger-query.txt", dagger.ContainerWithNewFileOpts{Contents: fmt.Sprintf(query, dirBID)}).
		WithExec([]string{
			"sh", "-c", cliBinPath + " query --doc .dagger-query.txt",
		}).Stdout(ctx)
	require.NoError(t, err)
	shaB := strings.TrimSpace(gjson.Get(outputB, "container.from.withMountedDirectory.withExec.stdout").String())
	require.NotEmpty(t, shaB, "shaB is empty")

	require.Equal(t, shaA, shaB)
}
