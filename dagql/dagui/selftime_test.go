package dagui

import (
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/call/callpbv1"
)

// The time-breakdown tests model the span shapes the engine actually emits
// (verified against real traces): call spans with same-name call_exec twins
// nested under them, lazy "resume" marker spans parented under the row whose
// evaluation triggered them and cause-linked back to the op they resume, and
// deferred exec children recorded under the original call span long after it
// returned.

var testEpoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func at(sec float64) time.Time {
	return testEpoch.Add(time.Duration(sec * float64(time.Second)))
}

func waitLink(target SpanID, reason string, from, to time.Time) SpanLink {
	return SpanLink{
		SpanContext: SpanContext{SpanID: target},
		Purpose:     "wait",
		WaitReason:  reason,
		WaitStart:   from,
		WaitEnd:     to,
	}
}

func causeLink(target SpanID) SpanLink {
	return SpanLink{
		SpanContext: SpanContext{SpanID: target},
		Purpose:     "cause",
	}
}

// buildLazyChainDB constructs the reference-trace shape:
//
//	q (query root) [0,16]
//	├── w2 "Container.withExec" [1,1.5]      (go build; forced later)
//	│   ├── r2 "resume withExec" [3,13]      (cause→w2; the forced eval marker)
//	│   └── e2 exec [3,13]                   (the real go build, argv attr)
//	├── w1 "Container.withExec" [2,2.5]      (waitdemo; forced later)
//	│   └── (r1 is not w1's child; it hangs under the trigger, like real traces)
//	└── s "Container.stdout" [3,16]          (the forcing op)
//	    └── x "Container.stdout" twin [3,16] (call_exec; wait lazy→r1 [3,16])
//	        └── r1 "resume withExec" [3,16]  (cause→w1; wait lazy→r2 [3,13])
func buildLazyChainDB(t *testing.T) (*DB, SpanID, SpanID, SpanID) {
	t.Helper()
	db := NewDB()

	q := testID(1)
	w2 := testID(2)
	r2 := testID(3)
	e2 := testID(4)
	w1 := testID(5)
	s := testID(6)
	x := testID(7)
	r1 := testID(8)

	e2snap := SpanSnapshot{
		ID: e2, ParentID: w2, Name: "exec go build",
		StartTime: at(3), EndTime: at(13),
	}
	e2snap.ProcessAttribute("wcprof.exec.argv", `["go","build","-o","/tmp/waitdemo","."]`)

	db.ImportSnapshots([]SpanSnapshot{
		{ID: q, Name: "query", StartTime: at(0), EndTime: at(16)},
		{ID: w2, ParentID: q, Name: "Container.withExec", StartTime: at(1), EndTime: at(1.5)},
		{ID: r2, ParentID: w2, Name: "resume withExec", StartTime: at(3), EndTime: at(13),
			Links: []SpanLink{causeLink(w2)}},
		e2snap,
		{ID: w1, ParentID: q, Name: "Container.withExec", StartTime: at(2), EndTime: at(2.5)},
		{ID: s, ParentID: q, Name: "Container.stdout", StartTime: at(3), EndTime: at(16)},
		{ID: x, ParentID: s, Name: "Container.stdout", StartTime: at(3), EndTime: at(16),
			Links: []SpanLink{waitLink(r1, "lazy", at(3), at(16))}},
		{ID: r1, ParentID: x, Name: "resume withExec", StartTime: at(3), EndTime: at(16),
			Links: []SpanLink{causeLink(w1), waitLink(r2, "lazy", at(3), at(13))}},
	})
	return db, s, w1, w2
}

func TestTimeBreakdownForcingOp(t *testing.T) {
	db, s, w1, w2 := buildLazyChainDB(t)
	span := db.Spans.Map[s]
	hb := span.TimeBreakdown(at(20))

	if !hb.Material {
		t.Fatalf("forcing op should be material: %+v", hb)
	}
	if hb.Self > 100*time.Millisecond {
		t.Fatalf("forcing op own work should be ~0, got %v", hb.Self)
	}
	if hb.Waiting != 13*time.Second {
		t.Fatalf("forcing op should wait 13s, got %v", hb.Waiting)
	}

	// Per-instant transitive resolution: while go build ran, stdout reads as
	// waiting on w2 (indirectly); after it finished, on w1 (directly).
	var segs []TimeSegment
	for _, seg := range hb.Segments {
		if seg.Waiting {
			segs = append(segs, seg)
		}
	}
	if len(segs) != 2 {
		t.Fatalf("expected 2 waiting segments, got %+v", segs)
	}
	if segs[0].Target != db.Spans.Map[w2].ID || !segs[0].Indirect {
		t.Fatalf("first stretch should be indirect on w2: %+v", segs[0])
	}
	if segs[0].Label != "go build -o /tmp/waitdemo ." {
		t.Fatalf("blocker label should use the exec argv, got %q", segs[0].Label)
	}
	if segs[1].Target != db.Spans.Map[w1].ID || segs[1].Indirect {
		t.Fatalf("second stretch should be direct on w1: %+v", segs[1])
	}

	if hb.DominantTarget != w2 {
		t.Fatalf("dominant blocker should be w2, got %v (%s)", hb.DominantTarget, hb.DominantLabel)
	}
}

func TestTimeBreakdownForcedOpSelfTime(t *testing.T) {
	db, _, w1, w2 := buildLazyChainDB(t)

	// w2 hosted its own deferred exec: painted via the late child + resume
	// effect, no genuine waits, all own work.
	hb2 := db.Spans.Map[w2].TimeBreakdown(at(20))
	if hb2.Material {
		t.Fatalf("w2 should not be material (hosting its own work): %+v", hb2)
	}
	if want := 10*time.Second + 500*time.Millisecond; hb2.Self != want {
		t.Fatalf("w2 own work should be %v (sync 0.5 + exec 10), got %v", want, hb2.Self)
	}

	// w1's deferred evaluation (r1) spent [3,13] waiting on w2's eval and
	// [13,16] doing its own work.
	hb1 := db.Spans.Map[w1].TimeBreakdown(at(20))
	if !hb1.Material {
		t.Fatalf("w1 should be material: %+v", hb1)
	}
	if hb1.Waiting != 10*time.Second {
		t.Fatalf("w1 should wait 10s on w2, got %v", hb1.Waiting)
	}
	if want := 3*time.Second + 500*time.Millisecond; hb1.Self != want {
		t.Fatalf("w1 own work should be %v (sync 0.5 + tail 3), got %v", want, hb1.Self)
	}
	if hb1.DominantTarget != w2 {
		t.Fatalf("w1 dominant blocker should be w2, got %v", hb1.DominantTarget)
	}
}

func TestTimeBreakdownSubtreeRule(t *testing.T) {
	db := NewDB()
	q := testID(1)
	m := testID(2)
	tw := testID(3)
	inner := testID(4)
	rh := testID(5)

	db.ImportSnapshots([]SpanSnapshot{
		{ID: q, Name: "query", StartTime: at(0), EndTime: at(10)},
		{ID: m, ParentID: q, Name: "Viztest.useExecService", StartTime: at(0), EndTime: at(10)},
		{ID: tw, ParentID: m, Name: "Viztest.useExecService", StartTime: at(0), EndTime: at(10),
			Links: []SpanLink{waitLink(rh, "lazy", at(1), at(4))}},
		{ID: inner, ParentID: tw, Name: "load sdk runtime", StartTime: at(0.5), EndTime: at(4)},
		{ID: rh, ParentID: inner, Name: "resume withoutMount", StartTime: at(1), EndTime: at(4),
			Links: []SpanLink{causeLink(inner)}},
	})

	// The wait resolves to work rendered inside the row's own subtree: that
	// is the row hosting its nested work, not waiting on something else.
	hb := db.Spans.Map[m].TimeBreakdown(at(20))
	if hb.Waiting != 0 {
		t.Fatalf("hosting row should have no waiting, got %v (%+v)", hb.Waiting, hb.Segments)
	}
	if hb.Self != 10*time.Second {
		t.Fatalf("hosting row own work should be 10s, got %v", hb.Self)
	}
}

func TestTimeBreakdownCalmWithoutWaits(t *testing.T) {
	db := NewDB()
	db.ImportSnapshots([]SpanSnapshot{
		{ID: testID(1), Name: "plain", StartTime: at(0), EndTime: at(2)},
	})
	hb := db.Spans.Map[testID(1)].TimeBreakdown(at(20))
	if hb.Material || hb.Waiting != 0 || hb.Self != 2*time.Second {
		t.Fatalf("plain span should be calm all-own: %+v", hb)
	}
}

func TestWaitingNowLiveBlocked(t *testing.T) {
	db := NewDB()
	q := testID(1)
	w1 := testID(2)
	s := testID(3)
	x := testID(4)
	r1 := testID(5)
	now := at(8)

	// stdout started at 3 and is still running; the forced evaluation of
	// w1's lazy (r1) is running too. No wait edge exists yet (it appears
	// only when the wait ends) — the running resume chain is the signal.
	db.ImportSnapshots([]SpanSnapshot{
		{ID: q, Name: "query", StartTime: at(0)},
		{ID: w1, ParentID: q, Name: "Container.withExec", StartTime: at(2), EndTime: at(2.5)},
		{ID: s, ParentID: q, Name: "Container.stdout", StartTime: at(3)},
		{ID: x, ParentID: s, Name: "Container.stdout", StartTime: at(3)},
		{ID: r1, ParentID: x, Name: "resume withExec", StartTime: at(3),
			Links: []SpanLink{causeLink(w1)}},
	})

	span := db.Spans.Map[s]
	blocker, ok := span.WaitingNow(now)
	if !ok {
		t.Fatal("running forcing op should report blocked-now")
	}
	if blocker == nil || blocker.ID != w1 {
		t.Fatalf("live blocker should resolve to w1, got %+v", blocker)
	}

	// The displayed number must not tick up while blocked: own work stays ~0
	// as now advances.
	hbA := span.TimeBreakdown(at(6))
	hbB := span.TimeBreakdown(at(12))
	if hbA.Self > 100*time.Millisecond || hbB.Self > 100*time.Millisecond {
		t.Fatalf("own work should stay ~0 while blocked: %v then %v", hbA.Self, hbB.Self)
	}
	if hbB.Waiting <= hbA.Waiting {
		t.Fatalf("waiting should grow while blocked: %v then %v", hbA.Waiting, hbB.Waiting)
	}
}

func TestWaitingNowHostingNotBlocked(t *testing.T) {
	db := NewDB()
	q := testID(1)
	m := testID(2)
	inner := testID(3)
	now := at(5)

	// A module call hosting its own running nested work is working, not
	// waiting.
	db.ImportSnapshots([]SpanSnapshot{
		{ID: q, Name: "query", StartTime: at(0)},
		{ID: m, ParentID: q, Name: "Viztest.useExecService", StartTime: at(0)},
		{ID: inner, ParentID: m, Name: "load sdk runtime", StartTime: at(0.5)},
	})

	if _, ok := db.Spans.Map[m].WaitingNow(now); ok {
		t.Fatal("hosting row should not report blocked-now")
	}
	hb := db.Spans.Map[m].TimeBreakdown(now)
	if hb.Waiting != 0 {
		t.Fatalf("hosting row should have no waiting, got %+v", hb.Segments)
	}
}

func TestWaitingNowCompletedRow(t *testing.T) {
	db, s, _, _ := buildLazyChainDB(t)
	if _, ok := db.Spans.Map[s].WaitingNow(at(20)); ok {
		t.Fatal("completed row must never report blocked-now")
	}
}

func TestWaitingNowForcedRowBlockedDeeper(t *testing.T) {
	db := NewDB()
	q := testID(1)
	a := testID(2)
	b := testID(3)
	rb := testID(4)
	ra := testID(5)
	now := at(9)

	// The forced-row live case: b's own deferred eval (rb) is running —
	// which alone is b working, not waiting — but rb is itself stuck on a's
	// eval (ra, running, resolving to sibling row a). No wait edges exist
	// yet. b must read as blocked on a, with own work frozen, not ticking.
	db.ImportSnapshots([]SpanSnapshot{
		{ID: q, Name: "query", StartTime: at(0)},
		{ID: a, ParentID: q, Name: "Container.withExec", StartTime: at(1), EndTime: at(1.5)},
		{ID: b, ParentID: q, Name: "Container.withExec", StartTime: at(2), EndTime: at(2.5)},
		{ID: rb, ParentID: q, Name: "resume withExec", StartTime: at(3),
			Links: []SpanLink{causeLink(b)}},
		{ID: ra, ParentID: rb, Name: "resume withExec", StartTime: at(3),
			Links: []SpanLink{causeLink(a)}},
	})

	span := db.Spans.Map[b]
	blocker, ok := span.WaitingNow(now)
	if !ok {
		t.Fatal("forced row with deferred eval blocked deeper should report blocked-now")
	}
	if blocker == nil || blocker.ID != a {
		t.Fatalf("live blocker should resolve to sibling row a, got %+v", blocker)
	}

	// Self work must not tick up while blocked: frozen at the sync part.
	hbA := span.TimeBreakdown(at(6))
	hbB := span.TimeBreakdown(at(12))
	if hbA.Self != hbB.Self {
		t.Fatalf("own work must stay frozen while blocked: %v then %v", hbA.Self, hbB.Self)
	}
	if hbA.Self != 500*time.Millisecond {
		t.Fatalf("own work should be the 0.5s sync part, got %v", hbA.Self)
	}
}

func TestTimeBreakdownInferredPastWait(t *testing.T) {
	db := NewDB()
	q := testID(1)
	a := testID(2)
	b := testID(3)
	rb := testID(4)
	ra := testID(5)
	eb := testID(6)
	now := at(6)

	// The mid-run forced-row case seen in live captures: b's deferred eval
	// spent [1,5.5] blocked on a's eval (ra, completed), and b's own exec
	// (eb) has been running since 5.5. The explicit wait edge for [1,5.5]
	// lives on the still-open resume span and hasn't been re-exported yet —
	// the completed ra interval must stand in for it, or b's number falls
	// back to the wall-clock lie the moment its own work starts.
	db.ImportSnapshots([]SpanSnapshot{
		{ID: q, Name: "query", StartTime: at(0)},
		{ID: a, ParentID: q, Name: "Container.withExec", StartTime: at(0.5), EndTime: at(0.9)},
		{ID: b, ParentID: q, Name: "Container.withExec", StartTime: at(0.9), EndTime: at(1)},
		{ID: rb, ParentID: q, Name: "resume withExec", StartTime: at(1),
			Links: []SpanLink{causeLink(b)}},
		{ID: ra, ParentID: b, Name: "resume withExec", StartTime: at(1), EndTime: at(5.5),
			Links: []SpanLink{causeLink(a)}},
		{ID: eb, ParentID: b, Name: "exec sh", StartTime: at(5.5)},
	})

	span := db.Spans.Map[b]
	hb := span.TimeBreakdown(now)
	if want := 600 * time.Millisecond; hb.Self != want {
		t.Fatalf("own work should be sync 0.1 + exec 0.5 = %v, got %v (%+v)", want, hb.Self, hb.Segments)
	}
	if want := 4500 * time.Millisecond; hb.Waiting != want {
		t.Fatalf("waiting should cover the completed deeper eval %v, got %v", want, hb.Waiting)
	}
	// And right now b is working (its own exec), not waiting.
	if _, ok := span.WaitingNow(now); ok {
		t.Fatal("row running its own exec must not report blocked-now")
	}
}

func TestBlockerLabelFromCallArgs(t *testing.T) {
	db := NewDB()
	q := testID(1)
	w := testID(2)
	s := testID(3)
	x := testID(4)
	r := testID(5)
	now := at(8)

	// The blocker hasn't started its exec yet (no wcprof.exec.argv anywhere),
	// but its call args are known from chain-build time — live labels must
	// carry the real command, not the generic call name.
	call := &callpbv1.Call{
		Field: "withExec",
		Type:  &callpbv1.Type{NamedType: "Container"},
		Args: []*callpbv1.Argument{{
			Name: "args",
			Value: &callpbv1.Literal{Value: &callpbv1.Literal_List{List: &callpbv1.List{
				Values: []*callpbv1.Literal{
					{Value: &callpbv1.Literal_String_{String_: "go"}},
					{Value: &callpbv1.Literal_String_{String_: "build"}},
					{Value: &callpbv1.Literal_String_{String_: "-o"}},
					{Value: &callpbv1.Literal_String_{String_: "/tmp/waitdemo"}},
					{Value: &callpbv1.Literal_String_{String_: "."}},
				},
			}}},
		}},
		Digest: "sha256:test-withexec",
	}
	payload, err := call.Encode()
	if err != nil {
		t.Fatal(err)
	}

	db.ImportSnapshots([]SpanSnapshot{
		{ID: q, Name: "query", StartTime: at(0)},
		{ID: w, ParentID: q, Name: "Container.withExec", StartTime: at(2), EndTime: at(2.5),
			CallDigest: call.Digest, CallPayload: payload},
		{ID: s, ParentID: q, Name: "Container.stdout", StartTime: at(3)},
		{ID: x, ParentID: s, Name: "Container.stdout", StartTime: at(3)},
		{ID: r, ParentID: x, Name: "resume withExec", StartTime: at(3),
			Links: []SpanLink{causeLink(w)}},
	})

	span := db.Spans.Map[s]
	hb := span.TimeBreakdown(now)
	seg, ok := hb.BlockedNow(now)
	if !ok {
		t.Fatal("forcing op should be blocked-now")
	}
	if seg.Label != "go build -o /tmp/waitdemo ." {
		t.Fatalf("live blocker label should come from the call args, got %q", seg.Label)
	}
}

func TestLiveChainResolvesToWorkingOp(t *testing.T) {
	db := NewDB()
	q := testID(1)
	cRow := testID(2)
	cExec := testID(3)
	bRow := testID(4)
	mC := testID(5)
	aRow := testID(6)
	aTwin := testID(7)
	mB := testID(8)
	now := at(8)

	// Live chain: a is blocked on b, b is blocked on c, and c is the one
	// actually working (its exec is running). No wait edges exist yet.
	// a must read "waiting on c" (via b), not stop at its next hop.
	cCall := &callpbv1.Call{
		Field: "withExec",
		Type:  &callpbv1.Type{NamedType: "Container"},
		Args: []*callpbv1.Argument{{
			Name: "args",
			Value: &callpbv1.Literal{Value: &callpbv1.Literal_List{List: &callpbv1.List{
				Values: []*callpbv1.Literal{
					{Value: &callpbv1.Literal_String_{String_: "sh"}},
					{Value: &callpbv1.Literal_String_{String_: "-c"}},
					{Value: &callpbv1.Literal_String_{String_: "printf hi"}},
				},
			}}},
		}},
		Digest: "sha256:test-c",
	}
	payload, err := cCall.Encode()
	if err != nil {
		t.Fatal(err)
	}

	db.ImportSnapshots([]SpanSnapshot{
		{ID: q, Name: "query", StartTime: at(0)},
		{ID: cRow, ParentID: q, Name: "Container.withExec", StartTime: at(0.5), EndTime: at(0.9),
			CallDigest: cCall.Digest, CallPayload: payload},
		{ID: cExec, ParentID: cRow, Name: "exec sh", StartTime: at(1)},
		{ID: bRow, ParentID: q, Name: "Container.withExec", StartTime: at(1), EndTime: at(1.5)},
		{ID: mC, ParentID: bRow, Name: "resume withExec", StartTime: at(2),
			Links: []SpanLink{causeLink(cRow)}},
		{ID: aRow, ParentID: q, Name: "Container.stdout", StartTime: at(3)},
		{ID: aTwin, ParentID: aRow, Name: "Container.stdout", StartTime: at(3)},
		{ID: mB, ParentID: aTwin, Name: "resume withExec", StartTime: at(3),
			Links: []SpanLink{causeLink(bRow)}},
	})

	a := db.Spans.Map[aRow]
	blocker, ok := a.WaitingNow(now)
	if !ok {
		t.Fatal("a should be blocked-now")
	}
	if blocker == nil || blocker.ID != cRow {
		t.Fatalf("live chain should resolve to the working op c, got %+v", blocker)
	}
	seg, _ := a.TimeBreakdown(now).BlockedNow(now)
	if seg.Label != "sh -c printf hi" {
		t.Fatalf("label should be c's real command, got %q", seg.Label)
	}
	if !seg.Indirect || seg.Via == "" || seg.Via == seg.Label {
		t.Fatalf("segment should be indirect via the first hop, got %+v", seg)
	}

	// b's own row also resolves through to c.
	b := db.Spans.Map[bRow]
	bBlocker, ok := b.WaitingNow(now)
	if !ok || bBlocker == nil || bBlocker.ID != cRow {
		t.Fatalf("b should be blocked-now on c, got %+v ok=%v", bBlocker, ok)
	}
}
