package main

import (
	"strconv"
	"strings"

	bkconfig "github.com/moby/buildkit/cmd/buildkitd/config"
	"github.com/moby/buildkit/util/disk"
	"github.com/pkg/errors"
)

func gcConfigToString(bkcfg bkconfig.GCConfig, dstat disk.DiskStat) string {
	if bkcfg.IsUnset() {
		//nolint:staticcheck // used for backward compatibility
		bkcfg.GCReservedSpace = bkcfg.GCKeepStorage
	}
	if bkcfg.IsUnset() {
		// we'll handle this later in dagger
		return ""
	}
	out := []int64{bkcfg.GCReservedSpace.AsBytes(disk.DiskStat{}) / 1e6}
	free := bkcfg.GCMinFreeSpace.AsBytes(dstat) / 1e6
	max := bkcfg.GCMaxUsedSpace.AsBytes(dstat) / 1e6
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

func stringToGCConfig(in string) (bkconfig.GCConfig, error) {
	var cfg bkconfig.GCConfig
	if in == "" {
		return cfg, nil
	}
	parts := strings.SplitN(in, ",", 3)
	reserved, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return cfg, errors.Wrapf(err, "failed to parse storage %q", in)
	}
	cfg.GCReservedSpace = bkconfig.DiskSpace{Bytes: reserved * 1e6}
	if len(parts) == 1 {
		return cfg, nil
	}
	free, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return cfg, errors.Wrapf(err, "failed to parse free storage %q", in)
	}
	cfg.GCMinFreeSpace = bkconfig.DiskSpace{Bytes: free * 1e6}
	if len(parts) == 2 {
		return cfg, nil
	}
	max, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return cfg, errors.Wrapf(err, "failed to parse max storage %q", in)
	}
	cfg.GCMaxUsedSpace = bkconfig.DiskSpace{Bytes: max * 1e6}
	return cfg, nil
}
