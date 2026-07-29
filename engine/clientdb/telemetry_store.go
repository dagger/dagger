package clientdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
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
}

func newSpanLookup() *spanLookup {
	return &spanLookup{
		firstRow: make(map[spanLookupKey]int64),
		children: make(map[string][]string),
	}
}

func (l *spanLookup) add(row Span) {
	l.mu.Lock()
	l.addLocked(row)
	l.mu.Unlock()
}

func (l *spanLookup) addAll(rows []Span) {
	l.mu.Lock()
	for _, row := range rows {
		l.addLocked(row)
	}
	l.mu.Unlock()
}

func (l *spanLookup) addLocked(row Span) {
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

func (l *spanLookup) first(traceID, spanID string) (int64, bool) {
	l.mu.RLock()
	id, found := l.firstRow[spanLookupKey{traceID: traceID, spanID: spanID}]
	l.mu.RUnlock()
	return id, found
}

func (l *spanLookup) descendants(root string) map[string]struct{} {
	descendants := make(map[string]struct{})
	seen := map[string]struct{}{root: {}}
	queue := []string{root}
	for len(queue) > 0 {
		parent := queue[0]
		queue = queue[1:]

		l.mu.RLock()
		children := append([]string(nil), l.children[parent]...)
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
