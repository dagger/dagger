package wcprof

import (
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"sync"
)

// StopDumpPath is where an engine with state directory root keeps the dump
// of its stop (see WriteDumpFile).
func StopDumpPath(root string) string {
	return filepath.Join(root, "wcprof", "stop.ndjson.gz")
}

// WriteDumpFile writes the recorder's dump to path, gzipped, without flushing
// the recorder. It writes a temporary file beside path and renames it over
// path, so a reader, or an engine killed mid-write, never leaves a partial
// dump. It does nothing when profiling was never enabled.
//
// The engine writes its stop dump this way (RequestStopDump) when it receives
// SIGTERM, and as each graceful-stop phase and final drain begins and ends:
// an engine killed before it exits leaves the dump of its last finished
// write, with the work still running then as open ops. It compresses at
// gzip's fastest level, since it runs during the stop it records.
func WriteDumpFile(path string) (err error) {
	r := Active()
	if r == nil {
		return nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	zw, err := gzip.NewWriterLevel(tmp, gzip.BestSpeed)
	if err != nil {
		return err
	}
	if err := r.WriteDump(zw, false); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// stopDumpHook runs at the start of each stop dump write, on the writer;
// tests set it to hold or count writes.
var stopDumpHook = func() {}

// stopDumper is the stop dump's one writer. A write of a large recording
// takes seconds, so the stop doesn't wait for it: a request marks the dump
// out of date and starts the writer if it isn't running, and requests made
// while a write runs become one more write, from the state at its start.
// One writer also means an older snapshot can't be renamed over a newer one.
var stopDumper struct {
	mu      sync.Mutex
	changed *sync.Cond
	path    string
	pending bool
	running bool
	// started and finished count writes; err is the last finished one's.
	started, finished uint64
	err               error
}

func init() {
	stopDumper.changed = sync.NewCond(&stopDumper.mu)
}

// RequestStopDump asks for the engine's stop dump at path to be rewritten,
// while the engine records everything (GloballyEnabled), and returns without
// waiting for the write. Each write is an op of its own, wcprof.stopDump.
// The dump on disk lags the latest request by at most the write in progress
// and one more; FlushStopDump waits for it.
func RequestStopDump(path string) {
	if !GloballyEnabled() {
		return
	}
	stopDumper.mu.Lock()
	requestStopDumpLocked(path)
	stopDumper.mu.Unlock()
}

func requestStopDumpLocked(path string) {
	stopDumper.path = path
	stopDumper.pending = true
	if !stopDumper.running {
		stopDumper.running = true
		go writeStopDumps()
	}
}

// FlushStopDump asks for the stop dump at path to be rewritten and waits for
// a write that began after the call, returning its error: the engine's last
// state before it exits is then on disk.
func FlushStopDump(path string) error {
	if !GloballyEnabled() {
		return nil
	}
	stopDumper.mu.Lock()
	defer stopDumper.mu.Unlock()
	requestStopDumpLocked(path)
	// the write serving this request is the next one to start
	want := stopDumper.started + 1
	for stopDumper.finished < want {
		stopDumper.changed.Wait()
	}
	return stopDumper.err
}

// writeStopDumps writes the stop dump while one is pending, then stops.
func writeStopDumps() {
	stopDumper.mu.Lock()
	for stopDumper.pending {
		stopDumper.pending = false
		path := stopDumper.path
		stopDumper.started++
		n := stopDumper.started
		stopDumper.mu.Unlock()

		_, op := BeginOp(context.Background(), OpKindInternal, "wcprof.stopDump", OpOpts{})
		stopDumpHook()
		err := WriteDumpFile(path)
		op.EndErr(err)

		stopDumper.mu.Lock()
		stopDumper.finished, stopDumper.err = n, err
		stopDumper.changed.Broadcast()
	}
	stopDumper.running = false
	stopDumper.mu.Unlock()
}
