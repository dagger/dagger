package clientdb

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"sync"

	telemetry "github.com/dagger/otel-go"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	otlptracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/engine/telemetryattrs"
)

type spanLookupKey struct {
	traceID string
	spanID  string
}

type spanLookup struct {
	mu sync.RWMutex

	// The composite key preserves SelectSpan's trace_id + span_id predicate.
	firstRow map[spanLookupKey]int64
	// lastRow maps each span ID to its NEWEST snapshot row. OTel span
	// snapshots are cumulative — a later row carries the span's full current
	// state (name, attributes, links, status, end time) — so the last row
	// alone reconstructs the state a sequential replay of every snapshot
	// would end with. This is what lets a scoped reader materialize a span
	// subtree without replaying the whole stream. Keyed by span ID only,
	// following the children map's identity.
	lastRow map[string]int64
	// parent maps each span ID to its parent span ID, from the first
	// snapshot that carries one. Together with children it makes ancestor
	// chains answerable from the index alone — a scoped load includes the
	// chain from every scope member up to the trace root, so no member's
	// parent pointer dangles at a placeholder.
	parent map[string]string
	// checkSpans/testSpans remember the spans whose attributes mark them as
	// named checks or tests (telemetry.CheckNameAttr, semconv test.case.name
	// / test.suite.name). Detected with a raw byte scan of the encoded
	// attributes — no decode — so a name lookup (e.g. ReadTrace(check:))
	// answers from the index instead of scanning the stream. A false
	// positive (marker bytes inside a value) merely loads one extra span.
	checkSpans map[string]struct{}
	testSpans  map[string]struct{}
	// Duplicate snapshots are suppressed when firstRow is populated, leaving
	// one copy of each child span ID in its parent's slice.
	children map[string][]string
	// causalChildren indexes cause-purpose span links: linked (target) span ID
	// → linking span IDs. dagui treats such links as parent→child edges (the
	// linking span joins the linked span's ChildSpans; see dagql/dagui/db.go),
	// so the descendant walk follows them too — e.g. a service's long-lived
	// exec span cause-links to the API spans that installed the Service value
	// (Container.asService and friends), and the service's logs belong beneath
	// them. Unlike children, this index is fed from EVERY snapshot row of a
	// span, not just the first: links are added to live spans over time
	// (RunningService.addOriginSpanContexts), so later snapshots carry edges
	// the first row lacked.
	causalChildren map[string][]string
	// causalParents is causalChildren's reverse: linking span ID → linked
	// (target) span IDs — dagui's causesViaLinks direction. It seeds the
	// log-scope walk from the other end of a cause link: a service's exec
	// span cause-links the API spans that installed the Service value, and
	// the service's stdio log records are deliberately attributed to those
	// install spans (core/service.go routes them there), so reading logs
	// "beneath" the exec span must reach the install spans' rows too. Fed
	// from every snapshot row, like causalChildren.
	causalParents map[string][]string

	// Archive-facing indexes always include the trace ID. Archive reads are
	// trace-addressed and must not merge the same span ID from another trace.
	archiveLastRow map[spanLookupKey]int64
	archiveParent  map[spanLookupKey]spanLookupKey
	agentSpans     map[string]map[spanLookupKey]struct{}
}

func newSpanLookup() *spanLookup {
	return &spanLookup{
		firstRow:       make(map[spanLookupKey]int64),
		lastRow:        make(map[string]int64),
		parent:         make(map[string]string),
		checkSpans:     make(map[string]struct{}),
		testSpans:      make(map[string]struct{}),
		children:       make(map[string][]string),
		causalChildren: make(map[string][]string),
		causalParents:  make(map[string][]string),
		archiveLastRow: make(map[spanLookupKey]int64),
		archiveParent:  make(map[spanLookupKey]spanLookupKey),
		agentSpans:     make(map[string]map[spanLookupKey]struct{}),
	}
}

func (l *spanLookup) add(row Span) {
	targets := causalLinkTargets(row)
	l.mu.Lock()
	l.addLocked(row, targets)
	l.mu.Unlock()
}

func (l *spanLookup) addAll(rows []Span) {
	targets := make([][]string, len(rows))
	for i, row := range rows {
		targets[i] = causalLinkTargets(row)
	}
	l.mu.Lock()
	for i, row := range rows {
		l.addLocked(row, targets[i])
	}
	l.mu.Unlock()
}

func (l *spanLookup) addLocked(row Span, causalTargets []string) {
	archiveKey := spanLookupKey{traceID: row.TraceID, spanID: row.SpanID}
	l.archiveLastRow[archiveKey] = row.ID
	if row.ParentSpanID.Valid {
		if _, known := l.archiveParent[archiveKey]; !known {
			l.archiveParent[archiveKey] = spanLookupKey{traceID: row.TraceID, spanID: row.ParentSpanID.String}
		}
	}
	if isAgentIdentitySpan(row) {
		spans := l.agentSpans[row.TraceID]
		if spans == nil {
			spans = make(map[spanLookupKey]struct{})
			l.agentSpans[row.TraceID] = spans
		}
		spans[archiveKey] = struct{}{}
	}

	// Link edges are indexed before (and regardless of) the duplicate-snapshot
	// suppression below: a later snapshot of an already-seen span may carry
	// links its first row lacked.
	for _, target := range causalTargets {
		kids := l.causalChildren[target]
		if !slices.Contains(kids, row.SpanID) {
			l.causalChildren[target] = append(kids, row.SpanID)
		}
		parents := l.causalParents[row.SpanID]
		if !slices.Contains(parents, target) {
			l.causalParents[row.SpanID] = append(parents, target)
		}
	}
	// Likewise per-row, not per-span: the newest snapshot is the span's
	// current state, and check/test markers may only appear on a snapshot
	// taken after the attribute was set.
	l.lastRow[row.SpanID] = row.ID
	if _, marked := l.checkSpans[row.SpanID]; !marked && bytes.Contains(row.Attributes, checkNameAttrMarker) {
		l.checkSpans[row.SpanID] = struct{}{}
	}
	if _, marked := l.testSpans[row.SpanID]; !marked &&
		(bytes.Contains(row.Attributes, testCaseNameAttrMarker) || bytes.Contains(row.Attributes, testSuiteNameAttrMarker)) {
		l.testSpans[row.SpanID] = struct{}{}
	}
	if row.ParentSpanID.Valid {
		if _, known := l.parent[row.SpanID]; !known {
			l.parent[row.SpanID] = row.ParentSpanID.String
		}
	}
	key := spanLookupKey{traceID: row.TraceID, spanID: row.SpanID}
	if _, exists := l.firstRow[key]; exists {
		return
	}
	l.firstRow[key] = row.ID
	if row.ParentSpanID.Valid {
		children := l.children[row.ParentSpanID.String]
		// A repeated span ID in another trace is vanishingly rare, but the
		// children map follows the SQL query's span-ID-only identity and must
		// remain unique if it happens.
		for _, child := range children {
			if child == row.SpanID {
				return
			}
		}
		l.children[row.ParentSpanID.String] = append(children, row.SpanID)
	}
}

// Marker byte strings for the raw-attribute prefilter in addLocked. The
// attributes column is a protojson-encoded KeyValue list, so an attribute's
// presence implies its quoted key appears verbatim.
var (
	checkNameAttrMarker     = []byte(`"` + telemetry.CheckNameAttr + `"`)
	testCaseNameAttrMarker  = []byte(`"` + string(semconv.TestCaseNameKey) + `"`)
	testSuiteNameAttrMarker = []byte(`"` + string(semconv.TestSuiteNameKey) + `"`)
)

func isAgentIdentitySpan(row Span) bool {
	if row.TraceID == "" || row.SpanID == "" ||
		!bytes.Contains(row.Attributes, []byte(`"`+telemetryattrs.AgentAttr+`"`)) ||
		!bytes.Contains(row.Attributes, []byte(`"`+telemetryattrs.AgentIDAttr+`"`)) {
		return false
	}
	var attrs []*otlpcommonv1.KeyValue
	if err := UnmarshalProtoJSONs(row.Attributes, &otlpcommonv1.KeyValue{}, &attrs); err != nil {
		return false
	}
	var agent, agentID bool
	for _, attr := range attrs {
		switch attr.GetKey() {
		case telemetryattrs.AgentAttr:
			value, ok := attr.GetValue().GetValue().(*otlpcommonv1.AnyValue_BoolValue)
			agent = ok && value.BoolValue
		case telemetryattrs.AgentIDAttr:
			value, ok := attr.GetValue().GetValue().(*otlpcommonv1.AnyValue_StringValue)
			agentID = ok && value.StringValue != ""
		}
	}
	return agent && agentID
}

// causalLinkTargets decodes row's span links and returns the span IDs it
// cause-links to. Purpose handling mirrors dagui's link ingestion
// (dagql/dagui/db.go): links marked LinkPurposeCause — and links with no
// purpose, which imply causality — are parent→child edges; other purposes
// (error_origin, wait) are not.
func causalLinkTargets(row Span) []string {
	if len(row.Links) <= len("[]") {
		// no links: nil, or an empty JSON array
		return nil
	}
	var linksPB []*otlptracev1.Span_Link
	if err := UnmarshalProtoJSONs(row.Links, &otlptracev1.Span_Link{}, &linksPB); err != nil {
		slog.Warn("failed to unmarshal span links", "span", row.SpanID, "error", err)
		return nil
	}
	var targets []string
	for _, link := range telemetry.SpanLinksFromPB(linksPB) {
		purpose := ""
		for _, kv := range link.Attributes {
			if string(kv.Key) == telemetry.LinkPurposeAttr {
				purpose = kv.Value.AsString()
				break
			}
		}
		switch purpose {
		case telemetry.LinkPurposeCause, "":
		default:
			continue
		}
		if !link.SpanContext.HasSpanID() {
			continue
		}
		target := link.SpanContext.SpanID().String()
		if target == row.SpanID {
			// a self-link would add a pointless self-edge; skip it
			continue
		}
		if !slices.Contains(targets, target) {
			targets = append(targets, target)
		}
	}
	return targets
}

func (l *spanLookup) first(traceID, spanID string) (int64, bool) {
	l.mu.RLock()
	id, found := l.firstRow[spanLookupKey{traceID: traceID, spanID: spanID}]
	l.mu.RUnlock()
	return id, found
}

// causalChildrenOf returns the span IDs that cause-link to the given span.
func (l *spanLookup) causalChildrenOf(spanID string) []string {
	l.mu.RLock()
	kids := append([]string(nil), l.causalChildren[spanID]...)
	l.mu.RUnlock()
	return kids
}

// hasDescendants reports whether root has anything nested beneath it, over
// the downward edges logScope walks (child edges and cause-purpose link
// edges). It only ever inspects root's own direct edges -- a span with any
// child at all has a descendant -- so it is O(direct children) and never
// materializes the subtree. Note it deliberately ignores logScope's reverse
// seeds: a service exec span's install spans sit beside it, not beneath it,
// and don't make it "have descendants".
func (l *spanLookup) hasDescendants(root string) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	for _, kid := range l.children[root] {
		// A malformed self-parented row would otherwise report itself as its
		// own descendant.
		if kid != root {
			return true
		}
	}
	for _, kid := range l.causalChildren[root] {
		if kid != root {
			return true
		}
	}
	return false
}

// logScope returns every span whose log rows belong to a capture rooted at
// root: root itself, the spans root cause-links to (its containing spans in
// dagui's model — e.g. the install spans a service exec span links, which
// carry the service's stdio records; see causalParents), and everything
// reachable from those seeds via child edges and cause-purpose link edges —
// the same containment dagui renders, where a cause-linking span (e.g. a
// service's exec span) appears as a child of the span it links to (e.g. the
// Container.asService install span). Seeding from both ends of the root's
// cause links is what keeps a capture rooted at either handle — the exec
// span or an install span — returning the same log lines.
//
// The reverse (linking→linked) edges are followed from the root ONLY: a span
// merely reached during the walk (e.g. a lazy resume span beneath a tool
// call, which cause-links the install spans of the value it resumes) must
// not drag unrelated subtrees into the capture.
func (l *spanLookup) logScope(root string) map[string]struct{} {
	l.mu.RLock()
	seeds := append([]string{root}, l.causalParents[root]...)
	l.mu.RUnlock()

	scope := make(map[string]struct{}, len(seeds))
	queue := make([]string, 0, len(seeds))
	for _, seed := range seeds {
		if _, exists := scope[seed]; exists {
			continue
		}
		scope[seed] = struct{}{}
		queue = append(queue, seed)
	}
	for len(queue) > 0 {
		parent := queue[0]
		queue = queue[1:]

		l.mu.RLock()
		children := append([]string(nil), l.children[parent]...)
		children = append(children, l.causalChildren[parent]...)
		l.mu.RUnlock()

		for _, child := range children {
			if _, exists := scope[child]; exists {
				continue
			}
			scope[child] = struct{}{}
			queue = append(queue, child)
		}
	}
	return scope
}

// ancestorClosure returns ids plus every member's ancestor chain, walked over
// the parent index up to each trace root. Cycle-safe: a span already in the
// set ends its walk, so a (malformed) parent cycle cannot loop.
func (l *spanLookup) ancestorClosure(ids map[string]struct{}) map[string]struct{} {
	closure := make(map[string]struct{}, len(ids))
	l.mu.RLock()
	defer l.mu.RUnlock()
	for id := range ids {
		for id != "" {
			if _, seen := closure[id]; seen {
				break
			}
			closure[id] = struct{}{}
			id = l.parent[id]
		}
	}
	return closure
}

// latestRowIDs returns the newest snapshot row ID of every span in ids that
// the index knows, in ascending row order — append order, which is the order
// a sequential replay would have delivered them in.
func (l *spanLookup) latestRowIDs(ids map[string]struct{}) []int64 {
	rowIDs := make([]int64, 0, len(ids))
	l.mu.RLock()
	for id := range ids {
		if rowID, found := l.lastRow[id]; found {
			rowIDs = append(rowIDs, rowID)
		}
	}
	l.mu.RUnlock()
	sort.Slice(rowIDs, func(i, j int) bool { return rowIDs[i] < rowIDs[j] })
	return rowIDs
}

func (l *spanLookup) hasSpanForTrace(traceID, spanID string) bool {
	l.mu.RLock()
	_, found := l.archiveLastRow[spanLookupKey{traceID: traceID, spanID: spanID}]
	l.mu.RUnlock()
	return found
}

func (l *spanLookup) hasSpan(spanID string) bool {
	l.mu.RLock()
	_, found := l.lastRow[spanID]
	l.mu.RUnlock()
	return found
}

// markedSpanIDs snapshots the check- and test-marked span ID sets.
func (l *spanLookup) markedSpanIDs() (checks, tests map[string]struct{}) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	checks = make(map[string]struct{}, len(l.checkSpans))
	for id := range l.checkSpans {
		checks[id] = struct{}{}
	}
	tests = make(map[string]struct{}, len(l.testSpans))
	for id := range l.testSpans {
		tests[id] = struct{}{}
	}
	return checks, tests
}

func (l *spanLookup) archiveAgentSpanIDs(traceID string) []string {
	l.mu.RLock()
	spans := l.agentSpans[traceID]
	ids := make([]string, 0, len(spans))
	for key := range spans {
		ids = append(ids, key.spanID)
	}
	l.mu.RUnlock()
	sort.Strings(ids)
	return ids
}

func (l *spanLookup) archiveAncestorClosure(traceID string, ids map[string]struct{}) map[string]struct{} {
	closure := make(map[string]struct{}, len(ids))
	l.mu.RLock()
	defer l.mu.RUnlock()
	for id := range ids {
		key := spanLookupKey{traceID: traceID, spanID: id}
		for key.spanID != "" {
			if _, seen := closure[key.spanID]; seen {
				break
			}
			if _, exists := l.archiveLastRow[key]; !exists {
				break
			}
			closure[key.spanID] = struct{}{}
			key = l.archiveParent[key]
		}
	}
	return closure
}

func (l *spanLookup) archiveLatestRowIDs(traceID string, ids map[string]struct{}) []int64 {
	rowIDs := make([]int64, 0, len(ids))
	l.mu.RLock()
	for id := range ids {
		if rowID, found := l.archiveLastRow[spanLookupKey{traceID: traceID, spanID: id}]; found {
			rowIDs = append(rowIDs, rowID)
		}
	}
	l.mu.RUnlock()
	sort.Slice(rowIDs, func(i, j int) bool { return rowIDs[i] < rowIDs[j] })
	return rowIDs
}

// logLookup indexes log rows by the span they're attributed to, so scoped log
// reads (SelectLogsBeneathSpan, SelectLogsForSpans) resolve to direct row
// reads instead of scanning the whole log stream — a scan that is linear in
// SESSION size, paid per read, on paths that run per LLM tool call. The cost
// is 8 bytes per log row plus per-span slice overhead; rows are appended with
// monotonic IDs, so each span's slice is ascending by construction.
type callPayloadLookupKey struct {
	traceID string
	digest  string
}

type indexedCallPayload struct {
	key   callPayloadLookupKey
	rowID int64
	ok    bool
}

type logLookup struct {
	mu          sync.RWMutex
	rowsBySpan  map[string][]int64
	archiveRows map[spanLookupKey][]int64
	callPayload map[callPayloadLookupKey]int64
}

func newLogLookup() *logLookup {
	return &logLookup{
		rowsBySpan:  make(map[string][]int64),
		archiveRows: make(map[spanLookupKey][]int64),
		callPayload: make(map[callPayloadLookupKey]int64),
	}
}

func (l *logLookup) add(row Log) {
	payload := callPayloadIndexEntry(row)
	l.mu.Lock()
	l.addLocked(row, payload)
	l.mu.Unlock()
}

func (l *logLookup) addAll(rows []Log) {
	payloads := make([]indexedCallPayload, len(rows))
	for i, row := range rows {
		payloads[i] = callPayloadIndexEntry(row)
	}
	l.mu.Lock()
	for i, row := range rows {
		l.addLocked(row, payloads[i])
	}
	l.mu.Unlock()
}

func (l *logLookup) addLocked(row Log, payload indexedCallPayload) {
	if row.SpanID.Valid {
		l.rowsBySpan[row.SpanID.String] = append(l.rowsBySpan[row.SpanID.String], row.ID)
		if row.TraceID.Valid {
			key := spanLookupKey{traceID: row.TraceID.String, spanID: row.SpanID.String}
			l.archiveRows[key] = append(l.archiveRows[key], row.ID)
		}
	}
	if payload.ok {
		if _, exists := l.callPayload[payload.key]; !exists {
			l.callPayload[payload.key] = payload.rowID
		}
	}
}

func callPayloadIndexEntry(row Log) indexedCallPayload {
	if !row.TraceID.Valid || row.TraceID.String == "" || row.ID <= 0 {
		return indexedCallPayload{}
	}
	var attrs []*otlpcommonv1.KeyValue
	if err := UnmarshalProtoJSONs(row.Attributes, &otlpcommonv1.KeyValue{}, &attrs); err != nil {
		return indexedCallPayload{}
	}
	claimed := false
	for _, attr := range attrs {
		if attr.GetKey() != telemetry.ContentTypeAttr {
			continue
		}
		claimed = attr.GetValue().GetStringValue() == telemetryattrs.CallPayloadContentType
		break
	}
	if !claimed {
		return indexedCallPayload{}
	}
	var body otlpcommonv1.AnyValue
	if err := proto.Unmarshal(row.Body, &body); err != nil {
		return indexedCallPayload{}
	}
	payload, ok := body.GetValue().(*otlpcommonv1.AnyValue_BytesValue)
	if !ok {
		return indexedCallPayload{}
	}
	decoded := new(callpbv1.Call)
	if err := proto.Unmarshal(payload.BytesValue, decoded); err != nil || decoded.GetDigest() == "" {
		return indexedCallPayload{}
	}
	return indexedCallPayload{
		key: callPayloadLookupKey{
			traceID: row.TraceID.String,
			digest:  decoded.GetDigest(),
		},
		rowID: row.ID,
		ok:    true,
	}
}

func (l *logLookup) archiveRowIDs(traceID string, ids map[string]struct{}, perSpanTail int) []int64 {
	var rowIDs []int64
	l.mu.RLock()
	for id := range ids {
		rows := l.archiveRows[spanLookupKey{traceID: traceID, spanID: id}]
		if perSpanTail > 0 && len(rows) > perSpanTail {
			rows = rows[len(rows)-perSpanTail:]
		}
		rowIDs = append(rowIDs, rows...)
	}
	l.mu.RUnlock()
	sort.Slice(rowIDs, func(i, j int) bool { return rowIDs[i] < rowIDs[j] })
	return rowIDs
}

func (l *logLookup) callPayloadRow(traceID, digest string) (int64, bool) {
	l.mu.RLock()
	rowID, found := l.callPayload[callPayloadLookupKey{traceID: traceID, digest: digest}]
	l.mu.RUnlock()
	return rowID, found
}

// rowIDsForSpans returns the row IDs of every log row attributed to a span in
// ids, ascending. perSpanTail > 0 keeps only each span's newest perSpanTail
// rows — the shape report renderers need, which bound every span's rendered
// log output to a tail anyway — so one pathological span (e.g. a service that
// streamed millions of lines) cannot balloon a scoped load.
func (l *logLookup) rowIDsForSpans(ids map[string]struct{}, perSpanTail int) []int64 {
	var rowIDs []int64
	l.mu.RLock()
	for id := range ids {
		rows := l.rowsBySpan[id]
		if perSpanTail > 0 && len(rows) > perSpanTail {
			rows = rows[len(rows)-perSpanTail:]
		}
		rowIDs = append(rowIDs, rows...)
	}
	l.mu.RUnlock()
	sort.Slice(rowIDs, func(i, j int) bool { return rowIDs[i] < rowIDs[j] })
	return rowIDs
}

// DB is one client's standalone append-only telemetry store.
type DB struct {
	spans   *logStream[Span]
	logs    *logStream[Log]
	metrics *logStream[Metric]
	lookup  *spanLookup
	logIdx  *logLookup

	clientID string
	refCount int
	closeFn  func() error
}

func openStore(ctx context.Context, root, clientID string, tailBudget int64) (_ *DB, rerr error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", root, err)
	}

	store := &DB{
		lookup:   newSpanLookup(),
		logIdx:   newLogLookup(),
		clientID: clientID,
	}
	defer func() {
		if rerr != nil {
			rerr = errors.Join(rerr, store.closeStreams())
		}
	}()

	var err error
	store.spans, err = openLogStream(
		ctx,
		filepath.Join(root, clientID+".spans.log"),
		spanCodec,
		tailBudget,
		store.lookup.add,
		store.lookup.addAll,
	)
	if err != nil {
		return nil, fmt.Errorf("open span stream: %w", err)
	}
	store.logs, err = openLogStream(
		ctx,
		filepath.Join(root, clientID+".logs.log"),
		logCodec,
		tailBudget,
		store.logIdx.add,
		store.logIdx.addAll,
	)
	if err != nil {
		return nil, fmt.Errorf("open log stream: %w", err)
	}
	store.metrics, err = openLogStream(
		ctx,
		filepath.Join(root, clientID+".metrics.log"),
		metricCodec,
		tailBudget,
		nil,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("open metric stream: %w", err)
	}
	store.closeFn = store.closeStreams
	return store, nil
}

func (s *DB) AppendSpans(rows []Span) (AppendStats, error) {
	stats, err := s.spans.append(rows)
	if err != nil {
		return stats, fmt.Errorf("append spans: %w", err)
	}
	return stats, nil
}

func (s *DB) AppendLogs(rows []Log) (AppendStats, error) {
	stats, err := s.logs.append(rows)
	if err != nil {
		return stats, fmt.Errorf("append logs: %w", err)
	}
	return stats, nil
}

func (s *DB) AppendMetrics(rows []Metric) (AppendStats, error) {
	stats, err := s.metrics.append(rows)
	if err != nil {
		return stats, fmt.Errorf("append metrics: %w", err)
	}
	return stats, nil
}

// Read mirrors the current DB handle seam. The append-only store needs no
// separate read pool, so selectors are bound to the same immutable streams.
func (s *DB) Read() *DB {
	return s
}

func (s *DB) Close() error {
	if s == nil {
		return nil
	}
	return s.closeFn()
}

func (s *DB) HighWater() HighWater {
	s.spans.mu.Lock()
	s.logs.mu.Lock()
	s.metrics.mu.Lock()
	highWater := HighWater{
		Spans:   s.spans.nextID - 1,
		Logs:    s.logs.nextID - 1,
		Metrics: s.metrics.nextID - 1,
	}
	s.metrics.mu.Unlock()
	s.logs.mu.Unlock()
	s.spans.mu.Unlock()
	return highWater
}

// Checkpoint makes the current fixed stream cut durable. It first snapshots
// all three high-water cursors, then forces every in-memory tail to its spill
// file and fsyncs each file. Rows appended concurrently after the snapshot may
// also become durable, but are outside the returned cut.
func (s *DB) Checkpoint(ctx context.Context) (HighWater, error) {
	highWater := s.HighWater()
	type checkpointResult struct {
		stream string
		err    error
	}
	results := make(chan checkpointResult, 3)
	for stream, checkpoint := range map[string]func(context.Context) error{
		"spans":   s.spans.checkpoint,
		"logs":    s.logs.checkpoint,
		"metrics": s.metrics.checkpoint,
	} {
		go func() {
			results <- checkpointResult{stream: stream, err: checkpoint(ctx)}
		}()
	}
	var result error
	for range 3 {
		checkpoint := <-results
		if checkpoint.err != nil {
			result = errors.Join(result, fmt.Errorf("checkpoint %s: %w", checkpoint.stream, checkpoint.err))
		}
	}
	return highWater, result
}

// SizeBytes returns the physical size of the three telemetry streams. Archive
// finalization calls this after Checkpoint, when every row in its fixed cut has
// been flushed to these files.
func (s *DB) SizeBytes() (int64, error) {
	streams := []struct {
		name string
		stat func() (os.FileInfo, error)
	}{
		{name: "spans", stat: s.spans.spill.file.Stat},
		{name: "logs", stat: s.logs.spill.file.Stat},
		{name: "metrics", stat: s.metrics.spill.file.Stat},
	}
	var size int64
	for _, stream := range streams {
		info, err := stream.stat()
		if err != nil {
			return 0, fmt.Errorf("stat %s telemetry stream: %w", stream.name, err)
		}
		size += info.Size()
	}
	return size, nil
}

// ValidateTraceCut verifies the trace-addressed signals in a fixed stream cut.
// Metrics have no per-row trace identity; their request trace is validated at
// ingestion by the session exporter.
func (s *DB) ValidateTraceCut(ctx context.Context, traceID string, cut HighWater) error {
	const batchSize = int64(1024)
	for cursor := int64(0); cursor < cut.Spans; {
		rows, err := s.SelectSpansRange(ctx, SelectSpansRangeParams{
			AfterID: cursor, ThroughID: cut.Spans, Limit: batchSize,
		})
		if err != nil {
			return fmt.Errorf("read spans: %w", err)
		}
		if len(rows) == 0 {
			return fmt.Errorf("span stream ended at %d before high-water %d", cursor, cut.Spans)
		}
		for _, row := range rows {
			cursor = row.ID
			if row.TraceID != traceID {
				return fmt.Errorf("span row %d has trace %q, want %q", row.ID, row.TraceID, traceID)
			}
		}
	}
	for cursor := int64(0); cursor < cut.Logs; {
		rows, err := s.SelectLogsRange(ctx, SelectLogsRangeParams{
			AfterID: cursor, ThroughID: cut.Logs, Limit: batchSize,
		})
		if err != nil {
			return fmt.Errorf("read logs: %w", err)
		}
		if len(rows) == 0 {
			return fmt.Errorf("log stream ended at %d before high-water %d", cursor, cut.Logs)
		}
		for _, row := range rows {
			cursor = row.ID
			if !row.TraceID.Valid || row.TraceID.String != traceID {
				return fmt.Errorf("log row %d has trace %q (valid %t), want %q", row.ID, row.TraceID.String, row.TraceID.Valid, traceID)
			}
		}
	}
	return nil
}

func (s *DB) SelectSpansSince(ctx context.Context, arg SelectSpansSinceParams) ([]Span, error) {
	return s.spans.Since(ctx, arg.ID, storeLimit(arg.Limit))
}

func (s *DB) SelectLogsSince(ctx context.Context, arg SelectLogsSinceParams) ([]Log, error) {
	return s.logs.Since(ctx, arg.ID, storeLimit(arg.Limit))
}

func (s *DB) SelectMetricsSince(ctx context.Context, arg SelectMetricsSinceParams) ([]Metric, error) {
	return s.metrics.Since(ctx, arg.ID, storeLimit(arg.Limit))
}

func (s *DB) SelectSpansRange(ctx context.Context, arg SelectSpansRangeParams) ([]Span, error) {
	return s.spans.Range(ctx, arg.AfterID, arg.ThroughID, storeLimit(arg.Limit))
}

func (s *DB) SelectLogsRange(ctx context.Context, arg SelectLogsRangeParams) ([]Log, error) {
	return s.logs.Range(ctx, arg.AfterID, arg.ThroughID, storeLimit(arg.Limit))
}

func (s *DB) SelectMetricsRange(ctx context.Context, arg SelectMetricsRangeParams) ([]Metric, error) {
	return s.metrics.Range(ctx, arg.AfterID, arg.ThroughID, storeLimit(arg.Limit))
}

func (s *DB) SelectSpan(ctx context.Context, arg SelectSpanParams) (Span, error) {
	id, found := s.lookup.first(arg.TraceID, arg.SpanID)
	if !found {
		return Span{}, sql.ErrNoRows
	}
	row, found, err := s.spans.readID(ctx, id)
	if err != nil {
		return Span{}, err
	}
	if !found {
		return Span{}, fmt.Errorf("indexed span row %d: %w", id, sql.ErrNoRows)
	}
	return row, nil
}

// CausalChildren returns the span IDs that cause-link to the given span —
// e.g. a service's long-lived exec span cause-links to the API spans that
// installed the Service value (Container.asService and friends).
func (s *DB) CausalChildren(spanID string) []string {
	return s.lookup.causalChildrenOf(spanID)
}

// SpanLogScope returns the set of span IDs whose log rows belong to a capture
// rooted at spanID — the same walk SelectLogsBeneathSpan scopes its logs to:
// the span itself, the spans it cause-links to (e.g. the install spans a
// service exec span links, which carry the service's stdio records), and
// everything beneath either over child edges and cause-purpose link edges.
// The returned map is a fresh snapshot the caller owns; the set only ever
// grows as spans arrive.
func (s *DB) SpanLogScope(spanID string) map[string]struct{} {
	return s.lookup.logScope(spanID)
}

// HasDescendants reports whether any span is nested beneath spanID, following
// the same edges as the log queries: parent→child plus cause-purpose links.
//
// It answers purely from the in-memory span index -- no stream reads, no
// subtree materialization -- so it is cheap enough to use as a pre-filter
// (e.g. "did this tool call produce any child telemetry worth rendering?").
func (s *DB) HasDescendants(spanID string) bool {
	return s.lookup.hasDescendants(spanID)
}

// HasSpanForTrace reports whether the store has seen any snapshot of spanID in
// traceID. Archive callers use the composite identity so a span ID collision in
// another trace cannot hide a missing archive parent.
func (s *DB) HasSpanForTrace(traceID, spanID string) bool {
	return s.lookup.hasSpanForTrace(traceID, spanID)
}

// HasSpan reports whether the store has seen any snapshot of spanID.
func (s *DB) HasSpan(spanID string) bool {
	return s.lookup.hasSpan(spanID)
}

// AncestorClosure returns ids plus every member's ancestor chain up to its
// trace root. A scoped span load includes it so that no loaded span's parent
// pointer resolves to an unreceived placeholder — dagui would otherwise
// mistake a placeholder for a root and lose the chain's UI flags.
func (s *DB) AncestorClosure(ids map[string]struct{}) map[string]struct{} {
	return s.lookup.ancestorClosure(ids)
}

// CheckTestSpanIDs returns the spans marked as named checks and as test cases
// or suites, answered from the span index. This is the seed for resolving a
// check/test NAME to a span: load these spans (plus ancestors, for the test
// view's containment walks) into a throwaway dagui.DB and match names there,
// instead of retaining a whole-session DB just to answer name lookups.
func (s *DB) CheckTestSpanIDs() (checks, tests map[string]struct{}) {
	return s.lookup.markedSpanIDs()
}

// AgentIdentitySpanIDs returns the valid agent identity span IDs in traceID.
// IDs are sorted for deterministic archive bootstrap construction.
func (s *DB) AgentIdentitySpanIDs(traceID string) []string {
	return s.lookup.archiveAgentSpanIDs(traceID)
}

// AncestorClosureForTrace is the trace-scoped archive form of
// AncestorClosure. Every index lookup uses the (trace ID, span ID) pair.
func (s *DB) AncestorClosureForTrace(traceID string, ids map[string]struct{}) map[string]struct{} {
	return s.lookup.archiveAncestorClosure(traceID, ids)
}

// SelectSpansLatestForTrace returns the newest cumulative snapshot for each
// requested span in one trace, ordered by append row ID.
func (s *DB) SelectSpansLatestForTrace(ctx context.Context, traceID string, ids map[string]struct{}) ([]Span, error) {
	return s.readSpanRows(ctx, s.lookup.archiveLatestRowIDs(traceID, ids))
}

// SelectLogsForTraceSpans returns only logs whose trace and span IDs both
// match the archive scope.
func (s *DB) SelectLogsForTraceSpans(ctx context.Context, traceID string, ids map[string]struct{}, perSpanTail int) ([]Log, error) {
	return s.readLogRows(ctx, s.logIdx.archiveRowIDs(traceID, ids, perSpanTail))
}

// SelectCallPayload returns the first valid canonical call-payload record for
// digest in traceID. Payload identity is computed from the encoded call body;
// it is never trusted from record metadata.
func (s *DB) SelectCallPayload(ctx context.Context, traceID, digest string) (Log, error) {
	rowID, found := s.logIdx.callPayloadRow(traceID, digest)
	if !found {
		return Log{}, sql.ErrNoRows
	}
	row, found, err := s.logs.readID(ctx, rowID)
	if err != nil {
		return Log{}, fmt.Errorf("read call payload log row %d: %w", rowID, err)
	}
	if !found {
		return Log{}, fmt.Errorf("indexed call payload log row %d: %w", rowID, sql.ErrNoRows)
	}
	return row, nil
}

// SelectSpansLatest returns the newest snapshot row of every span in ids, in
// append order. A span's snapshots are cumulative, so its newest row alone
// reconstructs the state a full sequential replay would end with — this is
// the span half of a scoped load, sized by the scope instead of the session.
func (s *DB) SelectSpansLatest(ctx context.Context, ids map[string]struct{}) ([]Span, error) {
	return s.readSpanRows(ctx, s.lookup.latestRowIDs(ids))
}

func (s *DB) readSpanRows(ctx context.Context, rowIDs []int64) ([]Span, error) {
	rows := make([]Span, 0, len(rowIDs))
	for _, rowID := range rowIDs {
		row, found, err := s.spans.readID(ctx, rowID)
		if err != nil {
			return nil, fmt.Errorf("read span row %d: %w", rowID, err)
		}
		if !found {
			return nil, fmt.Errorf("indexed span row %d: %w", rowID, sql.ErrNoRows)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// SelectLogsForSpans returns the log rows attributed to any span in ids, in
// append order — the log half of a scoped load. perSpanTail > 0 bounds each
// span to its newest perSpanTail rows; renderers bound every span's log
// output to a tail anyway, and the cap keeps one pathological span (e.g. a
// service that streamed millions of lines) from ballooning the load.
func (s *DB) SelectLogsForSpans(ctx context.Context, ids map[string]struct{}, perSpanTail int) ([]Log, error) {
	return s.readLogRows(ctx, s.logIdx.rowIDsForSpans(ids, perSpanTail))
}

func (s *DB) readLogRows(ctx context.Context, rowIDs []int64) ([]Log, error) {
	rows := make([]Log, 0, len(rowIDs))
	for _, rowID := range rowIDs {
		row, found, err := s.logs.readID(ctx, rowID)
		if err != nil {
			return nil, fmt.Errorf("read log row %d: %w", rowID, err)
		}
		if !found {
			return nil, fmt.Errorf("indexed log row %d: %w", rowID, sql.ErrNoRows)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// SelectLogsBeneathSpan returns the log rows of the capture rooted at
// arg.SpanID — the rows attributed to any span in its log scope (the span
// itself, its cause-link targets, and both sets' subtrees; see SpanLogScope)
// — in append order, starting after cursor arg.ID, up to arg.Limit rows. The
// root's own rows are part of the capture: a span's directly-attributed
// output (e.g. the service stdio records routed to an install span) is
// exactly what a reader asking about that span wants.
//
// Resolved through the per-span log index: the row IDs come straight from
// the scope's index entries, so the cost scales with the capture, not with
// the session — the previous implementation scanned the whole log stream
// past the cursor, linear in session size, on a path that runs per LLM tool
// call.
func (s *DB) SelectLogsBeneathSpan(ctx context.Context, arg SelectLogsBeneathSpanParams) ([]Log, error) {
	limit := storeLimit(arg.Limit)
	if limit == 0 || !arg.SpanID.Valid {
		return nil, nil
	}
	scope := s.lookup.logScope(arg.SpanID.String)
	rowIDs := s.logIdx.rowIDsForSpans(scope, 0)
	// Skip rows at or before the cursor; rowIDs is ascending.
	start := sort.Search(len(rowIDs), func(i int) bool { return rowIDs[i] > arg.ID })
	rowIDs = rowIDs[start:]
	if len(rowIDs) > limit {
		rowIDs = rowIDs[:limit]
	}
	logs := make([]Log, 0, len(rowIDs))
	for _, rowID := range rowIDs {
		row, found, err := s.logs.readID(ctx, rowID)
		if err != nil {
			return nil, fmt.Errorf("read log row %d: %w", rowID, err)
		}
		if !found {
			return nil, fmt.Errorf("indexed log row %d: %w", rowID, sql.ErrNoRows)
		}
		logs = append(logs, row)
	}
	return logs, nil
}

func (s *DB) closeStreams() error {
	streams := []func() error{}
	if s.spans != nil {
		streams = append(streams, s.spans.close)
	}
	if s.logs != nil {
		streams = append(streams, s.logs.close)
	}
	if s.metrics != nil {
		streams = append(streams, s.metrics.close)
	}

	errs := make(chan error, len(streams))
	var group sync.WaitGroup
	for _, closeStream := range streams {
		group.Go(func() {
			errs <- closeStream()
		})
	}
	group.Wait()
	close(errs)
	var result error
	for err := range errs {
		result = errors.Join(result, err)
	}
	return result
}

func storeLimit(limit int64) int {
	if limit <= 0 {
		return 0
	}
	if uint64(limit) > uint64(maxInt) {
		return maxInt
	}
	return int(limit)
}
