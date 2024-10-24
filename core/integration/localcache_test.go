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
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/dagger/testctx"
)

func (EngineSuite) TestLocalCacheGCKeepBytesConfig(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	for _, tc := range []struct {
		name          string
		keepStorage   string
		maxUsedSpace  string
		reservedSpace string
		minFreeSpace  string
	}{
		{
			name: "default",
		},
		{
			name:        "bytes",
			keepStorage: fmt.Sprint(1024 * 1024 * 1024),
		},
		{
			name:        "percent",
			keepStorage: "5%",
		},
		{
			name:          "complex bytes",
			reservedSpace: fmt.Sprint(2 * 1024 * 1024 * 1024),
			maxUsedSpace:  fmt.Sprint(4 * 1024 * 1024 * 1024),
			minFreeSpace:  fmt.Sprint(1 * 1024 * 1024 * 1024),
		},
		{
			name:          "complex percent",
			reservedSpace: "20%",
			maxUsedSpace:  "80%",
			minFreeSpace:  "10%",
		},
	} {
		tc := tc
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			var opts []func(*dagger.Container) *dagger.Container
			if tc.keepStorage != "" {
				opts = append(opts, engineWithConfig(ctx, t, engineConfigWithKeepBytes(tc.keepStorage)))
			}
			if tc.reservedSpace != "" || tc.maxUsedSpace != "" || tc.minFreeSpace != "" {
				opts = append(opts, engineWithConfig(ctx, t, engineConfigWithGC(tc.reservedSpace, tc.minFreeSpace, tc.maxUsedSpace)))
			}

			engineSvc, err := c.Host().Tunnel(devEngineContainer(c, opts...).AsService()).Start(ctx)
			require.NoError(t, err)
			t.Cleanup(func() { engineSvc.Stop(ctx) })

			endpoint, err := engineSvc.Endpoint(ctx, dagger.ServiceEndpointOpts{Scheme: "tcp"})
			require.NoError(t, err)

			c2, err := dagger.Connect(ctx, dagger.WithRunnerHost(endpoint), dagger.WithLogOutput(testutil.NewTWriter(t)))
			require.NoError(t, err)
			t.Cleanup(func() { c2.Close() })

			cache := c2.DaggerEngine().LocalCache()

			if tc.keepStorage != "" {
				expectedKeepBytes := getEngineBytesFromSpec(ctx, t, c2, tc.keepStorage)
				actualKeepBytes, err := cache.KeepBytes(ctx) //nolint:staticcheck
				require.NoError(t, err)
				require.Equal(t, expectedKeepBytes, actualKeepBytes)
			}
			if tc.maxUsedSpace != "" {
				expectedMaxUsedSpace := getEngineBytesFromSpec(ctx, t, c2, tc.maxUsedSpace)
				actualMaxUsedSpace, err := cache.MaxUsedSpace(ctx)
				require.NoError(t, err)
				require.Equal(t, expectedMaxUsedSpace, actualMaxUsedSpace)
			}
			if tc.minFreeSpace != "" {
				expectedMinFreeSpace := getEngineBytesFromSpec(ctx, t, c2, tc.minFreeSpace)
				actualMinFreeSpace, err := cache.MinFreeSpace(ctx)
				require.NoError(t, err)
				require.Equal(t, expectedMinFreeSpace, actualMinFreeSpace)
			}
			if tc.reservedSpace != "" {
				expectedReservedSpace := getEngineBytesFromSpec(ctx, t, c2, tc.reservedSpace)
				actualReservedSpace, err := cache.ReservedSpace(ctx)
				require.NoError(t, err)
				require.Equal(t, expectedReservedSpace, actualReservedSpace)
			}
		})
	}
}

func (EngineSuite) TestLocalCacheAutomaticGC(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	for _, tc := range []struct {
		name string

		// configs
		keepStorage   string
		maxUsedSpace  string
		reservedSpace string
		minFreeSpace  string

		// try and bring storage usage below the target
		target string
	}{
		{
			// test creates 2gb, this is over keepStorage, so gc kicks in
			name:        "keep",
			keepStorage: fmt.Sprint(1024 * 1024 * 1024), // 1GB
			target:      fmt.Sprint(1024 * 1024 * 1024), // 1GB
		},
		{
			// test creates 2gb, this means we have no free storage, so gc kicks in
			name:          "free",
			maxUsedSpace:  "100%",
			reservedSpace: fmt.Sprint(1024 * 1024 * 1024), // 1GB
			minFreeSpace:  "100%",
			target:        fmt.Sprint(1024 * 1024 * 1024), // 1GB
		},
	} {
		t.Run(tc.name, func(context.Context, *testctx.T) {
			var opts []func(*dagger.Container) *dagger.Container
			if tc.keepStorage != "" {
				opts = append(opts, engineWithConfig(ctx, t, engineConfigWithKeepBytes(tc.keepStorage)))
			}
			if tc.reservedSpace != "" || tc.maxUsedSpace != "" || tc.minFreeSpace != "" {
				opts = append(opts, engineWithConfig(ctx, t, engineConfigWithGC(tc.reservedSpace, tc.minFreeSpace, tc.maxUsedSpace)))
			}

			engineSvc, err := c.Host().Tunnel(devEngineContainer(c, opts...).AsService()).Start(ctx)
			require.NoError(t, err)
			t.Cleanup(func() { engineSvc.Stop(ctx) })

			endpoint, err := engineSvc.Endpoint(ctx, dagger.ServiceEndpointOpts{Scheme: "tcp"})
			require.NoError(t, err)

			c2, err := dagger.Connect(ctx, dagger.WithRunnerHost(endpoint), dagger.WithLogOutput(testutil.NewTWriter(t)))
			require.NoError(t, err)
			t.Cleanup(func() { c2.Close() })

			target := getEngineBytesFromSpec(ctx, t, c2, tc.target)

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
			require.Greater(t, newUsedBytes, target)

			// verify automatic gc kicks in now that we exceeded the configured keepstorage
			// automatic gc is time based (currently kicks in 1sec after a session ends but throttled to run at most once a min) so no choice but to sleep and retry
			require.NoError(t, c4.Close())
			tryCount := 300
			for i := range tryCount {
				cacheEnts = c2.DaggerEngine().LocalCache().EntrySet()
				newUsedBytes, err = cacheEnts.DiskSpaceBytes(ctx)
				require.NoError(t, err)

				if newUsedBytes < target {
					break
				}

				t.Logf("current used bytes %d >= keep storage bytes %d, waiting for gc to free up space", newUsedBytes, target)
				if i < tryCount-1 {
					time.Sleep(1 * time.Second)
					continue
				}

				// failed, print current usage for debugging

				reservedSpace, err := c2.DaggerEngine().LocalCache().ReservedSpace(ctx)
				require.NoError(t, err)
				minFreeSpace, err := c2.DaggerEngine().LocalCache().MinFreeSpace(ctx)
				require.NoError(t, err)
				maxUsedSpace, err := c2.DaggerEngine().LocalCache().MaxUsedSpace(ctx)
				require.NoError(t, err)
				t.Logf("Base cache policy: reservedSpace=%d minFreeSpace=%d maxUsedSpace=%d", reservedSpace, minFreeSpace, maxUsedSpace)

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

				t.Fatalf("Expected used bytes to decrease below %d from gc, got %d", target, newUsedBytes)
			}
		})
	}
}

func engineConfigWithKeepBytes(keepStorage string) func(context.Context, *testctx.T, bkconfig.Config) bkconfig.Config {
	return func(ctx context.Context, t *testctx.T, cfg bkconfig.Config) bkconfig.Config {
		t.Helper()
		require.NoError(t, cfg.Workers.OCI.GCKeepStorage.UnmarshalText([]byte(keepStorage))) //nolint: staticcheck
		return cfg
	}
}

func engineConfigWithGC(reserved, minFree, maxUsed string) func(context.Context, *testctx.T, bkconfig.Config) bkconfig.Config {
	return func(ctx context.Context, t *testctx.T, cfg bkconfig.Config) bkconfig.Config {
		t.Helper()
		require.NoError(t, cfg.Workers.OCI.GCReservedSpace.UnmarshalText([]byte(reserved)))
		require.NoError(t, cfg.Workers.OCI.GCMinFreeSpace.UnmarshalText([]byte(minFree)))
		require.NoError(t, cfg.Workers.OCI.GCMaxUsedSpace.UnmarshalText([]byte(maxUsed)))
		return cfg
	}
}

func getEngineBytesFromSpec(ctx context.Context, t *testctx.T, c *dagger.Client, amount string) int {
	if amount, ok := strings.CutSuffix(amount, "%"); ok {
		percent, err := strconv.Atoi(amount)
		require.NoError(t, err)
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

	keepBytes, err := strconv.Atoi(amount)
	require.NoError(t, err)
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
