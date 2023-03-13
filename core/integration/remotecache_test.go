package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"dagger.io/dagger"
	"github.com/moby/buildkit/identity"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

func TestRemoteCacheRegistry(t *testing.T) {
	// TODO: until this setting is configurable at runtime, just spawning separate engines w/ the config set
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	registryContainerName := runRegistryInDocker(ctx, t)
	getClient := func() *dagger.Client {
		c, _ := runSeparateEngine(ctx, t, map[string]string{
			"_EXPERIMENTAL_DAGGER_CACHE_CONFIG": "type=registry,ref=127.0.0.1:5000/test-cache,mode=max",
		}, "container:"+registryContainerName)
		return c
	}

	pipelineOutput := func(c *dagger.Client) string {
		output, err := c.Container().From("alpine:3.17").WithExec([]string{
			"sh", "-c", "head -c 128 /dev/random | sha256sum",
		}).Stdout(ctx)
		require.NoError(t, err)
		return output
	}

	/*
		1. Start a registry for storing the cache
		2. Start two independent engines from empty cache that are configured to use the registry as remote cache backend
		3. Run an exec w/ output from /dev/random in the first engine
		4. Close the first engine's client, flushing the remote cache for the session
		5. Run the same exec in the second engine, verify it imports the cache and output the same value as the first engine
	*/
	clientA := getClient()
	clientB := getClient()
	outputA := pipelineOutput(clientA)
	require.NoError(t, clientA.Close())
	outputB := pipelineOutput(clientB)
	require.Equal(t, outputA, outputB)
}

func TestRemoteCacheS3(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	t.Run("buildkit s3 caching", func(t *testing.T) {
		bucket := "dagger-test-remote-cache-s3-" + identity.NewID()
		s3ContainerName := runS3InDocker(ctx, t, bucket)
		getClient := func() *dagger.Client {
			c, _ := runSeparateEngine(ctx, t, map[string]string{
				"_EXPERIMENTAL_DAGGER_CACHE_CONFIG": "type=s3,mode=max,endpoint_url=http://localhost:9000,access_key_id=minioadmin,secret_access_key=minioadmin,region=mars,use_path_style=true,bucket=" + bucket,
			}, "container:"+s3ContainerName)
			return c
		}

		pipelineOutput := func(c *dagger.Client) string {
			output, err := c.Container().From("alpine:3.17").WithExec([]string{
				"sh", "-c", "head -c 128 /dev/random | sha256sum",
			}).Stdout(ctx)
			require.NoError(t, err)
			return output
		}

		/*
			1. Start an s3 compatible server (minio) locally for storing the cache
			2. Start two independent engines from empty cache that are configured to use s3 as remote cache backend
			3. Run an exec w/ output from /dev/random in the first engine
			4. Close the first engine's client, flushing the remote cache for the session
			5. Run the same exec in the second engine, verify it imports the cache and output the same value as the first engine
		*/
		clientA := getClient()
		clientB := getClient()
		outputA := pipelineOutput(clientA)
		require.NoError(t, clientA.Close())
		outputB := pipelineOutput(clientB)
		require.Equal(t, outputA, outputB)
	})

	t.Run("dagger s3 caching (with pooling)", func(t *testing.T) {
		bucket := "dagger-test-remote-cache-s3-" + identity.NewID()
		s3ContainerName := runS3InDocker(ctx, t, bucket)
		getClient := func(engineName string) (*dagger.Client, func() error) {
			return runSeparateEngine(ctx, t, map[string]string{
				"_EXPERIMENTAL_DAGGER_CACHE_CONFIG": "type=experimental_dagger_s3,mode=max,endpoint_url=http://127.0.0.1:9000,access_key_id=minioadmin,secret_access_key=minioadmin,region=mars,use_path_style=true,bucket=" + bucket + ",prefix=test-cache-pool/,name=" + engineName,
				// TODO: temporarily disable networking to fix flakiness around containers in the
				// same netns interfering with each other.
				// Real fix is to either override CNI settings for each engine or to remove the need
				// for them to be in the same netns.
				"_EXPERIMENTAL_DAGGER_SERVICES_DNS": "0",
			}, "container:"+s3ContainerName)
		}

		pipelineOutput := func(c *dagger.Client, id string) string {
			output, err := c.Container().
				From("alpine:3.17").
				WithEnvVariable("ID", id).
				WithExec([]string{
					"sh", "-c", "head -c 128 /dev/random | sha256sum",
				}).Stdout(ctx)
			require.NoError(t, err)
			return output
		}

		generatedOutputs := map[string]string{} // map of unique id set in exec -> output
		const numEngines = 2
		var mu sync.Mutex
		var eg errgroup.Group
		for i := 0; i < numEngines; i++ {
			eg.Go(func() error {
				id := identity.NewID()
				client, stopEngine := getClient(id)
				mu.Lock()
				defer mu.Unlock()
				generatedOutputs[id] = pipelineOutput(client, id)
				return errors.Join(client.Close(), stopEngine())
			})
		}
		require.NoError(t, eg.Wait())
		require.Len(t, generatedOutputs, numEngines)
		eg = errgroup.Group{}
		client, stopEngine := getClient(identity.NewID())
		for id, cachedOutput := range generatedOutputs {
			id, cachedOutput := id, cachedOutput
			eg.Go(func() error {
				require.Equal(t, cachedOutput, pipelineOutput(client, id))
				return nil
			})
		}
		require.NoError(t, eg.Wait())
		require.NoError(t, client.Close())
		require.NoError(t, stopEngine())
	})

	t.Run("dagger s3 mount caching", func(t *testing.T) {
		bucket := "dagger-test-remote-cache-mount-s3-" + identity.NewID()
		s3ContainerName := runS3InDocker(ctx, t, bucket)
		getClient := func(engineName string) (*dagger.Client, func() error) {
			return runSeparateEngine(ctx, t, map[string]string{
				"_EXPERIMENTAL_DAGGER_CACHE_CONFIG": "type=experimental_dagger_s3,mode=max,server_implementation=Minio,endpoint_url=http://127.0.0.1:9000,access_key_id=minioadmin,secret_access_key=minioadmin,region=mars,use_path_style=true,bucket=" + bucket + ",prefix=test-cache-pool/,synchronized_cache_mounts=test-cache-mount,name=" + engineName,
				// TODO: temporarily disable networking to fix flakiness around containers in the
				// same netns interfering with each other.
				// Real fix is to either override CNI settings for each engine or to remove the need
				// for them to be in the same netns.
				"_EXPERIMENTAL_DAGGER_SERVICES_DNS": "0",
			}, "container:"+s3ContainerName)
		}

		pipelineOutput := func(c *dagger.Client, id string) string {
			output, err := c.Container().
				From("alpine:3.17").
				WithMountedCache("/cache", c.CacheVolume("test-cache-mount")).
				WithExec([]string{
					"sh", "-c", "if [ ! -f /cache/test.txt ]; then echo '" + id + "' > /cache/test.txt; fi; cat /cache/test.txt",
				}).Stdout(ctx)
			require.NoError(t, err)
			return output
		}

		clientA, stopEngineA := getClient("a")
		t.Cleanup(func() {
			stopEngineA()
		})
		outputA := pipelineOutput(clientA, "a")
		require.NoError(t, clientA.Close())
		require.NoError(t, stopEngineA())
		clientB, stopEngineB := getClient("b")
		t.Cleanup(func() {
			stopEngineB()
		})
		outputB := pipelineOutput(clientB, "b")
		require.NoError(t, stopEngineB())
		require.Equal(t, outputA, outputB)
	})
}

func runS3InDocker(ctx context.Context, t *testing.T, bucket string) string {
	t.Helper()
	name := "dagger-test-remote-cache-s3-" + identity.NewID()
	cmd := exec.Command("docker", "run", "--rm", "--name", name, "minio/minio", "server", "/data")
	t.Cleanup(func() {
		stopDockerRun(cmd, name)
	})
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Start()
	require.NoError(t, err)
	// wait for the s3 to be ready
	for i := 0; i < 100; i++ {
		cmd := exec.CommandContext(ctx, "docker", "exec", name, "sh", "-c", "curl -s -o /dev/null -w '%{http_code}' http://localhost:9000/minio/health/live")
		out, err := cmd.CombinedOutput()
		if string(out) == "200" && err == nil {
			break
		}
		if i == 99 {
			t.Fatalf("minio s3 not ready: %v: %s", err, out)
		}
		time.Sleep(100 * time.Millisecond)
	}

	// create the bucket
	cmd = exec.Command("docker", "run", "--rm", "--network", "container:"+name, "--entrypoint", "sh", "minio/mc", "-c", "mc config host add minio http://localhost:9000 minioadmin minioadmin && mc mb minio/"+bucket) // #nosec G204
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	require.NoError(t, err)

	return name
}

func runRegistryInDocker(ctx context.Context, t *testing.T) string {
	t.Helper()
	name := "dagger-test-remote-cache-registry-" + identity.NewID()
	cmd := exec.Command("docker", "run", "--rm", "--name", name, "registry:2")
	t.Cleanup(func() {
		stopDockerRun(cmd, name)
	})
	err := cmd.Start()
	require.NoError(t, err)
	// wait for the registry to be ready
	for i := 0; i < 100; i++ {
		cmd := exec.CommandContext(ctx, "docker", "exec", name, "sh", "-c", "wget -q -O - http://localhost:5000/v2/")
		out, err := cmd.CombinedOutput()
		if string(out) == "{}" && err == nil {
			break
		}
		if i == 99 {
			t.Fatalf("registry not ready: %v: %s", err, out)
		}
		time.Sleep(100 * time.Millisecond)
	}
	return name
}

var connectLock sync.Mutex

func runSeparateEngine(ctx context.Context, t *testing.T, env map[string]string, network string) (_ *dagger.Client, gracefulStop func() error) {
	// Setting the RUNNER_HOST env var is global so while silly we need to lock here. This also seems to help with
	// some race conditions setting up the engine network when they are all sharing one netns
	connectLock.Lock()
	defer connectLock.Unlock()

	t.Helper()
	name := "dagger-test-remote-cache-" + identity.NewID()

	allArgs := []string{"run"}
	dockerRunArgs := []string{
		"--rm",
		"-v", "/var/lib/dagger", // path is set in internal/mage/engine.go
		"--privileged",
		"--name", name,
	}
	for k, v := range env {
		dockerRunArgs = append(dockerRunArgs, "-e", k+"="+v)
	}
	if network != "" {
		dockerRunArgs = append(dockerRunArgs, "--network", network)
	}
	allArgs = append(allArgs, dockerRunArgs...)
	allArgs = append(allArgs,
		"localhost/dagger-engine.dev:latest", // set in internal/mage/engine.go
		"--debug",
	)

	cmd := exec.Command("docker", allArgs...)
	t.Cleanup(func() {
		stopDockerRun(cmd, name)
	})
	err := cmd.Start()
	require.NoError(t, err)

	currentRunnerHost, ok := os.LookupEnv("_EXPERIMENTAL_DAGGER_RUNNER_HOST")
	os.Setenv("_EXPERIMENTAL_DAGGER_RUNNER_HOST", "docker-container://"+name)
	if ok {
		defer os.Setenv("_EXPERIMENTAL_DAGGER_RUNNER_HOST", currentRunnerHost)
	} else {
		defer os.Unsetenv("_EXPERIMENTAL_DAGGER_RUNNER_HOST")
	}

	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stdout))
	require.NoError(t, err)

	return c, func() error {
		out, err := exec.Command("docker", "stop", "-s", "SIGTERM", "-t", "30", name).CombinedOutput()
		if err != nil {
			return fmt.Errorf("error stopping docker container: %v: %s", err, out)
		}
		// wait for the container to stop
		exec.CommandContext(ctx, "docker", "wait", name).Run()
		return nil
	}
}

func stopDockerRun(cmd *exec.Cmd, ctrName string) {
	exec.Command("docker", "rm", "-fv", ctrName).Run()
	cmd.Process.Kill()
}
