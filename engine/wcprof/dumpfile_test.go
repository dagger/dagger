package wcprof

import (
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
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
