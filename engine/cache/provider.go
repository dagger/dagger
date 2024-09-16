package cache

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"dagger.io/dagger/telemetry"
	"github.com/containerd/containerd/content"
	"github.com/dagger/dagger/engine/session"
	"github.com/moby/buildkit/util/bklog"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const otelMagicacheDigestKey = "dagger.io/magicache.digest"
const otelMagicacheSize = "dagger.io/magicache.size"

type layerProvider struct {
	httpClient  *http.Client
	cacheClient Service
}

func (p *layerProvider) ReaderAt(ctx context.Context, desc ocispecs.Descriptor) (content.ReaderAt, error) {
	ctx, span := telemetry.Tracer(ctx, session.InstrumentationLibrary).
		Start(ctx, "magicache layer download")
	span.SetAttributes(
		attribute.String(otelMagicacheDigestKey, desc.Digest.String()),
		attribute.Int64(otelMagicacheSize, desc.Size),
	)

	resp, err := p.cacheClient.GetLayerDownloadURL(ctx, GetLayerDownloadURLRequest{
		Digest: desc.Digest,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get layer download url for digest %s: %w", desc.Digest, err)
	}

	return &urlReaderAt{
		ctx:        ctx,
		httpClient: p.httpClient,
		url:        resp.URL,
		desc:       desc,
		span:       span,
	}, nil
}

type cacheMountProvider struct {
	httpClient *http.Client
	url        string
}

func (p *cacheMountProvider) ReaderAt(ctx context.Context, desc ocispecs.Descriptor) (content.ReaderAt, error) {
	ctx, span := telemetry.Tracer(ctx, session.InstrumentationLibrary).
		Start(ctx, "magicache cachemount download")
	span.SetAttributes(
		attribute.String(otelMagicacheDigestKey, desc.Digest.String()),
		attribute.Int64(otelMagicacheSize, desc.Size),
	)
	return &urlReaderAt{
		ctx:        ctx,
		httpClient: p.httpClient,
		url:        p.url,
		desc:       desc,
		span:       span,
	}, nil
}

// urlReaderAt is optimized for reading a layer into the content store. Layers are read sequentially and in
// 1MB chunks by the underlying containerd content code. We therefore initialize the reader at the first
// offset and after that keep reading sequentially. If an attempt is made at a non-sequental read the reader
// is re-opened from the new offset, which is slow but not expected to happen often.
//
// The relevant code currently lives here:
// https://github.com/containerd/containerd/blob/7a77da2c26007fbf4b8526fd01d5ab06ac12d452/content/helpers.go#L150
type urlReaderAt struct {
	ctx        context.Context
	httpClient *http.Client
	url        string
	desc       ocispecs.Descriptor
	span       trace.Span

	// internally set fields
	body   io.ReadCloser
	offset int64
}

func (r *urlReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if r.body == nil || off != r.offset {
		// this is either the first read or a non-sequential one, so we need to (re-)open the reader
		req, err := http.NewRequest("GET", r.url, nil)
		if err != nil {
			return 0, err
		}
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", off))
		//nolint:bodyclose // the body is closed once we're done with it
		resp, err := r.httpClient.Do(req)
		if err != nil {
			return 0, err
		}

		if r.body != nil {
			// close previous body if we had to reset due to non-sequential read
			bklog.G(r.ctx).Debugf("non-sequential read in urlReaderAt for %s at offset %d", r.desc.Digest, off)
			r.body.Close()
		}
		r.body = resp.Body
		r.offset = off
	}

	n, err := r.body.Read(p)
	r.offset += int64(n)
	return n, err
}

func (r *urlReaderAt) Size() int64 {
	return r.desc.Size
}

func (r *urlReaderAt) Close() error {
	if r.span != nil {
		r.span.End()
	}

	if r.body != nil {
		return r.body.Close()
	}
	return nil
}
