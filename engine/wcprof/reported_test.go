package wcprof

import (
	"bytes"
	"context"
	"testing"
)

func TestRecordReportedRoundtrip(t *testing.T) {
	r := withTestRecorder(t, 0)
	ctx := context.Background()
	base := r.wallEpoch
	// The child arrives before its parent and before the resource it
	// acquires: references resolve at dump time.
	RecordReported(ctx, ReportedOp{
		TraceID: "t", SpanID: "pkg", ParentSpanID: "walk", Kind: OpKindTask, Work: WorkTypeEngine,
		Name: "package", Label: "example.com/a", Producer: "p1", StartUnixNS: base + 100, EndUnixNS: base + 900,
		Waits: []ReportedWait{{TargetTraceID: "t", TargetSpanID: "dep", Reason: WaitReasonDependency, StartUnixNS: base + 100, EndUnixNS: base + 300}},
	})
	RecordReported(ctx, ReportedOp{
		TraceID: "t", SpanID: "acq", ParentSpanID: "pkg", Kind: OpKindAcquire, Name: "acquire", Producer: "p1",
		StartUnixNS: base + 300, EndUnixNS: base + 800,
		Acquisition: &ReportedAcquisition{ResourceSpanID: "res", Priority: 7, PriorityInputs: map[string]string{"weight": "3"}, GrantedUnixNS: base + 350, Outcome: "released"},
	})
	RecordReported(ctx, ReportedOp{TraceID: "t", SpanID: "walk", Kind: OpKindTaskGroup, Name: "walk", Producer: "p1", StartUnixNS: base, EndUnixNS: base + 1000})
	RecordReported(ctx, ReportedOp{TraceID: "t", SpanID: "res", ParentSpanID: "walk", Kind: OpKindResource, Name: "resource", Producer: "p1",
		StartUnixNS: base, EndUnixNS: base + 1000, Resource: &ReportedResource{Name: "gobuild.slots", Capacity: 16, Policy: "priority_fifo"}})
	// A repeated export of a recorded span is not recorded twice.
	RecordReported(ctx, ReportedOp{TraceID: "t", SpanID: "walk", Kind: OpKindTaskGroup, Name: "walk", Producer: "p1", StartUnixNS: base, EndUnixNS: base + 1000})
	// An unended (live) span is not recorded.
	RecordReported(ctx, ReportedOp{TraceID: "t", SpanID: "live", Kind: OpKindTask, Name: "package", StartUnixNS: base})
	RecordProducerComplete(ctx, "p1", 5, 0)
	// An engine op caused by the package's span.
	_, engineOp := BeginOp(ctx, OpKindSessionPhase, "session.serveQuery", OpOpts{})
	LinkOTelParent(ctx, engineOp.ID(), "t", "pkg")
	LinkOTelParent(ctx, engineOp.ID(), "t", "not-reported") // no link for a non-reported cause
	engineOp.End(OutcomeOK)

	var buf bytes.Buffer
	if err := r.WriteDump(&buf, true); err != nil {
		t.Fatal(err)
	}
	header, events, err := ReadDump(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if header.EventCount != len(events) {
		t.Errorf("header counts %d events, dump has %d", header.EventCount, len(events))
	}
	byClass := map[string]DumpEvent{}
	var waits, unresolved, causes []DumpEvent
	var res, acq *DumpEvent
	for i, ev := range events {
		switch ev.Type {
		case "op":
			byClass[header.Strings[ev.ClassID]] = ev
		case "wait":
			waits = append(waits, ev)
		case "link":
			switch ev.LinkKind {
			case "unresolved_span":
				unresolved = append(unresolved, ev)
			case "reported_cause":
				causes = append(causes, ev)
			}
		case DumpTypeResource:
			res = &events[i]
		case DumpTypeAcquisition:
			acq = &events[i]
		}
	}
	walk, pkg := byClass["walk"], byClass["package"]
	if walk.OpKind != "task_group" || pkg.OpKind != "task" || pkg.ParentID != walk.OpID {
		t.Errorf("walk %+v, package %+v", walk, pkg)
	}
	if header.Strings[pkg.LabelID] != "example.com/a" || header.Strings[pkg.ProducerID] != "p1" || pkg.StartNS != 100 || pkg.EndNS != 900 {
		t.Errorf("package label/producer/times: %+v", pkg)
	}
	if len(waits) != 1 || waits[0].Reason != "dependency" || waits[0].TargetID != 0 || waits[0].ParentID != pkg.OpID {
		t.Errorf("waits %+v", waits)
	}
	if len(unresolved) != 1 || header.Strings[unresolved[0].IdentID] != "t/dep" {
		t.Errorf("unresolved %+v", unresolved)
	}
	if res == nil || res.Capacity != 16 || header.Strings[res.PolicyID] != "priority_fifo" || header.Strings[res.IdentID] != "gobuild.slots" || res.OpID != byClass["resource"].OpID {
		t.Errorf("resource %+v", res)
	}
	if acq == nil || acq.TargetID != byClass["resource"].OpID || acq.Priority != 7 || acq.GrantedNS != 350 || acq.Outcome != "released" || header.Strings[acq.MetaID] != `{"weight":"3"}` {
		t.Errorf("acquisition %+v", acq)
	}
	if len(causes) != 1 || causes[0].ParentID != engineOp.ID() || causes[0].TargetID != pkg.OpID {
		t.Errorf("causes %+v", causes)
	}
	if len(header.Producers) != 1 || header.Producers[0] != (DumpProducer{Producer: "p1", Received: 4, Declared: 5, Complete: true}) {
		t.Errorf("producers %+v", header.Producers)
	}
	if _, ok := byClass["live"]; ok {
		t.Error("a live span was recorded")
	}

	// Flushed: nothing reported is dumped again, but the span map is kept, so
	// a later span still resolves its parent to the earlier op.
	RecordReported(ctx, ReportedOp{TraceID: "t", SpanID: "pkg2", ParentSpanID: "walk", Kind: OpKindTask, Name: "package2", StartUnixNS: base + 10, EndUnixNS: base + 20})
	buf.Reset()
	if err := r.WriteDump(&buf, true); err != nil {
		t.Fatal(err)
	}
	_, events, err = ReadDump(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].ParentID != walk.OpID {
		t.Errorf("after flush: %+v", events)
	}
	if n := r.total.Load(); n != 0 {
		t.Errorf("total after flush = %d", n)
	}
}

func TestParseNames(t *testing.T) {
	if ParseOpKind("task_group") != OpKindTaskGroup || ParseOpKind("nope") != OpKindInvalid {
		t.Error("ParseOpKind")
	}
	if ParseWorkType("user") != WorkTypeUser || ParseWorkType("") != WorkTypeEngine {
		t.Error("ParseWorkType")
	}
	if ParseWaitReason("dependency") != WaitReasonDependency || ParseWaitReason("x") != WaitReasonInvalid {
		t.Error("ParseWaitReason")
	}
}
