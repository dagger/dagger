package clientdb

import (
	"context"
	"errors"
	"fmt"
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

// Selected reads answer the same span and log selections as Dagger Cloud's
// OTLP stream endpoints (see internal/cloud/otlp.go), over this store at a
// fixed cut: a span view (priority, listened subtrees, full, with dagui
// annotations) and log reads scoped to a span or its descendants and to a
// record class.

// logMeta never retains a body, resource, or attribute blob. The index lets
// selected reads reject unrelated rows before reading their (possibly huge)
// payloads from disk. It is rebuilt during ordinary spill recovery, and every
// log row of every store carries one, so it is kept compact and pointer-free:
// trace and span IDs are interned (see logLookup), classes are bit flags, and
// the rare per-row decode error lives in a side map keyed by row index.
type logMeta struct {
	timestamp int64
	size      int64
	trace     uint32
	span      uint32
	// digest interns the row's dagger.io/dag.digest attribute; 0 is none.
	digest uint32
	flags  logFlags
}

type logFlags uint8

const (
	// Row classes, as read by SelectLogsRangeMatching and SpanViewAt.
	logText logFlags = 1 << iota
	logNonempty
	logCall
	logMetadata
	logControl
	// logFailed marks a row whose attributes or body failed to decode; its
	// error is in logLookup.metaErrs.
	logFailed
	// logHidden is an attribute class only (global/verbose logs); it folds
	// into logText and is never stored.
	logHidden
)

func (f logFlags) has(flag logFlags) bool { return f&flag != 0 }

// display reports display text: a decodable text row that is neither a
// semantic record nor agent control. Unlike logText it includes global and
// verbose rows, which only the frontend may hide.
func (f logFlags) display() bool { return !f.has(logMetadata | logControl | logFailed) }

// logAttrClass classifies a row's protojson-encoded attributes: the hidden,
// control, metadata and call flags, and its dag.digest. It depends on the
// attribute bytes alone, so its results may be memoized by those bytes.
func logAttrClass(raw []byte) (logFlags, string, error) {
	var attrs []*commonpb.KeyValue
	if err := UnmarshalProtoJSONs(raw, &commonpb.KeyValue{}, &attrs); err != nil {
		return 0, "", err
	}
	var flags logFlags
	var digest string
	var digestSeen bool
	for _, a := range attrs {
		switch a.GetKey() {
		case telemetry.DagDigestAttr:
			// dagui routes by the first one (dagui.DB.routeLog).
			if !digestSeen {
				digest, digestSeen = a.GetValue().GetStringValue(), true
			}
		case telemetry.LogsGlobalAttr, telemetry.LogsVerboseAttr:
			if a.GetValue().GetBoolValue() {
				flags |= logHidden
			}
		case agentcontrol.VersionAttr:
			flags |= logControl
		case telemetryattrs.LogRoleAttr, telemetryattrs.ProgressItemAttr, telemetryattrs.AgentStateAttr, telemetryattrs.AgentSnapshotDigestAttr:
			flags |= logMetadata
		case telemetry.ContentTypeAttr:
			// The last content type wins.
			flags &^= logCall
			if a.GetValue().GetStringValue() == telemetryattrs.CallPayloadContentType {
				// Typed binary semantic records include agent state and
				// snapshots.
				flags |= logCall | logMetadata
			}
		}
	}
	return flags, digest, nil
}

// logClass completes a row's classification from its attribute class and its
// encoded AnyValue body. A failed row carries no text classes.
func logClass(attrFlags logFlags, attrErr error, body []byte) (logFlags, error) {
	if attrErr != nil {
		return logFailed, attrErr
	}
	flags := attrFlags &^ logHidden
	// Inspect the oneof wire tag without allocating a copy of a large body.
	var text, nonempty bool
	for len(body) > 0 {
		num, typ, n := protowire.ConsumeTag(body)
		if n < 0 {
			return flags | logFailed, protowire.ParseError(n)
		}
		body = body[n:]
		n = protowire.ConsumeFieldValue(num, typ, body)
		if n < 0 {
			return flags | logFailed, protowire.ParseError(n)
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
	if text && !attrFlags.has(logMetadata|logControl|logHidden) {
		flags |= logText
	}
	if nonempty {
		flags |= logNonempty
	}
	if !text {
		flags |= logMetadata
	}
	return flags, nil
}

// Bounds of logLookup's attribute class memo. Log rows overwhelmingly repeat
// a few small attribute blobs (stdio streams and the like), so a small memo
// skips nearly every protojson decode while retaining little.
const (
	logAttrMemoEntries  = 256
	logAttrMemoMaxBytes = 512
)

type logAttrClassResult struct {
	flags  logFlags
	digest string
	err    error
}

// classify classifies rows, memoizing attribute classes by their exact bytes,
// which keeps the classification identical to decoding every row. digests
// holds each row's dag.digest ("" for none).
func (l *logLookup) classify(rows []Log) ([]logFlags, []string, []error) {
	flags := make([]logFlags, len(rows))
	digests := make([]string, len(rows))
	var errs []error
	l.memoMu.Lock()
	defer l.memoMu.Unlock()
	for i, row := range rows {
		attrs, ok := l.attrMemo[string(row.Attributes)]
		if !ok {
			attrs.flags, attrs.digest, attrs.err = logAttrClass(row.Attributes)
			if len(row.Attributes) <= logAttrMemoMaxBytes {
				if len(l.attrMemo) >= logAttrMemoEntries {
					clear(l.attrMemo)
				}
				l.attrMemo[string(row.Attributes)] = attrs
			}
		}
		digests[i] = attrs.digest
		var err error
		flags[i], err = logClass(attrs.flags, attrs.err, row.Body)
		if err != nil {
			if errs == nil {
				errs = make([]error, len(rows))
			}
			errs[i] = err
		}
	}
	return flags, digests, errs
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

// SpanView is one request's view of a trace's span topology at precisely one
// span/log cut: the shared, immutable topology plus the request's own
// selection. It stores only the last row ID and view metadata, never full
// spans or log bodies.
type SpanView struct {
	*spanTopology
	selected map[string]bool
	partial  bool
}

// spanTopology is the selection-independent part of a span view. Rows at or
// below a cut never change, so a topology is immutable once built and is
// shared by every request for the same (trace, cut) on a store; see
// spanTopologyCache. Nothing may mutate it after construction.
type spanTopology struct {
	nodes    map[string]spanNode
	children map[string][]string
}

type spanNode struct {
	row                   int64
	parent                string
	priority, passthrough bool
	causes                []string
	hasLogs               bool
}

// SpanViewAt returns the span view of traceID at cut, with sel applied. The
// topology is built once per (trace, cut) on this store and shared; only the
// selection is computed per call.
func (s *DB) SpanViewAt(ctx context.Context, traceID string, cut HighWater, sel *archive.SpanSelection) (*SpanView, error) {
	if cut.Spans < 0 || cut.Logs < 0 {
		return nil, errors.New("invalid telemetry cut")
	}
	topo, err := s.spanTopos.get(ctx, spanTopologyKey{trace: traceID, spans: cut.Spans, logs: cut.Logs}, func(ctx context.Context) (*spanTopology, error) {
		return s.buildSpanTopology(ctx, traceID, cut)
	})
	if err != nil {
		return nil, err
	}
	v := &SpanView{spanTopology: topo}
	v.selectSpans(sel)
	return v, nil
}

// buildSpanTopology reads every span row up to the cut and the log metadata
// index up to the cut. It is the expensive part of a span view.
func (s *DB) buildSpanTopology(ctx context.Context, traceID string, cut HighWater) (*spanTopology, error) {
	v := &spanTopology{nodes: map[string]spanNode{}, children: map[string][]string{}}
	for cursor := int64(0); cursor < cut.Spans; {
		rows, err := s.SelectSpansRange(ctx, SelectSpansRangeParams{AfterID: cursor, ThroughID: cut.Spans, Limit: 128})
		if err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			return nil, errors.New("span stream truncated before cut")
		}
		for _, row := range rows {
			cursor = row.ID
			if row.TraceID != traceID {
				continue
			}
			node := spanNode{row: row.ID, parent: row.ParentSpanID.String, causes: causalLinkTargets(row)}
			var attrs []*commonpb.KeyValue
			if err := UnmarshalProtoJSONs(row.Attributes, &commonpb.KeyValue{}, &attrs); err != nil {
				return nil, err
			}
			node.priority, node.passthrough = spanViewClass(attrs)
			node.priority = node.priority || row.StatusCode == int64(codes.Error)
			v.nodes[row.SpanID] = node
		}
	}
	s.logIdx.mu.RLock()
	if cut.Logs > int64(len(s.logIdx.meta)) {
		s.logIdx.mu.RUnlock()
		return nil, errors.New("log stream truncated before cut")
	}
	// A trace the index has never seen has no log rows to mark.
	if trace, ok := s.logIdx.traceIndex[traceID]; ok {
		spans := s.logIdx.spanIDs
		for i, m := range s.logIdx.meta[:cut.Logs] {
			if i%128 == 0 {
				if err := ctx.Err(); err != nil {
					s.logIdx.mu.RUnlock()
					return nil, err
				}
			}
			if m.trace != trace || !m.flags.has(logText) || !m.flags.has(logNonempty) {
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
		// Record children of parents outside the store too. A trace's root is
		// often the CLI's own command span, which only the CLI exports (to
		// its frontend and Cloud), so the store holds its children but not it;
		// scoping logs to that absent root still selects the whole trace.
		if node.parent != "" {
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

// spanTopologyCacheSize bounds the topologies one open store retains. A main
// client's store normally backs a single trace, at a single cut.
const spanTopologyCacheSize = 4

type spanTopologyKey struct {
	trace       string
	spans, logs int64
}

// spanTopologyCache keeps the most recently used span topologies of one
// store, and coalesces concurrent construction of the same one: clients issue
// many span-view and descendant-log requests at once against one trace. It
// lives on the DB, so it is released with the store.
type spanTopologyCache struct {
	mu      sync.Mutex
	entries map[spanTopologyKey]*spanTopologyEntry
	// lru orders keys from least to most recently used.
	lru []spanTopologyKey
}

type spanTopologyEntry struct {
	done chan struct{}
	topo *spanTopology
	err  error
}

func newSpanTopologyCache() *spanTopologyCache {
	return &spanTopologyCache{entries: map[spanTopologyKey]*spanTopologyEntry{}}
}

// touchLocked marks key most recently used, evicting the least recently used
// keys beyond the bound. An evicted in-flight build still completes for the
// callers already waiting on it.
func (c *spanTopologyCache) touchLocked(key spanTopologyKey) {
	c.lru = slices.DeleteFunc(c.lru, func(k spanTopologyKey) bool { return k == key })
	c.lru = append(c.lru, key)
	for len(c.lru) > spanTopologyCacheSize {
		delete(c.entries, c.lru[0])
		c.lru = c.lru[1:]
	}
}

func (c *spanTopologyCache) forgetLocked(key spanTopologyKey, e *spanTopologyEntry) {
	if c.entries[key] != e {
		return
	}
	delete(c.entries, key)
	c.lru = slices.DeleteFunc(c.lru, func(k spanTopologyKey) bool { return k == key })
}

// get returns the cached topology for key, building it at most once at a
// time. Failures are not cached. A waiter whose builder was canceled (by its
// own request's context) retries with its own context rather than failing.
func (c *spanTopologyCache) get(ctx context.Context, key spanTopologyKey, build func(context.Context) (*spanTopology, error)) (*spanTopology, error) {
	for {
		c.mu.Lock()
		e := c.entries[key]
		if e == nil {
			e = &spanTopologyEntry{done: make(chan struct{})}
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

// spanViewClass reads a span's view classification from its attributes:
// whether it belongs to the priority view, and whether the UI renders its
// children in its place.
//
// Priority matches Dagger Cloud's: checks, tests, revealed spans, and agent
// conversation spans. The engine stamps gen_ai.agent.id on an agent's loop
// span and each of its message spans, so a conversation is whole -- every
// message's chain up to its loop -- without expanding down to it.
func spanViewClass(attrs []*commonpb.KeyValue) (priority, passthrough bool) {
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
func (v *SpanView) selectSpans(sel *archive.SpanSelection) {
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
	// As Cloud's: a listened span, its children and their passthrough
	// descendants (with ancestors), so expanding loads one visible level.
	for _, id := range sel.Listen {
		if _, ok := v.nodes[id]; !ok {
			continue
		}
		ancestors(id)
		visibleChildren(id, map[string]bool{})
	}
	v.partial = len(v.selected) < len(v.nodes)
}

// Scope follows UI containment, including cause links. Reverse cause links
// seed only the requested root (matching live SelectLogsBeneathSpan).
func (v *SpanView) Scope(root string, descendants bool) map[string]bool {
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

// Includes reports whether row is the latest row of a selected span.
func (v *SpanView) Includes(row Span) bool {
	node, ok := v.nodes[row.SpanID]
	return ok && node.row == row.ID && (v.selected == nil || v.selected[row.SpanID])
}

// Attributes are the dagui view annotations for spanID: its child count,
// whether it has logs, and whether the selection left spans out.
func (v *SpanView) Attributes(spanID string) []*commonpb.KeyValue {
	node := v.nodes[spanID]
	return []*commonpb.KeyValue{
		{Key: telemetryattrs.UIChildCountAttr, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: int64(len(v.children[spanID]))}}},
		{Key: telemetryattrs.UIHasLogsAttr, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: node.hasLogs}}},
		{Key: telemetryattrs.UIPartialAttr, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: v.partial}}},
	}
}

// selectedLogBatchBytes bounds the bodies one SelectLogsRangeMatching call
// reads. A single oversized record is left to the framing layer.
const selectedLogBatchBytes = 1 << 20

// SelectLogsRangeMatching scans a bounded metadata window and reads only
// matching bodies. next advances across filtered rows too, so terminal and
// resume cursors still refer to the immutable underlying stream, not a filter.
// Agent control records are skipped unless includeControl is set (an unsealed
// archive, which has no bootstrap to supply them).
func (s *DB) SelectLogsRangeMatching(ctx context.Context, p SelectLogsRangeParams, traceID string, scope map[string]bool, sel *archive.LogSelection, includeControl bool) ([]Log, int64, error) {
	trace, traceKnown := uint32(0), false
	traceResolved := false
	return s.scanLogs(ctx, p, func(idx *logLookup, i int64, m logMeta) (bool, error) {
		if !traceResolved {
			// Rows of a trace the index has never seen cannot match.
			trace, traceKnown = idx.traceIndex[traceID]
			traceResolved = true
		}
		if !traceKnown || m.trace != trace || (scope != nil && !scope[idx.spanIDs[m.span]]) {
			return false, nil
		}
		if m.flags.has(logFailed) {
			return false, idx.metaErrs[i]
		}
		if m.flags.has(logControl) && !includeControl {
			return false, nil
		}
		if sel != nil {
			if sel.After != nil && m.timestamp <= sel.After.UnixNano() {
				return false, nil
			}
			switch sel.Records {
			case archive.LogRecordsLogs:
				return m.flags.has(logText), nil
			case archive.LogRecordsCallPayloads:
				return m.flags.has(logCall), nil
			case archive.LogRecordsMetadata:
				return m.flags.has(logMetadata), nil
			}
		}
		return true, nil
	})
}

// scanLogs scans a bounded metadata window (p.AfterID, min(p.ThroughID,
// p.AfterID+p.Limit)] under the index's read lock and reads the bodies of
// the rows match selects, bounded by bytes as well as rows. next is the last
// row the scan covered, matched or not, so cursors address the underlying
// stream. A match error ends the scan with that error.
func (s *DB) scanLogs(ctx context.Context, p SelectLogsRangeParams, match func(idx *logLookup, i int64, m logMeta) (bool, error)) ([]Log, int64, error) {
	if err := ctx.Err(); err != nil {
		return nil, p.AfterID, err
	}
	if p.AfterID < 0 || p.ThroughID < p.AfterID || p.Limit <= 0 {
		return nil, p.AfterID, fmt.Errorf("invalid log range")
	}
	next := p.AfterID + min(p.ThroughID-p.AfterID, p.Limit)
	var ids []int64
	var bytes int64
	idx := s.logIdx
	idx.mu.RLock()
	if p.ThroughID > int64(len(idx.meta)) {
		idx.mu.RUnlock()
		return nil, p.AfterID, errors.New("log stream truncated before cut")
	}
	for i := p.AfterID; i < next; i++ {
		m := idx.meta[i]
		ok, err := match(idx, i, m)
		if err != nil {
			idx.mu.RUnlock()
			return nil, p.AfterID, err
		}
		if !ok {
			continue
		}
		// Bound retained bodies by bytes as well as rows.
		if len(ids) > 0 && m.size > selectedLogBatchBytes-bytes {
			next = i
			break
		}
		bytes += m.size
		ids = append(ids, i+1)
	}
	idx.mu.RUnlock()
	rows, err := s.SelectLogRows(ctx, ids)
	if err != nil {
		return nil, p.AfterID, err
	}
	return rows, next, nil
}
