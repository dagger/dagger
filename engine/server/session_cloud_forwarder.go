package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/proto"

	"github.com/dagger/dagger/engine/clientdb"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/engine/telemetryattrs"
)

// A session that publishes to Dagger Cloud does so from its main client's
// telemetry store, which already holds everything the session and its clients
// emit or post (every client's telemetry routes to its ancestors, and the main
// client is the root of them all). One cloudForwarder per publishing session
// tails the store's span and log streams, each from its own cursor, and exports
// every row to Cloud once, in order, as the CLI's forwarding of the same stream
// did before the engine published itself.
//
// The store is the buffer: a lane holds one batch at a time and advances its
// cursor only past rows Cloud took, so a Cloud outage costs the forwarder one
// batch of memory per signal however long it lasts, and the rows wait on disk.
//
// While the session runs the forwarder follows the store's end. The main
// client's shutdown waits, within the session's Cloud bound, for it to reach
// the end as of then (drain), while the client's attachables can still
// refresh an OAuth token. Once the session's providers have shut down, the
// store's end is final (finish): the forwarder keeps going in the background,
// after the session is gone, until it is caught up, the credential can no
// longer be used, or cloudForwardBackgroundTimeout passes, then releases the
// exporters and the store. The server keeps the store from collection while
// it forwards.

const (
	// cloudForwardBatchRows and cloudForwardMaxPayloadSize bound one export:
	// a batch whose OTLP request exceeds the size is read again, halved, and
	// only a single row larger than it is sent alone.
	cloudForwardBatchRows      = otlpBatchSize
	cloudForwardMaxPayloadSize = 4 << 20
	// cloudForwardExportTimeout bounds one export attempt, retries of the
	// OTLP exporter included.
	cloudForwardExportTimeout = 30 * time.Second
	// A failed export is retried from the same cursor after a backoff that
	// doubles from cloudForwardMinBackoff up to cloudForwardMaxBackoff.
	cloudForwardMinBackoff = 250 * time.Millisecond
	cloudForwardMaxBackoff = 30 * time.Second
	// cloudForwardBackgroundTimeout bounds forwarding after the session ends.
	cloudForwardBackgroundTimeout = 10 * time.Minute
)

var (
	errCloudForwardStopped       = errors.New("cloud telemetry forwarder stopped")
	errCloudForwardDeadline      = errors.New("cloud telemetry forwarding deadline passed")
	errCloudForwardEngineStop    = errors.New("engine is shutting down")
	errCloudForwardTokenUnusable = errors.New("cloud credential can no longer be used")
)

// cloudForwardTuning holds the forwarder's limits; tests shrink them.
type cloudForwardTuning struct {
	batchRows         int
	maxPayloadSize    int
	poll              time.Duration
	exportTimeout     time.Duration
	minBackoff        time.Duration
	maxBackoff        time.Duration
	backgroundTimeout time.Duration
	// shutdownTimeout bounds releasing the exporters once forwarding ends.
	shutdownTimeout time.Duration
}

func defaultCloudForwardTuning() cloudForwardTuning {
	return cloudForwardTuning{
		batchRows:         cloudForwardBatchRows,
		maxPayloadSize:    cloudForwardMaxPayloadSize,
		poll:              telemetry.NearlyImmediate,
		exportTimeout:     cloudForwardExportTimeout,
		minBackoff:        cloudForwardMinBackoff,
		maxBackoff:        cloudForwardMaxBackoff,
		backgroundTimeout: cloudForwardBackgroundTimeout,
		shutdownTimeout:   sessionTelemetryFlushTimeout,
	}
}

type cloudForwarder struct {
	sessionID string
	clientID  string
	tuning    cloudForwardTuning
	// db is the forwarder's own reference to the main client's store,
	// closed when the forwarder releases.
	db    *clientdb.DB
	spans sdktrace.SpanExporter
	logs  sdklog.Exporter
	// onRelease runs once everything is released.
	onRelease func()

	lanes []*cloudForwardLane

	// ctx ends every fetch, wait and export; stop cancels it.
	ctx    context.Context
	cancel context.CancelCauseFunc

	finishOnce sync.Once
	finished   chan struct{} // closed once the store's end is final
	timerMu    sync.Mutex
	deadline   *time.Timer
	done       chan struct{} // closed once released
}

// cloudForwardLane forwards one signal of the store.
type cloudForwardLane struct {
	signal string
	// fetch reads up to limit rows after since.
	fetch func(ctx context.Context, since int64, limit int) (cloudForwardBatch, error)
	// end is the stream's last row ID in a high water.
	end  func(clientdb.HighWater) int64
	kick chan struct{}

	mu       sync.Mutex
	cursor   int64         // the last row ID Cloud took (or that was skipped)
	advanced chan struct{} // closed and replaced whenever cursor advances
}

// cloudForwardBatch is one read of a lane: rows rows up to next, and, when
// any of them is to be published, their OTLP request of size bytes and send,
// which exports it.
type cloudForwardBatch struct {
	next int64
	rows int
	size int
	send func(context.Context) error
}

func (lane *cloudForwardLane) position() (int64, chan struct{}) {
	lane.mu.Lock()
	defer lane.mu.Unlock()
	return lane.cursor, lane.advanced
}

func (lane *cloudForwardLane) advance(to int64) {
	lane.mu.Lock()
	defer lane.mu.Unlock()
	lane.cursor = to
	close(lane.advanced)
	lane.advanced = make(chan struct{})
}

func (lane *cloudForwardLane) wake() {
	select {
	case lane.kick <- struct{}{}:
	default:
	}
}

// newCloudForwarder starts forwarding db's spans and logs to Cloud through
// spans and logs, which it owns from now on, as it owns the db reference.
func newCloudForwarder(sessionID, clientID string, db *clientdb.DB, spans sdktrace.SpanExporter, logs sdklog.Exporter, tuning cloudForwardTuning, onRelease func()) *cloudForwarder {
	f := &cloudForwarder{
		sessionID: sessionID,
		clientID:  clientID,
		tuning:    tuning,
		db:        db,
		spans:     spans,
		logs:      logs,
		onRelease: onRelease,
		finished:  make(chan struct{}),
		done:      make(chan struct{}),
	}
	f.ctx, f.cancel = context.WithCancelCause(context.Background())
	f.lanes = []*cloudForwardLane{
		{
			signal: "spans",
			fetch: func(ctx context.Context, since int64, limit int) (cloudForwardBatch, error) {
				next, req, rows, err := fetchSpanBatch(ctx, db, since, limit, forwardSpanRow)
				batch := cloudForwardBatch{next: next, rows: rows}
				if err == nil && len(req.GetResourceSpans()) > 0 {
					batch.size = proto.Size(req)
					batch.send = func(ctx context.Context) error {
						return spans.ExportSpans(ctx, telemetry.SpansFromPB(req.GetResourceSpans()))
					}
				}
				return batch, err
			},
			end: func(hw clientdb.HighWater) int64 { return hw.Spans },
		},
		{
			signal: "logs",
			fetch: func(ctx context.Context, since int64, limit int) (cloudForwardBatch, error) {
				next, req, rows, err := fetchLogBatch(ctx, db, since, limit, forwardLogRow)
				batch := cloudForwardBatch{next: next, rows: rows}
				if err == nil && len(req.GetResourceLogs()) > 0 {
					batch.size = proto.Size(req)
					batch.send = func(ctx context.Context) error {
						return telemetry.ReexportLogsFromPB(ctx, logs, req)
					}
				}
				return batch, err
			},
			end: func(hw clientdb.HighWater) int64 { return hw.Logs },
		},
	}
	var wg sync.WaitGroup
	for _, lane := range f.lanes {
		lane.kick = make(chan struct{}, 1)
		lane.advanced = make(chan struct{})
		wg.Go(func() { f.run(lane) })
	}
	go func() {
		wg.Wait()
		f.release()
	}()
	return f
}

// forwardSpanRow and forwardLogRow leave out the rows another writer
// publishes (see TelemetryCloudPublishedAttr).
func forwardSpanRow(row *clientdb.Span) bool { return !cloudPublishedRow(row.Attributes) }
func forwardLogRow(row *clientdb.Log) bool   { return !cloudPublishedRow(row.Attributes) }

var cloudPublishedAttrKey = []byte(`"` + telemetryattrs.TelemetryCloudPublishedAttr + `"`)

// cloudPublishedRow reports whether a row's stored attributes carry the
// published marker. The byte search keeps the common case cheap; a hit is
// confirmed by decoding.
func cloudPublishedRow(attrs []byte) bool {
	if !bytes.Contains(attrs, cloudPublishedAttrKey) {
		return false
	}
	var kvs []*otlpcommonv1.KeyValue
	if err := clientdb.UnmarshalProtoJSONs(attrs, &otlpcommonv1.KeyValue{}, &kvs); err != nil {
		return false
	}
	for _, kv := range kvs {
		if kv.GetKey() == telemetryattrs.TelemetryCloudPublishedAttr && kv.GetValue().GetBoolValue() {
			return true
		}
	}
	return false
}

// run forwards one lane until the store's end is final and reached, or the
// forwarder stops.
func (f *cloudForwarder) run(lane *cloudForwardLane) {
	lg := slog.With("session", f.sessionID, "client", f.clientID, "signal", lane.signal)
	limit := f.tuning.batchRows
	var backoff time.Duration
	failing := false
	for f.ctx.Err() == nil {
		// Observe finish before reading: every row was appended before
		// finish, so an empty read after it means the lane is done.
		final := f.isFinished()
		cursor, _ := lane.position()
		batch, err := lane.fetch(f.ctx, cursor, limit)
		if err != nil {
			if f.ctx.Err() != nil {
				return
			}
			lg.Warn("reading telemetry to publish to Cloud", "cursor", cursor, "error", err)
			backoff = nextCloudForwardBackoff(backoff, f.tuning)
			f.wait(lane, backoff)
			continue
		}
		if batch.rows == 0 {
			if final {
				return
			}
			f.wait(lane, f.tuning.poll)
			continue
		}
		if batch.send != nil && batch.rows > 1 && batch.size > f.tuning.maxPayloadSize {
			// Read a strictly smaller prefix from the same cursor.
			limit = max(1, batch.rows/2)
			continue
		}
		if batch.send != nil {
			ctx, cancel := context.WithTimeout(f.ctx, f.tuning.exportTimeout)
			err := batch.send(ctx)
			cancel()
			if err != nil && !isCloudPartialSuccess(err) {
				if f.ctx.Err() != nil {
					return
				}
				if final && cloudCredentialUnusable(err) {
					f.stop(fmt.Errorf("%w: %w", errCloudForwardTokenUnusable, err))
					return
				}
				if !failing {
					lg.Warn("publishing session telemetry to Cloud failed; retrying", "cursor", cursor, "rows", batch.rows, "error", err)
				} else {
					lg.Debug("publishing session telemetry to Cloud failed; retrying", "cursor", cursor, "rows", batch.rows, "error", err)
				}
				failing = true
				// A smaller batch gets past a request Cloud refuses for
				// its size; it is restored after the next success.
				limit = max(1, batch.rows/2)
				backoff = nextCloudForwardBackoff(backoff, f.tuning)
				f.wait(lane, backoff)
				continue
			}
			if err != nil {
				lg.Warn("Cloud rejected part of the session telemetry", "error", err)
			}
			if failing {
				lg.Info("publishing session telemetry to Cloud again", "cursor", cursor)
				failing = false
			}
		}
		lane.advance(batch.next)
		limit = f.tuning.batchRows
		backoff = 0
	}
}

func nextCloudForwardBackoff(prev time.Duration, tuning cloudForwardTuning) time.Duration {
	if prev <= 0 {
		return tuning.minBackoff
	}
	return min(prev*2, tuning.maxBackoff)
}

// wait pauses a lane for d, or until it is kicked (by drain or finish) or the
// forwarder stops.
func (f *cloudForwarder) wait(lane *cloudForwardLane, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-lane.kick:
	case <-f.ctx.Done():
	}
}

func (f *cloudForwarder) isFinished() bool {
	select {
	case <-f.finished:
		return true
	default:
		return false
	}
}

// drain waits until every lane has published the store's end as of the call,
// or ctx ends. Forwarding goes on regardless.
func (f *cloudForwarder) drain(ctx context.Context) error {
	target := f.db.HighWater()
	for _, lane := range f.lanes {
		lane.wake()
	}
	for _, lane := range f.lanes {
		want := lane.end(target)
		for {
			cursor, advanced := lane.position()
			if cursor >= want {
				break
			}
			select {
			case <-advanced:
			case <-ctx.Done():
				return fmt.Errorf("%s at %d of %d: %w", lane.signal, cursor, want, context.Cause(ctx))
			case <-f.done:
				return fmt.Errorf("%s at %d of %d: %w", lane.signal, cursor, want, errCloudForwardStopped)
			}
		}
	}
	return nil
}

// finish marks the store's end final: nothing is appended to it anymore. The
// forwarder publishes up to it in the background, within the background
// timeout, and then releases. It never waits.
func (f *cloudForwarder) finish() {
	f.finishOnce.Do(func() {
		close(f.finished)
		f.timerMu.Lock()
		f.deadline = time.AfterFunc(f.tuning.backgroundTimeout, func() {
			f.stop(errCloudForwardDeadline)
		})
		f.timerMu.Unlock()
		for _, lane := range f.lanes {
			lane.wake()
		}
	})
}

// stop ends forwarding at once, cancelling the exports in flight; the
// forwarder then releases in the background.
func (f *cloudForwarder) stop(cause error) {
	f.cancel(cause)
}

// wait blocks until the forwarder released, or ctx ends.
func (f *cloudForwarder) waitReleased(ctx context.Context) error {
	select {
	case <-f.done:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// release reports what was left unpublished, shuts the exporters down, and
// releases the store.
func (f *cloudForwarder) release() {
	defer close(f.done)
	f.timerMu.Lock()
	if f.deadline != nil {
		f.deadline.Stop()
	}
	f.timerMu.Unlock()
	lg := slog.With("session", f.sessionID, "client", f.clientID)
	end := f.db.HighWater()
	unpublished := map[string]int64{}
	for _, lane := range f.lanes {
		cursor, _ := lane.position()
		if left := lane.end(end) - cursor; left > 0 {
			unpublished[lane.signal] = left
		}
	}
	if len(unpublished) > 0 {
		lg.Warn("session telemetry not fully published to Cloud",
			"cause", context.Cause(f.ctx),
			"unpublishedSpans", unpublished["spans"],
			"unpublishedLogs", unpublished["logs"])
	} else {
		lg.Debug("session telemetry published to Cloud")
	}
	f.cancel(errCloudForwardStopped)
	ctx, cancel := context.WithTimeout(context.Background(), f.tuning.shutdownTimeout)
	defer cancel()
	if err := errors.Join(f.spans.Shutdown(ctx), f.logs.Shutdown(ctx)); err != nil {
		lg.Warn("shutting down the session's Cloud exporters", "error", err)
	}
	if err := f.db.Close(); err != nil {
		lg.Warn("releasing the session's telemetry store", "error", err)
	}
	if f.onRelease != nil {
		f.onRelease()
	}
}

// isCloudPartialSuccess reports an OTLP partial success: Cloud took the
// request but rejected some of its items, which sending it again would only
// repeat.
func isCloudPartialSuccess(err error) bool {
	return strings.Contains(err.Error(), "OTLP partial success")
}

// cloudCredentialUnusable reports an export that failed because the session's
// credential can no longer be used: its refresh needs the main client, which
// is gone, or Cloud refused it.
func cloudCredentialUnusable(err error) bool {
	if errors.Is(err, errCloudRefreshSessionClosing) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "401 Unauthorized") || strings.Contains(msg, "403 Forbidden")
}

// cloudForwarders tracks the server's active forwarders: their stores are
// kept from collection, and the engine's stop ends them.
type cloudForwarders struct {
	mu     sync.Mutex
	active map[*cloudForwarder]struct{}
}

func (r *cloudForwarders) add(f *cloudForwarder) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active == nil {
		r.active = map[*cloudForwarder]struct{}{}
	}
	r.active[f] = struct{}{}
}

func (r *cloudForwarders) remove(f *cloudForwarder) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.active, f)
}

// KeepSet returns the client IDs whose stores active forwarders read.
func (r *cloudForwarders) KeepSet() map[string]bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	keep := make(map[string]bool, len(r.active))
	for f := range r.active {
		keep[f.clientID] = true
	}
	return keep
}

// stopAll gives every forwarder until grace passes to finish on its own,
// then stops the rest and waits, until ctx ends, for them to release.
func (r *cloudForwarders) stopAll(ctx context.Context, grace time.Duration) error {
	r.mu.Lock()
	active := make([]*cloudForwarder, 0, len(r.active))
	for f := range r.active {
		active = append(active, f)
	}
	r.mu.Unlock()
	graceCtx, cancel := context.WithTimeout(ctx, grace)
	defer cancel()
	for _, f := range active {
		if f.waitReleased(graceCtx) != nil {
			break
		}
	}
	var errs error
	for _, f := range active {
		f.stop(errCloudForwardEngineStop)
	}
	for _, f := range active {
		errs = errors.Join(errs, f.waitReleased(ctx))
	}
	return errs
}

// cloudPublishedSpanExporter and cloudPublishedLogExporter mark what they
// pass on as published to Cloud by another writer (TelemetryCloudPublishedAttr):
// the stream of a scale-out engine that confirmed it publishes its own
// session still reaches the client routing, and the forwarder leaves it out.
type cloudPublishedSpanExporter struct {
	next sdktrace.SpanExporter
}

func (exp cloudPublishedSpanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	marked := make([]sdktrace.ReadOnlySpan, len(spans))
	for i, span := range spans {
		attrs := make([]attribute.KeyValue, 0, len(span.Attributes())+1)
		for _, attr := range span.Attributes() {
			if string(attr.Key) != telemetryattrs.TelemetryCloudPublishedAttr {
				attrs = append(attrs, attr)
			}
		}
		attrs = append(attrs, attribute.Bool(telemetryattrs.TelemetryCloudPublishedAttr, true))
		marked[i] = originReadOnlySpan{ReadOnlySpan: span, attrs: attrs}
	}
	return exp.next.ExportSpans(ctx, marked)
}

func (exp cloudPublishedSpanExporter) Shutdown(ctx context.Context) error {
	return exp.next.Shutdown(ctx)
}

type cloudPublishedLogExporter struct {
	next sdklog.Exporter
}

func (exp cloudPublishedLogExporter) Export(ctx context.Context, records []sdklog.Record) error {
	marked := make([]sdklog.Record, len(records))
	for i := range records {
		marked[i] = records[i].Clone()
		marked[i].AddAttributes(log.Bool(telemetryattrs.TelemetryCloudPublishedAttr, true))
	}
	return exp.next.Export(ctx, marked)
}

func (exp cloudPublishedLogExporter) ForceFlush(ctx context.Context) error {
	return exp.next.ForceFlush(ctx)
}

func (exp cloudPublishedLogExporter) Shutdown(ctx context.Context) error {
	return exp.next.Shutdown(ctx)
}
