package clientdb

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

// HighWater is a fixed inclusive cursor; zero denotes an empty stream.
type HighWater struct{ Spans, Logs, Metrics int64 }
type SelectSpansRangeParams struct{ AfterID, ThroughID, Limit int64 }
type SelectLogsRangeParams struct{ AfterID, ThroughID, Limit int64 }
type SelectMetricsRangeParams struct{ AfterID, ThroughID, Limit int64 }

// Checkpoint is a persistence barrier, not merely an in-memory high water.
// Producers must be quiesced to use the returned cut for finalization.
func (s *DB) Checkpoint(ctx context.Context) (HighWater, error) {
	cut := HighWater{s.spans.highWater(), s.logs.highWater(), s.metrics.highWater()}
	for _, sync := range []func(context.Context) error{s.spans.checkpoint, s.logs.checkpoint, s.metrics.checkpoint} {
		if err := sync(ctx); err != nil {
			return HighWater{}, err
		}
	}
	// Sync directory entries as well as stream contents before a manifest refers to them.
	dir, err := os.Open(filepath.Dir(s.logs.spill.file.Name()))
	if err != nil {
		return HighWater{}, err
	}
	err = errors.Join(dir.Sync(), dir.Close())
	return cut, err
}

func (s *DB) CheckpointLogs(ctx context.Context) error { return s.logs.checkpoint(ctx) }

// HighWater reports the current end of each stream without a persistence
// barrier. Use it only for a store no producer writes to anymore, such as an
// archive whose session ended without sealing.
func (s *DB) HighWater() HighWater {
	return HighWater{s.spans.highWater(), s.logs.highWater(), s.metrics.highWater()}
}
func (s *DB) SelectSpansRange(ctx context.Context, p SelectSpansRangeParams) ([]Span, error) {
	return s.spans.Range(ctx, p.AfterID, p.ThroughID, storeLimit(p.Limit))
}
func (s *DB) SelectLogsRange(ctx context.Context, p SelectLogsRangeParams) ([]Log, error) {
	return s.logs.Range(ctx, p.AfterID, p.ThroughID, storeLimit(p.Limit))
}
func (s *DB) SelectMetricsRange(ctx context.Context, p SelectMetricsRangeParams) ([]Metric, error) {
	return s.metrics.Range(ctx, p.AfterID, p.ThroughID, storeLimit(p.Limit))
}
func (s *DB) SizeBytes() (int64, error) {
	var total int64
	for _, file := range []*os.File{s.spans.spill.file, s.logs.spill.file, s.metrics.spill.file} {
		stat, err := file.Stat()
		if err != nil {
			return 0, err
		}
		total += stat.Size()
	}
	return total, nil
}
