package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"

	"dagger.io/dagger"
)

func (EngineSuite) TestDiskPersistenceAcrossRestart(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	startEngine := func(
		ctx context.Context,
		t *testctx.T,
		stateKey string,
		opts ...func(*dagger.Container) *dagger.Container,
	) (*dagger.Service, *dagger.Service, *dagger.Client) {
		t.Helper()

		engineCtr := devEngineContainerWithStateKey(c, stateKey, opts...)
		upstreamSvc := devEngineContainerAsService(engineCtr)
		engineSvc, err := c.Host().Tunnel(upstreamSvc).Start(ctx)
		require.NoError(t, err)

		endpoint, err := engineSvc.Endpoint(ctx, dagger.ServiceEndpointOpts{Scheme: "tcp"})
		require.NoError(t, err)

		engineClient, err := dagger.Connect(
			ctx,
			dagger.WithRunnerHost(endpoint),
			dagger.WithLogOutput(testutil.NewTWriter(t)),
		)
		require.NoError(t, err)
		return upstreamSvc, engineSvc, engineClient
	}

	stopEngine := func(
		ctx context.Context,
		t *testctx.T,
		upstreamSvc *dagger.Service,
		engineSvc *dagger.Service,
		engineClient *dagger.Client,
	) {
		t.Helper()
		if engineClient != nil {
			require.NoError(t, engineClient.Close())
		}
		if upstreamSvc != nil {
			_, err := upstreamSvc.Stop(ctx)
			require.NoError(t, err)
		}
		if engineSvc != nil {
			_, err := engineSvc.Stop(ctx, dagger.ServiceStopOpts{Kill: true})
			require.NoError(t, err)
		}
	}

	t.Run("local cache survives restart", func(ctx context.Context, t *testctx.T) {
		stateKey := "phase7-local-cache-state-" + identity.NewID()

		upstreamSvcA, engineSvcA, engineClientA := startEngine(ctx, t, stateKey, engineWithConfig(ctx, t, engineConfigWithEnabled(false)))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcA, engineSvcA, engineClientA) })

		_, err := engineClientA.
			Container().
			From(alpineImage).
			WithExec([]string{"sh", "-ec", "echo phase7-local-cache > /tmp/phase7.txt"}).
			Sync(ctx)
		require.NoError(t, err)

		entryCountA, err := engineClientA.Engine().LocalCache().EntrySet().EntryCount(ctx)
		require.NoError(t, err)
		require.Greater(t, entryCountA, 0)

		stopEngine(ctx, t, upstreamSvcA, engineSvcA, engineClientA)
		upstreamSvcA = nil
		engineSvcA = nil
		engineClientA = nil

		upstreamSvcB, engineSvcB, engineClientB := startEngine(ctx, t, stateKey, engineWithConfig(ctx, t, engineConfigWithEnabled(false)))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcB, engineSvcB, engineClientB) })

		entryCount, err := engineClientB.Engine().LocalCache().EntrySet().EntryCount(ctx)
		require.NoError(t, err)
		require.Greater(t, entryCount, 0)
	})

	t.Run("function cache control survives restart", func(ctx context.Context, t *testctx.T) {
		stateKey := "phase7-function-cache-state-" + identity.NewID()
		moduleSrc := `package main

import "crypto/rand"

type Test struct{}

func (m *Test) TestAlwaysCache() string {
	return rand.Text()
}
`

		upstreamSvcA, engineSvcA, engineClientA := startEngine(ctx, t, stateKey, engineWithConfig(ctx, t, engineConfigWithEnabled(false)))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcA, engineSvcA, engineClientA) })

		modA := modInit(t, engineClientA, "go", moduleSrc)
		outA, err := modA.
			WithEnvVariable("CACHE_BUST", identity.NewID()).
			With(daggerCall("test-always-cache")).
			Stdout(ctx)
		require.NoError(t, err)
		stopEngine(ctx, t, upstreamSvcA, engineSvcA, engineClientA)
		upstreamSvcA = nil
		engineSvcA = nil
		engineClientA = nil

		upstreamSvcB, engineSvcB, engineClientB := startEngine(ctx, t, stateKey, engineWithConfig(ctx, t, engineConfigWithEnabled(false)))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcB, engineSvcB, engineClientB) })

		modB := modInit(t, engineClientB, "go", moduleSrc)
		outB, err := modB.
			WithEnvVariable("CACHE_BUST", identity.NewID()).
			With(daggerCall("test-always-cache")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, outA, outB, "always-cached function result should survive engine restart")
	})

	t.Run("contextual function cache survives restart", func(ctx context.Context, t *testctx.T) {
		stateKey := "phase7-contextual-function-cache-state-" + identity.NewID()
		moduleSrc := `package main

import (
	"context"
	"crypto/rand"

	"dagger/test/internal/dagger"
)

type Test struct{}

func (m *Test) ContextDir(
	ctx context.Context,
	// +defaultPath="."
	dir *dagger.Directory,
) (string, error) {
	contents, err := dir.File("dagger.json").Contents(ctx)
	if err != nil {
		return "", err
	}
	return rand.Text() + "|" + contents, nil
}

func (m *Test) ContextFile(
	ctx context.Context,
	// +defaultPath="dagger.json"
	file *dagger.File,
) (string, error) {
	contents, err := file.Contents(ctx)
	if err != nil {
		return "", err
	}
	return rand.Text() + "|" + contents, nil
}

func (m *Test) ContextGitRepository(
	ctx context.Context,
	// +defaultPath="."
	repo *dagger.GitRepository,
) (string, error) {
	commit, err := repo.Head().Commit(ctx)
	if err != nil {
		return "", err
	}
	return rand.Text() + "|" + commit, nil
}

func (m *Test) ContextGitRef(
	ctx context.Context,
	// +defaultPath="."
	ref *dagger.GitRef,
) (string, error) {
	commit, err := ref.Commit(ctx)
	if err != nil {
		return "", err
	}
	return rand.Text() + "|" + commit, nil
}
`

		getMod := func(client *dagger.Client) *dagger.Container {
			return modInit(t, client, "go", moduleSrc).
				WithExec([]string{"git", "add", "."}).
				WithExec([]string{"git", "commit", "-m", "make HEAD exist"})
		}

		runCalls := func(ctx context.Context, t *testctx.T, engineClient *dagger.Client) map[string]string {
			t.Helper()

			mod := getMod(engineClient)
			outputs := map[string]string{}
			for _, fn := range []string{
				"context-dir",
				"context-file",
				"context-git-repository",
				"context-git-ref",
			} {
				out, err := mod.With(daggerCall(fn)).Stdout(ctx)
				require.NoError(t, err)
				outputs[fn] = out
			}
			return outputs
		}

		upstreamSvcA, engineSvcA, engineClientA := startEngine(ctx, t, stateKey, engineWithConfig(ctx, t, engineConfigWithEnabled(false)))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcA, engineSvcA, engineClientA) })

		outA := runCalls(ctx, t, engineClientA)
		stopEngine(ctx, t, upstreamSvcA, engineSvcA, engineClientA)
		upstreamSvcA = nil
		engineSvcA = nil
		engineClientA = nil

		upstreamSvcB, engineSvcB, engineClientB := startEngine(ctx, t, stateKey, engineWithConfig(ctx, t, engineConfigWithEnabled(false)))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcB, engineSvcB, engineClientB) })

		outB := runCalls(ctx, t, engineClientB)
		require.Equal(t, outA, outB, "contextual function results should survive engine restart")
	})

	t.Run("container withExec output on host mount survives restart", func(ctx context.Context, t *testctx.T) {
		stateKey := "phase7-container-host-mount-state-" + identity.NewID()

		hostDirA := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(hostDirA, "input.txt"), []byte("same-content\n"), 0o600))
		hostDirB := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(hostDirB, "input.txt"), []byte("same-content\n"), 0o600))

		runChain := func(ctx context.Context, t *testctx.T, engineClient *dagger.Client, hostPath string) string {
			t.Helper()
			workDir := engineClient.
				Container().
				From(alpineImage).
				WithMountedDirectory("/src", engineClient.Host().Directory(hostPath)).
				WithExec([]string{"sh", "-ec", "mkdir -p /work && cp /src/input.txt /work/input.txt && head -c 32 /dev/urandom | sha256sum | cut -d' ' -f1 > /work/random.txt"}).
				Directory("/work")

			entries, err := workDir.Entries(ctx)
			require.NoError(t, err)
			require.Contains(t, entries, "input.txt")
			require.Contains(t, entries, "random.txt")

			randomContents, err := workDir.File("random.txt").Contents(ctx)
			require.NoError(t, err)
			return strings.TrimSpace(randomContents)
		}

		upstreamSvcA, engineSvcA, engineClientA := startEngine(ctx, t, stateKey, engineWithConfig(ctx, t, engineConfigWithEnabled(false)))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcA, engineSvcA, engineClientA) })

		randomA := runChain(ctx, t, engineClientA, hostDirA)
		stopEngine(ctx, t, upstreamSvcA, engineSvcA, engineClientA)
		upstreamSvcA = nil
		engineSvcA = nil
		engineClientA = nil

		upstreamSvcB, engineSvcB, engineClientB := startEngine(ctx, t, stateKey, engineWithConfig(ctx, t, engineConfigWithEnabled(false)))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcB, engineSvcB, engineClientB) })

		randomB := runChain(ctx, t, engineClientB, hostDirB)
		require.Equal(t, randomA, randomB, "withExec output should survive engine restart for equivalent host-mounted input")
	})

	t.Run("cache volume survives restart", func(ctx context.Context, t *testctx.T) {
		stateKey := "phase7-cache-volume-state-" + identity.NewID()
		cacheKey := "phase7-cache-volume-data-" + identity.NewID()
		cacheValue := identity.NewID()

		upstreamSvcA, engineSvcA, engineClientA := startEngine(ctx, t, stateKey, engineWithConfig(ctx, t, engineConfigWithEnabled(false)))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcA, engineSvcA, engineClientA) })

		cacheA := engineClientA.CacheVolume(cacheKey)
		outA, err := engineClientA.
			Container().
			From(alpineImage).
			WithEnvVariable("CACHE_VALUE", cacheValue).
			WithMountedCache("/mnt/cache", cacheA).
			WithExec([]string{"sh", "-ec", "echo \"$CACHE_VALUE\" >> /mnt/cache/sub-file; cat /mnt/cache/sub-file"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, cacheValue+"\n", outA)

		stopEngine(ctx, t, upstreamSvcA, engineSvcA, engineClientA)
		upstreamSvcA = nil
		engineSvcA = nil
		engineClientA = nil

		upstreamSvcB, engineSvcB, engineClientB := startEngine(ctx, t, stateKey, engineWithConfig(ctx, t, engineConfigWithEnabled(false)))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcB, engineSvcB, engineClientB) })

		cacheB := engineClientB.CacheVolume(cacheKey)
		outB, err := engineClientB.
			Container().
			From(alpineImage).
			WithMountedCache("/mnt/cache", cacheB).
			WithExec([]string{"sh", "-ec", "cat /mnt/cache/sub-file"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, cacheValue+"\n", outB)
	})
}
