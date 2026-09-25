package clientdb

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/dagger/dagger/engine/agentcontrol"
	"github.com/dagger/dagger/engine/archive"
	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/codes"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/encoding/protowire"
)

// archiveLogMeta never retains a body, resource, or attribute blob. The index
// lets lazy reads reject unrelated rows before reading their (possibly huge)
// payloads from disk. It is rebuilt during ordinary spill recovery.
type archiveLogMeta struct {
	trace, span                             string
	timestamp                               int64
	size                                    int64
	text, nonempty, call, metadata, control bool
	err                                     error
}

func archiveLogMetadata(row Log) archiveLogMeta {
	m := archiveLogMeta{trace: row.TraceID.String, span: row.SpanID.String, timestamp: row.Timestamp, size: logCodec.size(row)}
	var attrs []*commonpb.KeyValue
	if err := UnmarshalProtoJSONs(row.Attributes, &commonpb.KeyValue{}, &attrs); err != nil {
		m.err = err
		return m
	}
	var hidden bool
	for _, a := range attrs {
		switch a.GetKey() {
		case telemetry.LogsGlobalAttr, telemetry.LogsVerboseAttr:
			hidden = hidden || a.GetValue().GetBoolValue()
		case agentcontrol.VersionAttr:
			m.control = true
		case telemetryattrs.LogRoleAttr, telemetryattrs.ProgressItemAttr, telemetryattrs.AgentStateAttr, telemetryattrs.AgentSnapshotDigestAttr:
			m.metadata = true
		case telemetry.ContentTypeAttr:
			m.call = a.GetValue().GetStringValue() == telemetryattrs.CallPayloadContentType
			// Typed binary semantic records include agent state and snapshots.
			if m.call {
				m.metadata = true
			}
		}
	}
	// Inspect the oneof wire tag without allocating a copy of a large body.
	var text, nonempty bool
	for body := row.Body; len(body) > 0; {
		num, typ, n := protowire.ConsumeTag(body)
		if n < 0 {
			m.err = protowire.ParseError(n)
			return m
		}
		body = body[n:]
		n = protowire.ConsumeFieldValue(num, typ, body)
		if n < 0 {
			m.err = protowire.ParseError(n)
			return m
		}
		if num >= 1 && num <= 7 {
			text = num == 1 && typ == protowire.BytesType
			if text {
				value, _ := protowire.ConsumeBytes(body)
				nonempty = len(value) > 0
			}
		}
		body = body[n:]
	}
	m.text = text && !m.metadata && !m.control && !hidden
	m.nonempty = nonempty
	if !text {
		m.metadata = true
	}
	return m
}

// ArchiveSpans is compact topology at precisely one span/log cut. It stores
// only the last row ID and view metadata, never full spans or log bodies.
type ArchiveSpans struct {
	nodes    map[string]archiveSpanNode
	children map[string][]string
	selected map[string]bool
	partial  bool
}
type archiveSpanNode struct {
	row                   int64
	parent                string
	priority, passthrough bool
	causes                []string
	hasLogs               bool
}

func (s *DB) ArchiveSpanView(ctx context.Context, traceID string, cut HighWater, sel *archive.SpanSelection) (*ArchiveSpans, error) {
	if cut.Spans < 0 || cut.Logs < 0 {
		return nil, errors.New("invalid archive cut")
	}
	v := &ArchiveSpans{nodes: map[string]archiveSpanNode{}, children: map[string][]string{}}
	for cursor := int64(0); cursor < cut.Spans; {
		rows, err := s.SelectSpansRange(ctx, SelectSpansRangeParams{AfterID: cursor, ThroughID: cut.Spans, Limit: 128})
		if err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			return nil, errors.New("archive span stream truncated before cut")
		}
		for _, row := range rows {
			cursor = row.ID
			if row.TraceID != traceID {
				continue
			}
			node := archiveSpanNode{row: row.ID, parent: row.ParentSpanID.String, priority: row.StatusCode == int64(codes.Error), causes: causalLinkTargets(row)}
			var attrs []*commonpb.KeyValue
			if err := UnmarshalProtoJSONs(row.Attributes, &commonpb.KeyValue{}, &attrs); err != nil {
				return nil, err
			}
			for _, a := range attrs {
				switch a.GetKey() {
				case telemetry.CheckNameAttr, "test.case.name", "test.suite.name":
					node.priority = true
				case telemetry.UIPassthroughAttr, telemetry.UIInternalAttr:
					node.passthrough = node.passthrough || a.GetValue().GetBoolValue()
				case telemetry.UIRevealAttr:
					node.priority = node.priority || a.GetValue().GetBoolValue()
				}
			}
			v.nodes[row.SpanID] = node
		}
	}
	s.logIdx.mu.RLock()
	if cut.Logs > int64(len(s.logIdx.archive)) {
		s.logIdx.mu.RUnlock()
		return nil, errors.New("archive log stream truncated before cut")
	}
	for i, m := range s.logIdx.archive[:cut.Logs] {
		if i%128 == 0 {
			if err := ctx.Err(); err != nil {
				s.logIdx.mu.RUnlock()
				return nil, err
			}
		}
		if m.trace != traceID || !m.text || !m.nonempty {
			continue
		}
		if node, ok := v.nodes[m.span]; ok {
			node.hasLogs = true
			v.nodes[m.span] = node
		}
	}
	s.logIdx.mu.RUnlock()
	for id, node := range v.nodes {
		if _, ok := v.nodes[node.parent]; ok {
			v.children[node.parent] = append(v.children[node.parent], id)
		}
		for _, parent := range node.causes {
			if _, ok := v.nodes[parent]; ok && parent != node.parent {
				v.children[parent] = append(v.children[parent], id)
			}
		}
	}
	v.selectSpans(sel)
	return v, nil
}

// selectSpans applies presentation selection only after the fixed-cut topology
// and log-presence metadata have been collected.
func (v *ArchiveSpans) selectSpans(sel *archive.SpanSelection) {
	if sel == nil || sel.Full {
		return
	}
	v.selected = map[string]bool{}
	// Include ancestors so priority failures/checks have no dangling parents.
	var ancestors func(string)
	ancestors = func(id string) {
		if v.selected[id] {
			return
		}
		node, ok := v.nodes[id]
		if !ok {
			return
		}
		v.selected[id] = true
		ancestors(node.parent)
		for _, parent := range node.causes {
			ancestors(parent)
		}
	}
	var visibleChildren func(string, map[string]bool)
	visibleChildren = func(id string, seen map[string]bool) {
		if seen[id] {
			return
		}
		seen[id] = true
		for _, kid := range v.children[id] {
			ancestors(kid)
			if v.nodes[kid].passthrough {
				visibleChildren(kid, seen)
			}
		}
	}
	if !sel.NoRoot {
		for id, node := range v.nodes {
			_, hasParent := v.nodes[node.parent]
			if !hasParent || node.priority {
				ancestors(id)
				visibleChildren(id, map[string]bool{})
			}
		}
	}
	for _, id := range sel.Listen {
		for member := range v.Scope(id, true) {
			ancestors(member)
		}
	}
	v.partial = len(v.selected) < len(v.nodes)
}

// Scope follows UI containment, including cause links. Reverse cause links
// seed only the requested root (matching live SelectLogsBeneathSpan).
func (v *ArchiveSpans) Scope(root string, descendants bool) map[string]bool {
	scope := map[string]bool{root: true}
	if !descendants {
		return scope
	}
	queue := append([]string{root}, v.nodes[root].causes...)
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		scope[id] = true
		for _, kid := range v.children[id] {
			if !scope[kid] {
				scope[kid] = true
				queue = append(queue, kid)
			}
		}
	}
	return scope
}

func (v *ArchiveSpans) Includes(row Span) bool {
	node, ok := v.nodes[row.SpanID]
	return ok && node.row == row.ID && (v.selected == nil || v.selected[row.SpanID])
}

func (v *ArchiveSpans) Attributes(spanID string) []*commonpb.KeyValue {
	node := v.nodes[spanID]
	return []*commonpb.KeyValue{
		{Key: telemetryattrs.UIChildCountAttr, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: int64(len(v.children[spanID]))}}},
		{Key: telemetryattrs.UIHasLogsAttr, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: node.hasLogs}}},
		{Key: telemetryattrs.UIPartialAttr, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: v.partial}}},
	}
}

// SelectArchiveLogsRange scans a bounded metadata window and reads only
// matching bodies. next advances across filtered rows too, so terminal and
// resume cursors still refer to the immutable underlying stream, not a filter.
func (s *DB) SelectArchiveLogsRange(ctx context.Context, p SelectLogsRangeParams, traceID string, scope map[string]bool, sel *archive.LogSelection, excluded map[int64]bool) ([]Log, int64, error) {
	if err := ctx.Err(); err != nil {
		return nil, p.AfterID, err
	}
	if p.AfterID < 0 || p.ThroughID < p.AfterID || p.Limit <= 0 {
		return nil, p.AfterID, fmt.Errorf("invalid archive log range")
	}
	next := p.AfterID + min(p.ThroughID-p.AfterID, p.Limit)
	var ids []int64
	var bytes int64
	s.logIdx.mu.RLock()
	if p.ThroughID > int64(len(s.logIdx.archive)) {
		s.logIdx.mu.RUnlock()
		return nil, p.AfterID, errors.New("archive log stream truncated before cut")
	}
	for i := p.AfterID; i < next; i++ {
		m := s.logIdx.archive[i]
		id := i + 1
		if m.trace != traceID || excluded[id] || (scope != nil && !scope[m.span]) {
			continue
		}
		if m.err != nil {
			s.logIdx.mu.RUnlock()
			return nil, p.AfterID, m.err
		}
		if m.control {
			continue
		}
		if sel != nil {
			if sel.After != nil && m.timestamp <= sel.After.UnixNano() {
				continue
			}
			switch sel.Records {
			case archive.LogRecordsLogs:
				if !m.text {
					continue
				}
			case archive.LogRecordsCallPayloads:
				if !m.call {
					continue
				}
			case archive.LogRecordsMetadata:
				if !m.metadata {
					continue
				}
			}
		}
		// Bound retained bodies by bytes as well as rows. A single oversized
		// record is left to the framing layer's existing explicit size error.
		if len(ids) > 0 && m.size > (1<<20)-bytes {
			next = i
			break
		}
		bytes += m.size
		ids = append(ids, id)
	}
	s.logIdx.mu.RUnlock()
	rows := make([]Log, 0, len(ids))
	for _, id := range ids {
		row, ok, err := s.logs.readArchiveID(ctx, id)
		if err != nil {
			return nil, p.AfterID, err
		}
		if !ok {
			return nil, p.AfterID, fmt.Errorf("missing archive log row %d", id)
		}
		rows = append(rows, row)
	}
	return rows, next, nil
}

// readArchiveID skips intervening spill frames by length instead of allocating
// and decoding their bodies. Recovery already validated frame/row integrity;
// selected rows still go through the normal codec and identity check.
func (s *logStream[Row]) readArchiveID(ctx context.Context, id int64) (Row, bool, error) {
	var zero Row
	s.mu.Lock()
	if id >= s.tailBase {
		index := id - s.tailBase
		if index >= int64(len(s.tail)) {
			s.mu.Unlock()
			return zero, false, nil
		}
		row := s.tail[index]
		s.mu.Unlock()
		return row, true, nil
	}
	s.mu.Unlock()
	offset, committed, ok := s.spill.readBounds(id - 1)
	if !ok {
		return zero, false, nil
	}
	for offset < committed {
		if err := ctx.Err(); err != nil {
			return zero, false, err
		}
		// Two varints fit in 20 bytes. Do not buffer arbitrary log content
		// merely to walk from a sparse index entry to the selected row.
		var header [2 * binary.MaxVarintLen64]byte
		n, err := s.spill.file.ReadAt(header[:min(int64(len(header)), committed-offset)], offset)
		if err != nil && !errors.Is(err, io.EOF) {
			return zero, false, err
		}
		length, width := binary.Uvarint(header[:n])
		if width <= 0 {
			return zero, false, errors.New("invalid archive frame length")
		}
		start := offset + int64(width)
		if length > uint64(committed-start) {
			return zero, false, io.ErrUnexpectedEOF
		}
		rowID, idWidth := binary.Varint(header[width:n])
		if idWidth <= 0 || uint64(idWidth) > length {
			return zero, false, errors.New("invalid archive row ID")
		}
		switch {
		case rowID == id:
			if length > uint64(maxInt) {
				return zero, false, fmt.Errorf("archive frame too large")
			}
			payload := make([]byte, int(length))
			if _, err := s.spill.file.ReadAt(payload, start); err != nil {
				return zero, false, err
			}
			row, err := s.codec.decode(payload)
			if err != nil {
				return zero, false, err
			}
			if s.codec.getID(row) != id {
				return zero, false, errors.New("archive row identity changed during read")
			}
			return row, true, nil
		case rowID > id:
			return zero, false, nil
		}
		offset = start + int64(length)
	}
	return zero, false, nil
}
