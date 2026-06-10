package resolver

import (
	"context"
	"sync"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	digest "github.com/opencontainers/go-digest"
	"go.opentelemetry.io/otel/log"
)

// progressEmitInterval throttles streaming progress records; the final
// record for an item is always emitted so consumers converge on the true
// completed state.
const progressEmitInterval = 100 * time.Millisecond

// emitProgress sends one streaming-progress log record for the named item,
// attributed to the current span via ctx. See engine/telemetryattrs for the
// conventions.
func emitProgress(ctx context.Context, item string, current, total int64) {
	rec := log.Record{}
	rec.SetTimestamp(time.Now())
	// Explicit empty body: log consumers skip empty-bodied records as text,
	// and an unset body does not survive the OTLP round-trip (nil AnyValue
	// triggers conversion errors on the receiving side).
	rec.SetBody(log.StringValue(""))
	rec.AddAttributes(
		log.String(telemetryattrs.ProgressItemAttr, item),
		log.Int64(telemetryattrs.ProgressCurrentAttr, current),
		log.Int64(telemetryattrs.ProgressTotalAttr, total),
		log.String(telemetryattrs.ProgressUnitAttr, "bytes"),
	)
	telemetry.Logger(ctx, "dagger.io/progress").Emit(ctx, rec)
}

// progressIngester wraps a content.Ingester so that layer blobs written
// through it (e.g. by remotes.FetchHandler) stream download progress as
// telemetry, keyed by blob digest.
type progressIngester struct {
	content.Ingester
}

func (pi progressIngester) Writer(ctx context.Context, opts ...content.WriterOpt) (content.Writer, error) {
	w, err := pi.Ingester.Writer(ctx, opts...)
	if err != nil {
		return nil, err
	}

	var wOpts content.WriterOpts
	for _, opt := range opts {
		if err := opt(&wOpts); err != nil {
			return w, nil //nolint:nilerr // ignore option errors; progress is best-effort
		}
	}
	desc := wOpts.Desc
	if !images.IsLayerType(desc.MediaType) || desc.Size <= 0 {
		// only layers are interesting enough to track; manifests and configs
		// are tiny
		return w, nil
	}

	pw := &progressWriter{
		Writer: w,
		ctx:    ctx,
		item:   desc.Digest.String(),
		total:  desc.Size,
	}
	if status, err := w.Status(); err == nil {
		// resume from a partially fetched blob
		pw.offset = status.Offset
	}
	pw.emit(true)
	return pw, nil
}

type progressWriter struct {
	content.Writer
	ctx   context.Context
	item  string
	total int64

	mu       sync.Mutex
	offset   int64
	lastEmit time.Time
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	n, err := pw.Writer.Write(p)
	if n > 0 {
		pw.mu.Lock()
		pw.offset += int64(n)
		pw.mu.Unlock()
		pw.emit(false)
	}
	return n, err
}

func (pw *progressWriter) Commit(ctx context.Context, size int64, expected digest.Digest, opts ...content.Opt) error {
	err := pw.Writer.Commit(ctx, size, expected, opts...)
	if err == nil {
		pw.mu.Lock()
		pw.offset = pw.total
		pw.mu.Unlock()
		pw.emit(true)
	}
	return err
}

func (pw *progressWriter) emit(force bool) {
	pw.mu.Lock()
	now := time.Now()
	if !force && now.Sub(pw.lastEmit) < progressEmitInterval && pw.offset < pw.total {
		pw.mu.Unlock()
		return
	}
	pw.lastEmit = now
	current := pw.offset
	pw.mu.Unlock()
	emitProgress(pw.ctx, pw.item, current, pw.total)
}
