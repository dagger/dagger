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
// the stop dump to path beside the signal's handling, while the engine
// records: what's running at the signal, an export racing the stop
// included, is then in the dump even if the engine is killed before its
// graceful stop starts. The returned channel closes once the dump is
// written.
func recordStopSignal(path string) <-chan struct{} {
	written := make(chan struct{})
	ctx, op := wcprof.BeginOp(context.Background(), wcprof.OpKindInternal, "engine.stop.signal", wcprof.OpOpts{})
	op.End(wcprof.OutcomeOK)
	go func() {
		defer close(written)
		if err := wcprof.WriteStopDump(ctx, path); err != nil {
			slog.Warn("wcprof stop dump", "error", err)
		}
	}()
	return written
}

// exitStep runs one of the engine's final drains, after its server stopped,
// as an op, engine.exit.<name>, with the stop dump rewritten as it begins and
// ends, so an engine killed during a drain leaves a dump that shows which.
func exitStep(name string, fn func()) {
	path := stopDumpPath.Load()
	ctx, op := wcprof.BeginOp(context.Background(), wcprof.OpKindInternal, "engine.exit."+name, wcprof.OpOpts{})
	if path != nil {
		if err := wcprof.WriteStopDump(ctx, *path); err != nil {
			slog.Warn("wcprof stop dump", "error", err)
		}
	}
	fn()
	op.End(wcprof.OutcomeOK)
	if path != nil {
		if err := wcprof.WriteStopDump(context.Background(), *path); err != nil {
			slog.Warn("wcprof stop dump", "error", err)
		}
	}
}
