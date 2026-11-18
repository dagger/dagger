package clientdb

import (
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

	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	"go.opentelemetry.io/otel/trace"
	otlptracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/encoding/protowire"
)

// Marker byte strings for the raw-attribute prefilter in addLocked. The
// attributes column is a binary KeyValue list (MarshalProtoList), so an
// attribute's presence implies its key bytes appear verbatim.
var (
	checkNameAttrMarker     = []byte(telemetry.CheckNameAttr)
	testCaseNameAttrMarker  = []byte(string(semconv.TestCaseNameKey))
	testSuiteNameAttrMarker = []byte(string(semconv.TestSuiteNameKey))
	agentIDAttrMarker       = []byte(telemetryattrs.AgentIDAttr)
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
	if targets, ok := readCausalLinkTargets(row.SpanID, row.Links); ok {
		return targets
	}
	return decodeCausalLinkTargets(row)
}

// readCausalLinkTargets is decodeCausalLinkTargets read from the stored links
// in place, without decoding them into messages and SDK values. The store's
// link lists are MarshalProtoList's well-formed encodings, so reading only
// the span ID and the purpose attribute gives the same result. ok is false
// for a list it can't read that way: one whose elements don't split, or a
// purpose that isn't a string, bytes, number or bool, which decoding
// turns into strings of its own.
func readCausalLinkTargets(self string, links []byte) (targets []string, ok bool) {
	for len(links) > 0 {
		link, n := protowire.ConsumeBytes(links)
		if n < 0 {
			return nil, false
		}
		links = links[n:]
		sid, purpose, ok := readLink(link)
		if !ok {
			return nil, false
		}
		switch purpose {
		case telemetry.LinkPurposeCause, "":
		default:
			continue
		}
		if !sid.IsValid() {
			continue
		}
		target := sid.String()
		if target == self {
			// a self-link would add a pointless self-edge; skip it
			continue
		}
		if !slices.Contains(targets, target) {
			targets = append(targets, target)
		}
	}
	return targets, true
}

// readLink reads a Span_Link encoding's span ID (field 2) and the string
// value of its first purpose attribute (field 4), as SpanLinksFromPB and
// attribute.Value.AsString give them.
func readLink(link []byte) (sid trace.SpanID, purpose string, ok bool) {
	seenPurpose := false
	for len(link) > 0 {
		num, typ, n := protowire.ConsumeTag(link)
		if n < 0 {
			return sid, "", false
		}
		link = link[n:]
		if typ != protowire.BytesType || (num != 2 && num != 4) {
			if n = protowire.ConsumeFieldValue(num, typ, link); n < 0 {
				return sid, "", false
			}
			link = link[n:]
			continue
		}
		v, n := protowire.ConsumeBytes(link)
		if n < 0 {
			return sid, "", false
		}
		link = link[n:]
		if num == 2 {
			sid = trace.SpanID{}
			copy(sid[:], v)
			continue
		}
		if seenPurpose {
			continue
		}
		key, value, hasValue, ok := readKeyValue(v)
		if !ok {
			return sid, "", false
		}
		if string(key) != telemetry.LinkPurposeAttr {
			continue
		}
		seenPurpose = true
		if !hasValue {
			continue
		}
		if purpose, ok = readStringish(value); !ok {
			return sid, "", false
		}
	}
	return sid, purpose, true
}

// readKeyValue reads a KeyValue encoding's key (field 1) and value (field 2).
func readKeyValue(kv []byte) (key, value []byte, hasValue, ok bool) {
	for len(kv) > 0 {
		num, typ, n := protowire.ConsumeTag(kv)
		if n < 0 {
			return nil, nil, false, false
		}
		kv = kv[n:]
		if typ != protowire.BytesType || (num != 1 && num != 2) {
			if n = protowire.ConsumeFieldValue(num, typ, kv); n < 0 {
				return nil, nil, false, false
			}
			kv = kv[n:]
			continue
		}
		v, n := protowire.ConsumeBytes(kv)
		if n < 0 {
			return nil, nil, false, false
		}
		kv = kv[n:]
		if num == 1 {
			key = v
		} else {
			value, hasValue = v, true
		}
	}
	return key, value, hasValue, true
}

// readStringish is AsString of the attribute an AnyValue encoding decodes to,
// for the value types that decode to themselves: a string, or bytes, are that
// string, and a number or bool is the empty string.
func readStringish(value []byte) (string, bool) {
	s, found := "", false
	for len(value) > 0 {
		num, typ, n := protowire.ConsumeTag(value)
		if n < 0 {
			return "", false
		}
		value = value[n:]
		switch {
		case (num == 1 || num == 7) && typ == protowire.BytesType:
			v, m := protowire.ConsumeBytes(value)
			if m < 0 {
				return "", false
			}
			s, found, n = string(v), true, m
		case (num == 2 || num == 3) && typ == protowire.VarintType, num == 4 && typ == protowire.Fixed64Type:
			s, found = "", true
			n = protowire.ConsumeFieldValue(num, typ, value)
		default:
			// An array, a key-value list or an unknown field: leave it to
			// decoding.
			return "", false
		}
		if n < 0 {
			return "", false
		}
		value = value[n:]
	}
	return s, found
}

// decodeCausalLinkTargets is causalLinkTargets by decoding row's links into
// SDK links.
func decodeCausalLinkTargets(row Span) []string {
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

// logLookup indexes log rows by the span they're attributed to, so scoped log
// reads (SelectLogsBeneathSpan, SelectLogsForSpans) resolve to direct row
// reads instead of scanning the whole log stream — a scan that is linear in
// SESSION size, paid per read, on paths that run per LLM tool call. The cost
// is 8 bytes per log row plus per-span slice overhead; rows are appended with
// monotonic IDs, so each span's slice is ascending by construction.
//
// Span IDs and digests are interned by their bytes (see idTable), and the
// per-span row lists are indexed by the interned span: a string-keyed table
// and map kept every span ID three times over.
type logLookup struct {
	mu sync.RWMutex
	// rowsBySpan holds, by interned span, the rows with a valid span ID.
	rowsBySpan [][]int64
	// meta holds one logMeta per log row, indexed by row ID - 1, for
	// selected reads (see selection.go). Its trace and span fields index the
	// intern tables below.
	meta       []logMeta
	metaErrs   map[int64]error
	traceIDs   []string
	traceIndex map[string]uint32
	spans      idTable
	// digests interns logMeta.digest; index 0 is the empty digest.
	digests idTable

	// For one-shot display reads by index (see ReadDisplayLogs), beside
	// rowsBySpan: displayByDigest holds the display rows (logFlags.display)
	// of each digest, which few rows carry; unspanned holds the rows with no
	// span ID, which rowsBySpan leaves out.
	displayByDigest map[uint32][]int64
	unspanned       []int64
	// unplaced holds the spans (interned) that have rows but no snapshot in
	// spanKnown's span index, whose place a live log scope leaves to its
	// reader. A span joins when its first row arrives before its snapshot
	// and leaves when the snapshot arrives (spansStored).
	unplaced  map[uint32]struct{}
	spanKnown func(spanID string) bool

	// memoMu guards attrMemo, which caches attribute classes by their bytes.
	memoMu   sync.Mutex
	attrMemo map[string]logAttrClassResult
}

func newLogLookup() *logLookup {
	l := &logLookup{
		metaErrs:        make(map[int64]error),
		traceIndex:      make(map[string]uint32),
		digests:         idTable{prefix: "xxh3:"},
		displayByDigest: make(map[uint32][]int64),
		unplaced:        make(map[uint32]struct{}),
		attrMemo:        make(map[string]logAttrClassResult),
	}
	l.digests.intern("") // 0: no digest
	return l
}

func (l *logLookup) addAll(rows []Log) {
	flags, digests, errs := l.classify(rows)
	l.mu.Lock()
	for i, row := range rows {
		if errs != nil && errs[i] != nil {
			l.metaErrs[int64(len(l.meta))] = errs[i]
		}
		spans := l.spans.count()
		m := logMeta{
			timestamp: row.Timestamp,
			size:      logCodec.size(row),
			trace:     internLocked(l.traceIndex, &l.traceIDs, row.TraceID.String),
			span:      l.spans.intern(row.SpanID.String),
			digest:    l.digests.intern(digests[i]),
			flags:     flags[i],
		}
		l.meta = append(l.meta, m)
		if l.spans.count() > spans {
			l.rowsBySpan = append(l.rowsBySpan, nil)
			if l.spanKnown != nil && !l.spanKnown(row.SpanID.String) {
				l.unplaced[m.span] = struct{}{}
			}
		}
		if m.digest != 0 && m.flags.display() {
			l.displayByDigest[m.digest] = append(l.displayByDigest[m.digest], row.ID)
		}
	}
	first := len(l.meta) - len(rows)
	for i, row := range rows {
		if !row.SpanID.Valid {
			l.unspanned = append(l.unspanned, row.ID)
			continue
		}
		span := l.meta[first+i].span
		l.rowsBySpan[span] = append(l.rowsBySpan[span], row.ID)
	}
	l.mu.Unlock()
}

// spanName is the span ID interned as span.
func (l *logLookup) spanName(span uint32) string { return l.spans.name(span) }

// spanCount is how many span IDs are interned.
func (l *logLookup) spanCount() int { return l.spans.count() }

// digestName is the digest interned as digest ("" for 0).
func (l *logLookup) digestName(digest uint32) string { return l.digests.name(digest) }

// spansStored takes the spans of rows, now in the span index, out of
// unplaced. It runs after the span index added them, so that a log row
// interning one of them since sees it as known.
func (l *logLookup) spansStored(rows []Span) {
	l.mu.RLock()
	empty := len(l.unplaced) == 0
	l.mu.RUnlock()
	if empty {
		return
	}
	l.mu.Lock()
	for _, row := range rows {
		if span, ok := l.spans.lookup(row.SpanID); ok {
			delete(l.unplaced, span)
		}
	}
	l.mu.Unlock()
}

// spanRowsLocked returns the rows of span id: rowsBySpan's, and for "" the
// rows with no span ID too, which a scan attributes to "" as well.
func (l *logLookup) spanRowsLocked(id string, lists [][]int64) [][]int64 {
	if span, ok := l.spans.lookup(id); ok && len(l.rowsBySpan[span]) > 0 {
		lists = append(lists, l.rowsBySpan[span])
	}
	if id == "" && len(l.unspanned) > 0 {
		lists = append(lists, l.unspanned)
	}
	return lists
}

// unplacedSpanIDs snapshots the IDs of the spans in unplaced.
func (l *logLookup) unplacedSpanIDs() []string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	ids := make([]string, 0, len(l.unplaced))
	for span := range l.unplaced {
		ids = append(ids, l.spans.name(span))
	}
	return ids
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
		span, ok := l.spans.lookup(id)
		if !ok {
			continue
		}
		rows := l.rowsBySpan[span]
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
	store.logIdx.spanKnown = store.lookup.hasSpan
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
			store.logIdx.spansStored(rows)
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

// CausalParents returns the span IDs the given span cause-links to, over
// every snapshot of it.
func (s *DB) CausalParents(spanID string) []string {
	return s.lookup.causalParentsOf(spanID)
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

// AgentSpanIDs returns the spans whose attributes carry an agent ID -- agent
// loop spans, the seed for an agent roster -- answered from the span index.
func (s *DB) AgentSpanIDs() map[string]struct{} {
	return s.lookup.agentSpanIDs()
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
	type located struct {
		digest          string
		spanRow, logRow int64
	}
	var wanted []located
	var spanIDs, logIDs []int64
	for _, digest := range ordered {
		spanRow, logRow := s.callIdx.rows(digest)
		if spanRow == 0 && logRow == 0 {
			continue
		}
		wanted = append(wanted, located{digest, spanRow, logRow})
		if spanRow != 0 {
			spanIDs = append(spanIDs, spanRow)
		}
		if logRow != 0 {
			logIDs = append(logIDs, logRow)
		}
	}
	// Each stream is read in one ascending pass, through one scanner and
	// read buffer: rows read one at a time each paid for a scanner of their
	// own.
	logs, err := readRowsByID(ctx, s.logs, logIDs, func(row Log) int64 { return row.ID }, "log")
	if err != nil {
		return nil, err
	}
	spans, err := readRowsByID(ctx, s.spans, spanIDs, func(row Span) int64 { return row.ID }, "span")
	if err != nil {
		return nil, err
	}
	frames := make([]CallFrame, 0, len(wanted))
	for _, w := range wanted {
		frame := CallFrame{Digest: w.digest}
		if w.logRow != 0 {
			frame.Log = logs[w.logRow]
		}
		if w.spanRow != 0 {
			frame.Span = spans[w.spanRow]
		}
		frames = append(frames, frame)
	}
	return frames, nil
}

// readRowsByID reads the indexed rows ids, in any order, by ID; a row that
// cannot be read is an error.
func readRowsByID[Row any](ctx context.Context, stream *logStream[Row], ids []int64, getID func(Row) int64, kind string) (map[int64]*Row, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	sorted := slices.Clone(ids)
	slices.Sort(sorted)
	sorted = slices.Compact(sorted)
	rows, err := readIndexedRows(ctx, stream, sorted, getID, kind)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]*Row, len(rows))
	for i := range rows {
		byID[getID(rows[i])] = &rows[i]
	}
	return byID, nil
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
