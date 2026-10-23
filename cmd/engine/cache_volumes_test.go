package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli"

	"github.com/dagger/dagger/engine/config"
)

func TestCacheVolumesDirFlag(t *testing.T) {
	apply := func(t *testing.T, cfg config.Config, args ...string) config.Config {
		t.Helper()
		app := cli.NewApp()
		addFlags(app)
		app.Action = func(c *cli.Context) error {
			applyCacheVolumesFlags(c, &cfg)
			return nil
		}
		require.NoError(t, app.Run(append([]string{"dagger-engine"}, args...)))
		return cfg
	}
	var fromConfig config.Config
	fromConfig.CacheVolumes.Dir = "/from/config"

	require.Empty(t, apply(t, config.Config{}).CacheVolumes.Dir, "off by default")
	require.Equal(t, "/from/config", apply(t, fromConfig).CacheVolumes.Dir)
	require.Equal(t, "/from/flag", apply(t, fromConfig, "--cache-volumes-dir", "/from/flag").CacheVolumes.Dir)
}

func TestCacheVolumesCapacityConfig(t *testing.T) {
	cfg, err := config.Load(strings.NewReader(`{"cacheVolumes": {"dir": "/cache-volumes", "capacity": "100GB"}}`))
	require.NoError(t, err)
	require.Equal(t, config.DiskSpace{Bytes: 100 << 30}, cfg.CacheVolumes.Capacity)
}
