package main

import (
	"context"
	"sync/atomic"

	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/engine/wcprof"
)

// stopDumpPath is where the engine keeps its stop dump
// (wcprof.StopDumpPath of its state directory), once main knows it.
var stopDumpPath atomic.Pointer[string]

// recordStopSignal records the first SIGTERM or SIGINT as an op and writes
// the stop dump to path, when the engine records: what's running at the
// signal, an export racing the stop included, is then in the dump even if
// the engine is killed before its graceful stop starts.
func recordStopSignal(path string) {
	if wcprof.Active() == nil {
		return
	}
	_, op := wcprof.BeginOp(context.Background(), wcprof.OpKindInternal, "engine.stop.signal", wcprof.OpOpts{})
	op.End(wcprof.OutcomeOK)
	if err := wcprof.WriteDumpFile(path); err != nil {
		slog.Warn("wcprof stop dump", "error", err)
	}
}
