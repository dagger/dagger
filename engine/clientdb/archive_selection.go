package clientdb

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"

	"github.com/dagger/dagger/engine/agentcontrol"
	"github.com/dagger/dagger/engine/archive"
	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/encoding/protowire"
)

// archiveLogMeta never retains a body, resource, or attribute blob. The index
// lets lazy reads reject unrelated rows before reading their (possibly huge)
// payloads from disk. It is rebuilt during ordinary spill recovery, and every
// log row of every store carries one, so it is kept compact and pointer-free:
// trace and span IDs are interned (see logLookup), classes are bit flags, and
// the rare per-row decode error lives in a side map keyed by row index.
type archiveLogMeta struct {
	timestamp int64
	size      int64
	trace     uint32
	span      uint32
	flags     archiveLogFlags
}

type archiveLogFlags uint8

const (
	// Row classes, as read by SelectArchiveLogsRange and ArchiveSpanView.
	archiveLogText archiveLogFlags = 1 << iota
	archiveLogNonempty
	archiveLogCall
	archiveLogMetadata
	archiveLogControl
	// archiveLogFailed marks a row whose attributes or body failed to
	// decode; its error is in logLookup.archiveErrs.
	archiveLogFailed
	// archiveLogHidden is an attribute class only (global/verbose logs); it
	// folds into archiveLogText and is never stored.
	archiveLogHidden
)

func (f archiveLogFlags) has(flag archiveLogFlags) bool { return f&flag != 0 }

// archiveLogAttrClass classifies a row's protojson-encoded attributes: the
// hidden, control, metadata and call flags. It depends on the attribute bytes
// alone, so its results may be memoized by those bytes.
func archiveLogAttrClass(raw []byte) (archiveLogFlags, error) {
	var attrs []*commonpb.KeyValue
	if err := UnmarshalProtoJSONs(raw, &commonpb.KeyValue{}, &attrs); err != nil {
		return 0, err
	}
	var flags archiveLogFlags
	for _, a := range attrs {
		switch a.GetKey() {
		case telemetry.LogsGlobalAttr, telemetry.LogsVerboseAttr:
			if a.GetValue().GetBoolValue() {
				flags |= archiveLogHidden
			}
		case agentcontrol.VersionAttr:
			flags |= archiveLogControl
		case telemetryattrs.LogRoleAttr, telemetryattrs.ProgressItemAttr, telemetryattrs.AgentStateAttr, telemetryattrs.AgentSnapshotDigestAttr:
			flags |= archiveLogMetadata
		case telemetry.ContentTypeAttr:
			// The last content type wins.
			flags &^= archiveLogCall
			if a.GetValue().GetStringValue() == telemetryattrs.CallPayloadContentType {
				// Typed binary semantic records include agent state and
				// snapshots.
				flags |= archiveLogCall | archiveLogMetadata
			}
		}
	}
	return flags, nil
}

// archiveLogClass completes a row's classification from its attribute class
// and its encoded AnyValue body. A failed row carries no text classes.
func archiveLogClass(attrFlags archiveLogFlags, attrErr error, body []byte) (archiveLogFlags, error) {
	if attrErr != nil {
		return archiveLogFailed, attrErr
	}
	flags := attrFlags &^ archiveLogHidden
	// Inspect the oneof wire tag without allocating a copy of a large body.
	var text, nonempty bool
	for len(body) > 0 {
		num, typ, n := protowire.ConsumeTag(body)
		if n < 0 {
			return flags | archiveLogFailed, protowire.ParseError(n)
		}
		body = body[n:]
		n = protowire.ConsumeFieldValue(num, typ, body)
		if n < 0 {
			return flags | archiveLogFailed, protowire.ParseError(n)
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
	if text && !attrFlags.has(archiveLogMetadata|archiveLogControl|archiveLogHidden) {
		flags |= archiveLogText
	}
	if nonempty {
		flags |= archiveLogNonempty
	}
	if !text {
		flags |= archiveLogMetadata
	}
	return flags, nil
}

// Bounds of logLookup's attribute class memo. Log rows overwhelmingly repeat
// a few small attribute blobs (stdio streams and the like), so a small memo
// skips nearly every protojson decode while retaining little.
const (
	archiveAttrMemoEntries  = 256
	archiveAttrMemoMaxBytes = 512
)

type archiveAttrClassResult struct {
	flags archiveLogFlags
	err   error
}

// archiveClassify classifies rows, memoizing attribute classes by their exact
// bytes, which keeps the classification identical to decoding every row.
func (l *logLookup) archiveClassify(rows []Log) ([]archiveLogFlags, []error) {
	flags := make([]archiveLogFlags, len(rows))
	var errs []error
	l.memoMu.Lock()
	defer l.memoMu.Unlock()
	for i, row := range rows {
		attrs, ok := l.attrMemo[string(row.Attributes)]
		if !ok {
			attrs.flags, attrs.err = archiveLogAttrClass(row.Attributes)
			if len(row.Attributes) <= archiveAttrMemoMaxBytes {
				if len(l.attrMemo) >= archiveAttrMemoEntries {
					clear(l.attrMemo)
				}
				l.attrMemo[string(row.Attributes)] = attrs
			}
		}
		var err error
		flags[i], err = archiveLogClass(attrs.flags, attrs.err, row.Body)
		if err != nil {
			if errs == nil {
				errs = make([]error, len(rows))
			}
			errs[i] = err
		}
	}
	return flags, errs
}

// internLocked returns id's index in a string intern table. It requires the
// table's owner lock for writing.
func internLocked(index map[string]uint32, table *[]string, id string) uint32 {
	if i, ok := index[id]; ok {
		return i
	}
	i := uint32(len(*table))
	*table = append(*table, id)
	index[id] = i
	return i
}

// ArchiveSpans is one request's view of an archive's span topology at
// precisely one span/log cut: the shared, immutable topology plus the
// request's own selection. It stores only the last row ID and view metadata,
// never full spans or log bodies.
type ArchiveSpans struct {
	*archiveTopology
	selected map[string]bool
	partial  bool
}

// archiveTopology is the selection-independent part of an archive span view.
// Rows at or below a cut never change, so a topology is immutable once built
// and is shared by every request for the same (trace, cut) on a store; see
// archiveTopologyCache. Nothing may mutate it after construction.
type archiveTopology struct {
	nodes    map[string]archiveSpanNode
	children map[string][]string
}
type archiveSpanNode struct {
	row                   int64
	parent                string
	priority, passthrough bool
	causes                []string
	hasLogs               bool
}

// ArchiveSpanView returns the archive span view of traceID at cut, with sel
// applied. The topology is built once per (trace, cut) on this store and
// shared; only the selection is computed per call.
func (s *DB) ArchiveSpanView(ctx context.Context, traceID string, cut HighWater, sel *archive.SpanSelection) (*ArchiveSpans, error) {
	if cut.Spans < 0 || cut.Logs < 0 {
		return nil, errors.New("invalid archive cut")
	}
	topo, err := s.archiveTopos.get(ctx, archiveTopologyKey{trace: traceID, spans: cut.Spans, logs: cut.Logs}, func(ctx context.Context) (*archiveTopology, error) {
		return s.buildArchiveTopology(ctx, traceID, cut)
	})
	if err != nil {
		return nil, err
	}
	v := &ArchiveSpans{archiveTopology: topo}
	v.selectSpans(sel)
	return v, nil
}

// buildArchiveTopology reads every span row up to the cut and the log
// metadata index up to the cut. It is the expensive part of a span view.
func (s *DB) buildArchiveTopology(ctx context.Context, traceID string, cut HighWater) (*archiveTopology, error) {
	v := &archiveTopology{nodes: map[string]archiveSpanNode{}, children: map[string][]string{}}
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
			node := archiveSpanNode{row: row.ID, parent: row.ParentSpanID.String, causes: causalLinkTargets(row)}
			var attrs []*commonpb.KeyValue
			if err := UnmarshalProtoJSONs(row.Attributes, &commonpb.KeyValue{}, &attrs); err != nil {
				return nil, err
			}
			node.priority, node.passthrough = archiveSpanClass(attrs)
			node.priority = node.priority || row.StatusCode == int64(codes.Error)
			v.nodes[row.SpanID] = node
		}
	}
	s.logIdx.mu.RLock()
	if cut.Logs > int64(len(s.logIdx.archive)) {
		s.logIdx.mu.RUnlock()
		return nil, errors.New("archive log stream truncated before cut")
	}
	// A trace the index has never seen has no log rows to mark.
	if trace, ok := s.logIdx.traceIndex[traceID]; ok {
		spans := s.logIdx.spanIDs
		for i, m := range s.logIdx.archive[:cut.Logs] {
			if i%128 == 0 {
				if err := ctx.Err(); err != nil {
					s.logIdx.mu.RUnlock()
					return nil, err
				}
			}
			if m.trace != trace || !m.flags.has(archiveLogText) || !m.flags.has(archiveLogNonempty) {
				continue
			}
			if node, ok := v.nodes[spans[m.span]]; ok {
				node.hasLogs = true
				v.nodes[spans[m.span]] = node
			}
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
	return v, nil
}

// archiveTopologyCacheSize bounds the topologies one open store retains. A
// main client's store normally backs a single archive trace, at a single cut.
const archiveTopologyCacheSize = 4

type archiveTopologyKey struct {
	trace       string
	spans, logs int64
}

// archiveTopologyCache keeps the most recently used archive topologies of one
// store, and coalesces concurrent construction of the same one: clients issue
// many span-view and descendant-log requests at once against one archive.
// It lives on the DB, so it is released with the store.
type archiveTopologyCache struct {
	mu      sync.Mutex
	entries map[archiveTopologyKey]*archiveTopologyEntry
	// lru orders keys from least to most recently used.
	lru []archiveTopologyKey
}

type archiveTopologyEntry struct {
	done chan struct{}
	topo *archiveTopology
	err  error
}

func newArchiveTopologyCache() *archiveTopologyCache {
	return &archiveTopologyCache{entries: map[archiveTopologyKey]*archiveTopologyEntry{}}
}

// touchLocked marks key most recently used, evicting the least recently used
// keys beyond the bound. An evicted in-flight build still completes for the
// callers already waiting on it.
func (c *archiveTopologyCache) touchLocked(key archiveTopologyKey) {
	c.lru = slices.DeleteFunc(c.lru, func(k archiveTopologyKey) bool { return k == key })
	c.lru = append(c.lru, key)
	for len(c.lru) > archiveTopologyCacheSize {
		delete(c.entries, c.lru[0])
		c.lru = c.lru[1:]
	}
}

func (c *archiveTopologyCache) forgetLocked(key archiveTopologyKey, e *archiveTopologyEntry) {
	if c.entries[key] != e {
		return
	}
	delete(c.entries, key)
	c.lru = slices.DeleteFunc(c.lru, func(k archiveTopologyKey) bool { return k == key })
}

// get returns the cached topology for key, building it at most once at a
// time. Failures are not cached. A waiter whose builder was canceled (by its
// own request's context) retries with its own context rather than failing.
func (c *archiveTopologyCache) get(ctx context.Context, key archiveTopologyKey, build func(context.Context) (*archiveTopology, error)) (*archiveTopology, error) {
	for {
		c.mu.Lock()
		e := c.entries[key]
		if e == nil {
			e = &archiveTopologyEntry{done: make(chan struct{})}
			c.entries[key] = e
			c.touchLocked(key)
			c.mu.Unlock()
			e.topo, e.err = build(ctx)
			if e.err != nil {
				c.mu.Lock()
				c.forgetLocked(key, e)
				c.mu.Unlock()
			}
			close(e.done)
			return e.topo, e.err
		}
		c.touchLocked(key)
		c.mu.Unlock()
		select {
		case <-e.done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if e.err == nil {
			return e.topo, nil
		}
		if (errors.Is(e.err, context.Canceled) || errors.Is(e.err, context.DeadlineExceeded)) && ctx.Err() == nil {
			continue
		}
		return nil, e.err
	}
}

// archiveSpanClass reads a span's view classification from its attributes:
// whether it belongs to the priority view, and whether the UI renders its
// children in its place.
//
// Priority matches Dagger Cloud's: checks, tests, revealed spans, and agent
// conversation spans. The engine stamps gen_ai.agent.id on an agent's loop
// span and each of its message spans, so a conversation is whole -- every
// message's chain up to its loop -- without expanding down to it.
func archiveSpanClass(attrs []*commonpb.KeyValue) (priority, passthrough bool) {
	for _, a := range attrs {
		switch a.GetKey() {
		case telemetry.CheckNameAttr, string(semconv.TestCaseNameKey), string(semconv.TestSuiteNameKey), string(semconv.GenAIAgentIDKey):
			priority = true
		case telemetry.UIPassthroughAttr, telemetry.UIInternalAttr:
			passthrough = passthrough || a.GetValue().GetBoolValue()
		case telemetry.UIRevealAttr:
			priority = priority || a.GetValue().GetBoolValue()
		}
	}
	return priority, passthrough
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
// Agent control records are skipped unless includeControl is set (an unsealed
// archive, which has no bootstrap to supply them).
func (s *DB) SelectArchiveLogsRange(ctx context.Context, p SelectLogsRangeParams, traceID string, scope map[string]bool, sel *archive.LogSelection, includeControl bool) ([]Log, int64, error) {
	if err := ctx.Err(); err != nil {
		return nil, p.AfterID, err
	}
	if p.AfterID < 0 || p.ThroughID < p.AfterID || p.Limit <= 0 {
		return nil, p.AfterID, fmt.Errorf("invalid archive log range")
	}
	next := p.AfterID + min(p.ThroughID-p.AfterID, p.Limit)
	var ids []int64
	var bytes int64
	idx := s.logIdx
	idx.mu.RLock()
	if p.ThroughID > int64(len(idx.archive)) {
		idx.mu.RUnlock()
		return nil, p.AfterID, errors.New("archive log stream truncated before cut")
	}
	// Rows of a trace the index has never seen cannot match.
	trace, traceKnown := idx.traceIndex[traceID]
	for i := p.AfterID; traceKnown && i < next; i++ {
		m := idx.archive[i]
		id := i + 1
		if m.trace != trace || (scope != nil && !scope[idx.spanIDs[m.span]]) {
			continue
		}
		if m.flags.has(archiveLogFailed) {
			err := idx.archiveErrs[i]
			idx.mu.RUnlock()
			return nil, p.AfterID, err
		}
		if m.flags.has(archiveLogControl) && !includeControl {
			continue
		}
		if sel != nil {
			if sel.After != nil && m.timestamp <= sel.After.UnixNano() {
				continue
			}
			switch sel.Records {
			case archive.LogRecordsLogs:
				if !m.flags.has(archiveLogText) {
					continue
				}
			case archive.LogRecordsCallPayloads:
				if !m.flags.has(archiveLogCall) {
					continue
				}
			case archive.LogRecordsMetadata:
				if !m.flags.has(archiveLogMetadata) {
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
	idx.mu.RUnlock()
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
