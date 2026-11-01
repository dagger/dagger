package clientdb

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
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
	otlptracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
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
	// alone reconstructs the state that sequential processing of every snapshot
	// would end with. This is what lets a scoped reader materialize a span
	// subtree without reprocessing the whole stream. Keyed by span ID only,
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
	// attributes — no decode — so check/test span selection
	// answers from the index instead of scanning the stream. A false
	// positive (marker bytes inside a value) merely loads one extra span.
	checkSpans map[string]struct{}
	testSpans  map[string]struct{}
	// Duplicate snapshots are suppressed when firstRow is populated, leaving
	// one copy of each child span ID in its parent's slice.
	children map[string][]string
	// The cause-purpose span links index both ways. dagui treats a cause
	// link as a parent→child edge (the linking span joins the linked span's
	// ChildSpans; see dagql/dagui/db.go), so the descendant walk follows them
	// from linked (target) span to linking span — e.g. a service's
	// long-lived exec span cause-links to the API spans that installed the
	// Service value (Container.asService and friends), and the service's
	// logs belong beneath them. The log-scope walk also seeds from the other
	// end, linking span to target: the service's stdio log records are
	// deliberately attributed to those install spans (core/service.go routes
	// them there), so reading logs "beneath" the exec span must reach the
	// install spans' rows too.
	//
	// Unlike children, links are fed from EVERY snapshot row of a span, not
	// just the first: links are added to live spans over time
	// (RunningService.addOriginSpanContexts), so later snapshots carry edges
	// the first row lacked.
	//
	// Many spans carry the same set of cause links — every lazy evaluation
	// of a result links the API spans that installed it — so each distinct
	// set is stored once, with the spans that carry it, over interned span
	// IDs. The index grows with the spans and the distinct sets, not with
	// the links: thousands of spans sharing hundreds of links cost thousands
	// of entries, not millions.
	causalIDs  map[string]uint32
	causalName []string
	linkSets   []causalLinkSet
	linkSetIDs map[string]uint32
	// spanLinkSet maps a linking span to the set it carries.
	spanLinkSet map[uint32]uint32
	// targetLinkSets maps a linked span to the sets that contain it.
	targetLinkSets map[uint32][]uint32
	// freeLinkSets are the slots of sets emptied by spans whose links grew,
	// for reuse.
	freeLinkSets []uint32
}

// causalLinkSet is one distinct set of cause-link targets and the spans
// whose links are exactly that set.
type causalLinkSet struct {
	targets []uint32 // sorted
	members []uint32
}

func newSpanLookup() *spanLookup {
	return &spanLookup{
		firstRow:       make(map[spanLookupKey]int64),
		lastRow:        make(map[string]int64),
		parent:         make(map[string]string),
		checkSpans:     make(map[string]struct{}),
		testSpans:      make(map[string]struct{}),
		children:       make(map[string][]string),
		causalIDs:      make(map[string]uint32),
		linkSetIDs:     make(map[string]uint32),
		spanLinkSet:    make(map[uint32]uint32),
		targetLinkSets: make(map[uint32][]uint32),
	}
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
	// Link edges are indexed before (and regardless of) the duplicate-snapshot
	// suppression below: a later snapshot of an already-seen span may carry
	// links its first row lacked.
	l.addCauseLinksLocked(row.SpanID, causalTargets)
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
	var parentKnown bool
	if row.ParentSpanID.Valid {
		_, parentKnown = l.parent[row.SpanID]
		if !parentKnown {
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
		// New span IDs cannot already be in a child list. Only a repeated
		// ID in another trace needs the span-ID-only uniqueness check.
		if parentKnown && slices.Contains(children, row.SpanID) {
			return
		}
		l.children[row.ParentSpanID.String] = append(children, row.SpanID)
	}
}

// addCauseLinksLocked adds targets to the set of spans spanID cause-links.
// A span keeps one set: links that grow it move the span to the larger set.
func (l *spanLookup) addCauseLinksLocked(spanID string, targets []string) {
	if len(targets) == 0 {
		return
	}
	span := l.causalIDLocked(spanID)
	old, linked := l.spanLinkSet[span]
	var current []uint32
	if linked {
		current = l.linkSets[old].targets
	}
	next := current
	for _, target := range targets {
		id := l.causalIDLocked(target)
		i, found := slices.BinarySearch(next, id)
		if found {
			continue
		}
		if len(next) == len(current) {
			next = slices.Clone(current)
		}
		next = slices.Insert(next, i, id)
	}
	if linked && len(next) == len(current) {
		return
	}
	if linked {
		members := l.linkSets[old].members
		i := slices.Index(members, span)
		l.linkSets[old].members = slices.Delete(members, i, i+1)
		if len(l.linkSets[old].members) == 0 {
			l.dropLinkSetLocked(old)
		}
	}
	set := l.linkSetLocked(next)
	l.linkSets[set].members = append(l.linkSets[set].members, span)
	l.spanLinkSet[span] = set
}

func (l *spanLookup) causalIDLocked(spanID string) uint32 {
	if id, ok := l.causalIDs[spanID]; ok {
		return id
	}
	id := uint32(len(l.causalName))
	l.causalIDs[spanID] = id
	l.causalName = append(l.causalName, spanID)
	return id
}

// linkSetLocked returns the set with exactly targets (sorted), adding it.
func (l *spanLookup) linkSetLocked(targets []uint32) uint32 {
	key := linkSetKey(targets)
	if set, ok := l.linkSetIDs[key]; ok {
		return set
	}
	var set uint32
	if n := len(l.freeLinkSets); n > 0 {
		set = l.freeLinkSets[n-1]
		l.freeLinkSets = l.freeLinkSets[:n-1]
		l.linkSets[set] = causalLinkSet{targets: targets}
	} else {
		set = uint32(len(l.linkSets))
		l.linkSets = append(l.linkSets, causalLinkSet{targets: targets})
	}
	l.linkSetIDs[key] = set
	for _, target := range targets {
		l.targetLinkSets[target] = append(l.targetLinkSets[target], set)
	}
	return set
}

// dropLinkSetLocked removes a set no span carries any more, so that a span
// whose links grow leaves nothing behind.
func (l *spanLookup) dropLinkSetLocked(set uint32) {
	targets := l.linkSets[set].targets
	delete(l.linkSetIDs, linkSetKey(targets))
	for _, target := range targets {
		sets := l.targetLinkSets[target]
		i := slices.Index(sets, set)
		if sets = slices.Delete(sets, i, i+1); len(sets) == 0 {
			delete(l.targetLinkSets, target)
		} else {
			l.targetLinkSets[target] = sets
		}
	}
	l.linkSets[set] = causalLinkSet{}
	l.freeLinkSets = append(l.freeLinkSets, set)
}

func linkSetKey(targets []uint32) string {
	key := make([]byte, 0, 4*len(targets))
	for _, id := range targets {
		key = binary.LittleEndian.AppendUint32(key, id)
	}
	return string(key)
}

// causalChildrenLocked calls f with each span that cause-links to spanID.
func (l *spanLookup) causalChildrenLocked(spanID string, f func(string) bool) {
	target, ok := l.causalIDs[spanID]
	if !ok {
		return
	}
	for _, set := range l.targetLinkSets[target] {
		for _, member := range l.linkSets[set].members {
			if !f(l.causalName[member]) {
				return
			}
		}
	}
}

// causalParentsLocked returns the spans spanID cause-links to.
func (l *spanLookup) causalParentsLocked(spanID string) []string {
	span, ok := l.causalIDs[spanID]
	if !ok {
		return nil
	}
	set, ok := l.spanLinkSet[span]
	if !ok {
		return nil
	}
	targets := l.linkSets[set].targets
	parents := make([]string, len(targets))
	for i, target := range targets {
		parents[i] = l.causalName[target]
	}
	return parents
}

// Marker byte strings for the raw-attribute prefilter in addLocked. The
// attributes column is a binary KeyValue list (MarshalProtoList), so an
// attribute's presence implies its key bytes appear verbatim.
var (
	checkNameAttrMarker     = []byte(telemetry.CheckNameAttr)
	testCaseNameAttrMarker  = []byte(string(semconv.TestCaseNameKey))
	testSuiteNameAttrMarker = []byte(string(semconv.TestSuiteNameKey))
)

// causalLinkTargets decodes row's span links and returns the span IDs it
// cause-links to. Purpose handling mirrors dagui's link ingestion
// (dagql/dagui/db.go): links marked LinkPurposeCause — and links with no
// purpose, which imply causality — are parent→child edges; other purposes
// (error_origin, wait) are not.
func causalLinkTargets(row Span) []string {
	if len(row.Links) == 0 {
		return nil
	}
	var linksPB []*otlptracev1.Span_Link
	if err := UnmarshalProtoList(row.Links, &otlptracev1.Span_Link{}, &linksPB); err != nil {
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

// causalChildrenOf returns the span IDs that cause-link to the given span, in
// no particular order.
func (l *spanLookup) causalChildrenOf(spanID string) []string {
	var kids []string
	l.mu.RLock()
	l.causalChildrenLocked(spanID, func(kid string) bool {
		kids = append(kids, kid)
		return true
	})
	l.mu.RUnlock()
	return kids
}

// causalParentsOf returns the span IDs the given span cause-links to.
func (l *spanLookup) causalParentsOf(spanID string) []string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.causalParentsLocked(spanID)
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
	descendant := false
	l.causalChildrenLocked(root, func(kid string) bool {
		descendant = kid != root
		return !descendant
	})
	return descendant
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
	seeds := append([]string{root}, l.causalParentsLocked(root)...)
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
		l.causalChildrenLocked(parent, func(kid string) bool {
			children = append(children, kid)
			return true
		})
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

// parentSubtree returns root plus every span beneath it over parent→child
// edges alone -- no cause links, unlike logScope. Cycle-safe.
func (l *spanLookup) parentSubtree(root string) map[string]struct{} {
	l.mu.RLock()
	defer l.mu.RUnlock()
	subtree := map[string]struct{}{root: {}}
	queue := []string{root}
	for len(queue) > 0 {
		parent := queue[0]
		queue = queue[1:]
		for _, child := range l.children[parent] {
			if _, seen := subtree[child]; seen {
				continue
			}
			subtree[child] = struct{}{}
			queue = append(queue, child)
		}
	}
	return subtree
}

// latestRowIDs returns the newest snapshot row ID of every span in ids that
// the index knows, in ascending row order — append order, which is the order
// sequential processing would have delivered them in.
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

func (l *spanLookup) hasSpan(spanID string) bool {
	l.mu.RLock()
	_, found := l.lastRow[spanID]
	l.mu.RUnlock()
	return found
}

// beneath reports whether spanID is in scope or reachable from it upward,
// over the reverse of logScope's downward edges: the parent edge and the
// span's own cause links. Cycle-safe.
func (l *spanLookup) beneath(spanID string, scope map[string]bool) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	seen := map[string]bool{}
	queue := []string{spanID}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if scope[id] {
			return true
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		if parent, ok := l.parent[id]; ok {
			queue = append(queue, parent)
		}
		queue = append(queue, l.causalParentsLocked(id)...)
	}
	return false
}

// allSpanIDs snapshots every span ID the index has seen a snapshot of.
func (l *spanLookup) allSpanIDs() map[string]struct{} {
	l.mu.RLock()
	defer l.mu.RUnlock()
	ids := make(map[string]struct{}, len(l.lastRow))
	for id := range l.lastRow {
		ids[id] = struct{}{}
	}
	return ids
}

// directChildren returns the spans one edge beneath spanID over the same
// downward edges logScope walks: child edges and cause-purpose link edges.
func (l *spanLookup) directChildren(spanID string) map[string]struct{} {
	l.mu.RLock()
	defer l.mu.RUnlock()
	kids := make(map[string]struct{}, len(l.children[spanID]))
	for _, kid := range l.children[spanID] {
		if kid != spanID {
			kids[kid] = struct{}{}
		}
	}
	l.causalChildrenLocked(spanID, func(kid string) bool {
		if kid != spanID {
			kids[kid] = struct{}{}
		}
		return true
	})
	return kids
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

// logLookup indexes log rows by the span they're attributed to, so scoped log
// reads (SelectLogsBeneathSpan, SelectLogsForSpans) resolve to direct row
// reads instead of scanning the whole log stream — a scan that is linear in
// SESSION size, paid per read, on paths that run per LLM tool call. The cost
// is 8 bytes per log row plus per-span slice overhead; rows are appended with
// monotonic IDs, so each span's slice is ascending by construction.
type logLookup struct {
	mu         sync.RWMutex
	rowsBySpan map[string][]int64
	// meta holds one logMeta per log row, indexed by row ID - 1, for
	// selected reads (see selection.go). Its trace and span fields index the
	// intern tables below.
	meta       []logMeta
	metaErrs   map[int64]error
	traceIDs   []string
	traceIndex map[string]uint32
	spanIDs    []string
	spanIndex  map[string]uint32
	// digests interns logMeta.digest; index 0 is the empty digest.
	digests     []string
	digestIndex map[string]uint32

	// memoMu guards attrMemo, which caches attribute classes by their bytes.
	memoMu   sync.Mutex
	attrMemo map[string]logAttrClassResult
}

func newLogLookup() *logLookup {
	return &logLookup{
		rowsBySpan:  make(map[string][]int64),
		metaErrs:    make(map[int64]error),
		traceIndex:  make(map[string]uint32),
		spanIndex:   make(map[string]uint32),
		digests:     []string{""},
		digestIndex: map[string]uint32{"": 0},
		attrMemo:    make(map[string]logAttrClassResult),
	}
}

func (l *logLookup) addAll(rows []Log) {
	flags, digests, errs := l.classify(rows)
	l.mu.Lock()
	for i, row := range rows {
		if errs != nil && errs[i] != nil {
			l.metaErrs[int64(len(l.meta))] = errs[i]
		}
		l.meta = append(l.meta, logMeta{
			timestamp: row.Timestamp,
			size:      logCodec.size(row),
			trace:     internLocked(l.traceIndex, &l.traceIDs, row.TraceID.String),
			span:      internLocked(l.spanIndex, &l.spanIDs, row.SpanID.String),
			digest:    internLocked(l.digestIndex, &l.digests, digests[i]),
			flags:     flags[i],
		})
	}
	for _, row := range rows {
		if !row.SpanID.Valid {
			continue
		}
		l.rowsBySpan[row.SpanID.String] = append(l.rowsBySpan[row.SpanID.String], row.ID)
	}
	l.mu.Unlock()
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
	imports    importedTraces
	spans      *logStream[Span]
	logs       *logStream[Log]
	metrics    *logStream[Metric]
	lookup     *spanLookup
	logIdx     *logLookup
	callIdx    *callLookup
	archiveIdx *archiveLookup
	// spanTopos caches span topologies for selected reads (see
	// selection.go); it is released with the store.
	spanTopos *spanTopologyCache

	clientID string
	refCount int
	closeFn  func() error
}

// storePosition is where each of a closed store's streams ends; see
// spillPosition.
type storePosition struct {
	spans, logs, metrics *spillPosition
}

// openStore opens a client's store with its indexes built.
func openStore(ctx context.Context, root, clientID string, tailBudget int64) (*DB, error) {
	store, err := openStoreStreams(ctx, root, clientID, tailBudget, nil)
	if err != nil {
		return nil, err
	}
	if err := store.ensureIndexed(ctx); err != nil {
		return nil, errors.Join(err, store.closeStreams())
	}
	return store, nil
}

// openStoreStreams opens a client's store without indexing the rows it
// already holds; see ensureIndexed. A non-nil pos is where the store's
// streams ended when it was last closed.
func openStoreStreams(ctx context.Context, root, clientID string, tailBudget int64, pos *storePosition) (_ *DB, rerr error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", root, err)
	}
	if pos == nil {
		pos = &storePosition{}
	}

	store := &DB{
		lookup:     newSpanLookup(),
		logIdx:     newLogLookup(),
		callIdx:    newCallLookup(),
		archiveIdx: newArchiveLookup(),
		spanTopos:  newSpanTopologyCache(),
		clientID:   clientID,
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
		pos.spans,
		func(rows []Span) {
			store.lookup.addAll(rows)
			store.callIdx.addSpans(rows)
		},
	)
	if err != nil {
		return nil, fmt.Errorf("open span stream: %w", err)
	}
	store.logs, err = openLogStream(
		ctx,
		filepath.Join(root, clientID+".logs.log"),
		logCodec,
		tailBudget,
		pos.logs,
		func(rows []Log) {
			store.logIdx.addAll(rows)
			store.callIdx.addLogs(rows)
			store.archiveIdx.addAll(rows)
		},
	)
	if err != nil {
		return nil, fmt.Errorf("open log stream: %w", err)
	}
	store.metrics, err = openLogStream(
		ctx,
		filepath.Join(root, clientID+".metrics.log"),
		metricCodec,
		tailBudget,
		pos.metrics,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("open metric stream: %w", err)
	}
	store.closeFn = store.closeStreams
	return store, nil
}

// ensureIndexed builds the store's indexes over the rows it holds, if they
// aren't built yet, and keeps them current from then on. Every selector that
// answers from an index relies on it; the registry's Open calls it.
func (s *DB) ensureIndexed(ctx context.Context) error {
	if err := s.spans.ensureIndexed(ctx); err != nil {
		return fmt.Errorf("index spans: %w", err)
	}
	if err := s.logs.ensureIndexed(ctx); err != nil {
		return fmt.Errorf("index logs: %w", err)
	}
	return nil
}

// position reports where the store's streams end. It is only meaningful once
// the store has been closed without error.
func (s *DB) position() *storePosition {
	return &storePosition{
		spans:   s.spans.position(),
		logs:    s.logs.position(),
		metrics: s.metrics.position(),
	}
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

func (s *DB) SelectSpansSince(ctx context.Context, arg SelectSpansSinceParams) ([]Span, error) {
	return s.spans.Since(ctx, arg.ID, storeLimit(arg.Limit))
}

func (s *DB) SelectLogsSince(ctx context.Context, arg SelectLogsSinceParams) ([]Log, error) {
	return s.logs.Since(ctx, arg.ID, storeLimit(arg.Limit))
}

func (s *DB) SelectMetricsSince(ctx context.Context, arg SelectMetricsSinceParams) ([]Metric, error) {
	return s.metrics.Since(ctx, arg.ID, storeLimit(arg.Limit))
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

// SpanParentSubtree returns spanID plus every span beneath it over
// parent→child edges alone, answered from the span index. Unlike SpanLogScope
// it follows no cause links. The returned map is a fresh snapshot the caller
// owns.
func (s *DB) SpanParentSubtree(spanID string) map[string]struct{} {
	return s.lookup.parentSubtree(spanID)
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

// HasSpan reports whether the store has seen any snapshot of spanID.
func (s *DB) HasSpan(spanID string) bool {
	return s.lookup.hasSpan(spanID)
}

// SpanIDs returns every span ID the store has seen a snapshot of -- the seed
// for a session-wide scoped load (e.g. a name search), sized by the span
// count rather than by the snapshot stream. The returned map is a fresh
// snapshot the caller owns.
func (s *DB) SpanIDs() map[string]struct{} {
	return s.lookup.allSpanIDs()
}

// ChildSpanIDs returns the spans one edge beneath spanID, over the same edges
// as the log queries: parent→child plus cause-purpose links. Answered from the
// index alone, so a caller can load a span's immediate surroundings without
// materializing its whole subtree.
func (s *DB) ChildSpanIDs(spanID string) map[string]struct{} {
	return s.lookup.directChildren(spanID)
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

// SelectSpansLatest returns the newest snapshot row of every span in ids, in
// append order. A span's snapshots are cumulative, so its newest row alone
// reconstructs the state that full sequential processing would end with — this is
// the span half of a scoped load, sized by the scope instead of the session.
func (s *DB) SelectSpansLatest(ctx context.Context, ids map[string]struct{}) ([]Span, error) {
	return readIndexedRows(ctx, s.spans, s.lookup.latestRowIDs(ids), func(row Span) int64 { return row.ID }, "span")
}

// SelectLogsForSpans returns the log rows attributed to any span in ids, in
// append order — the log half of a scoped load. perSpanTail > 0 bounds each
// span to its newest perSpanTail rows; renderers bound every span's log
// output to a tail anyway, and the cap keeps one pathological span (e.g. a
// service that streamed millions of lines) from ballooning the load.
func (s *DB) SelectLogsForSpans(ctx context.Context, ids map[string]struct{}, perSpanTail int) ([]Log, error) {
	return s.SelectLogRows(ctx, s.logIdx.rowIDsForSpans(ids, perSpanTail))
}

// LogRowIDsForSpans returns the row IDs of every log row attributed to a span
// in ids, ascending -- a snapshot of a capture's rows, answered from the log
// index alone. A caller that pages through a large capture resolves its rows
// once with this and reads them in batches with SelectLogRows, rather than
// re-resolving the scope per page.
func (s *DB) LogRowIDsForSpans(ids map[string]struct{}) []int64 {
	return s.logIdx.rowIDsForSpans(ids, 0)
}

// SelectLogRows reads the given log rows, which must be ascending (as the
// log index returns them), in that order.
func (s *DB) SelectLogRows(ctx context.Context, rowIDs []int64) ([]Log, error) {
	return readIndexedRows(ctx, s.logs, rowIDs, func(row Log) int64 { return row.ID }, "log")
}

// readIndexedRows reads rows the store's indexes vouched for, as one batch:
// a row that cannot be read is an error, as for a single indexed read.
func readIndexedRows[Row any](ctx context.Context, stream *logStream[Row], rowIDs []int64, getID func(Row) int64, kind string) ([]Row, error) {
	rows, err := stream.readIDs(ctx, rowIDs)
	if err != nil {
		return nil, fmt.Errorf("read %s rows: %w", kind, err)
	}
	if len(rows) != len(rowIDs) {
		for i, rowID := range rowIDs {
			if i >= len(rows) || getID(rows[i]) != rowID {
				return nil, fmt.Errorf("indexed %s row %d: %w", kind, rowID, sql.ErrNoRows)
			}
		}
	}
	return rows, nil
}

// CallFrame is where the store holds one call's frame: the newest snapshot
// of the span that carries it as dagger.io/dag.call, and/or the call-payload
// log record that carries it. A frame delivered by both transports has both;
// the log record is the cheaper one to decode (the span's attributes hold
// everything else about the span too).
type CallFrame struct {
	Digest string
	Span   *Span
	Log    *Log
}

// CallDigests returns every call digest the store holds a frame for, on
// either transport -- the seed for a session-wide call search, sized by the
// number of distinct calls. The returned map is a fresh snapshot the caller
// owns.
func (s *DB) CallDigests() map[string]struct{} {
	return s.callIdx.digests()
}

// SelectCallFrames returns the frames the store holds for digests, in
// ascending digest order, skipping digests it holds no frame for. Answered
// through the call index, so a recipe rebuild that follows a chain's
// references loads a row per frame rather than scanning either stream.
func (s *DB) SelectCallFrames(ctx context.Context, digests map[string]struct{}) ([]CallFrame, error) {
	ordered := make([]string, 0, len(digests))
	for digest := range digests {
		ordered = append(ordered, digest)
	}
	sort.Strings(ordered)
	frames := make([]CallFrame, 0, len(ordered))
	for _, digest := range ordered {
		spanRow, logRow := s.callIdx.rows(digest)
		if spanRow == 0 && logRow == 0 {
			continue
		}
		frame := CallFrame{Digest: digest}
		if logRow != 0 {
			row, found, err := s.logs.readID(ctx, logRow)
			if err != nil {
				return nil, fmt.Errorf("read log row %d: %w", logRow, err)
			}
			if !found {
				return nil, fmt.Errorf("indexed log row %d: %w", logRow, sql.ErrNoRows)
			}
			frame.Log = &row
		}
		if spanRow != 0 {
			row, found, err := s.spans.readID(ctx, spanRow)
			if err != nil {
				return nil, fmt.Errorf("read span row %d: %w", spanRow, err)
			}
			if !found {
				return nil, fmt.Errorf("indexed span row %d: %w", spanRow, sql.ErrNoRows)
			}
			frame.Span = &row
		}
		frames = append(frames, frame)
	}
	return frames, nil
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
	return s.SelectLogRows(ctx, rowIDs)
}

func (s *DB) closeStreams() error {
	streams := []func() error{s.closeImports}
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
