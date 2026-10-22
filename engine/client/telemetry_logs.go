package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	"google.golang.org/protobuf/proto"

	"github.com/dagger/dagger/engine/archive"
	enginetel "github.com/dagger/dagger/engine/telemetry"
)

// ErrLiveLogsUnsupported reports that the engine does not serve on-demand
// live log reads: it predates them, so the log push carries everything.
var ErrLiveLogsUnsupported = errors.New("engine does not serve on-demand live log reads")

// ErrTelemetryBarrierUnsupported reports that the engine predates
// TelemetryBarrier (enginetel.LiveFeatureFlush).
var ErrTelemetryBarrierUnsupported = errors.New("engine does not serve a telemetry barrier")

// SupportsTelemetryFeature reports whether the engine named a live telemetry
// feature (enginetel.LiveFeature*) on this client's trace subscription. It is
// false for an older engine, or a client without a trace subscription.
func (c *Client) SupportsTelemetryFeature(feature string) bool {
	return c.telemetryFeatures[feature]
}

// LogsPresence reports whether this client's log push omits display text
// (see Params.EngineLogsPresence): EngineLogs then sees presence records for
// it, and the frontend must read it with QueryLogs.
func (c *Client) LogsPresence() bool {
	return c.logsPresence
}

// WaitTelemetryIdle waits until the trace and log subscriptions have
// delivered nothing for quiet, or ctx ends. The engine batches what it
// records and the subscriptions poll for it, so a span (or presence) for work
// that just ended reaches EngineTrace/EngineLogs a little later. A frontend
// reading display text on demand waits for them at exit, before choosing what
// its final render reads, while the session can still serve the reads.
func (c *Client) WaitTelemetryIdle(ctx context.Context, quiet time.Duration) {
	start := time.Now().UnixNano()
	for {
		last := max(start, c.telemetryFrameAt.Load())
		wait := time.Duration(last + int64(quiet) - time.Now().UnixNano())
		if wait <= 0 {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// TelemetryBarrier waits until the trace and log subscriptions have
// delivered everything the engine stored for this client up to now: the
// engine flushes the session's telemetry and reports its store's high-water
// marks, which the subscriptions then reach. A frontend reading display text
// on demand passes it at exit, so the spans of work that just ended are
// there when it chooses what its final render reads, and the store still
// serves the reads. It needs enginetel.LiveFeatureFlush; an older engine
// answers ErrTelemetryBarrierUnsupported, and WaitTelemetryIdle remains.
func (c *Client) TelemetryBarrier(ctx context.Context) error {
	if !c.SupportsTelemetryFeature(enginetel.LiveFeatureFlush) {
		return ErrTelemetryBarrierUnsupported
	}
	marks, err := c.flushTelemetry(ctx)
	if err != nil {
		return err
	}
	if err := c.traceDelivered.wait(ctx, marks.Spans); err != nil {
		return fmt.Errorf("wait for spans: %w", err)
	}
	if err := c.logsDelivered.wait(ctx, marks.Logs); err != nil {
		return fmt.Errorf("wait for logs: %w", err)
	}
	return nil
}

func (c *Client) flushTelemetry(ctx context.Context) (enginetel.LiveFlushResult, error) {
	var marks enginetel.LiveFlushResult
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://dagger"+enginetel.LiveFlushPath, nil)
	if err != nil {
		return marks, err
	}
	resp, err := c.liveTelemetryHTTP().Do(req)
	if err != nil {
		return marks, fmt.Errorf("flush telemetry: %w", err)
	}
	defer resp.Body.Close()
	if err := expectLiveLogResponse(resp, "application/json"); err != nil {
		return marks, fmt.Errorf("flush telemetry: %w", err)
	}
	if err := json.NewDecoder(resp.Body).Decode(&marks); err != nil {
		return marks, fmt.Errorf("decode flush result: %w", err)
	}
	return marks, nil
}

// deliveredCursor is how far a subscription has delivered: the cursor of
// the last frame its callback returned from. The zero value is ready; a nil
// one ignores updates.
type deliveredCursor struct {
	mu     sync.Mutex
	cursor int64
	// ended is set once the subscription stops consuming.
	ended   bool
	changed chan struct{}
}

func (d *deliveredCursor) update(fn func()) {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	fn()
	if d.changed != nil {
		close(d.changed)
		d.changed = nil
	}
}

func (d *deliveredCursor) advance(cursor int64) {
	d.update(func() { d.cursor = max(d.cursor, cursor) })
}

func (d *deliveredCursor) end() {
	d.update(func() { d.ended = true })
}

// wait blocks until the subscription has delivered through cursor, ctx
// ends, or the subscription stops short of it.
func (d *deliveredCursor) wait(ctx context.Context, cursor int64) error {
	for {
		d.mu.Lock()
		at, ended := d.cursor, d.ended
		if at >= cursor {
			d.mu.Unlock()
			return nil
		}
		if ended {
			d.mu.Unlock()
			return fmt.Errorf("subscription ended at cursor %d, before %d", at, cursor)
		}
		if d.changed == nil {
			d.changed = make(chan struct{})
		}
		changed := d.changed
		d.mu.Unlock()
		select {
		case <-ctx.Done():
			return fmt.Errorf("delivered through cursor %d of %d: %w", at, cursor, context.Cause(ctx))
		case <-changed:
		}
	}
}

func (c *Client) liveTelemetryHTTP() *httpClient {
	if c.telemetryHTTP != nil {
		return c.telemetryHTTP
	}
	return c.httpClient
}

// QueryLogs reads display text for the spans q names from this client's own
// store, after cursor (a log row ID; 0 reads from the start). consume
// receives each batch with the cursor to resume after it; a batch may be
// empty, and may hold rows beyond the selection, which the caller routes
// as the frontend does. A one-shot query returns at the store's end as of
// the request; a follow returns when ctx ends (with ctx's error) or when the
// session's telemetry drains (nil). The returned cursor is the last one
// consumed, for resuming after an error.
func (c *Client) QueryLogs(ctx context.Context, q archive.LogQuery, cursor int64, consume func(int64, *collogspb.ExportLogsServiceRequest) error) (int64, error) {
	if !c.SupportsTelemetryFeature(enginetel.LiveFeatureLogQuery) {
		return cursor, ErrLiveLogsUnsupported
	}
	if err := q.Validate(); err != nil {
		return cursor, err
	}
	body, err := json.Marshal(q)
	if err != nil {
		return cursor, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://dagger/v1/logs/query", bytes.NewReader(body))
	if err != nil {
		return cursor, err
	}
	req.Header.Set("Content-Type", archive.LogQueryContentType)
	req.Header.Set("Accept", enginetel.LiveContentType)
	if cursor > 0 {
		req.Header.Set(enginetel.LiveCursorHeader, strconv.FormatInt(cursor, 10))
	}
	resp, err := c.liveTelemetryHTTP().Do(req)
	if err != nil {
		return cursor, fmt.Errorf("query logs: %w", err)
	}
	defer resp.Body.Close()
	if err := expectLiveLogResponse(resp, enginetel.LiveContentType); err != nil {
		return cursor, err
	}
	return consumeLogQuery(resp.Body, cursor, consume)
}

func consumeLogQuery(r io.Reader, cursor int64, consume func(int64, *collogspb.ExportLogsServiceRequest) error) (int64, error) {
	for {
		kind, next, payload, err := enginetel.ReadLiveFrame(r)
		if err != nil {
			return cursor, fmt.Errorf("read log query: %w", err)
		}
		switch kind {
		case enginetel.LiveFrameHello:
			continue
		case enginetel.LiveFrameTerminal:
			if next != cursor {
				return cursor, fmt.Errorf("%w: log query terminal cursor %d, expected %d", enginetel.ErrInvalidLiveFrame, next, cursor)
			}
			return cursor, nil
		}
		if next <= cursor {
			return cursor, fmt.Errorf("%w: non-increasing log query cursor %d after %d", enginetel.ErrInvalidLiveFrame, next, cursor)
		}
		batch := &collogspb.ExportLogsServiceRequest{}
		if err := proto.Unmarshal(payload, batch); err != nil {
			return cursor, fmt.Errorf("decode log query batch: %w", err)
		}
		enginetel.NormalizeLogsRequest(batch)
		if err := consume(next, batch); err != nil {
			return cursor, err
		}
		cursor = next
	}
}

// SearchLogs searches display text in this client's own store.
func (c *Client) SearchLogs(ctx context.Context, search archive.LogSearch) (archive.LogSearchResult, error) {
	if !c.SupportsTelemetryFeature(enginetel.LiveFeatureLogSearch) {
		return archive.LogSearchResult{}, ErrLiveLogsUnsupported
	}
	if _, err := search.Matcher(); err != nil {
		return archive.LogSearchResult{}, err
	}
	body, err := json.Marshal(search)
	if err != nil {
		return archive.LogSearchResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://dagger/v1/logs/search", bytes.NewReader(body))
	if err != nil {
		return archive.LogSearchResult{}, err
	}
	req.Header.Set("Content-Type", archive.LogQueryContentType)
	resp, err := c.liveTelemetryHTTP().Do(req)
	if err != nil {
		return archive.LogSearchResult{}, fmt.Errorf("search logs: %w", err)
	}
	defer resp.Body.Close()
	if err := expectLiveLogResponse(resp, "application/json"); err != nil {
		return archive.LogSearchResult{}, err
	}
	var result archive.LogSearchResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return archive.LogSearchResult{}, fmt.Errorf("decode log search: %w", err)
	}
	return result, nil
}

func expectLiveLogResponse(resp *http.Response, contentType string) error {
	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if resp.StatusCode == http.StatusOK && mediaType == contentType {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	return fmt.Errorf("live log request: %s (%s): %s", resp.Status, resp.Header.Get("Content-Type"), strings.TrimSpace(string(body)))
}
