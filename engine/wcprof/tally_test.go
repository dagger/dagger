package wcprof

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"
)

var testTally = NewTallyClass(OpKindInternal, "test.tallied")

// tallyEvents dumps r and returns its tally events and its ops of class.
func tallyEvents(t *testing.T, r *Recorder, class string) (tallies, ops []DumpEvent, names []string) {
	t.Helper()
	var buf bytes.Buffer
	if err := r.WriteDump(&buf, false); err != nil {
		t.Fatal(err)
	}
	header, events, err := ReadDump(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if header.EventCount != len(events) {
		t.Fatalf("header counts %d events, the dump has %d", header.EventCount, len(events))
	}
	for _, ev := range events {
		switch {
		case ev.Type == DumpTypeTally:
			tallies = append(tallies, ev)
		case ev.Type == "op" && header.Strings[ev.ClassID] == class:
			ops = append(ops, ev)
		}
	}
	return tallies, ops, header.Strings
}

// tallied makes a tallied call of testTally under ctx that takes d, failed
// when err is set.
func tallied(ctx context.Context, d time.Duration, err error) {
	call := BeginTally(ctx, testTally)
	time.Sleep(d)
	call.End(err)
}

// Calls tallied under an op are one tally event on it, recorded when it
// ends: their kind, count, failures and summed duration, between its start
// and end. A dump taken while it's open has its tallies so far, to the dump.
func TestTallyOnItsOp(t *testing.T) {
	r := withTestRecorder(t, 0)
	ctx, parent := BeginOp(context.Background(), OpKindInternal, "test.parent", OpOpts{})
	tallied(ctx, time.Millisecond, nil)
	if tallies, _, _ := tallyEvents(t, r, "test.tallied"); len(tallies) != 1 || tallies[0].Count != 1 || tallies[0].ParentID != parent.ID() {
		t.Fatalf("an open op's tallies so far: %+v", tallies)
	}
	tallied(ctx, 2*time.Millisecond, errors.New("failed"))
	tallied(ctx, 3*time.Millisecond, nil)
	parent.End(OutcomeOK)

	tallies, ops, names := tallyEvents(t, r, "test.tallied")
	if len(ops) != 0 {
		t.Fatalf("tallied calls recorded as ops: %+v", ops)
	}
	if len(tallies) != 1 {
		t.Fatalf("want one tally event, got %+v", tallies)
	}
	got := tallies[0]
	if got.ParentID != parent.ID() || names[got.ClassID] != "test.tallied" || got.OpKind != OpKindInternal.String() || got.Count != 3 || got.Errors != 1 {
		t.Fatalf("tally: %+v", got)
	}
	if got.TotalNS < int64(6*time.Millisecond) {
		t.Fatalf("summed duration %d, want at least the 1+2+3 ms the calls took", got.TotalNS)
	}
	if got.StartNS > got.EndNS {
		t.Fatalf("tally spans %d to %d", got.StartNS, got.EndNS)
	}
}

// A tallied call under an op handed over by ID, whose op it can't reach, or
// under an op that has ended, is recorded as an op of its own under it.
func TestTallyFallsBackToAnOp(t *testing.T) {
	r := withTestRecorder(t, 0)
	ctx, parent := BeginOp(context.Background(), OpKindInternal, "test.parent", OpOpts{})
	tallied(ContextWithOpID(context.Background(), parent.ID()), 0, errors.New("failed"))
	parent.End(OutcomeOK)
	tallied(ctx, 0, nil)

	tallies, ops, _ := tallyEvents(t, r, "test.tallied")
	if len(tallies) != 0 {
		t.Fatalf("calls tallied on an op they couldn't reach or after its end: %+v", tallies)
	}
	if len(ops) != 2 {
		t.Fatalf("want two ops, got %+v", ops)
	}
	for _, op := range ops {
		if op.ParentID != parent.ID() || op.OpKind != OpKindInternal.String() {
			t.Fatalf("op %+v, want under the parent, internal", op)
		}
	}
}

// An unprofiled call is the zero call, which records nothing.
func TestTallyUnprofiled(t *testing.T) {
	withTestRecorder(t, 0)
	globalOn.Store(false)
	if call := BeginTally(context.Background(), testTally); call != (TallyCall{}) {
		t.Fatalf("an unprofiled call began %+v", call)
	}
}
