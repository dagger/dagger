package dagger

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"dagger.io/dagger/internal/engineconn/dockerprovision"
	"github.com/adrg/xdg"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

func TestImageProvision(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	daggerHost, ok := os.LookupEnv("DAGGER_HOST")
	if ok {
		if !strings.HasPrefix(daggerHost, dockerprovision.DockerImageConnName+"://") {
			t.Skip("DAGGER_HOST is not set to docker-image://")
		}
	}

	tmpdir := t.TempDir()
	os.Setenv("XDG_CACHE_HOME", tmpdir)
	defer os.Unsetenv("XDG_CACHE_HOME")
	xdg.Reload()
	cacheDir := filepath.Join(tmpdir, "dagger")

	// create some garbage for the image provisioner to collect
	err := os.MkdirAll(cacheDir, 0700)
	require.NoError(t, err)
	f, err := os.Create(filepath.Join(cacheDir, "dagger-engine-session-gcme"))
	require.NoError(t, err)
	f.Close()

	tmpContainerName := "dagger-engine-gcme-" + strconv.Itoa(int(time.Now().UnixNano()))
	if output, err := exec.CommandContext(ctx,
		"docker", "run",
		"--rm",
		"--detach",
		"--name", tmpContainerName,
		"busybox",
		"sleep", "120",
	).CombinedOutput(); err != nil {
		t.Fatalf("failed to create container: %s", output)
	}

	parallelism := 30
	start := make(chan struct{})
	var eg errgroup.Group
	for i := 0; i < parallelism; i++ {
		eg.Go(func() error {
			<-start
			c, err := Connect(ctx)
			if err != nil {
				return err
			}
			defer c.Close()
			// do a trivial query to ensure the engine is actually there
			_, err = c.Container().From("alpine:3.16").ID(ctx)
			return err
		})
	}
	close(start)
	require.NoError(t, eg.Wait())

	entries, err := os.ReadDir(cacheDir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	entry := entries[0]
	require.True(t, entry.Type().IsRegular())
	require.True(t, strings.HasPrefix(entry.Name(), "dagger-engine-session-"))
	shortSha := entry.Name()[len("dagger-engine-session-"):]
	require.Len(t, shortSha, 16)

	output, err := exec.CommandContext(ctx,
		"docker", "ps",
		"-a",
		"--no-trunc",
	).CombinedOutput()
	require.NoError(t, err)
	var found bool
	for _, line := range strings.Split(string(output), "\n") {
		if line == "" {
			continue
		}
		require.NotContains(t, line, tmpContainerName)
		if strings.Contains(line, shortSha) {
			found = true
			break
		}
	}
	require.True(t, found, "container with sha %s not found in docker ps output: %s", shortSha, output)
}
