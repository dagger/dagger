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
func (db *DB) Checkpoint(ctx context.Context) (HighWater, error) {
	cut := HighWater{db.spans.highWater(), db.logs.highWater(), db.metrics.highWater()}
	for _, sync := range []func(context.Context) error{db.spans.checkpoint, db.logs.checkpoint, db.metrics.checkpoint} {
		if err := sync(ctx); err != nil {
			return HighWater{}, err
		}
	}
	// Sync directory entries as well as stream contents before a manifest refers to them.
	dir, err := os.Open(filepath.Dir(db.logs.spill.file.Name()))
	if err != nil {
		return HighWater{}, err
	}
	err = errors.Join(dir.Sync(), dir.Close())
	return cut, err
}

func (db *DB) CheckpointLogs(ctx context.Context) error { return db.logs.checkpoint(ctx) }
func (db *DB) SelectSpansRange(ctx context.Context, p SelectSpansRangeParams) ([]Span, error) {
	return db.spans.Range(ctx, p.AfterID, p.ThroughID, storeLimit(p.Limit))
}
func (db *DB) SelectLogsRange(ctx context.Context, p SelectLogsRangeParams) ([]Log, error) {
	return db.logs.Range(ctx, p.AfterID, p.ThroughID, storeLimit(p.Limit))
}
func (db *DB) SelectMetricsRange(ctx context.Context, p SelectMetricsRangeParams) ([]Metric, error) {
	return db.metrics.Range(ctx, p.AfterID, p.ThroughID, storeLimit(p.Limit))
}
func (db *DB) SizeBytes() (int64, error) {
	var total int64
	for _, file := range []*os.File{db.spans.spill.file, db.logs.spill.file, db.metrics.spill.file} {
		stat, err := file.Stat()
		if err != nil {
			return 0, err
		}
		total += stat.Size()
	}
	return total, nil
}
