package tracesource

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/dagger/dagger/engine/archive"
	"github.com/dagger/dagger/internal/cloud"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	"golang.org/x/sync/errgroup"
)

// LogQuery asks a LogReader for the display text of a set of spans: what a
// frontend that renders only some spans reads on demand.
type LogQuery struct {
	// Spans each select a span, optionally with descendants, plus rows that
	// carry one of its dagger.io/dag.digest values (which the frontend
	// routes to the span that created the digest).
	Spans []archive.LogSpan
	// Follow keeps reading as records arrive, until ctx ends or the source
	// has no more to give (the session drained, the spans ended).
	Follow bool
	// Cursor resumes a read after the batch that carried it; "" reads from
	// the start. Cursors are opaque, and valid only for the same reader and
	// spans.
	Cursor string
	// Through, when set, ends a one-shot read at this cursor (one a
	// previous batch carried) instead of the source's end: catching newly
	// selected spans up to where a follow of other spans stands. Only Live
	// honors it; finite sources end where they end.
	Through string
}

// LogReader reads display text for a set of spans. A reader may deliver more
// than was selected (other record classes, rows of spans whose place it does
// not know); the caller applies the frontend's exact routing and roll-up.
// cb is called serially, each batch with the cursor to resume after it.
type LogReader interface {
	ReadLogs(ctx context.Context, traceID string, q LogQuery, cb func(context.Context, *collogspb.ExportLogsServiceRequest, string) error) error
}

func rowCursor(cursor string) (int64, error) {
	if cursor == "" {
		return 0, nil
	}
	row, err := strconv.ParseInt(cursor, 10, 64)
	if err != nil || row < 0 {
		return 0, fmt.Errorf("invalid log cursor %q", cursor)
	}
	return row, nil
}

// LiveLogClient is an engine client's on-demand read of its own live store
// (engine/client.Client).
type LiveLogClient interface {
	QueryLogs(context.Context, archive.LogQuery, int64, func(int64, *collogspb.ExportLogsServiceRequest) error) (int64, error)
}

// Live reads a running session's display text from the connected client's
// own engine store. Its trace is the session's; traceID is not consulted.
type Live struct {
	Client LiveLogClient
}

var _ LogReader = Live{}

func (l Live) ReadLogs(ctx context.Context, _ string, q LogQuery, cb func(context.Context, *collogspb.ExportLogsServiceRequest, string) error) error {
	cursor, err := rowCursor(q.Cursor)
	if err != nil {
		return err
	}
	through, err := rowCursor(q.Through)
	if err != nil {
		return err
	}
	if q.Through != "" && !q.Follow && through <= cursor {
		// Nothing lies between the cursor and the bound.
		return nil
	}
	_, err = l.Client.QueryLogs(ctx, archive.LogQuery{Spans: q.Spans, Follow: q.Follow, Through: through}, cursor, func(next int64, req *collogspb.ExportLogsServiceRequest) error {
		return cb(ctx, req, strconv.FormatInt(next, 10))
	})
	return err
}

var _ LogReader = (*Store)(nil)

// StoreLogQuerier is the archive client's multi-span log read
// (archive.Client.LogQuery), which Store.ReadLogs needs of its reader.
type StoreLogQuerier interface {
	LogQuery(context.Context, string, archive.StreamOptions, archive.LogQuery, func(int64, *collogspb.ExportLogsServiceRequest) error) (int64, error)
}

var _ StoreLogQuerier = (*archive.Client)(nil)

// ReadLogs reads display text at the archive's fixed cut. An archive is
// finite, so Follow ends there too.
func (a *Store) ReadLogs(ctx context.Context, traceID string, q LogQuery, cb func(context.Context, *collogspb.ExportLogsServiceRequest, string) error) error {
	reader, ok := a.reader.(StoreLogQuerier)
	if !ok {
		return errors.New("store reader does not support log queries")
	}
	opts, err := a.options(traceID, a.manifest.HighWater.Logs)
	if err != nil {
		return err
	}
	if opts.Cursor, err = rowCursor(q.Cursor); err != nil {
		return err
	}
	if opts.Cursor > opts.HighWater {
		return fmt.Errorf("log cursor %d is past the archive's cut %d", opts.Cursor, opts.HighWater)
	}
	_, err = reader.LogQuery(ctx, traceID, opts, archive.LogQuery{Spans: q.Spans}, func(next int64, req *collogspb.ExportLogsServiceRequest) error {
		return cb(ctx, req, strconv.FormatInt(next, 10))
	})
	return err
}

// CloudLogFetcher is Cloud's per-span log stream (cloud.OTLPClient).
type CloudLogFetcher interface {
	FetchLogs(context.Context, string, cloud.LogSelection, func(context.Context, *collogspb.ExportLogsServiceRequest) error) error
}

// DefaultCloudLogConcurrency bounds CloudLogs' concurrent per-span streams.
const DefaultCloudLogConcurrency = 8

// CloudLogs reads display text from Dagger Cloud, which serves one span (or
// subtree) per stream: it fans out one GET /v1/logs/{trace}?span_id=…
// stream per selected span, at most Concurrency at once.
//
// Cloud has no display-text record class that keeps global and verbose
// records, so it asks for every record class and leaves the filtering to
// the caller; nor a digest selection, so a span's Digests are not read (the
// routed rows remain readable through the span that emitted them). Cloud's
// streams end once their span has ended, whether or not Follow is set. The
// cursor maps each span to the timestamp of the last record received for it.
type CloudLogs struct {
	Client      CloudLogFetcher
	Concurrency int
}

var _ LogReader = CloudLogs{}

var _ CloudLogFetcher = (*cloud.OTLPClient)(nil)

func (c CloudLogs) ReadLogs(ctx context.Context, traceID string, q LogQuery, cb func(context.Context, *collogspb.ExportLogsServiceRequest, string) error) error {
	after := map[string]time.Time{}
	if q.Cursor != "" {
		if err := json.Unmarshal([]byte(q.Cursor), &after); err != nil {
			return fmt.Errorf("invalid Cloud log cursor: %w", err)
		}
	}
	limit := c.Concurrency
	if limit <= 0 {
		limit = DefaultCloudLogConcurrency
	}
	var mu sync.Mutex // serializes cb and guards after
	eg, ctx := errgroup.WithContext(ctx)
	eg.SetLimit(limit)
	for _, span := range q.Spans {
		eg.Go(func() error {
			sel := cloud.LogSelection{SpanID: span.SpanID, Descendants: span.Descendants, Records: cloud.LogRecordsAll}
			mu.Lock()
			if t, ok := after[span.SpanID]; ok {
				sel.After = &t
			}
			mu.Unlock()
			return c.Client.FetchLogs(ctx, traceID, sel, func(ctx context.Context, req *collogspb.ExportLogsServiceRequest) error {
				mu.Lock()
				defer mu.Unlock()
				if last, ok := lastLogTime(req); ok && last.After(after[span.SpanID]) {
					after[span.SpanID] = last
				}
				cursor, err := json.Marshal(after)
				if err != nil {
					return err
				}
				return cb(ctx, req, string(cursor))
			})
		})
	}
	return eg.Wait()
}

func lastLogTime(req *collogspb.ExportLogsServiceRequest) (time.Time, bool) {
	var last uint64
	for _, rl := range req.GetResourceLogs() {
		for _, sl := range rl.GetScopeLogs() {
			for _, lr := range sl.GetLogRecords() {
				last = max(last, lr.GetTimeUnixNano())
			}
		}
	}
	if last == 0 {
		return time.Time{}, false
	}
	return time.Unix(0, int64(last)).UTC(), true
}
