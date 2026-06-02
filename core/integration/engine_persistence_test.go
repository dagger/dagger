package core

// These tests cover engine state that must persist across restarts. They verify
// disk persistence for cached module calls and context inputs.
//
// See also:
// - engine_test.go: engine lifecycle behavior.
// - cross_session_test.go: behavior across Dagger sessions.

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/engine/config"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"dagger.io/dagger"
)

func moduleResolveServiceHost(t *testctx.T, rawURL string) string {
	t.Helper()

	parsed, err := url.Parse(rawURL)
	require.NoError(t, err)

	host := parsed.Hostname()
	require.NotEmpty(t, host)
	return host
}

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

		upstreamSvcA, engineSvcA, engineClientA := startEngine(c, ctx, t, stateKey, engineWithPersistenceTestGC(ctx, t))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcA, engineSvcA, engineClientA) })

		modA := moduleFixture(t, engineClientA, "go/cache-random")
		outA, err := modA.
			WithEnvVariable("CACHE_BUST", identity.NewID()).
			With(daggerCallAt(".", "test-always-cache")).
			Stdout(ctx)
		require.NoError(t, err)
		stopEngine(ctx, t, upstreamSvcA, engineSvcA, engineClientA)
		upstreamSvcA = nil
		engineSvcA = nil
		engineClientA = nil

		upstreamSvcB, engineSvcB, engineClientB := startEngine(c, ctx, t, stateKey, engineWithPersistenceTestGC(ctx, t))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcB, engineSvcB, engineClientB) })

		modB := moduleFixture(t, engineClientB, "go/cache-random")
		outB, err := modB.
			WithEnvVariable("CACHE_BUST", identity.NewID()).
			With(daggerCallAt(".", "test-always-cache")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, outA, outB, "always-cached function result should survive engine restart")
	})

	t.Run("typescript function cache control survives restart", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		stateKey := "phase7-typescript-function-cache-state-" + identity.NewID()

		upstreamSvcA, engineSvcA, engineClientA := startEngine(c, ctx, t, stateKey, engineWithPersistenceTestGC(ctx, t))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcA, engineSvcA, engineClientA) })

		modA := moduleFixture(t, engineClientA, "typescript/runtime-cache-control")
		outA, err := modA.
			WithEnvVariable("CACHE_BUST", identity.NewID()).
			With(daggerCallAt(".", "test-always-cache")).
			Stdout(ctx)
		require.NoError(t, err)
		stopEngine(ctx, t, upstreamSvcA, engineSvcA, engineClientA)
		upstreamSvcA = nil
		engineSvcA = nil
		engineClientA = nil

		upstreamSvcB, engineSvcB, engineClientB := startEngine(c, ctx, t, stateKey, engineWithPersistenceTestGC(ctx, t))
		t.Cleanup(func() { stopEngine(ctx, t, upstreamSvcB, engineSvcB, engineClientB) })

		modB := moduleFixture(t, engineClientB, "typescript/runtime-cache-control")
		outB, err := modB.
			WithEnvVariable("CACHE_BUST", identity.NewID()).
			With(daggerCallAt(".", "test-always-cache")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, outA, outB, "always-cached TypeScript function result should survive engine restart")
	})

	t.Run("contextual function cache survives restart", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		stateKey := "phase7-contextual-function-cache-state-" + identity.NewID()

		getMod := func(client *dagger.Client) *dagger.Container {
			return moduleFixture(t, client, "go/contextual-cache").
				WithEnvVariable("GIT_AUTHOR_DATE", "2000-01-01T00:00:00Z").
				WithEnvVariable("GIT_COMMITTER_DATE", "2000-01-01T00:00:00Z").
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
				out, err := mod.With(daggerCallAt(".", fn)).Stdout(ctx)
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

	t.Run("release publish non-dry uses mock endpoints after main publish", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		stateKey := "phase7-release-nondry-version-state-" + identity.NewID()
		releaseTag := "v0.21.3"
		releaseVersion := strings.TrimPrefix(releaseTag, "v")
		awsBucket := "dagger-release-test-" + strings.ToLower(identity.NewID())
		registryUsername := "john"
		registryPassword := "xFlejaPdjrt25Dvr"

		goreleaserKey := lookupEnvFileValue(t, "GORELEASER_KEY")
		if goreleaserKey == "" {
			t.Skip("GORELEASER_KEY is required in the test env file for the non-dry release publish test")
		}

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
		})

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
for repo in repo.git go-sdk.git php-sdk.git; do
	if [ "$repo" != "repo.git" ]; then
		git init --bare "/root/srv/$repo"
	fi
	git -C "/root/srv/$repo" config http.receivepack true
	git -C "/root/srv/$repo" update-server-info
done
`).
			WithExec([]string{"sh", "/root/create-release-repo.sh"}).
			Directory("/root/srv")

		gitHostname := identity.NewID() + ".test"
		gitSvc, repoBaseURL := gitSmartHTTPServiceDirAuth(ctx, t, c, gitHostname, gitDir, "", nil)
		repoURL := repoBaseURL + "/repo.git"
		goSDKDestRemote := repoBaseURL + "/go-sdk.git"
		phpSDKDestRemote := repoBaseURL + "/php-sdk.git"
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
				WithEnvVariable("RELEASE_TAG", releaseTag).
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

		registrySvc := c.Container().
			From("registry:3").
			WithNewFile("/auth/htpasswd", registryUsername+":$2y$05$/iP8ud0Fs8o3NLlElyfVVOp6LesJl3oRLYoc3neArZKWX10OhynSC").
			WithEnvVariable("REGISTRY_AUTH", "htpasswd").
			WithEnvVariable("REGISTRY_AUTH_HTPASSWD_REALM", "Registry Realm").
			WithEnvVariable("REGISTRY_AUTH_HTPASSWD_PATH", "/auth/htpasswd").
			WithExposedPort(5000, dagger.ContainerWithExposedPortOpts{Protocol: dagger.NetworkProtocolTcp}).
			AsService()
		registrySvc, err = registrySvc.Start(ctx)
		require.NoError(t, err)
		t.Cleanup(func() {
			_, err := registrySvc.Stop(ctx)
			require.NoError(t, err)
		})

		motoSvc := c.Container().
			From("motoserver/moto:latest").
			WithExposedPort(5000, dagger.ContainerWithExposedPortOpts{Protocol: dagger.NetworkProtocolTcp}).
			AsService(dagger.ContainerAsServiceOpts{UseEntrypoint: true})
		motoSvc, err = motoSvc.Start(ctx)
		require.NoError(t, err)
		t.Cleanup(func() {
			_, err := motoSvc.Stop(ctx)
			require.NoError(t, err)
		})

		verdaccioConfig := `storage: /verdaccio/storage
auth:
  htpasswd:
    file: /verdaccio/conf/htpasswd
    max_users: -1
uplinks: {}
packages:
  '@*/*':
    access: $all
    publish: $all
    unpublish: $all
  '**':
    access: $all
    publish: $all
    unpublish: $all
log: { type: stdout, format: pretty, level: http }
`
		verdaccioSvc := c.Container().
			From("verdaccio/verdaccio:5").
			WithNewFile("/verdaccio/conf/config.yaml", verdaccioConfig).
			WithExposedPort(4873, dagger.ContainerWithExposedPortOpts{Protocol: dagger.NetworkProtocolTcp}).
			AsService(dagger.ContainerAsServiceOpts{UseEntrypoint: true})
		verdaccioSvc, err = verdaccioSvc.Start(ctx)
		require.NoError(t, err)
		t.Cleanup(func() {
			_, err := verdaccioSvc.Stop(ctx)
			require.NoError(t, err)
		})

		certGen := newGeneratedCerts(c, "ca")
		githubCert, githubKey := certGen.newServerCerts("github.test")
		mockRecords := c.CacheVolume("phase7-release-mock-records-" + identity.NewID())
		mockServerScript := `import http.server
import json
import os
import ssl
import threading
import time

records_path = "/records/events.jsonl"
os.makedirs(os.path.dirname(records_path), exist_ok=True)

def record(kind, handler, body):
    with open(records_path, "a", encoding="utf-8") as f:
        f.write(json.dumps({
            "time": time.time(),
            "kind": kind,
            "method": handler.command,
            "path": handler.path,
            "body_len": len(body),
        }, sort_keys=True) + "\n")

class Handler(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, fmt, *args):
        return

    def read_body(self):
        length = int(self.headers.get("content-length", "0") or "0")
        return self.rfile.read(length) if length else b""

    def send_bytes(self, status, body, content_type="application/json"):
        self.send_response(status)
        self.send_header("content-type", content_type)
        self.send_header("content-length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def send_json(self, status, data):
        self.send_bytes(status, json.dumps(data).encode("utf-8"))

    def do_HEAD(self):
        record("head", self, b"")
        self.send_bytes(200, b"")

    def do_GET(self):
        if self.path.startswith("/netlify/api/v1/sites/docs.dagger.io/deploys"):
            record("netlify_list_deploys", self, b"")
            self.send_json(200, [{"id": "deploy-1"}])
            return
        if self.path == "/api/v3/rate_limit":
            record("github_rate_limit", self, b"")
            self.send_json(200, {
                "resources": {"core": {"limit": 5000, "remaining": 4999, "reset": int(time.time()) + 3600}},
                "rate": {"limit": 5000, "remaining": 4999, "reset": int(time.time()) + 3600},
            })
            return
        if self.path.startswith("/api/v3/repos/") and "/releases/tags/" in self.path:
            record("github_release_lookup", self, b"")
            self.send_json(404, {"message": "Not Found"})
            return
        if self.path in ("/api/v3", "/api/v3/"):
            record("github_api_root", self, b"")
            self.send_json(200, {"verifiable_password_authentication": False})
            return
        if self.path.startswith("/api/v3/repos/") and "/contents/" in self.path:
            record("github_content_lookup", self, b"")
            self.send_json(404, {"message": "Not Found"})
            return
        if self.path.startswith("/api/v3/repos/") and "/branches/" in self.path:
            record("github_branch_lookup", self, b"")
            self.send_json(404, {"message": "Not Found"})
            return
        if self.path.startswith("/api/v3/repos/") and "/git/ref/heads/" in self.path:
            record("github_ref_lookup", self, b"")
            self.send_json(200, {"ref": "refs/heads/main", "object": {"sha": "1111111111111111111111111111111111111111"}})
            return
        if self.path.startswith("/api/v3/repos/"):
            record("github_repo", self, b"")
            parts = self.path.split("/")
            owner = parts[4] if len(parts) > 4 else "dagger"
            name = parts[5] if len(parts) > 5 else "dagger"
            default_branch = "master" if name == "winget-pkgs" else "main"
            self.send_json(200, {"full_name": owner + "/" + name, "default_branch": default_branch})
            return
        record("get", self, b"")
        self.send_json(200, {})

    def do_POST(self):
        body = self.read_body()
        if self.path.startswith("/netlify/api/v1/sites/docs.dagger.io/deploys/") and self.path.endswith("/restore"):
            record("netlify_restore", self, body)
            self.send_json(200, {"id": "deploy-1"})
            return
        if self.path.startswith("/pypi/"):
            record("pypi_publish", self, body)
            self.send_bytes(200, b"OK", "text/plain")
            return
        if self.path == "/api/v3/repos/dagger/dagger/releases":
            record("github_release_create", self, body)
            payload = json.loads(body.decode("utf-8") or "{}")
            tag = payload.get("tag_name", "")
            self.send_json(201, {
                "id": int(time.time() * 1000),
                "tag_name": tag,
                "name": payload.get("name", tag),
                "html_url": "https://github.test/dagger/dagger/releases/tag/" + tag,
                "upload_url": "https://github.test/api/uploads/repos/dagger/dagger/releases/1/assets{?name,label}",
            })
            return
        if self.path.startswith("/api/uploads/repos/dagger/dagger/releases/") and "/assets" in self.path:
            record("github_release_asset_upload", self, body)
            self.send_json(201, {"id": int(time.time() * 1000), "name": self.path.split("name=", 1)[-1].split("&", 1)[0]})
            return
        if self.path.startswith("/api/v3/repos/") and self.path.endswith("/merge-upstream"):
            record("github_merge_upstream", self, body)
            self.send_json(200, {"message": "mock merge upstream", "merge_type": "none", "base_branch": "master"})
            return
        if self.path.startswith("/api/v3/repos/") and self.path.endswith("/git/refs"):
            record("github_ref_create", self, body)
            self.send_json(201, {"ref": "refs/heads/mock", "object": {"sha": "1111111111111111111111111111111111111111"}})
            return
        if self.path.startswith("/api/v3/repos/") and self.path.endswith("/pulls"):
            record("github_pull_request_create", self, body)
            self.send_json(201, {"html_url": "https://github.test/mock/pull/1", "number": 1})
            return
        record("post", self, body)
        self.send_json(200, {})

    def do_PUT(self):
        body = self.read_body()
        if self.path.startswith("/api/v3/repos/") and "/contents/" in self.path:
            record("github_content_write", self, body)
            self.send_json(201, {
                "content": {"path": self.path.split("/contents/", 1)[-1].split("?", 1)[0], "sha": "2222222222222222222222222222222222222222"},
                "commit": {"sha": "3333333333333333333333333333333333333333"},
            })
            return
        record("put", self, body)
        self.send_json(200, {})

    def do_PATCH(self):
        body = self.read_body()
        if self.path.startswith("/api/v3/repos/dagger/dagger/releases/"):
            record("github_release_publish", self, body)
            self.send_json(200, {"id": 1, "tag_name": "mock", "html_url": "https://github.test/dagger/dagger/releases/tag/mock"})
            return
        record("patch", self, body)
        self.send_json(200, {})

def serve_http():
    http.server.ThreadingHTTPServer(("0.0.0.0", 8080), Handler).serve_forever()

threading.Thread(target=serve_http, daemon=True).start()
https = http.server.ThreadingHTTPServer(("0.0.0.0", 443), Handler)
ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
ctx.load_cert_chain("/certs/github.test.crt", "/certs/github.test.key")
https.socket = ctx.wrap_socket(https.socket, server_side=True)
https.serve_forever()
`
		mockSvc := c.Container().
			From("python:3.12-alpine").
			WithMountedFile("/certs/github.test.crt", githubCert).
			WithMountedFile("/certs/github.test.key", githubKey).
			WithMountedCache("/records", mockRecords).
			WithNewFile("/server.py", mockServerScript).
			WithExposedPort(443, dagger.ContainerWithExposedPortOpts{Protocol: dagger.NetworkProtocolTcp}).
			WithExposedPort(8080, dagger.ContainerWithExposedPortOpts{Protocol: dagger.NetworkProtocolTcp}).
			WithEntrypoint([]string{"python", "/server.py"}).
			AsService(dagger.ContainerAsServiceOpts{UseEntrypoint: true})
		mockSvc, err = mockSvc.Start(ctx)
		require.NoError(t, err)
		t.Cleanup(func() {
			_, err := mockSvc.Stop(ctx)
			require.NoError(t, err)
		})

		awsEndpointURL := "http://moto:5000"
		awsCLI := func(opts ...dagger.ContainerOpts) *dagger.Container {
			containerOpts := dagger.ContainerOpts{}
			if len(opts) > 0 {
				containerOpts = opts[0]
			}
			return c.Container(containerOpts).
				From(alpineImage).
				WithExec([]string{"apk", "add", "aws-cli", "curl"}).
				WithServiceBinding("moto", motoSvc).
				WithEnvVariable("AWS_ACCESS_KEY_ID", "test").
				WithEnvVariable("AWS_SECRET_ACCESS_KEY", "test").
				WithEnvVariable("AWS_REGION", "us-east-1").
				WithEnvVariable("AWS_ENDPOINT_URL", awsEndpointURL).
				WithEnvVariable("AWS_EC2_METADATA_DISABLED", "true")
		}
		cloudfrontDistribution, err := awsCLI().
			WithEnvVariable("AWS_BUCKET", awsBucket).
			WithNewFile("/tmp/distribution.json", `{
  "CallerReference": "`+identity.NewID()+`",
  "Comment": "dagger release integration test",
  "Enabled": true,
  "Origins": {
    "Quantity": 1,
    "Items": [{
      "Id": "origin",
      "DomainName": "example.com",
      "CustomOriginConfig": {
        "HTTPPort": 80,
        "HTTPSPort": 443,
        "OriginProtocolPolicy": "http-only",
        "OriginSslProtocols": {"Quantity": 1, "Items": ["TLSv1.2"]}
      }
    }]
  },
  "DefaultCacheBehavior": {
    "TargetOriginId": "origin",
    "ViewerProtocolPolicy": "allow-all",
    "TrustedSigners": {"Enabled": false, "Quantity": 0},
    "ForwardedValues": {"QueryString": false, "Cookies": {"Forward": "none"}},
    "MinTTL": 0
  }
}`).
			WithExec([]string{"sh", "-ec", `
aws --endpoint-url "$AWS_ENDPOINT_URL" s3 mb "s3://$AWS_BUCKET" >/dev/null
aws --endpoint-url "$AWS_ENDPOINT_URL" cloudfront create-distribution \
  --distribution-config file:///tmp/distribution.json \
  --query 'Distribution.Id' --output text
`}).
			Stdout(ctx)
		require.NoError(t, err)
		cloudfrontDistribution = strings.TrimSpace(cloudfrontDistribution)
		require.NotEmpty(t, cloudfrontDistribution)

		type startedReleaseEngine struct {
			service  *dagger.Service
			endpoint string
		}

		startReleaseEngine := func(ctx context.Context, t *testctx.T) *startedReleaseEngine {
			t.Helper()

			engineCtr := devEngineContainerWithStateKey(c, stateKey,
				engineWithPersistenceTestGC(ctx, t),
				engineWithConfig(ctx, t, func(ctx context.Context, t *testctx.T, cfg config.Config) config.Config {
					if cfg.Registries == nil {
						cfg.Registries = map[string]config.RegistryConfig{}
					}
					cfg.Registries["registry:5000"] = config.RegistryConfig{PlainHTTP: ptr(true)}
					return cfg
				}),
				func(ctr *dagger.Container) *dagger.Container {
					return ctr.
						WithServiceBinding(gitHost, gitSvc).
						WithServiceBinding("registry", registrySvc).
						WithServiceBinding("moto", motoSvc).
						WithServiceBinding("verdaccio", verdaccioSvc).
						WithServiceBinding("mock", mockSvc).
						WithServiceBinding("github.test", mockSvc)
				},
			)
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

		runReleasePublish := func(ctx context.Context, t *testctx.T, engine *startedReleaseEngine, tag string) string {
			t.Helper()

			script := `set +e
/bin/dagger --progress=plain -m "$MODULE_REF" call release publish \
  --tag "$RELEASE_TAG" --commit "$RELEASE_COMMIT" \
  --registry-image "registry:5000/dagger/engine" \
  --registry-username "$REGISTRY_USERNAME" \
  --registry-password=env:REGISTRY_PASSWORD \
  --goreleaser-key=env:GORELEASER_KEY \
  --github-token=env:FAKE_GITHUB_TOKEN \
  --github-release-token=env:FAKE_GITHUB_TOKEN \
  --github-org-name "dagger" \
  --github-host "github.test" \
  --github-ca-cert "/github-ca.pem" \
  --netlify-token=env:FAKE_NETLIFY_TOKEN \
  --netlify-api-url "http://mock:8080/netlify/api/v1" \
  --pypi-token=env:FAKE_PYPI_TOKEN \
  --pypi-url "http://mock:8080/pypi/legacy/" \
  --npm-token=env:FAKE_NPM_TOKEN \
  --npm-registry-url "http://verdaccio:4873" \
  --aws-access-key-id=env:AWS_ACCESS_KEY_ID \
  --aws-secret-access-key=env:AWS_SECRET_ACCESS_KEY \
  --aws-region "us-east-1" \
  --aws-bucket "$AWS_BUCKET" \
  --aws-cloudfront-distribution "$AWS_CLOUDFRONT_DISTRIBUTION" \
  --aws-endpoint-url "http://moto:5000" \
  --artefacts-fqdn "mock:8080" \
  --go-sdk-dest-remote "$GO_SDK_DEST_REMOTE" \
  --php-sdk-dest-remote "$PHP_SDK_DEST_REMOTE" \
  --helm-registry "registry:5000/dagger" \
  --skip-rust=true \
  --skip-elixir=true \
  markdown > /tmp/publish.log 2>&1
status=$?
cat /tmp/publish.log
exit "$status"
`
			out, err := c.Container().From(alpineImage).
				WithServiceBinding("dev-engine", engine.service).
				WithServiceBinding(gitHost, gitSvc).
				WithServiceBinding("registry", registrySvc).
				WithServiceBinding("moto", motoSvc).
				WithServiceBinding("verdaccio", verdaccioSvc).
				WithServiceBinding("mock", mockSvc).
				WithServiceBinding("github.test", mockSvc).
				WithMountedFile("/bin/dagger", daggerCliFile(t, c)).
				WithMountedFile("/github-ca.pem", certGen.caRootCert).
				WithSecretVariable("GORELEASER_KEY", c.SetSecret("goreleaser-key-"+identity.NewID(), goreleaserKey)).
				WithSecretVariable("REGISTRY_PASSWORD", c.SetSecret("registry-password-"+identity.NewID(), registryPassword)).
				WithSecretVariable("FAKE_GITHUB_TOKEN", c.SetSecret("fake-github-token-"+identity.NewID(), "fake-gh-token")).
				WithSecretVariable("FAKE_NETLIFY_TOKEN", c.SetSecret("fake-netlify-token-"+identity.NewID(), "fake-netlify-token")).
				WithSecretVariable("FAKE_PYPI_TOKEN", c.SetSecret("fake-pypi-token-"+identity.NewID(), "fake-pypi-token")).
				WithSecretVariable("FAKE_NPM_TOKEN", c.SetSecret("fake-npm-token-"+identity.NewID(), "fake-npm-token")).
				WithSecretVariable("AWS_ACCESS_KEY_ID", c.SetSecret("fake-aws-access-key-"+identity.NewID(), "test")).
				WithSecretVariable("AWS_SECRET_ACCESS_KEY", c.SetSecret("fake-aws-secret-key-"+identity.NewID(), "test")).
				WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", "/bin/dagger").
				WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", engine.endpoint).
				WithEnvVariable("MODULE_REF", moduleRef).
				WithEnvVariable("RELEASE_TAG", tag).
				WithEnvVariable("RELEASE_COMMIT", commit).
				WithEnvVariable("REGISTRY_USERNAME", registryUsername).
				WithEnvVariable("AWS_BUCKET", awsBucket).
				WithEnvVariable("AWS_CLOUDFRONT_DISTRIBUTION", cloudfrontDistribution).
				WithEnvVariable("GO_SDK_DEST_REMOTE", goSDKDestRemote).
				WithEnvVariable("PHP_SDK_DEST_REMOTE", phpSDKDestRemote).
				WithEnvVariable("PUBLISH_RUN_CACHE_BUSTER", identity.NewID()).
				WithExec([]string{"sh", "-ec", script}).
				Stdout(ctx)
			require.NoError(t, err, out)
			return out
		}

		engine := startReleaseEngine(ctx, t)
		t.Cleanup(func() { stopReleaseEngine(ctx, t, engine) })

		initialOut := runReleasePublish(ctx, t, engine, "main")
		require.Contains(t, initialOut, "- [x] 🚙 Engine", "initial main publish should publish the engine:\n%s", initialOut)
		require.Contains(t, initialOut, "- [x] 🚗 CLI", "initial main publish should publish the CLI:\n%s", initialOut)
		require.Contains(t, initialOut, ".goreleaser.nightly.yml", "initial main publish should use the nightly GoReleaser config:\n%s", initialOut)

		gitStdout(ctx, t, `git clone "$REPO_URL" .
git tag "$RELEASE_TAG" "`+commit+`"
git push origin "$RELEASE_TAG"
git ls-remote --tags origin "$RELEASE_TAG"
`)

		taggedOut := runReleasePublish(ctx, t, engine, releaseTag)
		require.Contains(t, taggedOut, fmt.Sprintf("- [x] 🚙 Engine ([`%s`]", releaseTag), "release publish should publish the engine")
		require.Contains(t, taggedOut, fmt.Sprintf("- [x] 🚗 CLI ([`%s`]", releaseTag), "release publish should publish the CLI")
		require.Contains(t, taggedOut, "- [x] 🐹 Go SDK", "release publish should publish the Go SDK")
		require.Contains(t, taggedOut, "- [x] 🐍 Python SDK", "release publish should publish the Python SDK")
		require.Contains(t, taggedOut, "- [x] ⬢ TypeScript SDK", "release publish should publish the TypeScript SDK")
		require.Contains(t, taggedOut, "- [x] 🐘 PHP SDK", "release publish should publish the PHP SDK")
		require.Contains(t, taggedOut, "- [x] ☸️ Helm Chart", "release publish should publish the Helm chart")
		require.Contains(t, taggedOut, ".goreleaser.yml", "tagged release publish should use the stable GoReleaser config:\n%s", taggedOut)
		require.NotContains(t, taggedOut, "Error while publishing", "release publish should complete against mock endpoints")

		mockEvents, err := c.Container().From(alpineImage).
			WithMountedCache("/records", mockRecords).
			WithExec([]string{"sh", "-ec", "cat /records/events.jsonl 2>/dev/null || true"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, mockEvents, `"kind": "netlify_restore"`)
		require.Contains(t, mockEvents, `"kind": "pypi_publish"`)
		require.Contains(t, mockEvents, `"kind": "github_release_create"`)
		require.Contains(t, mockEvents, `"kind": "github_release_asset_upload"`)
		require.Contains(t, mockEvents, `"kind": "github_content_write"`)
		require.Contains(t, mockEvents, `"kind": "github_pull_request_create"`)
		require.Contains(t, mockEvents, `/api/v3/repos/dagger/nix/contents/pkgs/dagger/default.nix`)
		require.Contains(t, mockEvents, `/api/v3/repos/dagger/homebrew-tap/contents/dagger.rb`)
		require.Contains(t, mockEvents, `/api/v3/repos/dagger/winget-pkgs/contents/manifests/d/Dagger/Cli/`+releaseVersion+`/Dagger.Cli.yaml`)
		require.Contains(t, mockEvents, `/api/v3/repos/dagger/winget-pkgs/contents/manifests/d/Dagger/Cli/`+releaseVersion+`/Dagger.Cli.installer.yaml`)
		require.Contains(t, mockEvents, `/api/v3/repos/dagger/winget-pkgs/contents/manifests/d/Dagger/Cli/`+releaseVersion+`/Dagger.Cli.locale.en-US.yaml`)
		require.Contains(t, mockEvents, `/api/v3/repos/microsoft/winget-pkgs/pulls`)

		registryTags, err := c.Container().From(alpineImage).
			WithExec([]string{"apk", "add", "curl"}).
			WithServiceBinding("registry", registrySvc).
			WithEnvVariable("REGISTRY_USERNAME", registryUsername).
			WithSecretVariable("REGISTRY_PASSWORD", c.SetSecret("registry-list-password-"+identity.NewID(), registryPassword)).
			WithExec([]string{"sh", "-ec", `curl -fsS -u "$REGISTRY_USERNAME:$REGISTRY_PASSWORD" http://registry:5000/v2/dagger/engine/tags/list`}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, registryTags, releaseTag)
		require.Contains(t, registryTags, commit)
		require.Contains(t, registryTags, "latest")

		engineVersion, err := c.Container().
			From("gcr.io/go-containerregistry/crane:debug").
			WithServiceBinding("registry", registrySvc).
			WithEnvVariable("REGISTRY_USERNAME", registryUsername).
			WithSecretVariable("REGISTRY_PASSWORD", c.SetSecret("registry-crane-password-"+identity.NewID(), registryPassword)).
			WithExec([]string{"sh", "-ec", "crane auth login registry:5000 --insecure --username \"$REGISTRY_USERNAME\" --password \"$REGISTRY_PASSWORD\" && crane export --insecure --platform linux/arm64 registry:5000/dagger/engine:" + releaseTag + " - | tar -xOf - usr/local/bin/dagger-engine > /tmp/dagger-engine && chmod +x /tmp/dagger-engine && /tmp/dagger-engine --version"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, engineVersion, releaseTag)

		cliVersion, err := awsCLI(dagger.ContainerOpts{Platform: dagger.Platform("linux/amd64")}).
			WithEnvVariable("AWS_BUCKET", awsBucket).
			WithExec([]string{"sh", "-ec", `
aws --endpoint-url "$AWS_ENDPOINT_URL" s3 cp "s3://$AWS_BUCKET/dagger/releases/` + releaseVersion + `/dagger_` + releaseTag + `_linux_amd64.tar.gz" /tmp/dagger.tgz
mkdir -p /tmp/dagger
tar -xzf /tmp/dagger.tgz -C /tmp/dagger
/tmp/dagger/dagger version
`}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, cliVersion, releaseTag)

		npmVersion, err := c.Container().
			From("node:20-alpine").
			WithServiceBinding("verdaccio", verdaccioSvc).
			WithExec([]string{"npm", "view", "@dagger.io/dagger@" + releaseVersion, "version", "--registry", "http://verdaccio:4873"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, releaseVersion, strings.TrimSpace(npmVersion))

		helmTags, err := c.Container().From(alpineImage).
			WithExec([]string{"apk", "add", "curl"}).
			WithServiceBinding("registry", registrySvc).
			WithEnvVariable("REGISTRY_USERNAME", registryUsername).
			WithSecretVariable("REGISTRY_PASSWORD", c.SetSecret("registry-helm-list-password-"+identity.NewID(), registryPassword)).
			WithExec([]string{"sh", "-ec", `curl -fsS -u "$REGISTRY_USERNAME:$REGISTRY_PASSWORD" http://registry:5000/v2/dagger/dagger-helm/tags/list`}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, helmTags, releaseVersion)

		goSDKTags := gitStdout(ctx, t, `git ls-remote --tags "`+goSDKDestRemote+`" "`+releaseTag+`"`)
		require.Contains(t, goSDKTags, releaseTag)
		phpSDKTags := gitStdout(ctx, t, `git ls-remote --tags "`+phpSDKDestRemote+`" "`+releaseTag+`"`)
		require.Contains(t, phpSDKTags, releaseTag)
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

func lookupEnvFileValue(t *testctx.T, key string) string {
	t.Helper()

	if val := strings.TrimSpace(os.Getenv(key)); val != "" {
		return val
	}

	if key == "GORELEASER_KEY" {
		home, err := os.UserHomeDir()
		if err == nil {
			contents, err := os.ReadFile(filepath.Join(home, ".goreleaserkey"))
			if err == nil {
				return strings.TrimSpace(string(contents))
			}
			require.True(t, os.IsNotExist(err), "failed to read ~/.goreleaserkey")
		}
	}

	contents, err := os.ReadFile("/dagger.env")
	if os.IsNotExist(err) {
		return ""
	}
	require.NoError(t, err)

	for _, line := range strings.Split(string(contents), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		name, val, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(name) != key {
			continue
		}
		return strings.Trim(strings.TrimSpace(val), `"'`)
	}

	return ""
}
