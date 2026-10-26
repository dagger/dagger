package contract

import (
	"reflect"
	"testing"

	telemetry "github.com/dagger/otel-go"

	"github.com/dagger/dagger/engine/telemetryattrs"
)

func waitLink(target, reason string, start, end string) Link {
	return Link{TraceID: "t1", SpanID: target, Attrs: map[string]any{
		telemetry.LinkPurposeAttr:                 telemetryattrs.LinkPurposeWait,
		telemetryattrs.WcprofWaitReasonAttr:       reason,
		telemetryattrs.WcprofWaitStartUnixNanoAttr: start,
		telemetryattrs.WcprofWaitEndUnixNanoAttr:   end,
	}}
}

func TestDecodeTask(t *testing.T) {
	r, err := Decode(Span{
		TraceID: "t1", SpanID: "s2", ParentSpanID: "s1", StartUnixNS: 100, EndUnixNS: 900,
		Attrs: map[string]any{
			telemetryattrs.WcprofOpKindAttr:   "task",
			telemetryattrs.WcprofWorkTypeAttr: "user",
			telemetryattrs.WcprofLabelAttr:    "example.com/a",
			telemetryattrs.WcprofProducerAttr: "gobuild-1",
		},
		Links: []Link{
			waitLink("s3", "dependency", "100", "250"),
			waitLink("s4", "dependency", "100", "100"), // already done: kept
			{SpanID: "s5", Attrs: map[string]any{telemetry.LinkPurposeAttr: "cause"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := &Op{Kind: "task", WorkType: "user", Label: "example.com/a", Producer: "gobuild-1", CausalParent: "s1", StartUnixNS: 100, EndUnixNS: 900}
	if !reflect.DeepEqual(r.Op, want) {
		t.Errorf("op %+v, want %+v", r.Op, want)
	}
	if len(r.Waits) != 2 || r.Waits[0].TargetSpanID != "s3" || r.Waits[0].EndUnixNS != 250 || r.Waits[1].StartUnixNS != r.Waits[1].EndUnixNS {
		t.Errorf("waits %+v", r.Waits)
	}
}

func TestDecodeCausalParentOverride(t *testing.T) {
	r, err := Decode(Span{SpanID: "s2", ParentSpanID: "s1", Attrs: map[string]any{
		telemetryattrs.WcprofOpKindAttr: "task_group",
		telemetryattrs.WcprofParentAttr: "s9",
	}})
	if err != nil || r.Op == nil || r.Op.CausalParent != "s9" || r.Op.WorkType != "engine" {
		t.Fatalf("got %+v, %v", r.Op, err)
	}
}

func TestDecodeResourceAndAcquire(t *testing.T) {
	r, err := Decode(Span{SpanID: "res1", Attrs: map[string]any{
		telemetryattrs.WcprofOpKindAttr:           "resource",
		telemetryattrs.WcprofResourceNameAttr:     "gobuild.slots",
		telemetryattrs.WcprofResourceCapacityAttr: "16",
		telemetryattrs.WcprofResourcePolicyAttr:   PolicyPriorityFIFO,
	}})
	if err != nil || r.Resource == nil || *r.Resource != (Resource{Name: "gobuild.slots", Capacity: 16, Policy: PolicyPriorityFIFO}) {
		t.Fatalf("resource %+v, %v", r.Resource, err)
	}
	r, err = Decode(Span{SpanID: "acq1", StartUnixNS: 10, EndUnixNS: 50, Attrs: map[string]any{
		telemetryattrs.WcprofOpKindAttr:                  "acquire",
		telemetryattrs.WcprofResourceIDAttr:              "res1",
		telemetryattrs.WcprofResourcePriorityAttr:        "504780",
		telemetryattrs.WcprofResourcePriorityInputsAttr:  `{"weight":"166315"}`,
		telemetryattrs.WcprofResourceGrantedUnixNanoAttr: "30",
		telemetryattrs.WcprofResourceOutcomeAttr:         OutcomeReleased,
	}})
	if err != nil {
		t.Fatal(err)
	}
	want := &Acquisition{ResourceID: "res1", Priority: 504780, PriorityInputs: map[string]string{"weight": "166315"}, GrantedUnixNS: 30, Outcome: OutcomeReleased}
	if !reflect.DeepEqual(r.Acquisition, want) {
		t.Errorf("acquisition %+v, want %+v", r.Acquisition, want)
	}
}

func TestDecodeRejectsMalformed(t *testing.T) {
	for name, attrs := range map[string]map[string]any{
		"resource without capacity": {telemetryattrs.WcprofOpKindAttr: "resource", telemetryattrs.WcprofResourcePolicyAttr: "fifo"},
		"resource with zero capacity": {telemetryattrs.WcprofOpKindAttr: "resource", telemetryattrs.WcprofResourceCapacityAttr: "0", telemetryattrs.WcprofResourcePolicyAttr: "fifo"},
		"resource with unknown policy": {telemetryattrs.WcprofOpKindAttr: "resource", telemetryattrs.WcprofResourceCapacityAttr: "2", telemetryattrs.WcprofResourcePolicyAttr: "lifo"},
		"acquire without resource": {telemetryattrs.WcprofOpKindAttr: "acquire", telemetryattrs.WcprofResourceOutcomeAttr: OutcomeCancelled},
		"released without a grant": {telemetryattrs.WcprofOpKindAttr: "acquire", telemetryattrs.WcprofResourceIDAttr: "r", telemetryattrs.WcprofResourceOutcomeAttr: OutcomeReleased},
		"unknown outcome": {telemetryattrs.WcprofOpKindAttr: "acquire", telemetryattrs.WcprofResourceIDAttr: "r", telemetryattrs.WcprofResourceGrantedUnixNanoAttr: "1", telemetryattrs.WcprofResourceOutcomeAttr: "lost"},
		"bad priority inputs": {telemetryattrs.WcprofOpKindAttr: "acquire", telemetryattrs.WcprofResourceIDAttr: "r", telemetryattrs.WcprofResourceOutcomeAttr: OutcomeCancelled, telemetryattrs.WcprofResourcePriorityInputsAttr: "{"},
		"non-decimal priority": {telemetryattrs.WcprofOpKindAttr: "acquire", telemetryattrs.WcprofResourceIDAttr: "r", telemetryattrs.WcprofResourceOutcomeAttr: OutcomeCancelled, telemetryattrs.WcprofResourcePriorityAttr: "high"},
	} {
		if _, err := Decode(Span{SpanID: "x", Attrs: attrs}); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	// A wait link without its interval is malformed too.
	if _, err := Decode(Span{SpanID: "x", Attrs: map[string]any{telemetryattrs.WcprofOpKindAttr: "task"},
		Links: []Link{{SpanID: "y", Attrs: map[string]any{telemetry.LinkPurposeAttr: telemetryattrs.LinkPurposeWait}}}}); err == nil {
		t.Error("wait link without times: no error")
	}
}

func TestDecodeProducerComplete(t *testing.T) {
	for _, flag := range []any{true, "true"} {
		r, err := Decode(Span{SpanID: "c", Attrs: map[string]any{
			telemetryattrs.WcprofProducerCompleteAttr:  flag,
			telemetryattrs.WcprofProducerAttr:          "gobuild-1",
			telemetryattrs.WcprofProducerSpanCountAttr: "1415",
			telemetryattrs.WcprofProducerDroppedAttr:   "0",
		}})
		if err != nil || r.Complete == nil || *r.Complete != (ProducerComplete{Producer: "gobuild-1", SpanCount: 1415}) || r.Op != nil {
			t.Errorf("%v: %+v %+v %v", flag, r.Complete, r.Op, err)
		}
	}
}

func TestDecodeIgnoresEngineSpans(t *testing.T) {
	r, err := Decode(Span{SpanID: "e", Attrs: map[string]any{telemetryattrs.WcprofOpKindAttr: "call"}})
	if err != nil || r.Op != nil || r.Complete != nil || len(r.Waits) != 0 {
		t.Errorf("engine span decoded as reported: %+v %v", r, err)
	}
	if !IsReportedKind("task") || IsReportedKind("call") {
		t.Error("IsReportedKind")
	}
}

// A producer may report its work with an existing op kind.
func TestDecodeExistingKindFromProducer(t *testing.T) {
	r, err := Decode(Span{SpanID: "c", StartUnixNS: 1, EndUnixNS: 2, Attrs: map[string]any{
		telemetryattrs.WcprofOpKindAttr:   "exec",
		telemetryattrs.WcprofWorkTypeAttr: "user",
		telemetryattrs.WcprofProducerAttr: "p1",
	}})
	if err != nil || r.Op == nil || r.Op.Kind != "exec" || r.Op.WorkType != "user" || r.Op.Producer != "p1" {
		t.Errorf("producer's exec span: %+v %v", r.Op, err)
	}
}
