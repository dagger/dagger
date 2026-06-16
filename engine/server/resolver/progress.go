package resolver

import (
	"context"
	"sync"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/dagger/dagger/engine/snapshots"
	digest "github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

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
	return wrapProgressWriter(ctx, w, wOpts.Desc), nil
}

// wrapProgressWriter wraps a content.Writer so the layer blob written
// through it streams transfer progress as telemetry, keyed by blob digest
// — the same wrapper serves pull (blobs fetched into the content store)
// and push (blobs copied to a registry's writer). Non-layer blobs pass
// through untouched: manifests and configs are tiny. The wrapper emits a
// pending state immediately so the item appears before bytes move.
func wrapProgressWriter(ctx context.Context, w content.Writer, desc ocispecs.Descriptor) content.Writer {
	if !images.IsLayerType(desc.MediaType) || desc.Size <= 0 {
		return w
	}
	pw := &progressWriter{
		Writer: w,
		ctx:    ctx,
		item:   desc.Digest.String(),
		total:  desc.Size,
	}
	if status, err := w.Status(); err == nil {
		// resume from a partially transferred blob
		pw.offset = status.Offset
	}
	pw.emit(true)
	return pw
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
	if !force && now.Sub(pw.lastEmit) < snapshots.ProgressEmitInterval && pw.offset < pw.total {
		pw.mu.Unlock()
		return
	}
	pw.lastEmit = now
	current := pw.offset
	pw.mu.Unlock()
	snapshots.EmitProgress(pw.ctx, pw.item, current, pw.total, "bytes")
}
