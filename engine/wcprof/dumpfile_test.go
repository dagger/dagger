package wcprof

import (
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

func readDumpFile(t *testing.T, path string) (*DumpHeader, []DumpEvent) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	h, evs, err := ReadDump(zr)
	if err != nil {
		t.Fatal(err)
	}
	return h, evs
}

func TestWriteDumpFile(t *testing.T) {
	// the file is the recorder's dump, gzipped, without flushing it: an
	// ended op as an event, a running one as an open op, and a rewrite
	// replaces the file whole, through a temporary file it doesn't leave
	r := withTestRecorder(t, 0)
	ctx := context.Background()
	_, ended := BeginOp(ctx, OpKindInternal, "engine.stop.first", OpOpts{})
	ended.End(OutcomeOK)
	_, running := BeginOp(ctx, OpKindInternal, "engine.stop.second", OpOpts{})
	path := StopDumpPath(t.TempDir())
	if err := WriteDumpFile(path); err != nil {
		t.Fatal(err)
	}
	h, evs := readDumpFile(t, path)
	if len(evs) != 1 || h.Strings[evs[0].ClassID] != "engine.stop.first" {
		t.Fatalf("events = %+v, want the ended op", evs)
	}
	if len(h.OpenOps) != 1 || h.Strings[h.OpenOps[0].ClassID] != "engine.stop.second" {
		t.Fatalf("open ops = %+v, want the running op", h.OpenOps)
	}
	if got := len(drainEvents(r)); got != 1 {
		t.Fatalf("recorder holds %d events after the write, want 1: a file dump must not flush", got)
	}
	running.End(OutcomeOK)
	if err := WriteDumpFile(path); err != nil {
		t.Fatal(err)
	}
	h, evs = readDumpFile(t, path)
	if len(evs) != 2 || len(h.OpenOps) != 0 {
		t.Fatalf("after the rewrite: %d events, %d open ops; want 2 and 0", len(evs), len(h.OpenOps))
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("dump directory holds %d entries, want only the dump", len(entries))
	}
}

func TestWriteDumpFileWithoutRecorder(t *testing.T) {
	prev := global.Load()
	global.Store(nil)
	t.Cleanup(func() { global.Store(prev) })
	path := StopDumpPath(t.TempDir())
	if err := WriteDumpFile(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("stat = %v, want no file when profiling was never enabled", err)
	}
}

func TestWriteStopDumpOnlyWhileRecording(t *testing.T) {
	// a recorder that exists but no longer records everything writes no
	// stop dump: the engine profiled once, then turned recording off
	withTestRecorder(t, 0)
	globalOn.Store(false)
	path := StopDumpPath(t.TempDir())
	WriteStopDump(context.Background(), path)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("stat = %v, want no stop dump while not recording", err)
	}
}

func TestWriteStopDumpIsAnOpUnderItsCaller(t *testing.T) {
	// the write is an op of its own, under the op whose context it runs in,
	// so its cost reads as the write's and not as that op's own time
	withTestRecorder(t, 0)
	path := StopDumpPath(t.TempDir())
	ctx, phase := BeginOp(context.Background(), OpKindInternal, "engine.stop.phase", OpOpts{})
	WriteStopDump(ctx, path)
	phase.End(OutcomeOK)
	WriteStopDump(context.Background(), path)
	h, evs := readDumpFile(t, path)
	var phaseID, writeParent uint64
	for _, ev := range evs {
		switch h.Strings[ev.ClassID] {
		case "engine.stop.phase":
			phaseID = ev.OpID
		case "wcprof.stopDump":
			if writeParent == 0 {
				writeParent = ev.ParentID
			}
		}
	}
	if phaseID == 0 || writeParent != phaseID {
		t.Fatalf("first stopDump op's parent = %d, want the phase op %d", writeParent, phaseID)
	}
}

func TestWriteStopDumpSerializesWriters(t *testing.T) {
	// writers from different goroutines (a signal's, a phase's, the stop's
	// end) take turns, so an older snapshot can't be renamed over a newer
	// one
	withTestRecorder(t, 0)
	path := StopDumpPath(t.TempDir())
	var inside, most atomic.Int32
	prev := stopDumpHook
	stopDumpHook = func() {
		n := inside.Add(1)
		for {
			m := most.Load()
			if n <= m || most.CompareAndSwap(m, n) {
				break
			}
		}
		runtime.Gosched()
		inside.Add(-1)
	}
	t.Cleanup(func() { stopDumpHook = prev })
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			WriteStopDump(context.Background(), path)
		}()
	}
	wg.Wait()
	if most.Load() != 1 {
		t.Fatalf("%d stop dump writers ran at once, want 1", most.Load())
	}
}
