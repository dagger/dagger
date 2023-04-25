package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"dagger.io/dagger"
	"github.com/moby/buildkit/identity"
	"github.com/stretchr/testify/require"
)

func devEngineContainer(c *dagger.Client) *dagger.Container {
	// This loads the engine.tar file from the host into the container, that was set up by
	// internal/mage/engine.go:test or by ./hack/dev. This is used to spin up additional dev engines.
	var tarPath string
	if v, ok := os.LookupEnv("_DAGGER_TESTS_ENGINE_TAR"); ok {
		tarPath = v
	} else {
		tarPath = "./bin/engine.tar"
	}
	parentDir := filepath.Dir(tarPath)
	tarFileName := filepath.Base(tarPath)
	devEngineTar := c.Host().Directory(parentDir, dagger.HostDirectoryOpts{Include: []string{tarFileName}}).File(tarFileName)
	return c.Container().Import(devEngineTar).
		WithExposedPort(1234, dagger.ContainerWithExposedPortOpts{Protocol: dagger.Tcp})
}

func TestEngineExitsZeroOnSignal(t *testing.T) {
	c, ctx := connect(t)
	defer c.Close()

	// engine should shutdown with exit code 0 when receiving SIGTERM
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, err := devEngineContainer(c).
		WithNewFile("/usr/local/bin/dagger-entrypoint.sh", dagger.ContainerWithNewFileOpts{
			Contents: `#!/bin/sh
env
/usr/local/bin/dagger-engine --debug &
engine_pid=$!

sleep 5
kill -TERM $engine_pid
wait $engine_pid
exit $?
`,
			Permissions: 0700,
		}).
		WithMountedCache("/var/lib/dagger", c.CacheVolume("dagger-dev-engine-state-"+identity.NewID())).
		WithExec(nil, dagger.ContainerWithExecOpts{
			InsecureRootCapabilities: true,
		}).
		ExitCode(ctx)
	require.NoError(t, err)
}
