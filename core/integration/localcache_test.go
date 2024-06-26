package core

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	bkconfig "github.com/moby/buildkit/cmd/buildkitd/config"
	"github.com/stretchr/testify/require"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/server"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/dagger/testctx"
)

func (EngineSuite) TestLocalCacheGCKeepBytesConfig(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	for _, tc := range []struct {
		name    string
		bytes   int
		percent int
	}{
		{
			name: "default",
		},
		{
			name:  "bytes",
			bytes: 1024 * 1024 * 1024,
		},
		{
			name:    "percent",
			percent: 5,
		},
	} {
		tc := tc
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			var keepStorageStr string
			switch {
			case tc.bytes != 0 && tc.percent != 0:
				t.Fatalf("expected either bytes or percent to be set, got both")
			case tc.bytes != 0:
				keepStorageStr = strconv.Itoa(tc.bytes)
			case tc.percent != 0:
				keepStorageStr = fmt.Sprintf(`"%d%%"`, tc.percent)
			}

			var opts []func(*dagger.Container) *dagger.Container
			if keepStorageStr != "" {
				opts = append(opts, engineWithConfig(ctx, t, engineConfigWithKeepBytes(keepStorageStr)))
			}

			engineSvc, err := c.Host().Tunnel(devEngineContainer(c, opts...).AsService()).Start(ctx)
			require.NoError(t, err)
			t.Cleanup(func() { engineSvc.Stop(ctx) })

			endpoint, err := engineSvc.Endpoint(ctx, dagger.ServiceEndpointOpts{Scheme: "tcp"})
			require.NoError(t, err)

			c2, err := dagger.Connect(ctx, dagger.WithRunnerHost(endpoint), dagger.WithLogOutput(testutil.NewTWriter(t)))
			require.NoError(t, err)
			t.Cleanup(func() { c2.Close() })

			var expectedGCConfigKeepBytes int
			switch {
			case tc.bytes != 0:
				expectedGCConfigKeepBytes = tc.bytes
			case tc.percent != 0:
				expectedGCConfigKeepBytes = getEngineKeepBytesByPercent(ctx, t, c2, tc.percent)
			default:
				expectedGCConfigKeepBytes = getEngineKeepBytesByPercent(ctx, t, c2, int(server.DefaultDiskSpacePercentage))
			}

			actualGCConfigKeepBytes, err := c2.DaggerEngine().LocalCache().KeepBytes(ctx)
			require.NoError(t, err)
			require.Equal(t, expectedGCConfigKeepBytes, actualGCConfigKeepBytes)
		})
	}
}

func (EngineSuite) TestLocalCacheAutomaticGC(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	const keepStorageBytes = 1024 * 1024 * 1024
	const keepStorageByteStr = "1GB"

	engineSvc, err := c.Host().Tunnel(devEngineContainer(c, engineWithConfig(ctx, t,
		engineConfigWithKeepBytes(keepStorageByteStr),
	)).AsService()).Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { engineSvc.Stop(ctx) })

	endpoint, err := engineSvc.Endpoint(ctx, dagger.ServiceEndpointOpts{Scheme: "tcp"})
	require.NoError(t, err)

	c2, err := dagger.Connect(ctx, dagger.WithRunnerHost(endpoint), dagger.WithLogOutput(testutil.NewTWriter(t)))
	require.NoError(t, err)
	t.Cleanup(func() { c2.Close() })

	cacheEnts := c2.DaggerEngine().LocalCache().EntrySet()
	previousUsedBytes, err := cacheEnts.DiskSpaceBytes(ctx)
	require.NoError(t, err)

	// sanity check that creating a new file increases cache disk space
	c3, err := dagger.Connect(ctx, dagger.WithRunnerHost(endpoint), dagger.WithLogOutput(testutil.NewTWriter(t)))
	require.NoError(t, err)
	_, err = c3.Directory().WithNewFile("/tmp/foo", "foo").Sync(ctx)
	require.NoError(t, err)
	require.NoError(t, c3.Close())

	cacheEnts = c2.DaggerEngine().LocalCache().EntrySet()
	newUsedBytes, err := cacheEnts.DiskSpaceBytes(ctx)
	require.NoError(t, err)
	require.Greater(t, newUsedBytes, previousUsedBytes)
	previousUsedBytes = newUsedBytes

	// consume 2GB of space, greater than configured keepstorage of 1GB
	c4, err := dagger.Connect(ctx, dagger.WithRunnerHost(endpoint), dagger.WithLogOutput(testutil.NewTWriter(t)))
	require.NoError(t, err)
	_, err = c4.Container().From(alpineImage).WithExec([]string{"dd", "if=/dev/zero", "of=/bigfile", "bs=1M", "count=2048"}).Sync(ctx)
	require.NoError(t, err)

	cacheEnts = c2.DaggerEngine().LocalCache().EntrySet()
	newUsedBytes, err = cacheEnts.DiskSpaceBytes(ctx)
	require.NoError(t, err)
	require.Greater(t, newUsedBytes, previousUsedBytes)
	require.Greater(t, newUsedBytes, keepStorageBytes)

	// verify automatic gc kicks in now that we exceeded the configured keepstorage
	// automatic gc is time based (currently kicks in 1sec after a session ends but throttled to run at most once a min) so no choice but to sleep and retry
	require.NoError(t, c4.Close())
	tryCount := 300
	for i := range tryCount {
		cacheEnts = c2.DaggerEngine().LocalCache().EntrySet()
		newUsedBytes, err = cacheEnts.DiskSpaceBytes(ctx)
		require.NoError(t, err)

		if newUsedBytes < keepStorageBytes {
			break
		}

		t.Logf("current used bytes %d >= keep storage bytes %d, waiting for gc to free up space", newUsedBytes, keepStorageBytes)
		if i < tryCount-1 {
			time.Sleep(1 * time.Second)
			continue
		}

		// failed, print current usage for debugging
		ents, err := cacheEnts.Entries(ctx)
		if err != nil {
			t.Logf("Failed to get cache entries for debugging: %v", err)
		} else {
			t.Log("Remaining entries:")
			for _, ent := range ents {
				entVal := getCacheEntryVals(ctx, t, ent)
				t.Logf("  %q: (%d bytes) active=%t", entVal.Description, entVal.DiskSpaceBytes, entVal.ActivelyUsed)
			}
		}

		t.Fatalf("Expected used bytes to decrease below %d from gc, got %d", keepStorageBytes, newUsedBytes)
	}
}

func (EngineSuite) TestLocalCacheManualGC(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	engineSvc, err := c.Host().Tunnel(devEngineContainer(c).AsService()).Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { engineSvc.Stop(ctx) })

	endpoint, err := engineSvc.Endpoint(ctx, dagger.ServiceEndpointOpts{Scheme: "tcp"})
	require.NoError(t, err)

	c2, err := dagger.Connect(ctx, dagger.WithRunnerHost(endpoint), dagger.WithLogOutput(testutil.NewTWriter(t)))
	require.NoError(t, err)
	t.Cleanup(func() { c2.Close() })

	c3, err := dagger.Connect(ctx, dagger.WithRunnerHost(endpoint), dagger.WithLogOutput(testutil.NewTWriter(t)))
	require.NoError(t, err)
	_, err = c3.Container().From(alpineImage).
		WithExec([]string{"touch", "/foo"}).
		Sync(ctx)
	require.NoError(t, err)
	require.NoError(t, c3.Close())

	// verify that the cache refs are no longer in use after closing the client+session
	// some parts of session cleanup happen asynchronously so we need retries here
	const tryCount = 10
	for i := range tryCount {
		ents, err := c2.DaggerEngine().LocalCache().EntrySet().Entries(ctx)
		require.NoError(t, err)
		var alpineImageEnt *cacheEntryVals
		var touchFooEnt *cacheEntryVals
		for _, ent := range ents {
			entVal := getCacheEntryVals(ctx, t, ent)
			switch {
			case strings.HasPrefix(entVal.Description, "pulled from docker.io/library/alpine"):
				alpineImageEnt = entVal
			case strings.HasPrefix(entVal.Description, "mount / from exec touch /foo"):
				touchFooEnt = entVal
			}
		}
		require.NotNil(t, alpineImageEnt)
		require.NotNil(t, touchFooEnt)

		if alpineImageEnt.ActivelyUsed || touchFooEnt.ActivelyUsed {
			if i == tryCount-1 {
				t.Fatalf("Expected cache entries to be unused after closing client+session, alpine=%t, touchFoo=%t", alpineImageEnt.ActivelyUsed, touchFooEnt.ActivelyUsed)
			}

			t.Logf("Waiting for cache entries to be unused: alpine=%t, touchFoo=%t", alpineImageEnt.ActivelyUsed, touchFooEnt.ActivelyUsed)
			time.Sleep(1 * time.Second)
		} else {
			break
		}
	}

	// prune everything
	_, err = c2.DaggerEngine().LocalCache().Prune(ctx)
	require.NoError(t, err)
	newEnts, err := c2.DaggerEngine().LocalCache().EntrySet().Entries(ctx)
	require.NoError(t, err)
	require.Len(t, newEnts, 1) // 1 because there are cache entries created internally when each session starts (the file containing the core schema)
}

func engineConfigWithKeepBytes(keepStorageSetting string) func(context.Context, *testctx.T, bkconfig.Config) bkconfig.Config {
	return func(ctx context.Context, t *testctx.T, cfg bkconfig.Config) bkconfig.Config {
		t.Helper()
		require.NoError(t, cfg.Workers.OCI.GCKeepStorage.UnmarshalText([]byte(keepStorageSetting)))
		return cfg
	}
}

func getEngineKeepBytesByPercent(ctx context.Context, t *testctx.T, c *dagger.Client, percent int) int {
	dfOut, err := c.Container().From(alpineImage).WithExec([]string{"df", "-B", "1", "/"}).Stdout(ctx)
	require.NoError(t, err)
	dfLines := strings.Split(strings.TrimSpace(dfOut), "\n")
	require.Len(t, dfLines, 2)
	dfFields := strings.Fields(dfLines[1])
	require.Len(t, dfFields, 6)
	diskSizeBytes, err := strconv.Atoi(dfFields[1])
	require.NoError(t, err)

	keepBytes := diskSizeBytes * percent / 100
	// mirroring logic from buildkit to round up to the nearest GB
	keepBytes = (keepBytes/(1<<30) + 1) * 1e9
	return keepBytes
}

type cacheEntryVals struct {
	Description               string
	DiskSpaceBytes            int
	CreatedTimeUnixNano       int
	MostRecentUseTimeUnixNano int
	ActivelyUsed              bool
}

func getCacheEntryVals(ctx context.Context, t *testctx.T, ent dagger.DaggerEngineCacheEntry) *cacheEntryVals {
	t.Helper()

	vals := &cacheEntryVals{}
	var err error

	vals.Description, err = ent.Description(ctx)
	require.NoError(t, err)

	vals.DiskSpaceBytes, err = ent.DiskSpaceBytes(ctx)
	require.NoError(t, err)

	vals.CreatedTimeUnixNano, err = ent.CreatedTimeUnixNano(ctx)
	require.NoError(t, err)

	vals.MostRecentUseTimeUnixNano, err = ent.MostRecentUseTimeUnixNano(ctx)
	require.NoError(t, err)

	vals.ActivelyUsed, err = ent.ActivelyUsed(ctx)
	require.NoError(t, err)

	return vals
}
