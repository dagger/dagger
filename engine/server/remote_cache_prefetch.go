package server

import (
	"github.com/dagger/dagger/internal/buildkit/util/disk"
)

// partPrefetchMargin is the free space, beyond what garbage collection keeps,
// that prefetching offered parts leaves on the engine's disk.
const partPrefetchMargin = 1 << 30

// partPrefetchRoom reports whether prefetching need more bytes leaves the
// engine's disk the free space its garbage collection keeps, and a margin.
// Prefetched blobs are held outside the cache's prune accounting, so this
// check, with the prefetch budget, is what bounds their disk use.
func (srv *Server) partPrefetchRoom(need int64) bool {
	dstat, err := disk.GetDiskStat(srv.rootDir)
	if err != nil {
		return false
	}
	var floor int64
	for _, policy := range srv.workerGCPolicies {
		floor = max(floor, policy.MinFreeSpace)
	}
	return dstat.Available-need > floor+partPrefetchMargin
}
