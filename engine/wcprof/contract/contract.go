// Package contract decodes the wcprof reporting contract: how a client or a
// program inside an exec reports its own work (tasks and task groups), what
// each task waited for (dependency waits), its limited resources and the
// acquisitions of them, and its completeness, as OTel spans and links with
// the wcprof.* attributes of engine/telemetryattrs.
//
// It is the one decoder of that contract: the recorder uses it for spans it
// records natively, and the offline analyzers for traces they load, so they
// can't read the contract differently. It works on Span, a source-neutral
// view of a span, so any span representation can be converted to it.
package contract

import (
	"encoding/json"
	"fmt"
	"strconv"

	telemetry "github.com/dagger/otel-go"

	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/dagger/dagger/engine/wcprof"
)

// Span is what the contract reads from a span. IDs are lower-hex; times are
// Unix nanoseconds. Attribute values are as the source decoded them: strings,
// or bools and numbers for sources that keep them (decimal-string values are
// accepted as numbers either way).
type Span struct {
	TraceID      string
	SpanID       string
	ParentSpanID string
	StartUnixNS  int64
	EndUnixNS    int64
	Attrs        map[string]any
	Links        []Link
}

// Link is a span link: its target and attributes.
type Link struct {
	TraceID string
	SpanID  string
	Attrs   map[string]any
}

// Reported is what one span reports under the contract. At most one of Op's
// kinds applies; Resource and Acquisition are set for those kinds.
type Reported struct {
	// Op is the reported op; nil for a span that isn't one (an engine span,
	// or a producer's completeness carrier).
	Op *Op
	// Waits are the span's wait links.
	Waits []Wait
	// Resource is set when Op.Kind is "resource".
	Resource *Resource
	// Acquisition is set when Op.Kind is "acquire".
	Acquisition *Acquisition
	// Complete is set on a producer's completeness carrier.
	Complete *ProducerComplete
}

// Op is a reported op.
type Op struct {
	Kind     string
	WorkType string
	Label    string
	Producer string
	// CausalParent is the span ID of the op's cause: wcprof.parent when set,
	// else the span's parent.
	CausalParent string
	StartUnixNS  int64
	EndUnixNS    int64
}

// Wait is a wait link: the waiter (the span carrying it) was blocked on the
// target span, or on the named resource Ident, from StartUnixNS to EndUnixNS.
type Wait struct {
	TargetTraceID string
	TargetSpanID  string
	Reason        string
	Ident         string
	StartUnixNS   int64
	EndUnixNS     int64
}

// Resource is a limited resource instance; its ID is its span's span ID.
type Resource struct {
	Name     string
	Capacity int64
	Policy   string
}

// Resource admission policies.
const (
	// PolicyPriorityFIFO admits the highest priority first, the earliest
	// request first among equals.
	PolicyPriorityFIFO = "priority_fifo"
	// PolicyFIFO admits the earliest request first.
	PolicyFIFO = "fifo"
)

// Acquisition outcomes.
const (
	OutcomeReleased  = "released"
	OutcomeCancelled = "cancelled"
	OutcomeHeld      = "held"
)

// Acquisition is one unit of a resource requested at the span's start and
// released (or given up, or still held) at its end.
type Acquisition struct {
	ResourceID     string
	Priority       int64
	PriorityInputs map[string]string
	// GrantedUnixNS is when the unit was granted; 0 when it never was.
	GrantedUnixNS int64
	Outcome       string
}

// ProducerComplete is a producer's declaration of how many spans it reported.
type ProducerComplete struct {
	Producer  string
	SpanCount int64
	Dropped   int64
}

// reportedKinds are the op kinds only reported spans carry.
var reportedKinds = map[string]bool{
	wcprof.OpKindTask.String():      true,
	wcprof.OpKindTaskGroup.String(): true,
	wcprof.OpKindResource.String():  true,
	wcprof.OpKindAcquire.String():   true,
}

// IsReportedKind reports whether kind is one of the contract's own op kinds.
func IsReportedKind(kind string) bool { return reportedKinds[kind] }

// Decode reads what s reports under the contract. A span that reports
// nothing (an engine span, an unrelated span) decodes to the zero Reported.
// Malformed contract values are errors, never guesses.
func Decode(s Span) (Reported, error) {
	var r Reported
	if b, _ := s.Attrs[telemetryattrs.WcprofProducerCompleteAttr].(bool); b || s.Attrs[telemetryattrs.WcprofProducerCompleteAttr] == "true" {
		count, err := decimal(s.Attrs, telemetryattrs.WcprofProducerSpanCountAttr, true)
		if err != nil {
			return r, err
		}
		dropped, err := decimal(s.Attrs, telemetryattrs.WcprofProducerDroppedAttr, false)
		if err != nil {
			return r, err
		}
		r.Complete = &ProducerComplete{Producer: str(s.Attrs, telemetryattrs.WcprofProducerAttr), SpanCount: count, Dropped: dropped}
		return r, nil
	}
	kind := str(s.Attrs, telemetryattrs.WcprofOpKindAttr)
	if !reportedKinds[kind] {
		return r, nil
	}
	parent := str(s.Attrs, telemetryattrs.WcprofParentAttr)
	if parent == "" {
		parent = s.ParentSpanID
	}
	r.Op = &Op{
		Kind:         kind,
		WorkType:     str(s.Attrs, telemetryattrs.WcprofWorkTypeAttr),
		Label:        str(s.Attrs, telemetryattrs.WcprofLabelAttr),
		Producer:     str(s.Attrs, telemetryattrs.WcprofProducerAttr),
		CausalParent: parent,
		StartUnixNS:  s.StartUnixNS,
		EndUnixNS:    s.EndUnixNS,
	}
	if r.Op.WorkType == "" {
		r.Op.WorkType = wcprof.WorkTypeEngine.String()
	}
	for _, l := range s.Links {
		if str(l.Attrs, telemetry.LinkPurposeAttr) != telemetryattrs.LinkPurposeWait {
			continue
		}
		start, err := decimal(l.Attrs, telemetryattrs.WcprofWaitStartUnixNanoAttr, true)
		if err != nil {
			return r, err
		}
		end, err := decimal(l.Attrs, telemetryattrs.WcprofWaitEndUnixNanoAttr, true)
		if err != nil {
			return r, err
		}
		r.Waits = append(r.Waits, Wait{
			TargetTraceID: l.TraceID,
			TargetSpanID:  l.SpanID,
			Reason:        str(l.Attrs, telemetryattrs.WcprofWaitReasonAttr),
			Ident:         str(l.Attrs, telemetryattrs.WcprofWaitIdentAttr),
			StartUnixNS:   start,
			EndUnixNS:     max(end, start),
		})
	}
	switch kind {
	case wcprof.OpKindResource.String():
		capacity, err := decimal(s.Attrs, telemetryattrs.WcprofResourceCapacityAttr, true)
		if err != nil {
			return r, err
		}
		if capacity < 1 {
			return r, fmt.Errorf("resource span %s: capacity %d", s.SpanID, capacity)
		}
		policy := str(s.Attrs, telemetryattrs.WcprofResourcePolicyAttr)
		if policy != PolicyPriorityFIFO && policy != PolicyFIFO {
			return r, fmt.Errorf("resource span %s: unknown policy %q", s.SpanID, policy)
		}
		r.Resource = &Resource{Name: str(s.Attrs, telemetryattrs.WcprofResourceNameAttr), Capacity: capacity, Policy: policy}
	case wcprof.OpKindAcquire.String():
		a := &Acquisition{ResourceID: str(s.Attrs, telemetryattrs.WcprofResourceIDAttr)}
		if a.ResourceID == "" {
			return r, fmt.Errorf("acquire span %s: no %s", s.SpanID, telemetryattrs.WcprofResourceIDAttr)
		}
		var err error
		if a.Priority, err = decimal(s.Attrs, telemetryattrs.WcprofResourcePriorityAttr, false); err != nil {
			return r, err
		}
		if a.GrantedUnixNS, err = decimal(s.Attrs, telemetryattrs.WcprofResourceGrantedUnixNanoAttr, false); err != nil {
			return r, err
		}
		if in := str(s.Attrs, telemetryattrs.WcprofResourcePriorityInputsAttr); in != "" {
			if err := json.Unmarshal([]byte(in), &a.PriorityInputs); err != nil {
				return r, fmt.Errorf("acquire span %s: %s: %w", s.SpanID, telemetryattrs.WcprofResourcePriorityInputsAttr, err)
			}
		}
		switch a.Outcome = str(s.Attrs, telemetryattrs.WcprofResourceOutcomeAttr); a.Outcome {
		case OutcomeReleased, OutcomeHeld:
			if a.GrantedUnixNS == 0 {
				return r, fmt.Errorf("acquire span %s: %s without a grant", s.SpanID, a.Outcome)
			}
		case OutcomeCancelled:
		default:
			return r, fmt.Errorf("acquire span %s: unknown outcome %q", s.SpanID, a.Outcome)
		}
		r.Acquisition = a
	}
	return r, nil
}

func str(attrs map[string]any, key string) string {
	switch v := attrs[key].(type) {
	case string:
		return v
	case nil:
		return ""
	default:
		return fmt.Sprint(v)
	}
}

// decimal reads a decimal-string (or numeric) attribute; absent is 0 unless
// required.
func decimal(attrs map[string]any, key string, required bool) (int64, error) {
	switch v := attrs[key].(type) {
	case nil:
		if required {
			return 0, fmt.Errorf("missing %s", key)
		}
		return 0, nil
	case string:
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", key, err)
		}
		return n, nil
	case int64:
		return v, nil
	case int:
		return int64(v), nil
	case float64:
		return int64(v), nil
	default:
		return 0, fmt.Errorf("%s: unexpected %T", key, v)
	}
}

// Record converts what s reports into the recorder's form (see
// wcprof.RecordReported); ok is false when s reports no op. name is the
// span's name, the op's class.
func Record(s Span, name string, r Reported) (op wcprof.ReportedOp, ok bool) {
	if r.Op == nil {
		return op, false
	}
	op = wcprof.ReportedOp{
		TraceID:      s.TraceID,
		SpanID:       s.SpanID,
		ParentSpanID: r.Op.CausalParent,
		Kind:         wcprof.ParseOpKind(r.Op.Kind),
		Work:         wcprof.ParseWorkType(r.Op.WorkType),
		Name:         name,
		Label:        r.Op.Label,
		Producer:     r.Op.Producer,
		StartUnixNS:  r.Op.StartUnixNS,
		EndUnixNS:    r.Op.EndUnixNS,
	}
	for _, w := range r.Waits {
		op.Waits = append(op.Waits, wcprof.ReportedWait{
			TargetTraceID: w.TargetTraceID,
			TargetSpanID:  w.TargetSpanID,
			Reason:        wcprof.ParseWaitReason(w.Reason),
			Ident:         w.Ident,
			StartUnixNS:   w.StartUnixNS,
			EndUnixNS:     w.EndUnixNS,
		})
	}
	if r.Resource != nil {
		op.Resource = &wcprof.ReportedResource{Capacity: r.Resource.Capacity, Policy: r.Resource.Policy}
	}
	if a := r.Acquisition; a != nil {
		op.Acquisition = &wcprof.ReportedAcquisition{
			ResourceSpanID: a.ResourceID,
			Priority:       a.Priority,
			PriorityInputs: a.PriorityInputs,
			GrantedUnixNS:  a.GrantedUnixNS,
			Outcome:        a.Outcome,
		}
	}
	return op, true
}
