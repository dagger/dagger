package wcprof

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestRecordUsageRoundtrip(t *testing.T) {
	r := withTestRecorder(t, 0)
	ctx, op := BeginOp(context.Background(), OpKindExec, "exec.run", OpOpts{})
	RecordUsage(ctx, op.ID(), UsageScopeMountHelpers, Usage{CPUUserUS: 1200, CPUSystemUS: 300, MemPeakBytes: 4096})
	RecordUsage(ctx, op.ID(), UsageScopeExecCgroup, Usage{CPUUserUS: 1250, CPUSystemUS: 340})
	RecordUsage(ctx, op.ID(), UsageScopeExecSched, Usage{RunqWaitUS: 700, RunqWaits: 9, OnCPUUS: 1500})
	RecordUsage(ctx, 0, UsageScopeMountHelpers, Usage{CPUUserUS: 1}) // no op: dropped silently
	op.End(OutcomeOK)

	var buf bytes.Buffer
	if err := r.WriteDump(&buf, true); err != nil {
		t.Fatal(err)
	}
	header, events, err := ReadDump(&buf)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]DumpEvent{}
	for _, ev := range events {
		if ev.Type == DumpTypeUsage {
			if ev.OpID != op.ID() {
				t.Errorf("usage on op %d, want %d", ev.OpID, op.ID())
			}
			got[header.Strings[ev.ClassID]] = ev
		}
	}
	if len(got) != 3 {
		t.Fatalf("got %d usage events, want 3: %+v", len(got), got)
	}
	if c := got[UsageScopeMountHelpers]; c.CPUUserUS != 1200 || c.CPUSystemUS != 300 || c.MemPeakBytes != 4096 {
		t.Errorf("mount helpers' usage %+v", c)
	}
	if e := got[UsageScopeExecCgroup]; e.CPUUserUS != 1250 || e.CPUSystemUS != 340 || e.MemPeakBytes != 0 {
		t.Errorf("exec cgroup usage %+v", e)
	}
	if e := got[UsageScopeExecSched]; e.RunqWaitUS != 700 || e.RunqWaits != 9 || e.OnCPUUS != 1500 {
		t.Errorf("exec scheduling usage %+v", e)
	}
	if header.EventCount != len(events) {
		t.Errorf("header counts %d events, dump has %d", header.EventCount, len(events))
	}

	// Flushed: a second dump has no usage left, and the cap's count is back to zero.
	buf.Reset()
	if err := r.WriteDump(&buf, true); err != nil {
		t.Fatal(err)
	}
	_, events, err = ReadDump(&buf)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range events {
		if ev.Type == DumpTypeUsage {
			t.Errorf("usage survived a flush: %+v", ev)
		}
	}
	if n := r.total.Load(); n != 0 {
		t.Errorf("total after flush = %d, want 0", n)
	}
}

func TestRecordUsageDisabledAndCapped(t *testing.T) {
	prev := global.Load()
	global.Store(nil)
	RecordUsage(context.Background(), 7, UsageScopeMountHelpers, Usage{CPUUserUS: 1}) // must not panic
	global.Store(prev)

	r := withTestRecorder(t, 1)
	RecordUsage(context.Background(), 7, UsageScopeMountHelpers, Usage{CPUUserUS: 1})
	RecordUsage(context.Background(), 8, UsageScopeMountHelpers, Usage{CPUUserUS: 2})
	if d := r.dropped.Load(); d != 1 {
		t.Errorf("dropped = %d, want 1 past the cap", d)
	}
}

// A dump's header lists the eBPF programs recordings asked for, with why any
// couldn't be attached.
func TestDumpEBPFPrograms(t *testing.T) {
	r := withTestRecorder(t, 0)
	t.Cleanup(func() {
		ebpfPrograms.mu.Lock()
		ebpfPrograms.byName = nil
		ebpfPrograms.mu.Unlock()
	})
	NoteEBPFProgram("b", errors.New("kernel BTF not available"))
	NoteEBPFProgram("a", nil)
	var buf bytes.Buffer
	if err := r.WriteDump(&buf, true); err != nil {
		t.Fatal(err)
	}
	header, _, err := ReadDump(&buf)
	if err != nil {
		t.Fatal(err)
	}
	want := []DumpEBPFProgram{{Name: "a"}, {Name: "b", Error: "kernel BTF not available"}}
	if !reflect.DeepEqual(header.EBPF, want) {
		t.Errorf("header's eBPF programs %+v, want %+v", header.EBPF, want)
	}
}
