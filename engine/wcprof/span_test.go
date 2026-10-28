package wcprof

import (
	"bytes"
	"context"
	"testing"
)

// TestRecordSpan: an op that owns an OTel span records it, and the dump
// carries it as a span event naming the op and the span's trace and span
// IDs in hex. An op recorded after the fact records its span by op ID.
func TestRecordSpan(t *testing.T) {
	r := withTestRecorder(t, 0)
	ctx := context.Background()
	trace := [16]byte{0x0a, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 0xff}
	_, op := BeginOp(ctx, OpKindCallExec, "Container.from", OpOpts{})
	op.SetSpan(trace, [8]byte{1, 2, 3, 4, 5, 6, 7, 8})
	op.End(OutcomeOK)
	phase := RecordOp(ctx, OpKindExecPhase, "exec.containerStart", OpOpts{}, 0, 1, OutcomeOK)
	RecordSpan(ctx, phase, trace, [8]byte{0xff, 0, 0, 0, 0, 0, 0, 0x01})

	var buf bytes.Buffer
	if err := r.WriteDump(&buf, false); err != nil {
		t.Fatal(err)
	}
	_, evs, err := ReadDump(&buf)
	if err != nil {
		t.Fatal(err)
	}
	got := map[uint64]DumpEvent{}
	for _, ev := range evs {
		if ev.Type == "span" {
			got[ev.OpID] = ev
		}
	}
	want := map[uint64]DumpEvent{
		op.ID(): {Type: "span", OpID: op.ID(), TraceID: "0a0102030405060708090a0b0c0d0eff", SpanID: "0102030405060708"},
		phase:   {Type: "span", OpID: phase, TraceID: "0a0102030405060708090a0b0c0d0eff", SpanID: "ff00000000000001"},
	}
	if len(got) != len(want) {
		t.Fatalf("span events = %+v, want %+v", got, want)
	}
	for id, w := range want {
		g := got[id]
		g.StartNS, g.EndNS = 0, 0
		if g != w {
			t.Errorf("op %d: span event = %+v, want %+v", id, g, w)
		}
	}
}

// TestRecordSpanIgnoresNoSpan: a nil op, op ID 0, or an invalid (zero)
// span records nothing, and a disabled recorder records nothing.
func TestRecordSpanIgnoresNoSpan(t *testing.T) {
	r := withTestRecorder(t, 0)
	ctx := context.Background()
	var nilOp *Op
	nilOp.SetSpan([16]byte{1}, [8]byte{1})
	_, op := BeginOp(ctx, OpKindLazy, "Container.from", OpOpts{})
	op.SetSpan([16]byte{}, [8]byte{})
	op.SetSpan([16]byte{1}, [8]byte{})
	op.End(OutcomeOK)
	RecordSpan(ctx, 0, [16]byte{1}, [8]byte{1})
	for _, ev := range drainEvents(r) {
		if ev.Type == EventTypeSpan {
			t.Fatalf("recorded %+v, want no span event", ev)
		}
	}

	global.Store(nil)
	RecordSpan(ctx, 1, [16]byte{1}, [8]byte{1}) // must not panic
}
