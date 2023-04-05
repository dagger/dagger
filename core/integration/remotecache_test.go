package core

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"dagger.io/dagger"
	"github.com/moby/buildkit/identity"
	"github.com/stretchr/testify/require"
)

func TestRemoteCacheRegistry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	registryContainerName := runRegistryInDocker(ctx, t)
	getClient := func() *dagger.Client {
		c, stop := runSeparateEngine(ctx, t, nil, map[string]string{
			"_EXPERIMENTAL_DAGGER_CACHE_CONFIG": "type=registry,ref=127.0.0.1:5000/test-cache,mode=max",
		}, "container:"+registryContainerName)
		t.Cleanup(func() {
			stop()
		})
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
			c, stop := runSeparateEngine(ctx, t, nil, map[string]string{
				"_EXPERIMENTAL_DAGGER_CACHE_CONFIG": "type=s3,mode=max,endpoint_url=http://localhost:9000,access_key_id=minioadmin,secret_access_key=minioadmin,region=mars,use_path_style=true,bucket=" + bucket,
			}, "container:"+s3ContainerName)
			t.Cleanup(func() {
				stop()
			})
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

var (
	connectLock sync.Mutex

	// keep track of how many networks we've created
	netInstance int
)

func runSeparateEngine(ctx context.Context, t *testing.T, engineEnv, clientEnv map[string]string, network string) (_ *dagger.Client, gracefulStop func() error) {
	// Setting the RUNNER_HOST env var is global so while silly we need to lock here. This also seems to help with
	// some race conditions setting up the engine network when they are all sharing one netns
	connectLock.Lock()
	defer connectLock.Unlock()

	netInstance++

	t.Helper()
	name := "dagger-test-separate-engine-" + identity.NewID()

	allArgs := []string{"run"}
	dockerRunArgs := []string{
		"--rm",
		"-v", "/var/lib/dagger", // path is set in internal/mage/engine.go
		"--privileged",
		"--name", name,
		// share xtables.lock across all engines so iptables --wait works
		//
		// NB: technically we're not sharing with the host's xtables.lock, but this
		// is much easier and should be good enough
		"-v", "xtables-lock:/run/xtables-lock",
		"-e", "XTABLES_LOCKFILE=/run/xtables-lock/xtables.lock",
	}
	for k, v := range engineEnv {
		dockerRunArgs = append(dockerRunArgs, "-e", k+"="+v)
	}
	if network != "" {
		dockerRunArgs = append(dockerRunArgs, "--network", network)
	}
	allArgs = append(allArgs, dockerRunArgs...)
	allArgs = append(allArgs,
		"localhost/dagger-engine.dev:latest", // set in internal/mage/engine.go
		"--debug",
		// configure non-overlapping networks
		"--network-name", fmt.Sprintf("testdagger%d", netInstance),
		"--network-cidr", fmt.Sprintf("10.88.%d.0/24", netInstance),
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
	currentCacheConfig, ok := os.LookupEnv("_EXPERIMENTAL_DAGGER_CACHE_CONFIG")
	if clientEnv != nil {
		os.Setenv("_EXPERIMENTAL_DAGGER_CACHE_CONFIG", clientEnv["_EXPERIMENTAL_DAGGER_CACHE_CONFIG"])
		if ok {
			defer os.Setenv("_EXPERIMENTAL_DAGGER_CACHE_CONFIG", currentCacheConfig)
		} else {
			defer os.Unsetenv("_EXPERIMENTAL_DAGGER_CACHE_CONFIG")
		}
	}

	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stdout))
	require.NoError(t, err)

	return c, func() error {
		t.Logf("stopping engine %s", name)
		out, err := exec.Command("docker", "kill", "-s", "SIGTERM", name).CombinedOutput()
		if err != nil {
			return fmt.Errorf("error stopping docker container: %v: %s", err, out)
		}
		waited := make(chan struct{})
		go func() {
			defer close(waited)
			// wait for the container to stop
			exec.CommandContext(ctx, "docker", "wait", name).Run()
		}()
		select {
		case <-time.After(10 * time.Second):
			t.Logf("timed out stopping engine %s; killing...", name)
			exec.Command("docker", "kill", name).Run()
			<-waited
		case <-waited:
		}
		t.Logf("engine %s exited", name)
		return nil
	}
}

func stopDockerRun(cmd *exec.Cmd, ctrName string) {
	exec.Command("docker", "rm", "-fv", ctrName).Run()
	cmd.Process.Kill()
}
