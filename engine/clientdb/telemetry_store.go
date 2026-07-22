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
	// Each nested map is a set, preserving unique child span IDs even though
	// live, final, and heartbeat snapshots repeat the same span row.
	children map[string]map[string]struct{}
}

func newSpanLookup() *spanLookup {
	return &spanLookup{
		firstRow: make(map[spanLookupKey]int64),
		children: make(map[string]map[string]struct{}),
	}
}

func (l *spanLookup) add(row Span) {
	l.mu.Lock()
	key := spanLookupKey{traceID: row.TraceID, spanID: row.SpanID}
	if _, exists := l.firstRow[key]; !exists {
		l.firstRow[key] = row.ID
	}
	if row.ParentSpanID.Valid {
		children := l.children[row.ParentSpanID.String]
		if children == nil {
			children = make(map[string]struct{})
			l.children[row.ParentSpanID.String] = children
		}
		children[row.SpanID] = struct{}{}
	}
	l.mu.Unlock()
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
		children := make([]string, 0, len(l.children[parent]))
		for child := range l.children[parent] {
			children = append(children, child)
		}
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

// Store is one client's standalone append-only telemetry store.
type Store struct {
	spans   *logStream[Span]
	logs    *logStream[Log]
	metrics *logStream[Metric]
	lookup  *spanLookup

	clientID string
	refCount int
	closeFn  func() error
}

func openStore(ctx context.Context, root, clientID string, tailBudget int64) (_ *Store, rerr error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", root, err)
	}

	store := &Store{
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
		store.lookup.add,
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

func (s *Store) AppendSpans(rows []Span) (int64, error) {
	last, err := s.spans.Append(rows)
	if err != nil {
		return 0, fmt.Errorf("append spans: %w", err)
	}
	return last, nil
}

func (s *Store) AppendLogs(rows []Log) (int64, error) {
	last, err := s.logs.Append(rows)
	if err != nil {
		return 0, fmt.Errorf("append logs: %w", err)
	}
	return last, nil
}

func (s *Store) AppendMetrics(rows []Metric) (int64, error) {
	last, err := s.metrics.Append(rows)
	if err != nil {
		return 0, fmt.Errorf("append metrics: %w", err)
	}
	return last, nil
}

// Read mirrors the current DB handle seam. The append-only store needs no
// separate read pool, so selectors are bound to the same immutable streams.
func (s *Store) Read() *Store {
	return s
}

func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	return s.closeFn()
}

func (s *Store) SelectSpansSince(ctx context.Context, arg SelectSpansSinceParams) ([]Span, error) {
	return s.spans.Since(ctx, arg.ID, storeLimit(arg.Limit))
}

func (s *Store) SelectLogsSince(ctx context.Context, arg SelectLogsSinceParams) ([]Log, error) {
	return s.logs.Since(ctx, arg.ID, storeLimit(arg.Limit))
}

func (s *Store) SelectMetricsSince(ctx context.Context, arg SelectMetricsSinceParams) ([]Metric, error) {
	return s.metrics.Since(ctx, arg.ID, storeLimit(arg.Limit))
}

func (s *Store) SelectSpan(ctx context.Context, arg SelectSpanParams) (Span, error) {
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

func (s *Store) SelectLogsBeneathSpan(ctx context.Context, arg SelectLogsBeneathSpanParams) ([]Log, error) {
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

func (s *Store) closeStreams() error {
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
