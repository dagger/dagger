package resolver

import (
	"context"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/remotes"
	"github.com/dagger/dagger/engine/snapshots"
	enginetelemetry "github.com/dagger/dagger/engine/telemetry"
	digest "github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

// progressIngester wraps a content.Ingester so layer blobs written through it
// stream download progress, keyed by blob digest.
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
	return wrapProgressWriter(ctx, w, wOpts.Desc, nil), nil
}

// networkFetcher accounts for every descriptor payload read through a registry
// fetcher, including metadata fetched before the layer dispatch starts.
type networkFetcher struct {
	remotes.Fetcher
	network *enginetelemetry.NetworkAccumulator
}

func (f networkFetcher) Fetch(ctx context.Context, desc ocispecs.Descriptor) (io.ReadCloser, error) {
	r, err := f.Fetcher.Fetch(ctx, desc)
	if err != nil {
		return nil, err
	}
	return &networkReadCloser{ReadCloser: r, network: f.network}, nil
}

type networkReadCloser struct {
	io.ReadCloser
	network *enginetelemetry.NetworkAccumulator
}

func (r *networkReadCloser) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	r.network.Add(int64(n))
	return n, err
}

// networkRoundTripper accounts for registry payloads read before a descriptor
// fetcher exists, such as the manifest GET fallback performed during resolve.
type networkRoundTripper struct {
	http.RoundTripper
	network *enginetelemetry.NetworkAccumulator
}

func (t networkRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.RoundTripper.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if resp.Body != nil {
		resp.Body = &networkReadCloser{ReadCloser: resp.Body, network: t.network}
	}
	return resp, nil
}

// wrapProgressWriter wraps a content.Writer so layer blobs stream transfer
// progress, keyed by blob digest. When network is non-nil, every descriptor
// also contributes to network metrics. The same wrapper serves pull (where
// accounting happens at the fetcher) and push (where it happens here).
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
