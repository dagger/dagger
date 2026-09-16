package resolver

import (
	"context"
	"sync"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/dagger/dagger/engine/snapshots"
	enginetelemetry "github.com/dagger/dagger/engine/telemetry"
	digest "github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

// progressIngester wraps a content.Ingester so descriptors written through it
// (e.g. by remotes.FetchHandler) contribute to network metrics. Layer blobs
// additionally stream download progress, keyed by blob digest.
type progressIngester struct {
	content.Ingester
	network *enginetelemetry.NetworkAccumulator
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
	return wrapProgressWriter(ctx, w, wOpts.Desc, pi.network), nil
}

// wrapProgressWriter wraps a content.Writer so every descriptor contributes
// to network metrics. Layer blobs additionally stream transfer progress,
// keyed by blob digest; manifests and configs stay out of the progress UI.
// The same wrapper serves pull (blobs fetched into the content store) and push
// (blobs copied to a registry's writer).
func wrapProgressWriter(ctx context.Context, w content.Writer, desc ocispecs.Descriptor, network *enginetelemetry.NetworkAccumulator) content.Writer {
	if desc.Size <= 0 {
		return w
	}
	pw := &progressWriter{
		Writer:        w,
		ctx:           ctx,
		item:          desc.Digest.String(),
		total:         desc.Size,
		network:       network,
		trackProgress: images.IsLayerType(desc.MediaType),
	}
	if status, err := w.Status(); err == nil {
		// resume from a partially transferred blob
		pw.offset = status.Offset
	}
	if pw.trackProgress {
		pw.emit(true)
	}
	return pw
}

type progressWriter struct {
	content.Writer
	ctx           context.Context
	item          string
	total         int64
	network       *enginetelemetry.NetworkAccumulator
	trackProgress bool

	mu       sync.Mutex
	offset   int64
	lastEmit time.Time
}

func (pw *progressWriter) Status() (content.Status, error) {
	status, err := pw.Writer.Status()
	if err != nil {
		return content.Status{}, err
	}
	pw.mu.Lock()
	pw.offset = status.Offset
	pw.mu.Unlock()
	return status, nil
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	n, err := pw.Writer.Write(p)
	if n > 0 {
		pw.network.Add(int64(n))
		pw.mu.Lock()
		pw.offset += int64(n)
		pw.mu.Unlock()
		if pw.trackProgress {
			pw.emit(false)
		}
	}
	return n, err
}

func (pw *progressWriter) Commit(ctx context.Context, size int64, expected digest.Digest, opts ...content.Opt) error {
	err := pw.Writer.Commit(ctx, size, expected, opts...)
	if err == nil {
		pw.mu.Lock()
		pw.offset = pw.total
		pw.mu.Unlock()
		if pw.trackProgress {
			pw.emit(true)
		}
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
