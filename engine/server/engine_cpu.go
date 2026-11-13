package server

import (
	"context"
	"sync"
	"time"
)

// engineCPUWindowDuration and engineCPUIdlePoll are the engine CPU sampler's
// window and its check for wcprof recording.
var (
	engineCPUWindowDuration = 10 * time.Second
	engineCPUIdlePoll       = time.Second
)

// unattributedCPU names a window's CPU that no kind's samples account for.
const unattributedCPU = "unattributed"

// engineCPUWindow is the engine's own CPU over one window.
type engineCPUWindow struct {
	Start, End time.Time
	ProcessUS  int64
	GCUS       int64
	Samples    int64
	Kinds      map[string]int64
}

// cpuProfiler is the process's one CPU profiler.
type cpuProfiler struct {
	mu sync.Mutex
}

// startEngineCPU doesn't sample yet.
func (srv *Server) startEngineCPU(context.Context) {}

// splitCPU doesn't split yet.
func splitCPU(int64, map[string]int64) map[string]int64 { return nil }
