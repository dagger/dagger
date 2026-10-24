package wcprof

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
)

// Client-reported work (see engine/wcprof/contract): ops, waits, resources
// and acquisitions that clients and programs inside execs report as OTel
// spans, recorded here so a native dump holds them next to the engine's own
// ops. A reported op gets a recorder op ID the first time its span is seen;
// parents and wait targets are kept as span references and resolved when the
// dump is written, since a span can arrive before the span it refers to.

// ReportedOp is one reported span to record, already decoded.
type ReportedOp struct {
	TraceID string
	SpanID  string
	// ParentSpanID is the op's causal parent span (same trace), "" for none.
	ParentSpanID string
	Kind         OpKind
	Work         WorkType
	// Name is the span's name, the op's class.
	Name        string
	Label       string
	Producer    string
	StartUnixNS int64
	EndUnixNS   int64
	Waits       []ReportedWait
	Resource    *ReportedResource
	Acquisition *ReportedAcquisition
}

// ReportedWait is a wait link of a reported op.
type ReportedWait struct {
	TargetTraceID string
	TargetSpanID  string
	Reason        WaitReason
	Ident         string
	StartUnixNS   int64
	EndUnixNS     int64
}

// ReportedResource describes a "resource" op.
type ReportedResource struct {
	Capacity int64
	Policy   string
}

// ReportedAcquisition describes an "acquire" op.
type ReportedAcquisition struct {
	ResourceSpanID string
	Priority       int64
	PriorityInputs map[string]string
	// GrantedUnixNS is when the unit was granted, 0 if never.
	GrantedUnixNS int64
	Outcome       string
}

type spanKey struct{ trace, span string }

type reportedOp struct {
	id          uint64
	parent      spanKey
	waits       []ReportedWait
	resource    *ReportedResource
	acquisition *ReportedAcquisition
	traceID     string
	ev          Event
	labelID     uint32
	producerID  uint32
}

type otelParentLink struct {
	opID   uint64
	parent spanKey
	atNS   int64
}

type producerCount struct {
	received          int64
	declared, dropped int64
	complete          bool
}

// reported holds client-reported work; it has its own lock, apart from the
// event shards, since reported spans arrive in batches from the telemetry
// path, not from the engine's hot paths.
type reported struct {
	mu        sync.Mutex
	opBySpan  map[spanKey]uint64
	ops       []*reportedOp
	links     []otelParentLink
	producers map[string]*producerCount
}

func (r *Recorder) rel(unixNS int64) int64 {
	if unixNS == 0 {
		return 0
	}
	return unixNS - r.wallEpoch
}

// RecordReported records one reported op. A span already recorded is not
// recorded again (live and final exports of one span), and only ended spans
// are recorded. A no-op when profiling is disabled for ctx.
func RecordReported(ctx context.Context, op ReportedOp) {
	r := recorderFor(ctx)
	if r == nil || op.SpanID == "" || op.EndUnixNS == 0 {
		return
	}
	key := spanKey{op.TraceID, op.SpanID}
	rp := &r.reported
	rp.mu.Lock()
	defer rp.mu.Unlock()
	if rp.opBySpan == nil {
		rp.opBySpan = map[spanKey]uint64{}
		rp.producers = map[string]*producerCount{}
	}
	if _, seen := rp.opBySpan[key]; seen {
		return
	}
	if r.total.Add(int64(1+len(op.Waits))) > r.maxTotal {
		r.total.Add(-int64(1 + len(op.Waits)))
		r.dropped.Add(uint64(1 + len(op.Waits)))
		return
	}
	id := r.newOpID()
	rp.opBySpan[key] = id
	if op.Producer != "" {
		pc := rp.producers[op.Producer]
		if pc == nil {
			pc = &producerCount{}
			rp.producers[op.Producer] = pc
		}
		pc.received++
	}
	ro := &reportedOp{
		id:          id,
		waits:       op.Waits,
		resource:    op.Resource,
		acquisition: op.Acquisition,
		traceID:     op.TraceID,
		labelID:     r.Intern(op.Label),
		producerID:  r.Intern(op.Producer),
		ev: Event{
			Type:     EventTypeOp,
			OpKind:   op.Kind,
			WorkType: op.Work,
			Outcome:  OutcomeOK,
			OpID:     id,
			ClassID:  r.Intern(op.Name),
			IdentID:  r.Intern(op.Label),
			StartNS:  r.rel(op.StartUnixNS),
			EndNS:    r.rel(op.EndUnixNS),
		},
	}
	if op.ParentSpanID != "" {
		ro.parent = spanKey{op.TraceID, op.ParentSpanID}
	}
	rp.ops = append(rp.ops, ro)
}

// RecordProducerComplete records a producer's declaration of how many spans
// it reported and how many it failed to export.
func RecordProducerComplete(ctx context.Context, producer string, spanCount, dropped int64) {
	r := recorderFor(ctx)
	if r == nil || producer == "" {
		return
	}
	rp := &r.reported
	rp.mu.Lock()
	defer rp.mu.Unlock()
	if rp.producers == nil {
		rp.opBySpan = map[spanKey]uint64{}
		rp.producers = map[string]*producerCount{}
	}
	pc := rp.producers[producer]
	if pc == nil {
		pc = &producerCount{}
		rp.producers[producer] = pc
	}
	pc.declared, pc.dropped, pc.complete = spanCount, dropped, true
}

// LinkOTelParent records that the engine op opID was caused by the span
// (traceID, spanID), e.g. a request a reported task made: in the dump the
// link resolves to that span's reported op, if it was recorded.
func LinkOTelParent(ctx context.Context, opID uint64, traceID, spanID string) {
	r := recorderFor(ctx)
	if r == nil || opID == 0 || spanID == "" {
		return
	}
	rp := &r.reported
	rp.mu.Lock()
	defer rp.mu.Unlock()
	rp.links = append(rp.links, otelParentLink{opID: opID, parent: spanKey{traceID, spanID}, atNS: r.Now()})
}

// dumpReported returns the reported work as dump events, resolving span
// references, and the producers' counts. With flush, the recorded ops and
// links are removed (the span-to-op map is kept, so later dumps still resolve
// references to them).
func (r *Recorder) dumpReported(flush bool) ([]DumpEvent, []DumpProducer) {
	rp := &r.reported
	rp.mu.Lock()
	defer rp.mu.Unlock()
	var out []DumpEvent
	resolve := func(k spanKey) (uint64, bool) {
		id, ok := rp.opBySpan[k]
		return id, ok
	}
	unresolved := func(from uint64, k spanKey, at int64) DumpEvent {
		return DumpEvent{Type: "link", LinkKind: LinkKindUnresolvedSpan.String(), ParentID: from, IdentID: r.Intern(k.trace + "/" + k.span), StartNS: at, EndNS: at}
	}
	for _, ro := range rp.ops {
		ev := ro.ev
		if ro.parent.span != "" {
			if id, ok := resolve(ro.parent); ok {
				ev.ParentID = id
			} else {
				out = append(out, unresolved(ro.id, ro.parent, ev.StartNS))
			}
		}
		de := toDumpEvent(ev)
		de.LabelID, de.ProducerID = ro.labelID, ro.producerID
		out = append(out, de)
		for _, w := range ro.waits {
			k := spanKey{w.TargetTraceID, w.TargetSpanID}
			if k.trace == "" {
				k.trace = ro.traceID
			}
			wd := DumpEvent{Type: "wait", Reason: w.Reason.String(), ParentID: ro.id, IdentID: r.Intern(w.Ident), StartNS: r.rel(w.StartUnixNS), EndNS: r.rel(w.EndUnixNS)}
			if id, ok := resolve(k); ok {
				wd.TargetID = id
			} else if k.span != "" {
				out = append(out, unresolved(ro.id, k, wd.StartNS))
			}
			out = append(out, wd)
		}
		if res := ro.resource; res != nil {
			out = append(out, DumpEvent{Type: DumpTypeResource, OpID: ro.id, Capacity: res.Capacity, PolicyID: r.Intern(res.Policy), StartNS: ev.StartNS, EndNS: ev.EndNS})
		}
		if a := ro.acquisition; a != nil {
			ad := DumpEvent{Type: DumpTypeAcquisition, OpID: ro.id, Outcome: a.Outcome, Priority: a.Priority, StartNS: ev.StartNS, EndNS: ev.EndNS}
			if a.GrantedUnixNS != 0 {
				ad.GrantedNS = r.rel(a.GrantedUnixNS)
			}
			if len(a.PriorityInputs) > 0 {
				if b, err := json.Marshal(a.PriorityInputs); err == nil {
					ad.MetaID = r.Intern(string(b))
				}
			}
			k := spanKey{ro.traceID, a.ResourceSpanID}
			if id, ok := resolve(k); ok {
				ad.TargetID = id
			} else {
				out = append(out, unresolved(ro.id, k, ev.StartNS))
			}
			out = append(out, ad)
		}
	}
	for _, l := range rp.links {
		if id, ok := resolve(l.parent); ok {
			out = append(out, DumpEvent{Type: "link", LinkKind: LinkKindReportedCause.String(), ParentID: l.opID, TargetID: id, StartNS: l.atNS, EndNS: l.atNS})
		}
		// An engine op whose cause is not a reported span (the usual case)
		// needs no link.
	}
	names := make([]string, 0, len(rp.producers))
	for name := range rp.producers {
		names = append(names, name)
	}
	sort.Strings(names)
	producers := make([]DumpProducer, 0, len(names))
	for _, name := range names {
		pc := rp.producers[name]
		producers = append(producers, DumpProducer{Producer: name, Received: pc.received, Declared: pc.declared, Dropped: pc.dropped, Complete: pc.complete})
	}
	if flush {
		var n int64
		for _, ro := range rp.ops {
			n += int64(1 + len(ro.waits))
		}
		r.total.Add(-n)
		rp.ops, rp.links = nil, nil
	}
	return out, producers
}

// ParseOpKind is the OpKind named s (as OpKind.String writes it), or
// OpKindInvalid.
func ParseOpKind(s string) OpKind {
	for k, name := range opKindNames {
		if name == s {
			return k
		}
	}
	return OpKindInvalid
}

// ParseWorkType is the WorkType named s, or WorkTypeEngine.
func ParseWorkType(s string) WorkType {
	for w, name := range workTypeNames {
		if name == s {
			return w
		}
	}
	return WorkTypeEngine
}

// ParseWaitReason is the WaitReason named s, or WaitReasonInvalid.
func ParseWaitReason(s string) WaitReason {
	for r, name := range waitReasonNames {
		if name == s {
			return r
		}
	}
	return WaitReasonInvalid
}

// SetActiveForTest installs r as the recorder, recording globally when on,
// and returns a function restoring the previous state. For tests of code that
// records through the package-level API.
func SetActiveForTest(r *Recorder, on bool) func() {
	prev, prevOn := global.Load(), globalOn.Load()
	global.Store(r)
	globalOn.Store(on)
	return func() {
		global.Store(prev)
		globalOn.Store(prevOn)
	}
}
