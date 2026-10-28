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

// recordStopSignal records the first SIGTERM or SIGINT as an op and asks for
// the stop dump at path, while the engine records: what's running at the
// signal, an export racing the stop included, is then in the dump even if the
// engine is killed before its graceful stop starts. It doesn't wait for the
// write, so the signal's handling doesn't either.
func recordStopSignal(path string) {
	_, op := wcprof.BeginOp(context.Background(), wcprof.OpKindInternal, "engine.stop.signal", wcprof.OpOpts{})
	op.End(wcprof.OutcomeOK)
	wcprof.RequestStopDump(path)
}

// exitStep runs one of the engine's final drains, after its server stopped,
// as an op, engine.exit.<name>, with the stop dump rewritten (in the
// background) as it begins and ends, so an engine killed during a drain
// leaves a dump that shows which.
func exitStep(name string, fn func()) {
	path := stopDumpPath.Load()
	_, op := wcprof.BeginOp(context.Background(), wcprof.OpKindInternal, "engine.exit."+name, wcprof.OpOpts{})
	if path != nil {
		wcprof.RequestStopDump(*path)
	}
	fn()
	op.End(wcprof.OutcomeOK)
	if path != nil {
		wcprof.RequestStopDump(*path)
	}
}

// flushStopDump waits, as the engine exits, for its stop dump to hold its
// last state: the one write of the stop it waits for.
func flushStopDump() {
	if path := stopDumpPath.Load(); path != nil {
		if err := wcprof.FlushStopDump(*path); err != nil {
			slog.Warn("wcprof stop dump", "error", err)
		}
	}
}
