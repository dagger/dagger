package wcprof

import (
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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

func TestRequestStopDumpOnlyWhileRecording(t *testing.T) {
	// a recorder that exists but no longer records everything writes no
	// stop dump: the engine profiled once, then turned recording off
	withTestRecorder(t, 0)
	globalOn.Store(false)
	path := StopDumpPath(t.TempDir())
	RequestStopDump(path)
	if err := FlushStopDump(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("stat = %v, want no stop dump while not recording", err)
	}
}

// holdStopDumpWrites makes each stop dump write wait for release, and
// counts the writes.
func holdStopDumpWrites(t *testing.T) (release func(), writes *atomic.Int32) {
	t.Helper()
	gate := make(chan struct{})
	var once sync.Once
	writes = &atomic.Int32{}
	prev := stopDumpHook
	stopDumpHook = func() {
		writes.Add(1)
		<-gate
	}
	release = func() { once.Do(func() { close(gate) }) }
	t.Cleanup(func() {
		release()
		stopDumpHook = prev
	})
	return release, writes
}

func TestRequestStopDumpDoesNotWait(t *testing.T) {
	// asking for a stop dump doesn't wait for the write, which for a large
	// recorder takes seconds: a stop phase asks and goes on
	withTestRecorder(t, 0)
	path := StopDumpPath(t.TempDir())
	release, _ := holdStopDumpWrites(t)
	asked := make(chan struct{})
	go func() {
		RequestStopDump(path)
		close(asked)
	}()
	select {
	case <-asked:
	case <-time.After(5 * time.Second):
		t.Fatal("RequestStopDump waited for the write")
	}
	release()
	if err := FlushStopDump(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("no stop dump after the flush: %v", err)
	}
}

func TestStopDumpRequestsCoalesce(t *testing.T) {
	// requests made while a write runs become one more write, from the
	// latest state, not one each
	withTestRecorder(t, 0)
	path := StopDumpPath(t.TempDir())
	release, writes := holdStopDumpWrites(t)
	RequestStopDump(path)
	deadline := time.Now().Add(5 * time.Second)
	for writes.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	for range 10 {
		RequestStopDump(path)
	}
	_, late := BeginOp(context.Background(), OpKindInternal, "engine.stop.late", OpOpts{})
	RequestStopDump(path)
	release()
	if err := FlushStopDump(path); err != nil {
		t.Fatal(err)
	}
	if n := writes.Load(); n > 3 {
		t.Fatalf("%d writes for 12 requests and a flush, want them coalesced (at most 3)", n)
	}
	h, _ := readDumpFile(t, path)
	var found bool
	for _, oo := range h.OpenOps {
		found = found || h.Strings[oo.ClassID] == "engine.stop.late"
	}
	if !found {
		t.Fatal("the flushed dump lacks the op begun before the last request")
	}
	late.End(OutcomeOK)
}

func TestStopDumpWriteIsAnOp(t *testing.T) {
	// each write is an op of its own, wcprof.stopDump, so its cost reads as
	// the write's; it runs on its own, so it's under no other op
	withTestRecorder(t, 0)
	path := StopDumpPath(t.TempDir())
	RequestStopDump(path)
	if err := FlushStopDump(path); err != nil {
		t.Fatal(err)
	}
	RequestStopDump(path)
	if err := FlushStopDump(path); err != nil {
		t.Fatal(err)
	}
	h, evs := readDumpFile(t, path)
	var writes int
	for _, ev := range evs {
		if ev.Type == "op" && h.Strings[ev.ClassID] == "wcprof.stopDump" {
			writes++
			if ev.ParentID != 0 {
				t.Fatalf("stop dump write under op %d, want none", ev.ParentID)
			}
		}
	}
	if writes == 0 {
		t.Fatal("no ended wcprof.stopDump op in the dump")
	}
}
