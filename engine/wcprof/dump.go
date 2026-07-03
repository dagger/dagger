package wcprof

import (
	"bufio"
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

	StartNS int64 `json:"s"`
	EndNS   int64 `json:"d"`
}

func eventTypeName(t EventType) string {
	switch t {
	case EventTypeOp:
		return "op"
	case EventTypeWait:
		return "wait"
	case EventTypeLink:
		return "link"
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
		open    []DumpOpenOp
		total   int
	)
	for i := range r.shards {
		sh := &r.shards[i]
		sh.mu.Lock()
		batches[i] = sh.events
		if flush {
			sh.events = nil
		} else {
			batches[i] = append([]Event(nil), sh.events...)
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
				StartNS:  oo.startNS,
			})
		}
		sh.mu.Unlock()
		total += len(batches[i])
	}
	if flush {
		r.total.Add(int64(-total))
	}

	header := DumpHeader{
		SchemaVersion:  DumpSchemaVersion,
		EpochUnixNano:  r.wallEpoch,
		DumpedUnixNano: time.Now().UnixNano(),
		DroppedEvents:  r.dropped.Load(),
		EventCount:     total,
		Strings:        r.strings.snapshot(),
		OpenOps:        open,
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
