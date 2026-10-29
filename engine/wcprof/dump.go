package wcprof

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// DumpSchemaVersion identifies the dump wire format.
const DumpSchemaVersion = 1

// DumpHeader is the first line of a dump. The remaining lines are one
// DumpEvent JSON object per line.
type DumpHeader struct {
	SchemaVersion  int      `json:"schema_version"`
	EpochUnixNano  int64    `json:"epoch_unix_nano"`
	DumpedUnixNano int64    `json:"dumped_unix_nano"`
	DroppedEvents  uint64   `json:"dropped_events"`
	EventCount     int      `json:"event_count"`
	Strings        []string `json:"strings"`
	// OpenOps are ops begun but not ended at dump time (e.g. in-flight or
	// hung work).
	OpenOps []DumpOpenOp `json:"open_ops,omitempty"`
	// Producers are the clients that reported their own work: per producer,
	// the spans received and, once it declared them, the count it reported
	// and how many it failed to export. A producer whose Received is below
	// its Declared, or that never declared, makes the dump's reported work
	// incomplete.
	Producers []DumpProducer `json:"producers,omitempty"`
	// Machine describes the machine the engine ran on.
	Machine *DumpMachine `json:"machine,omitempty"`
}

// DumpProducer is one producer's completeness accounting.
type DumpProducer struct {
	Producer string `json:"producer"`
	Received int64  `json:"received"`
	Declared int64  `json:"declared,omitempty"`
	Dropped  int64  `json:"dropped,omitempty"`
	Complete bool   `json:"complete,omitempty"`
}

// DumpOpenOp describes an in-progress op at dump time.
type DumpOpenOp struct {
	OpID     uint64 `json:"op_id"`
	ParentID uint64 `json:"parent_id,omitempty"`
	Kind     string `json:"kind"`
	WorkType string `json:"work_type,omitempty"`
	ClassID  uint32 `json:"class_id,omitempty"`
	IdentID  uint32 `json:"ident_id,omitempty"`
	ClientID uint32 `json:"client_id,omitempty"`
	MetaID   uint32 `json:"meta_id,omitempty"`
	StartNS  int64  `json:"start_ns"`
}

// DumpEvent is the JSON form of an Event. Short keys keep dumps compact.
type DumpEvent struct {
	Type     string `json:"e"`
	OpKind   string `json:"k,omitempty"`
	WorkType string `json:"w,omitempty"`
	Outcome  string `json:"o,omitempty"`
	Reason   string `json:"rs,omitempty"`
	LinkKind string `json:"lk,omitempty"`

	OpID     uint64 `json:"id,omitempty"`
	ParentID uint64 `json:"p,omitempty"`
	TargetID uint64 `json:"t,omitempty"`
	ResultID uint64 `json:"r,omitempty"`

	ClassID  uint32 `json:"c,omitempty"`
	IdentID  uint32 `json:"i,omitempty"`
	ClientID uint32 `json:"cl,omitempty"`
	MetaID   uint32 `json:"m,omitempty"`

	// TraceID and SpanID are a span event's OTel span, in lower-case hex.
	TraceID string `json:"tr,omitempty"`
	SpanID  string `json:"sp,omitempty"`

	StartNS int64 `json:"s"`
	EndNS   int64 `json:"d"`

	// Reported ops (see reported.go) also carry their label and producer.
	LabelID    uint32 `json:"lb,omitempty"`
	ProducerID uint32 `json:"pr,omitempty"`
	// Resource events ("e":"resource") describe the resource op OpID; IdentID
	// is its display name.
	Capacity int64  `json:"cap,omitempty"`
	PolicyID uint32 `json:"pol,omitempty"`
	// Acquisition events ("e":"acquisition") describe the acquire op OpID:
	// TargetID is its resource op, Priority its priority, MetaID its
	// priority inputs (a JSON object), GrantedNS when it was granted (0 if
	// never), Outcome released, cancelled or held.
	Priority  int64 `json:"pri,omitempty"`
	GrantedNS int64 `json:"gr,omitempty"`

	// Usage events ("e":"usage") carry a resource reading for the op OpID:
	// ClassID names the scope (see the UsageScope constants), StartNS and
	// EndNS are when it was read.
	CPUUserUS    int64 `json:"cu,omitempty"`
	CPUSystemUS  int64 `json:"cs,omitempty"`
	MemPeakBytes int64 `json:"mp,omitempty"`
	CPUIowaitUS  int64 `json:"ciw,omitempty"`
	CPUStealUS   int64 `json:"cst,omitempty"`
	CPUIdleUS    int64 `json:"cid,omitempty"`
	MinorFaults  int64 `json:"fmin,omitempty"`
	MajorFaults  int64 `json:"fmaj,omitempty"`
	HugeFaults   int64 `json:"fhuge,omitempty"`
	CPUStallUS   int64 `json:"stcpu,omitempty"`
	MemStallUS   int64 `json:"stmem,omitempty"`
	GoAllocBytes int64 `json:"ga,omitempty"`
	GoGCCycles   int64 `json:"gcn,omitempty"`
	GoGCCPUUS    int64 `json:"gcc,omitempty"`
	IOReadBytes  int64 `json:"iorb,omitempty"`
	IOWriteBytes int64 `json:"iowb,omitempty"`
	MemAnonBytes int64 `json:"manon,omitempty"`
	MemFileBytes int64 `json:"mfile,omitempty"`
	MemHugeBytes int64 `json:"mhuge,omitempty"`

	GCAssistUS       int64 `json:"gca,omitempty"`
	GCDedicatedUS    int64 `json:"gcd,omitempty"`
	GCIdleUS         int64 `json:"gci,omitempty"`
	GCPauseUS        int64 `json:"gcp,omitempty"`
	HeapLiveBytes    int64 `json:"hlive,omitempty"`
	HeapGoalBytes    int64 `json:"hgoal,omitempty"`
	ScanHeapBytes    int64 `json:"sheap,omitempty"`
	ScanStackBytes   int64 `json:"sstack,omitempty"`
	ScanGlobalsBytes int64 `json:"sglob,omitempty"`
}

// DumpTypeUsage is the DumpEvent.Type of a resource reading (RecordUsage).
const DumpTypeUsage = "usage"

func usageDumpEvent(u usageRecord) DumpEvent {
	return DumpEvent{
		Type:         DumpTypeUsage,
		OpID:         u.opID,
		ClassID:      u.scopeID,
		StartNS:      u.atNS,
		EndNS:        u.atNS,
		CPUUserUS:    u.usage.CPUUserUS,
		CPUSystemUS:  u.usage.CPUSystemUS,
		MemPeakBytes: u.usage.MemPeakBytes,
		CPUIowaitUS:  u.usage.CPUIowaitUS,
		CPUStealUS:   u.usage.CPUStealUS,
		CPUIdleUS:    u.usage.CPUIdleUS,
		MinorFaults:  u.usage.MinorFaults,
		MajorFaults:  u.usage.MajorFaults,
		HugeFaults:   u.usage.HugeFaults,
		CPUStallUS:   u.usage.CPUStallUS,
		MemStallUS:   u.usage.MemStallUS,
		GoAllocBytes: u.usage.GoAllocBytes,
		GoGCCycles:   u.usage.GoGCCycles,
		GoGCCPUUS:    u.usage.GoGCCPUUS,
		IOReadBytes:  u.usage.IOReadBytes,
		IOWriteBytes: u.usage.IOWriteBytes,
		MemAnonBytes: u.usage.MemAnonBytes,
		MemFileBytes: u.usage.MemFileBytes,
		MemHugeBytes: u.usage.MemHugeBytes,

		GCAssistUS:       u.usage.GCAssistUS,
		GCDedicatedUS:    u.usage.GCDedicatedUS,
		GCIdleUS:         u.usage.GCIdleUS,
		GCPauseUS:        u.usage.GCPauseUS,
		HeapLiveBytes:    u.usage.HeapLiveBytes,
		HeapGoalBytes:    u.usage.HeapGoalBytes,
		ScanHeapBytes:    u.usage.ScanHeapBytes,
		ScanStackBytes:   u.usage.ScanStackBytes,
		ScanGlobalsBytes: u.usage.ScanGlobalsBytes,
	}
}

// Dump event types for reported resources and acquisitions.
const (
	DumpTypeResource    = "resource"
	DumpTypeAcquisition = "acquisition"
)

func eventTypeName(t EventType) string {
	switch t {
	case EventTypeOp:
		return "op"
	case EventTypeWait:
		return "wait"
	case EventTypeLink:
		return "link"
	case EventTypeSpan:
		return "span"
	default:
		return "invalid"
	}
}

func toDumpEvent(ev Event) DumpEvent {
	out := DumpEvent{
		Type:     eventTypeName(ev.Type),
		OpID:     ev.OpID,
		ParentID: ev.ParentID,
		TargetID: ev.TargetID,
		ResultID: ev.ResultID,
		ClassID:  ev.ClassID,
		IdentID:  ev.IdentID,
		ClientID: ev.ClientID,
		MetaID:   ev.MetaID,
		StartNS:  ev.StartNS,
		EndNS:    ev.EndNS,
	}
	switch ev.Type {
	case EventTypeOp:
		out.OpKind = ev.OpKind.String()
		out.WorkType = ev.WorkType.String()
		out.Outcome = ev.Outcome.String()
	case EventTypeWait:
		out.Reason = ev.Reason.String()
	case EventTypeLink:
		out.LinkKind = ev.LinkKind.String()
	case EventTypeSpan:
		var trace [16]byte
		var span [8]byte
		binary.BigEndian.PutUint64(trace[:8], ev.TargetID)
		binary.BigEndian.PutUint64(trace[8:], ev.ResultID)
		binary.BigEndian.PutUint64(span[:], ev.ParentID)
		out.TraceID, out.SpanID = hex.EncodeToString(trace[:]), hex.EncodeToString(span[:])
		out.ParentID, out.TargetID, out.ResultID = 0, 0, 0
	}
	return out
}

// WriteDump streams the recorder state to w: one DumpHeader JSON line, then
// one DumpEvent JSON line per event. When flush is true, recorded events are
// removed from the buffer (the string table, open ops, and op ID counter are
// retained so later dumps remain consistent).
func (r *Recorder) WriteDump(w io.Writer, flush bool) error {
	if r == nil {
		return fmt.Errorf("wcprof: recorder not enabled")
	}

	// Snapshot (and optionally flush) each shard.
	var (
		batches [numShards][]Event
		usages  [numShards][]usageRecord
		open    []DumpOpenOp
		total   int
	)
	for i := range r.shards {
		sh := &r.shards[i]
		sh.mu.Lock()
		batches[i] = sh.events
		usages[i] = sh.usage
		if flush {
			sh.events = nil
			sh.usage = nil
		} else {
			batches[i] = append([]Event(nil), sh.events...)
			usages[i] = append([]usageRecord(nil), sh.usage...)
		}
		for id, oo := range sh.openOps {
			open = append(open, DumpOpenOp{
				OpID:     id,
				ParentID: oo.parentID,
				Kind:     oo.op.String(),
				WorkType: oo.work.String(),
				ClassID:  oo.classID,
				IdentID:  oo.identID,
				ClientID: oo.clientID,
				MetaID:   oo.metaID,
				StartNS:  oo.startNS,
			})
		}
		sh.mu.Unlock()
		total += len(batches[i]) + len(usages[i])
	}
	if flush {
		r.total.Add(int64(-total))
	}

	reportedEvents, producers := r.dumpReported(flush)
	total += len(reportedEvents)

	header := DumpHeader{
		SchemaVersion:  DumpSchemaVersion,
		EpochUnixNano:  r.wallEpoch,
		DumpedUnixNano: time.Now().UnixNano(),
		DroppedEvents:  r.dropped.Load(),
		EventCount:     total,
		Strings:        r.strings.snapshot(),
		OpenOps:        open,
		Producers:      producers,
		Machine:        readMachine(),
	}

	bw := bufio.NewWriterSize(w, 1<<20)
	enc := json.NewEncoder(bw)
	if err := enc.Encode(header); err != nil {
		return fmt.Errorf("wcprof: encode dump header: %w", err)
	}
	for _, batch := range batches {
		for _, ev := range batch {
			if err := enc.Encode(toDumpEvent(ev)); err != nil {
				return fmt.Errorf("wcprof: encode dump event: %w", err)
			}
		}
	}
	for _, batch := range usages {
		for _, u := range batch {
			if err := enc.Encode(usageDumpEvent(u)); err != nil {
				return fmt.Errorf("wcprof: encode dump usage: %w", err)
			}
		}
	}
	for _, ev := range reportedEvents {
		if err := enc.Encode(ev); err != nil {
			return fmt.Errorf("wcprof: encode reported event: %w", err)
		}
	}
	return bw.Flush()
}

// ReadDump parses a dump produced by WriteDump.
func ReadDump(rd io.Reader) (*DumpHeader, []DumpEvent, error) {
	dec := json.NewDecoder(rd)
	var header DumpHeader
	if err := dec.Decode(&header); err != nil {
		return nil, nil, fmt.Errorf("wcprof: decode dump header: %w", err)
	}
	if header.SchemaVersion != DumpSchemaVersion {
		return nil, nil, fmt.Errorf("wcprof: unsupported dump schema version %d (want %d)", header.SchemaVersion, DumpSchemaVersion)
	}
	events := make([]DumpEvent, 0, header.EventCount)
	for {
		var ev DumpEvent
		if err := dec.Decode(&ev); err != nil {
			if err == io.EOF {
				break
			}
			return nil, nil, fmt.Errorf("wcprof: decode dump event: %w", err)
		}
		events = append(events, ev)
	}
	return &header, events, nil
}
