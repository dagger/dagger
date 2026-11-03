package wcprof

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"
)

var testTally = NewTallyClass("test.tallied")

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

// Calls tallied under an op are one tally event on it, recorded when it
// ends: their count, failures and summed duration, between its start and end.
// Before it ends they aren't in a dump.
func TestTallyOnItsOp(t *testing.T) {
	r := withTestRecorder(t, 0)
	ctx, parent := BeginOp(context.Background(), OpKindInternal, "test.parent", OpOpts{})
	for i, err := range []error{nil, errors.New("failed"), nil} {
		start := time.Now().Add(-time.Duration(i+1) * time.Millisecond)
		Tally(ctx, testTally, start, err)
	}
	if tallies, _, _ := tallyEvents(t, r, "test.tallied"); len(tallies) != 0 {
		t.Fatalf("tallies recorded before their op ended: %+v", tallies)
	}
	parent.End(OutcomeOK)

	tallies, ops, names := tallyEvents(t, r, "test.tallied")
	if len(ops) != 0 {
		t.Fatalf("tallied calls recorded as ops: %+v", ops)
	}
	if len(tallies) != 1 {
		t.Fatalf("want one tally event, got %+v", tallies)
	}
	got := tallies[0]
	if got.ParentID != parent.ID() || names[got.ClassID] != "test.tallied" || got.Count != 3 || got.Errors != 1 {
		t.Fatalf("tally: %+v", got)
	}
	if got.TotalNS < int64(6*time.Millisecond) {
		t.Fatalf("summed duration %d, want at least the 1+2+3 ms the calls took", got.TotalNS)
	}
	if got.StartNS > got.EndNS {
		t.Fatalf("tally spans %d to %d", got.StartNS, got.EndNS)
	}
}

// A call tallied under an op handed over by ID, whose op Tally can't reach,
// is recorded as an op of its own under that ID.
func TestTallyUnderAHandedOverOp(t *testing.T) {
	r := withTestRecorder(t, 0)
	_, parent := BeginOp(context.Background(), OpKindInternal, "test.parent", OpOpts{})
	handed := ContextWithOpID(context.Background(), parent.ID())
	Tally(handed, testTally, time.Now(), errors.New("failed"))
	parent.End(OutcomeOK)

	tallies, ops, _ := tallyEvents(t, r, "test.tallied")
	if len(tallies) != 0 {
		t.Fatalf("a handed-over op got tallies: %+v", tallies)
	}
	if len(ops) != 1 || ops[0].ParentID != parent.ID() || ops[0].Outcome != OutcomeError.String() {
		t.Fatalf("want one failed op under the handed-over op, got %+v", ops)
	}
}
