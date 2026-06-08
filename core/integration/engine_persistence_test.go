package core

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"dagger.io/dagger"
)

func (CachePersistenceSuite) TestDiskPersistenceAcrossRestart(ctx context.Context, t *testctx.T) {
	const persistenceTestGCThresholdBytes = "1000000000000000"

	engineWithPersistenceTestGC := func(ctx context.Context, t *testctx.T) func(*dagger.Container) *dagger.Container {
		t.Helper()
		return engineWithConfig(
			ctx,
			t,
			engineConfigWithEnabled(true),
			engineConfigWithGC(
				persistenceTestGCThresholdBytes,
				"0",
				persistenceTestGCThresholdBytes,
				"0",
			),
		)
	}

	startEngineWithClientOpts := func(
		client *dagger.Client,
		ctx context.Context,
		t *testctx.T,
		stateKey string,
		clientOpts []dagger.ClientOpt,
		opts ...func(*dagger.Container) *dagger.Container,
	) (*dagger.Service, *dagger.Service, *dagger.Client) {
		t.Helper()

		engineCtr := devEngineContainerWithStateKey(client, stateKey, opts...)
		upstreamSvc := devEngineContainerAsService(engineCtr)
		engineSvc, err := client.Host().Tunnel(upstreamSvc).Start(ctx)
		require.NoError(t, err)

		endpoint, err := engineSvc.Endpoint(ctx, dagger.ServiceEndpointOpts{Scheme: "tcp"})
		require.NoError(t, err)

		connectOpts := []dagger.ClientOpt{
			dagger.WithRunnerHost(endpoint),
			dagger.WithLogOutput(testutil.NewTWriter(t)),
		}
		connectOpts = append(connectOpts, clientOpts...)
		engineClient, err := dagger.Connect(ctx, connectOpts...)
		require.NoError(t, err)
		return upstreamSvc, engineSvc, engineClient
	}

	startEngine := func(
		client *dagger.Client,
		ctx context.Context,
		t *testctx.T,
		stateKey string,
		opts ...func(*dagger.Container) *dagger.Container,
	) (*dagger.Service, *dagger.Service, *dagger.Client) {
		t.Helper()
		return startEngineWithClientOpts(client, ctx, t, stateKey, nil, opts...)
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
		c := connect(ctx, t)
		stateKey := "phase7-local-cache-state-" + identity.NewID()

		upstreamSvcA, engineSvcA, engineClientA := startEngine(c, ctx, t, stateKey, engineWithPersistenceTestGC(ctx, t))
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

		upstreamSvcB, engineSvcB, engineClientB := startEngine(c, ctx, t, stateKey, engineWithPersistenceTestGC(ctx, t))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcB, engineSvcB, engineClientB) })

		entryCount, err := engineClientB.Engine().LocalCache().EntrySet().EntryCount(ctx)
		require.NoError(t, err)
		require.Greater(t, entryCount, 0)
	})

	t.Run("lazy imported snapshot links count toward local cache usage and max-used prune", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		stateKey := "phase7-lazy-import-cache-usage-state-" + identity.NewID()

		runWorkload := func(ctx context.Context, t *testctx.T, client *dagger.Client) string {
			t.Helper()
			out, err := client.
				Container().
				From(alpineImage).
				WithExec([]string{
					"sh",
					"-ec",
					`token="$(dd if=/dev/urandom bs=32 count=1 status=none | base64)"
dd if=/dev/urandom of=/bigfile bs=1M count=64 status=none
printf "%s" "$token"`,
				}).
				Stdout(ctx)
			require.NoError(t, err)
			return out
		}

		localCacheDiskBytes := func(ctx context.Context, t *testctx.T, client *dagger.Client) int {
			t.Helper()
			used, err := client.Engine().LocalCache().EntrySet().DiskSpaceBytes(ctx)
			require.NoError(t, err)
			return used
		}

		upstreamSvcA, engineSvcA, engineClientA := startEngine(c, ctx, t, stateKey, engineWithPersistenceTestGC(ctx, t))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcA, engineSvcA, engineClientA) })

		outA := runWorkload(ctx, t, engineClientA)
		usedA := localCacheDiskBytes(ctx, t, engineClientA)
		require.Greater(t, usedA, 32*1024*1024)

		stopEngine(ctx, t, upstreamSvcA, engineSvcA, engineClientA)
		upstreamSvcA = nil
		engineSvcA = nil
		engineClientA = nil

		upstreamSvcB, engineSvcB, engineClientB := startEngine(c, ctx, t, stateKey, engineWithPersistenceTestGC(ctx, t))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcB, engineSvcB, engineClientB) })

		usedB := localCacheDiskBytes(ctx, t, engineClientB)
		require.Greater(t, usedB, 32*1024*1024)

		stopEngine(ctx, t, upstreamSvcB, engineSvcB, engineClientB)
		upstreamSvcB = nil
		engineSvcB = nil
		engineClientB = nil

		upstreamSvcC, engineSvcC, engineClientC := startEngine(c, ctx, t, stateKey, engineWithPersistenceTestGC(ctx, t))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcC, engineSvcC, engineClientC) })

		err := engineClientC.Engine().LocalCache().Prune(ctx, dagger.EngineCachePruneOpts{
			UseDefaultPolicy: false,
			MaxUsedSpace:     "1",
			ReservedSpace:    "0",
			MinFreeSpace:     "0",
			TargetSpace:      "1",
		})
		require.NoError(t, err)

		outC := runWorkload(ctx, t, engineClientC)
		require.NotEqual(t, outA, outC, "max-used prune should evict lazy imported snapshot-backed results before they are cache-hit")
	})

	t.Run("unclean shutdown discards local cache state and recovers", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		stateKey := "phase7-unclean-reset-state-" + identity.NewID()
		const sentinelPath = "/state/worker/reset-sentinel"
		randomScript := `
set -eu
mkdir -p /work
head -c 32 /dev/urandom | sha256sum | cut -d' ' -f1 > /work/random.txt
`

		runRandom := func(ctx context.Context, t *testctx.T, engineClient *dagger.Client) string {
			t.Helper()

			randomContents, err := engineClient.
				Container().
				From(alpineImage).
				WithExec([]string{"sh", "-ec", randomScript}).
				Directory("/work").
				File("random.txt").
				Contents(ctx)
			require.NoError(t, err)
			random := strings.TrimSpace(randomContents)
			require.NotEmpty(t, random)
			return random
		}

		upstreamSvcA, engineSvcA, engineClientA := startEngine(c, ctx, t, stateKey, engineWithPersistenceTestGC(ctx, t))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcA, engineSvcA, engineClientA) })

		randomA := runRandom(ctx, t, engineClientA)

		_, err := upstreamSvcA.Stop(ctx, dagger.ServiceStopOpts{Kill: true})
		require.NoError(t, err)
		upstreamSvcA = nil
		_, err = engineSvcA.Stop(ctx, dagger.ServiceStopOpts{Kill: true})
		require.NoError(t, err)
		engineSvcA = nil
		engineClientA = nil

		_, err = c.
			Container().
			From(alpineImage).
			WithMountedCache("/state", c.CacheVolume(stateKey)).
			WithExec([]string{"sh", "-ec", "test -d /state/worker && touch " + sentinelPath}).
			Sync(ctx)
		require.NoError(t, err)

		upstreamSvcB, engineSvcB, engineClientB := startEngine(c, ctx, t, stateKey, engineWithPersistenceTestGC(ctx, t))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcB, engineSvcB, engineClientB) })

		randomB := runRandom(ctx, t, engineClientB)
		require.NotEqual(t, randomA, randomB, "cache state from before the unclean shutdown should be discarded")

		stopEngine(ctx, t, upstreamSvcB, engineSvcB, engineClientB)
		upstreamSvcB = nil
		engineSvcB = nil
		engineClientB = nil

		_, err = c.
			Container().
			From(alpineImage).
			WithMountedCache("/state", c.CacheVolume(stateKey)).
			WithExec([]string{"sh", "-ec", "test ! -e " + sentinelPath}).
			Sync(ctx)
		require.NoError(t, err)

		upstreamSvcC, engineSvcC, engineClientC := startEngine(c, ctx, t, stateKey, engineWithPersistenceTestGC(ctx, t))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcC, engineSvcC, engineClientC) })

		randomC := runRandom(ctx, t, engineClientC)
		require.Equal(t, randomB, randomC, "cache state produced after reset should survive a later clean restart")
	})

	t.Run("container withNewFile hit survives restart", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		stateKey := "phase7-container-with-new-file-state-" + identity.NewID()
		const newFilePath = "/tmp/persisted-new-file.txt"
		const newFileContents = "persisted withNewFile\n"

		upstreamSvcA, engineSvcA, engineClientA := startEngine(c, ctx, t, stateKey, engineWithPersistenceTestGC(ctx, t))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcA, engineSvcA, engineClientA) })

		ctrID, err := engineClientA.
			Container().
			From(alpineImage).
			WithNewFile(newFilePath, newFileContents).
			ID(ctx)
		require.NoError(t, err)

		stopEngine(ctx, t, upstreamSvcA, engineSvcA, engineClientA)
		upstreamSvcA = nil
		engineSvcA = nil
		engineClientA = nil

		upstreamSvcB, engineSvcB, engineClientB := startEngine(c, ctx, t, stateKey, engineWithPersistenceTestGC(ctx, t))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcB, engineSvcB, engineClientB) })

		contents, err := dagger.Ref[*dagger.Container](engineClientB, ctrID).
			File(newFilePath).
			Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, newFileContents, contents)
	})

	t.Run("container selector lazy dependencies survive restart", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		stateKey := "phase7-container-selector-lazy-state-" + identity.NewID()
		const fileContents = "selector lazy persisted\n"

		buildRetainedGraph := func(engineClient *dagger.Client) *dagger.Directory {
			source := engineClient.
				Directory().
				WithNewFile("file.txt", fileContents)
			ctr := engineClient.
				Container().
				WithDirectory("/work", source)

			return engineClient.
				Directory().
				WithDirectory("rootfs", ctr.Rootfs()).
				WithDirectory("selected-dir", ctr.Directory("/work")).
				WithFile("selected-file.txt", ctr.File("/work/file.txt"))
		}

		upstreamSvcA, engineSvcA, engineClientA := startEngine(c, ctx, t, stateKey, engineWithPersistenceTestGC(ctx, t))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcA, engineSvcA, engineClientA) })

		dirID, err := buildRetainedGraph(engineClientA).ID(ctx)
		require.NoError(t, err)

		stopEngine(ctx, t, upstreamSvcA, engineSvcA, engineClientA)
		upstreamSvcA = nil
		engineSvcA = nil
		engineClientA = nil

		upstreamSvcB, engineSvcB, engineClientB := startEngine(c, ctx, t, stateKey, engineWithPersistenceTestGC(ctx, t))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcB, engineSvcB, engineClientB) })

		loaded := dagger.Ref[*dagger.Directory](engineClientB, dirID)

		selectedFile, err := loaded.File("selected-file.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, fileContents, selectedFile)

		selectedDirFile, err := loaded.File("selected-dir/file.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, fileContents, selectedDirFile)

		rootfsFile, err := loaded.File("rootfs/work/file.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, fileContents, rootfsFile)
	})

	t.Run("directory search result list survives restart", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		stateKey := "phase7-directory-search-result-state-" + identity.NewID()
		const pattern = `^\s*//\s*workspace:include\s+\S+\s*$`

		runSearch := func(ctx context.Context, t *testctx.T, engineClient *dagger.Client) []string {
			t.Helper()

			results, err := engineClient.
				Directory().
				WithNewFile("one.go", "package main\n// workspace:include ./one\n").
				WithNewFile("two.go", "package main\n// workspace:include ./two\n").
				Search(ctx, pattern, dagger.DirectorySearchOpts{
					Paths: []string{"one.go", "two.go"},
				})
			require.NoError(t, err)
			require.Len(t, results, 2)

			matches := make([]string, 0, len(results))
			for _, result := range results {
				filePath, err := result.FilePath(ctx)
				require.NoError(t, err)

				matchedLines, err := result.MatchedLines(ctx)
				require.NoError(t, err)

				submatches, err := result.Submatches(ctx)
				require.NoError(t, err)
				require.NotEmpty(t, submatches)
				submatchText, err := submatches[0].Text(ctx)
				require.NoError(t, err)
				require.Contains(t, submatchText, "workspace:include")

				matches = append(matches, filePath+":"+strings.TrimSpace(matchedLines))
			}
			require.ElementsMatch(t, []string{
				"one.go:// workspace:include ./one",
				"two.go:// workspace:include ./two",
			}, matches)

			return matches
		}

		upstreamSvcA, engineSvcA, engineClientA := startEngine(c, ctx, t, stateKey, engineWithPersistenceTestGC(ctx, t))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcA, engineSvcA, engineClientA) })

		matchesA := runSearch(ctx, t, engineClientA)
		stopEngine(ctx, t, upstreamSvcA, engineSvcA, engineClientA)
		upstreamSvcA = nil
		engineSvcA = nil
		engineClientA = nil

		upstreamSvcB, engineSvcB, engineClientB := startEngine(c, ctx, t, stateKey, engineWithPersistenceTestGC(ctx, t))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcB, engineSvcB, engineClientB) })

		matchesB := runSearch(ctx, t, engineClientB)
		require.ElementsMatch(t, matchesA, matchesB)
	})

	t.Run("changeset diff stat list survives restart", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		stateKey := "phase7-changeset-diff-stat-state-" + identity.NewID()

		runDiffStats := func(ctx context.Context, t *testctx.T, engineClient *dagger.Client) []string {
			t.Helper()

			before := engineClient.
				Directory().
				WithNewFile("same.txt", "same\n").
				WithNewFile("changed.txt", "old\n").
				WithNewFile("removed.txt", "gone\n")
			after := engineClient.
				Directory().
				WithNewFile("same.txt", "same\n").
				WithNewFile("changed.txt", "new\n").
				WithNewFile("added.txt", "new\n")

			stats, err := after.Changes(before).DiffStats(ctx)
			require.NoError(t, err)
			require.Len(t, stats, 3)

			got := make([]string, 0, len(stats))
			for _, stat := range stats {
				path, err := stat.Path(ctx)
				require.NoError(t, err)
				kind, err := stat.Kind(ctx)
				require.NoError(t, err)
				added, err := stat.AddedLines(ctx)
				require.NoError(t, err)
				removed, err := stat.RemovedLines(ctx)
				require.NoError(t, err)
				got = append(got, fmt.Sprintf("%s:%s:%d:%d", path, kind, added, removed))
			}
			require.ElementsMatch(t, []string{
				"added.txt:ADDED:1:0",
				"changed.txt:MODIFIED:1:1",
				"removed.txt:REMOVED:0:1",
			}, got)

			return got
		}

		upstreamSvcA, engineSvcA, engineClientA := startEngine(c, ctx, t, stateKey, engineWithPersistenceTestGC(ctx, t))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcA, engineSvcA, engineClientA) })

		statsA := runDiffStats(ctx, t, engineClientA)
		stopEngine(ctx, t, upstreamSvcA, engineSvcA, engineClientA)
		upstreamSvcA = nil
		engineSvcA = nil
		engineClientA = nil

		upstreamSvcB, engineSvcB, engineClientB := startEngine(c, ctx, t, stateKey, engineWithPersistenceTestGC(ctx, t))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcB, engineSvcB, engineClientB) })

		statsB := runDiffStats(ctx, t, engineClientB)
		require.ElementsMatch(t, statsA, statsB)
	})

	t.Run("service-bound graph does not break disk persistence", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		stateKey := "phase7-service-binding-state-" + identity.NewID()
		serviceScript := "#!/bin/sh\nwhile true; do cat /work/service-random.txt | nc -l -p 8080; done\n"
		serviceSetupScript := `
set -eu
mkdir -p /work
head -c 32 /dev/urandom | sha256sum | cut -d' ' -f1 > /work/service-random.txt
`
		serviceRunScript := `
set -eu
mkdir -p /work
nc sidecar 8080 > /work/service.txt
head -c 32 /dev/urandom | sha256sum | cut -d' ' -f1 > /work/client-random.txt
`

		type serviceBoundOutput struct {
			serviceRandom string
			clientRandom  string
		}

		runServiceBound := func(ctx context.Context, t *testctx.T, engineClient *dagger.Client, clientBust string) serviceBoundOutput {
			t.Helper()

			service := engineClient.
				Container().
				From(alpineImage).
				WithExec([]string{"sh", "-ec", serviceSetupScript}).
				WithNewFile("/bin/app", serviceScript, dagger.ContainerWithNewFileOpts{Permissions: 0o755}).
				WithExposedPort(8080).
				WithDefaultArgs([]string{"/bin/app"}).
				AsService()

			clientCtr := engineClient.
				Container().
				From(alpineImage).
				WithServiceBinding("sidecar", service)
			if clientBust != "" {
				clientCtr = clientCtr.WithEnvVariable("CLIENT_BUST", clientBust)
			}
			workDir := clientCtr.
				WithExec([]string{"sh", "-ec", serviceRunScript}).
				Directory("/work")

			serviceContents, err := workDir.File("service.txt").Contents(ctx)
			require.NoError(t, err)
			serviceRandom := strings.TrimSpace(serviceContents)
			require.NotEmpty(t, serviceRandom)

			clientContents, err := workDir.File("client-random.txt").Contents(ctx)
			require.NoError(t, err)
			clientRandom := strings.TrimSpace(clientContents)
			require.NotEmpty(t, clientRandom)

			return serviceBoundOutput{
				serviceRandom: serviceRandom,
				clientRandom:  clientRandom,
			}
		}

		upstreamSvcA, engineSvcA, engineClientA := startEngine(c, ctx, t, stateKey, engineWithPersistenceTestGC(ctx, t))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcA, engineSvcA, engineClientA) })

		outA := runServiceBound(ctx, t, engineClientA, "")
		stopEngine(ctx, t, upstreamSvcA, engineSvcA, engineClientA)
		upstreamSvcA = nil
		engineSvcA = nil
		engineClientA = nil

		upstreamSvcB, engineSvcB, engineClientB := startEngine(c, ctx, t, stateKey, engineWithPersistenceTestGC(ctx, t))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcB, engineSvcB, engineClientB) })

		outB := runServiceBound(ctx, t, engineClientB, "")
		require.Equal(t, outA.serviceRandom, outB.serviceRandom, "service container output should survive engine restart")
		require.Equal(t, outA.clientRandom, outB.clientRandom, "service-bound client output should survive engine restart")

		outC := runServiceBound(ctx, t, engineClientB, identity.NewID())
		require.Equal(t, outA.serviceRandom, outC.serviceRandom, "service container output should still be cached when only the client container is invalidated")
		require.NotEqual(t, outA.clientRandom, outC.clientRandom, "client container output should be recomputed after invalidation")
	})

	t.Run("generator group graph does not break disk persistence", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		stateKey := "phase7-generator-group-state-" + identity.NewID()
		generatorWorkdir, err := filepath.Abs("testdata/generators/hello-with-generators")
		require.NoError(t, err)
		clientOpts := []dagger.ClientOpt{
			dagger.WithWorkdir(generatorWorkdir),
			dagger.WithLoadWorkspaceModules(),
		}
		randomScript := `
set -eu
mkdir -p /work
head -c 32 /dev/urandom | sha256sum | cut -d' ' -f1 > /work/random.txt
`

		runRandom := func(ctx context.Context, t *testctx.T, engineClient *dagger.Client) string {
			t.Helper()

			randomContents, err := engineClient.
				Container().
				From(alpineImage).
				WithExec([]string{"sh", "-ec", randomScript}).
				Directory("/work").
				File("random.txt").
				Contents(ctx)
			require.NoError(t, err)
			return strings.TrimSpace(randomContents)
		}

		runGeneratorGroup := func(ctx context.Context, t *testctx.T, engineClient *dagger.Client) {
			t.Helper()

			run := engineClient.
				CurrentWorkspace().
				Generators(dagger.WorkspaceGeneratorsOpts{Include: []string{"generate-files"}}).
				Run()

			empty, err := run.IsEmpty(ctx)
			require.NoError(t, err)
			require.False(t, empty)

			changesEmpty, err := run.Changes().IsEmpty(ctx)
			require.NoError(t, err)
			require.False(t, changesEmpty)
		}

		upstreamSvcA, engineSvcA, engineClientA := startEngineWithClientOpts(c, ctx, t, stateKey, clientOpts, engineWithPersistenceTestGC(ctx, t))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcA, engineSvcA, engineClientA) })

		randomA := runRandom(ctx, t, engineClientA)
		runGeneratorGroup(ctx, t, engineClientA)
		stopEngine(ctx, t, upstreamSvcA, engineSvcA, engineClientA)
		upstreamSvcA = nil
		engineSvcA = nil
		engineClientA = nil

		upstreamSvcB, engineSvcB, engineClientB := startEngineWithClientOpts(c, ctx, t, stateKey, clientOpts, engineWithPersistenceTestGC(ctx, t))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcB, engineSvcB, engineClientB) })

		randomB := runRandom(ctx, t, engineClientB)
		require.Equal(t, randomA, randomB, "unrelated withExec output should survive engine restart after a generator group graph")
	})

	t.Run("function cache control survives restart", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		stateKey := "phase7-function-cache-state-" + identity.NewID()
		moduleSrc := `package main

import "crypto/rand"

type Test struct{}

func (m *Test) TestAlwaysCache() string {
	return rand.Text()
}
`

		upstreamSvcA, engineSvcA, engineClientA := startEngine(c, ctx, t, stateKey, engineWithPersistenceTestGC(ctx, t))
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

		upstreamSvcB, engineSvcB, engineClientB := startEngine(c, ctx, t, stateKey, engineWithPersistenceTestGC(ctx, t))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcB, engineSvcB, engineClientB) })

		modB := modInit(t, engineClientB, "go", moduleSrc)
		outB, err := modB.
			WithEnvVariable("CACHE_BUST", identity.NewID()).
			With(daggerCall("test-always-cache")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, outA, outB, "always-cached function result should survive engine restart")
	})

	t.Run("typescript function cache control survives restart", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		stateKey := "phase7-typescript-function-cache-state-" + identity.NewID()
		moduleSrc := `import crypto from "crypto"

import { object, func } from "@dagger.io/dagger"

@object()
export class Test {
	@func()
	testAlwaysCache(): string {
		return crypto.randomBytes(16).toString("hex")
	}
}
`

		upstreamSvcA, engineSvcA, engineClientA := startEngine(c, ctx, t, stateKey, engineWithPersistenceTestGC(ctx, t))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcA, engineSvcA, engineClientA) })

		modA := modInit(t, engineClientA, "typescript", moduleSrc)
		outA, err := modA.
			WithEnvVariable("CACHE_BUST", identity.NewID()).
			With(daggerCall("test-always-cache")).
			Stdout(ctx)
		require.NoError(t, err)
		stopEngine(ctx, t, upstreamSvcA, engineSvcA, engineClientA)
		upstreamSvcA = nil
		engineSvcA = nil
		engineClientA = nil

		upstreamSvcB, engineSvcB, engineClientB := startEngine(c, ctx, t, stateKey, engineWithPersistenceTestGC(ctx, t))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcB, engineSvcB, engineClientB) })

		modB := modInit(t, engineClientB, "typescript", moduleSrc)
		outB, err := modB.
			WithEnvVariable("CACHE_BUST", identity.NewID()).
			With(daggerCall("test-always-cache")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, outA, outB, "always-cached TypeScript function result should survive engine restart")
	})

	t.Run("contextual function cache survives restart", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
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

		upstreamSvcA, engineSvcA, engineClientA := startEngine(c, ctx, t, stateKey, engineWithPersistenceTestGC(ctx, t))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcA, engineSvcA, engineClientA) })

		outA := runCalls(ctx, t, engineClientA)
		stopEngine(ctx, t, upstreamSvcA, engineSvcA, engineClientA)
		upstreamSvcA = nil
		engineSvcA = nil
		engineClientA = nil

		upstreamSvcB, engineSvcB, engineClientB := startEngine(c, ctx, t, stateKey, engineWithPersistenceTestGC(ctx, t))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcB, engineSvcB, engineClientB) })

		outB := runCalls(ctx, t, engineClientB)
		require.Equal(t, outA, outB, "contextual function results should survive engine restart")
	})

	t.Run("container withExec output on host mount survives restart", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
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

		upstreamSvcA, engineSvcA, engineClientA := startEngine(c, ctx, t, stateKey, engineWithPersistenceTestGC(ctx, t))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcA, engineSvcA, engineClientA) })

		randomA := runChain(ctx, t, engineClientA, hostDirA)
		stopEngine(ctx, t, upstreamSvcA, engineSvcA, engineClientA)
		upstreamSvcA = nil
		engineSvcA = nil
		engineClientA = nil

		upstreamSvcB, engineSvcB, engineClientB := startEngine(c, ctx, t, stateKey, engineWithPersistenceTestGC(ctx, t))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcB, engineSvcB, engineClientB) })

		randomB := runChain(ctx, t, engineClientB, hostDirB)
		require.Equal(t, randomA, randomB, "withExec output should survive engine restart for equivalent host-mounted input")
	})

	t.Run("container withExec output on host mounted file survives restart", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		stateKey := "phase7-container-host-file-state-" + identity.NewID()

		hostDirA := t.TempDir()
		hostFileA := filepath.Join(hostDirA, "input.txt")
		require.NoError(t, os.WriteFile(hostFileA, []byte("same-content\n"), 0o600))
		hostDirB := t.TempDir()
		hostFileB := filepath.Join(hostDirB, "input.txt")
		require.NoError(t, os.WriteFile(hostFileB, []byte("same-content\n"), 0o600))

		runChain := func(ctx context.Context, t *testctx.T, engineClient *dagger.Client, hostPath string) string {
			t.Helper()
			workDir := engineClient.
				Container().
				From(alpineImage).
				WithMountedFile("/src/input.txt", engineClient.Host().File(hostPath)).
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

		upstreamSvcA, engineSvcA, engineClientA := startEngine(c, ctx, t, stateKey, engineWithPersistenceTestGC(ctx, t))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcA, engineSvcA, engineClientA) })

		randomA := runChain(ctx, t, engineClientA, hostFileA)
		stopEngine(ctx, t, upstreamSvcA, engineSvcA, engineClientA)
		upstreamSvcA = nil
		engineSvcA = nil
		engineClientA = nil

		upstreamSvcB, engineSvcB, engineClientB := startEngine(c, ctx, t, stateKey, engineWithPersistenceTestGC(ctx, t))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcB, engineSvcB, engineClientB) })

		randomB := runChain(ctx, t, engineClientB, hostFileB)
		require.Equal(t, randomA, randomB, "withExec output should survive engine restart for equivalent host-mounted file input")
	})

	t.Run("container child exec during concurrent mounted directory parent eval", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		stateKey := "phase7-mounted-dir-parent-eval-race-state-" + identity.NewID()

		hostDir := t.TempDir()
		gitDir := filepath.Join(hostDir, ".git")
		require.NoError(t, os.MkdirAll(gitDir, 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o600))

		upstreamSvc, engineSvc, engineClient := startEngine(c, ctx, t, stateKey, engineWithPersistenceTestGC(ctx, t))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvc, engineSvc, engineClient) })

		base := engineClient.Container().From(alpineImage)
		for i := range 24 {
			src := engineClient.Directory().WithNewFile("file.txt", fmt.Sprintf("slow-clone-%d\n", i))
			base = base.WithMountedDirectory(fmt.Sprintf("/slow/%02d", i), src)
		}
		var err error
		base, err = base.Sync(ctx)
		require.NoError(t, err)

		secret := engineClient.SetSecret("mounted-dir-parent-eval-race-"+identity.NewID(), "secret")
		source := engineClient.Host().Directory(gitDir)

		for attempt := range 50 {
			parent := base.
				WithMountedCache("/root/.cache/uv", engineClient.CacheVolume("phase7-race-uv-"+identity.NewID())).
				WithMountedCache("/var/cache/foobar/plugins", engineClient.CacheVolume("phase7-race-foobar-"+identity.NewID())).
				WithWorkdir("/work").
				WithMountedDirectory(".git", source).
				WithSecretVariable("FOOBAR_TOKEN", secret)

			parentID, err := parent.ID(ctx)
			require.NoError(t, err)

			start := make(chan struct{})
			var eg errgroup.Group
			eg.Go(func() error {
				<-start
				_, err := dagger.Ref[*dagger.Container](engineClient, parentID).Sync(ctx)
				return err
			})
			for worker := range 8 {
				worker := worker
				eg.Go(func() error {
					<-start
					if worker > 0 {
						time.Sleep(time.Duration(worker) * time.Millisecond)
					}
					out, err := dagger.Ref[*dagger.Container](engineClient, parentID).
						WithEnvVariable("CACHE_BUSTER", fmt.Sprintf("%d-%d", attempt, worker)).
						WithExec([]string{"sh", "-ec", "cat .git/HEAD"}).
						Stdout(ctx)
					if err != nil {
						return fmt.Errorf("attempt %d worker %d: %w", attempt, worker, err)
					}
					if out != "ref: refs/heads/main\n" {
						return fmt.Errorf("attempt %d worker %d: unexpected HEAD contents %q", attempt, worker, out)
					}
					return nil
				})
			}
			close(start)

			require.NoError(t, eg.Wait(), "attempt %d", attempt)
		}
	})

	t.Run("git repository and ref survive restart", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		stateKey := "phase7-git-restart-state-" + identity.NewID()
		repoDir := t.TempDir()

		runGit := func(args ...string) {
			t.Helper()
			cmd := exec.Command("git", args...)
			cmd.Dir = repoDir
			out, err := cmd.CombinedOutput()
			require.NoError(t, err, string(out))
		}

		runGit("init")
		runGit("config", "user.email", "dagger@example.com")
		runGit("config", "user.name", "Dagger Tests")
		require.NoError(t, os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("git persistence\n"), 0o600))
		runGit("add", "README.md")
		runGit("commit", "-m", "initial commit")

		type gitRunOutput struct {
			commit  string
			random  string
			readme  string
			layered string
			summary string
		}

		runChain := func(ctx context.Context, t *testctx.T, engineClient *dagger.Client, layerExtra bool) gitRunOutput {
			t.Helper()

			repo := engineClient.Host().Directory(repoDir).AsGit()
			ref := repo.Head()
			commitFromRef, err := ref.Commit(ctx)
			require.NoError(t, err)

			ctr := engineClient.
				Container().
				From(alpineImage).
				WithExec([]string{"apk", "add", "git"}).
				WithMountedDirectory("/repo", ref.Tree()).
				WithExec([]string{"sh", "-ec", `
set -eu
mkdir -p /work
git -C /repo rev-parse HEAD > /work/commit.txt
cat /repo/README.md > /work/readme.txt
head -c 32 /dev/urandom | sha256sum | cut -d' ' -f1 > /work/random.txt
`})

			if layerExtra {
				ctr = ctr.WithExec([]string{"sh", "-ec", `
set -eu
printf 'layered\n' > /work/layered.txt
{
  printf 'commit='
  tr -d '\n' < /work/commit.txt
  printf '\nrandom='
  tr -d '\n' < /work/random.txt
  printf '\n'
} > /work/summary.txt
`})
			}

			workDir := ctr.Directory("/work")

			commitFromWorktree, err := workDir.File("commit.txt").Contents(ctx)
			require.NoError(t, err)
			commitFromWorktree = strings.TrimSpace(commitFromWorktree)
			require.Equal(t, commitFromRef, commitFromWorktree)

			randomContents, err := workDir.File("random.txt").Contents(ctx)
			require.NoError(t, err)

			readmeContents, err := workDir.File("readme.txt").Contents(ctx)
			require.NoError(t, err)

			out := gitRunOutput{
				commit: commitFromWorktree,
				random: strings.TrimSpace(randomContents),
				readme: readmeContents,
			}
			if layerExtra {
				layeredContents, err := workDir.File("layered.txt").Contents(ctx)
				require.NoError(t, err)
				summaryContents, err := workDir.File("summary.txt").Contents(ctx)
				require.NoError(t, err)
				out.layered = strings.TrimSpace(layeredContents)
				out.summary = summaryContents
			}
			return out
		}

		upstreamSvcA, engineSvcA, engineClientA := startEngine(c, ctx, t, stateKey, engineWithPersistenceTestGC(ctx, t))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcA, engineSvcA, engineClientA) })

		outA := runChain(ctx, t, engineClientA, false)
		stopEngine(ctx, t, upstreamSvcA, engineSvcA, engineClientA)
		upstreamSvcA = nil
		engineSvcA = nil
		engineClientA = nil

		upstreamSvcB, engineSvcB, engineClientB := startEngine(c, ctx, t, stateKey, engineWithPersistenceTestGC(ctx, t))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcB, engineSvcB, engineClientB) })

		outB := runChain(ctx, t, engineClientB, true)
		require.Equal(t, outA.commit, outB.commit, "git ref commit should survive engine restart")
		require.Equal(t, outA.random, outB.random, "git-backed withExec result should survive engine restart")
		require.Equal(t, outA.readme, outB.readme, "mounted git tree contents should survive engine restart")
		require.Equal(t, "layered", outB.layered, "new withExec should still apply on top of the cached git-backed state")
		require.Contains(t, outB.summary, "commit="+outB.commit)
		require.Contains(t, outB.summary, "random="+outB.random)
	})

	t.Run("engine-dev container build survives restart", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		stateKey := "phase7-engine-dev-build-state-" + identity.NewID()
		repoDir := c.Host().Directory("/app")
		writeRandomScript := "set -eu\nhead -c 32 /dev/urandom | sha256sum | cut -d' ' -f1 > /tmp/random\n"
		writeSummaryScript := "set -eu\ntest -x /usr/local/bin/dagger-engine\nprintf '%s|layered\\n' \"$(cat /tmp/random)\" > /tmp/summary\n"

		type startedDevEngine struct {
			service  *dagger.Service
			endpoint string
		}

		startDevEngine := func(ctx context.Context, t *testctx.T, bootID string) *startedDevEngine {
			t.Helper()

			engineCtr := devEngineContainerWithStateKey(
				c,
				stateKey,
				engineWithPersistenceTestGC(ctx, t),
				func(ctr *dagger.Container) *dagger.Container {
					return ctr.WithEnvVariable("_DAGGER_EGRAPH_BOOT_ID", bootID)
				},
			)
			service := devEngineContainerAsService(engineCtr)

			endpoint, err := service.Endpoint(ctx, dagger.ServiceEndpointOpts{Scheme: "tcp"})
			require.NoError(t, err)

			return &startedDevEngine{
				service:  service,
				endpoint: endpoint,
			}
		}

		stopDevEngine := func(ctx context.Context, t *testctx.T, engine *startedDevEngine) {
			t.Helper()
			if engine == nil {
				return
			}
			if engine.service != nil {
				_, err := engine.service.Stop(ctx)
				require.NoError(t, err)
			}
		}

		runCLI := func(ctx context.Context, t *testctx.T, engine *startedDevEngine, sourceMutation string, args ...string) (string, error) {
			t.Helper()

			daggerCli := daggerCliFile(t, c)
			execArgs := append([]string{"/bin/dagger"}, args...)
			ctr := c.Container().From(alpineImage).
				WithServiceBinding("dev-engine", engine.service).
				WithMountedFile("/bin/dagger", daggerCli).
				WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", "/bin/dagger").
				WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", engine.endpoint).
				WithDirectory("/app", repoDir).
				WithWorkdir("/app").
				WithEnvVariable("CACHE_BUST", rand.Text())

			if sourceMutation != "" {
				ctr = ctr.WithEnvVariable("SOURCE_MUTATION", sourceMutation).WithExec([]string{
					"sh",
					"-ec",
					"printf '%s' \"$SOURCE_MUTATION\" >> /app/dagql/cache.go",
				})
			}

			ctr = ctr.WithExec(execArgs)

			stdout, err := ctr.Stdout(ctx)
			return strings.TrimSpace(stdout), err
		}

		runEngineDevRandom := func(ctx context.Context, t *testctx.T, engine *startedDevEngine, sourceMutation string) (string, error) {
			t.Helper()
			return runCLI(
				ctx,
				t,
				engine,
				sourceMutation,
				"call", "engine-dev", "container",
				"with-exec", "--args", "true",
				"with-new-file", "--path", "/tmp/write-random.sh", "--contents", writeRandomScript,
				"with-exec", "--args", "sh,/tmp/write-random.sh",
				"file", "--path", "/tmp/random",
				"contents",
			)
		}

		engineABootID := "phase7-engine-dev-build-engine-a"
		engineBBootID := "phase7-engine-dev-build-engine-b"
		engineCBootID := "phase7-engine-dev-build-engine-c"

		engineA := startDevEngine(ctx, t, engineABootID)
		t.Cleanup(func() { stopDevEngine(ctx, t, engineA) })

		randomA, err := runEngineDevRandom(ctx, t, engineA, "")
		require.NoError(t, err)
		require.NotEmpty(t, randomA)

		randomASecondSession, err := runEngineDevRandom(ctx, t, engineA, "")
		require.NoError(t, err)
		require.Equal(t, randomA, randomASecondSession, "engine-dev container build result should survive a new session on the same engine before restart")

		stopDevEngine(ctx, t, engineA)
		engineA = nil

		engineB := startDevEngine(ctx, t, engineBBootID)
		t.Cleanup(func() { stopDevEngine(ctx, t, engineB) })

		randomB, err := runEngineDevRandom(ctx, t, engineB, "")
		require.NoError(t, err)
		require.Equal(t, randomA, randomB, "engine-dev container build result should survive engine restart")

		layered, err := runCLI(
			ctx,
			t,
			engineB,
			"",
			"call", "engine-dev", "container",
			"with-exec", "--args", "true",
			"with-new-file", "--path", "/tmp/write-random.sh", "--contents", writeRandomScript,
			"with-exec", "--args", "sh,/tmp/write-random.sh",
			"with-new-file", "--path", "/tmp/write-summary.sh", "--contents", writeSummaryScript,
			"with-exec", "--args", "sh,/tmp/write-summary.sh",
			"file", "--path", "/tmp/summary",
			"contents",
		)
		require.NoError(t, err)
		require.Equal(t, randomB+"|layered", layered, "new withExec should still apply on top of the cached engine-dev container build")

		cacheGoMutation := "\n// TestDiskPersistenceAcrossRestart mutation\n"
		randomBChanged, err := runEngineDevRandom(ctx, t, engineB, cacheGoMutation)
		require.NoError(t, err)
		require.NotEqual(t, randomB, randomBChanged, "engine-dev container build result should miss after the repo source changes")

		stopDevEngine(ctx, t, engineB)
		engineB = nil

		engineC := startDevEngine(ctx, t, engineCBootID)
		t.Cleanup(func() { stopDevEngine(ctx, t, engineC) })

		randomCChanged, err := runEngineDevRandom(ctx, t, engineC, cacheGoMutation)
		require.NoError(t, err)
		require.Equal(t, randomBChanged, randomCChanged, "engine-dev container build result should survive engine restart with the repo source change in place")

		randomCRestored, err := runEngineDevRandom(ctx, t, engineC, "")
		require.NoError(t, err)
		require.Equal(t, randomB, randomCRestored, "engine-dev container build result should survive engine restart without the repo source change")
	})

	t.Run("release publish dry-run rebuilds version after main publish", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		stateKey := "phase7-release-tag-version-state-" + identity.NewID()
		tag := "v9.9.9-repro"

		cwd, err := os.Getwd()
		require.NoError(t, err)
		sourcePath := cwd
		for {
			_, err := os.Stat(filepath.Join(sourcePath, "toolchains", "release", "dagger.json"))
			if err == nil {
				break
			}
			parent := filepath.Dir(sourcePath)
			require.NotEqual(t, sourcePath, parent, "could not find repository root from %s", cwd)
			sourcePath = parent
		}
		releaseSource := c.Host().Directory(sourcePath, dagger.HostDirectoryOpts{
			Exclude: []string{".git"},
			NoCache: true,
		}).WithNewFile(".release-cache-repro", identity.NewID()+"\n")

		gitDir := c.Container().
			From(alpineImage).
			WithExec([]string{"apk", "add", "git"}).
			WithDirectory("/root/repo", releaseSource).
			WithNewFile("/root/create-release-repo.sh", `#!/bin/sh
set -e -u -x

git config --global user.email "test@dagger.io"
git config --global user.name "Test User"
git config --global init.defaultBranch main

cd /root/repo
git init
git checkout -B main
git add -A
git commit -m "initial release source"

mkdir -p /root/srv
git clone --no-local --bare . /root/srv/repo.git
git -C /root/srv/repo.git config http.receivepack true
git -C /root/srv/repo.git update-server-info
`).
			WithExec([]string{"sh", "/root/create-release-repo.sh"}).
			Directory("/root/srv")

		hostname := identity.NewID() + ".test"
		gitSvc, repoBaseURL := gitSmartHTTPServiceDirAuth(ctx, t, c, hostname, gitDir, "", nil)
		repoURL := repoBaseURL + "/repo.git"
		gitHost := moduleResolveServiceHost(t, repoURL)

		gitSvc, err = gitSvc.Start(ctx)
		require.NoError(t, err)
		t.Cleanup(func() {
			_, err := gitSvc.Stop(ctx)
			require.NoError(t, err)
		})

		gitClient := func() *dagger.Container {
			return c.Container().
				From(alpineImage).
				WithExec([]string{"apk", "add", "git"}).
				With(gitUserConfig).
				WithServiceBinding(gitHost, gitSvc).
				WithWorkdir("/src")
		}
		gitStdout := func(ctx context.Context, t *testctx.T, script string) string {
			t.Helper()
			out, err := gitClient().
				WithEnvVariable("REPO_URL", repoURL).
				WithEnvVariable("RELEASE_TAG", tag).
				WithEnvVariable("GIT_CACHE_BUSTER", identity.NewID()).
				WithExec([]string{"sh", "-ec", script}).
				Stdout(ctx)
			require.NoError(t, err, out)
			return strings.TrimSpace(out)
		}

		commit := gitStdout(ctx, t, `git clone "$REPO_URL" .
git rev-parse HEAD
`)
		require.NotEmpty(t, commit)
		moduleRef := repoURL + "@" + commit

		type startedReleaseEngine struct {
			service  *dagger.Service
			endpoint string
		}

		startReleaseEngine := func(ctx context.Context, t *testctx.T) *startedReleaseEngine {
			t.Helper()

			engineCtr := devEngineContainerWithStateKey(c, stateKey, engineWithPersistenceTestGC(ctx, t)).
				WithServiceBinding(gitHost, gitSvc)
			service := devEngineContainerAsService(engineCtr)

			endpoint, err := service.Endpoint(ctx, dagger.ServiceEndpointOpts{Scheme: "tcp"})
			require.NoError(t, err)

			return &startedReleaseEngine{
				service:  service,
				endpoint: endpoint,
			}
		}

		stopReleaseEngine := func(ctx context.Context, t *testctx.T, engine *startedReleaseEngine) {
			t.Helper()
			if engine == nil || engine.service == nil {
				return
			}
			_, err := engine.service.Stop(ctx)
			require.NoError(t, err)
		}

		runReleasePublish := func(ctx context.Context, t *testctx.T, engine *startedReleaseEngine, releaseTag string) string {
			t.Helper()

			daggerCli := daggerCliFile(t, c)
			script := `set +e
/bin/dagger --progress=plain -m "$MODULE_REF" call release publish --tag "$RELEASE_TAG" --commit "$RELEASE_COMMIT" --dry-run=true markdown > /tmp/publish.log 2>&1
status=$?
cat /tmp/publish.log
exit "$status"
`
			out, err := c.Container().From(alpineImage).
				WithServiceBinding("dev-engine", engine.service).
				WithServiceBinding(gitHost, gitSvc).
				WithMountedFile("/bin/dagger", daggerCli).
				WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", "/bin/dagger").
				WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", engine.endpoint).
				WithWorkdir("/app").
				WithEnvVariable("MODULE_REF", moduleRef).
				WithEnvVariable("RELEASE_TAG", releaseTag).
				WithEnvVariable("RELEASE_COMMIT", commit).
				WithEnvVariable("PUBLISH_RUN_CACHE_BUSTER", identity.NewID()).
				WithExec([]string{"sh", "-ec", script}).
				Stdout(ctx)
			require.NoError(t, err, out)
			return out
		}
		tagsVisibleInPublishContainer := func(ctx context.Context, t *testctx.T) string {
			t.Helper()
			return gitStdout(ctx, t, `git ls-remote --tags "$REPO_URL" "$RELEASE_TAG"`)
		}

		engine := startReleaseEngine(ctx, t)
		t.Cleanup(func() { stopReleaseEngine(ctx, t, engine) })

		require.NotContains(t, tagsVisibleInPublishContainer(ctx, t), tag, "release tag should not be visible before it is created")
		initialOut := runReleasePublish(ctx, t, engine, "main")
		require.Contains(t, initialOut, "- [x] 🚙 Engine", "initial main dry-run should build the engine")
		require.Contains(t, initialOut, "- [x] 🚗 CLI", "initial main dry-run should build the CLI")

		gitStdout(ctx, t, `git clone "$REPO_URL" .
git tag "$RELEASE_TAG" "`+commit+`"
git push origin "$RELEASE_TAG"
git ls-remote --tags origin "$RELEASE_TAG"
	`)

		require.Contains(t, tagsVisibleInPublishContainer(ctx, t), tag, "release tag should be visible in the publish container before the second dry-run")
		taggedOut := runReleasePublish(ctx, t, engine, tag)
		require.Contains(t, taggedOut, fmt.Sprintf("- [x] 🚙 Engine ([`%s`]", tag), "release dry-run should validate built engine version")
		require.Contains(t, taggedOut, fmt.Sprintf("- [x] 🚗 CLI ([`%s`]", tag), "release dry-run should validate built CLI version")
		require.NotContains(t, taggedOut, "Error while publishing", "release dry-run should validate built engine and CLI versions")
	})

	t.Run("cache volume survives restart", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		stateKey := "phase7-cache-volume-state-" + identity.NewID()
		cacheKey := "phase7-cache-volume-data-" + identity.NewID()
		cacheValue := identity.NewID()

		upstreamSvcA, engineSvcA, engineClientA := startEngine(c, ctx, t, stateKey, engineWithPersistenceTestGC(ctx, t))
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

		upstreamSvcB, engineSvcB, engineClientB := startEngine(c, ctx, t, stateKey, engineWithPersistenceTestGC(ctx, t))
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

	t.Run("source-backed cache volume supports concurrent mounts after restart", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		stateKey := "phase7-source-cache-volume-state-" + identity.NewID()
		cacheKey := "phase7-source-cache-volume-data-" + identity.NewID()

		cacheSource := func(client *dagger.Client) *dagger.Directory {
			return client.
				Container().
				From(alpineImage).
				WithNewFile("/cache-source/seed.txt", "seed\n").
				Directory("/cache-source")
		}

		mountSourceCache := func(client *dagger.Client) *dagger.Container {
			return client.
				Container().
				From(alpineImage).
				WithMountedCache(
					"/mnt/cache",
					client.CacheVolume(cacheKey),
					dagger.ContainerWithMountedCacheOpts{Source: cacheSource(client)},
				)
		}

		upstreamSvcA, engineSvcA, engineClientA := startEngine(c, ctx, t, stateKey, engineWithPersistenceTestGC(ctx, t))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcA, engineSvcA, engineClientA) })

		outA, err := mountSourceCache(engineClientA).
			WithExec([]string{"sh", "-ec", "cat /mnt/cache/seed.txt; echo initialized >> /mnt/cache/log.txt"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "seed\n", outA)

		stopEngine(ctx, t, upstreamSvcA, engineSvcA, engineClientA)
		upstreamSvcA = nil
		engineSvcA = nil
		engineClientA = nil

		upstreamSvcB, engineSvcB, engineClientB := startEngine(c, ctx, t, stateKey, engineWithPersistenceTestGC(ctx, t))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcB, engineSvcB, engineClientB) })

		var eg errgroup.Group
		for i := range 8 {
			eg.Go(func() error {
				out, err := mountSourceCache(engineClientB).
					WithEnvVariable("WORKER", fmt.Sprint(i)).
					WithExec([]string{"sh", "-ec", "cat /mnt/cache/seed.txt; sleep 2; echo \"$WORKER\" >> /mnt/cache/log.txt"}).
					Stdout(ctx)
				if err != nil {
					return fmt.Errorf("worker %d: %w", i, err)
				}
				if out != "seed\n" {
					return fmt.Errorf("worker %d: expected seed output, got %q", i, out)
				}
				return nil
			})
		}
		require.NoError(t, eg.Wait())
	})
}
