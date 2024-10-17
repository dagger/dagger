package main

import (
	"strconv"
	"strings"

	"github.com/dagger/dagger/engine/server"
	"github.com/moby/buildkit/cmd/buildkitd/config"
	"github.com/moby/buildkit/util/disk"
	"github.com/pkg/errors"
)

func gcConfigToString(cfg config.GCConfig, dstat disk.DiskStat) string {
	if cfg.IsUnset() {
		//nolint:staticcheck // used for backward compatibility
		cfg.GCReservedSpace = cfg.GCKeepStorage
	}
	if cfg.IsUnset() {
		cfg = server.DetectDefaultGCCap(dstat)
	}
	out := []int64{cfg.GCReservedSpace.AsBytes(disk.DiskStat{}) / 1e6}
	free := cfg.GCMinFreeSpace.AsBytes(dstat) / 1e6
	max := cfg.GCMaxUsedSpace.AsBytes(dstat) / 1e6
	if free != 0 || max != 0 {
		out = append(out, free)
		if max != 0 {
			out = append(out, max)
		}
	}
	return strings.Join(int64ToString(out), ",")
}

func int64ToString(in []int64) []string {
	out := make([]string, len(in))
	for i, v := range in {
		out[i] = strconv.FormatInt(v, 10)
	}
	return out
}

func stringToGCConfig(in string) (config.GCConfig, error) {
	var cfg config.GCConfig
	if in == "" {
		return cfg, nil
	}
	parts := strings.SplitN(in, ",", 3)
	reserved, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return cfg, errors.Wrapf(err, "failed to parse storage %q", in)
	}
	cfg.GCReservedSpace = config.DiskSpace{Bytes: reserved * 1e6}
	if len(parts) == 1 {
		return cfg, nil
	}
	free, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return cfg, errors.Wrapf(err, "failed to parse free storage %q", in)
	}
	cfg.GCMinFreeSpace = config.DiskSpace{Bytes: free * 1e6}
	if len(parts) == 2 {
		return cfg, nil
	}
	max, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return cfg, errors.Wrapf(err, "failed to parse max storage %q", in)
	}
	cfg.GCMaxUsedSpace = config.DiskSpace{Bytes: max * 1e6}
	return cfg, nil
}
