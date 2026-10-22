package main

import (
	"github.com/urfave/cli"

	"github.com/dagger/dagger/engine/config"
)

// applyCacheVolumesFlags applies --cache-volumes-dir over the config's
// cacheVolumes.dir.
func applyCacheVolumesFlags(c *cli.Context, cfg *config.Config) {
	if c.IsSet("cache-volumes-dir") {
		cfg.CacheVolumes.Dir = c.String("cache-volumes-dir")
	}
}
