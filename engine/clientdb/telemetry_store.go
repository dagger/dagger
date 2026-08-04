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
	"sync"

	telemetry "github.com/dagger/otel-go"
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
}

func newSpanLookup() *spanLookup {
	return &spanLookup{
		firstRow:       make(map[spanLookupKey]int64),
		children:       make(map[string][]string),
		causalChildren: make(map[string][]string),
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
	// Link edges are indexed before (and regardless of) the duplicate-snapshot
	// suppression below: a later snapshot of an already-seen span may carry
	// links its first row lacked.
	for _, target := range causalTargets {
		kids := l.causalChildren[target]
		if !slices.Contains(kids, row.SpanID) {
			l.causalChildren[target] = append(kids, row.SpanID)
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

// descendants returns every span reachable from root via child edges and
// cause-purpose link edges — the same containment dagui renders, where a
// cause-linking span (e.g. a service's exec span) appears as a child of the
// span it links to (e.g. the Container.asService install span).
func (l *spanLookup) descendants(root string) map[string]struct{} {
	descendants := make(map[string]struct{})
	seen := map[string]struct{}{root: {}}
	queue := []string{root}
	for len(queue) > 0 {
		parent := queue[0]
		queue = queue[1:]

		l.mu.RLock()
		children := append([]string(nil), l.children[parent]...)
		children = append(children, l.causalChildren[parent]...)
		l.mu.RUnlock()

		for _, child := range children {
			if _, exists := seen[child]; exists {
				continue
			}
			seen[child] = struct{}{}
			descendants[child] = struct{}{}
			queue = append(queue, child)
		}
	}
	return descendants
}

// DB is one client's standalone append-only telemetry store.
type DB struct {
	spans   *logStream[Span]
	logs    *logStream[Log]
	metrics *logStream[Metric]
	lookup  *spanLookup

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
		nil,
		nil,
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

func (s *DB) SelectLogsBeneathSpan(ctx context.Context, arg SelectLogsBeneathSpanParams) ([]Log, error) {
	limit := storeLimit(arg.Limit)
	if limit == 0 || !arg.SpanID.Valid {
		return nil, nil
	}
	descendants := s.lookup.descendants(arg.SpanID.String)
	if len(descendants) == 0 {
		return nil, nil
	}

	const scanBatch = int(sparseIndexStride)
	logs := make([]Log, 0, limit)
	cursor := arg.ID
	for len(logs) < limit {
		page, err := s.logs.Since(ctx, cursor, scanBatch)
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			return logs, nil
		}
		for _, row := range page {
			cursor = row.ID
			if !row.SpanID.Valid {
				continue
			}
			if _, found := descendants[row.SpanID.String]; !found {
				continue
			}
			logs = append(logs, row)
			if len(logs) == limit {
				return logs, nil
			}
		}
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
