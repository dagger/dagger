package dagql

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/dagger/dagger/engine/snapshots"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

var _ snapshots.PackProvider = (*partContentProvider)(nil)

// PackOf returns the pack the offer stores blob in, if it does.
func (p *partContentProvider) PackOf(blob digest.Digest) (digest.Digest, bool) {
	id, ok := p.packs[blob]
	return id, ok
}

// OpenPack opens the whole pack that holds blob, with one request to blob's
// address, which is the pack's. A retry status renews it through the
// demand's renewal, as a blob's address is.
func (p *partContentProvider) OpenPack(ctx context.Context, blob digest.Digest) (io.ReadCloser, error) {
	layer, ok := p.layer(blob)
	if !ok {
		return nil, partContentError(ctx, ocispecs.Descriptor{Digest: blob}, "provider", fmt.Errorf("unknown supplied blob %s", blob))
	}
	desc := layer.Descriptor
	address, renewed, err := p.address(ctx, blob, "")
	if err != nil {
		return nil, partContentError(ctx, desc, "provider", err)
	}
	for {
		stream, resp, err := p.get(ctx, address, 0, -1) //nolint:bodyclose // The body is handed to the stream wrapper, which closes it in stream.close and its context AfterFunc.
		if err != nil {
			return nil, partContentError(ctx, desc, "provider", err)
		}
		if _, retry := renewalRetryStatuses[resp.StatusCode]; retry {
			stream.close()
			err := fmt.Errorf("pack address returned HTTP %d", resp.StatusCode)
			if renewed {
				return nil, partContentError(ctx, desc, "provider", err)
			}
			next, _, renewErr := p.address(ctx, blob, address)
			if renewErr != nil {
				return nil, partContentError(ctx, desc, "provider", errors.Join(err, renewErr))
			}
			address, renewed = next, true
			continue
		}
		if err := acceptPack(resp); err != nil {
			stream.close()
			return nil, partContentError(ctx, desc, "provider", err)
		}
		p.stats.usedStreams(1)
		return packBody{stream}, nil
	}
}

// acceptPack checks a response to a request for a whole pack: the object, or
// a partial response that is all of it.
func acceptPack(resp *http.Response) error {
	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusPartialContent:
		var start, end, total int64
		value := resp.Header.Get("Content-Range")
		if _, err := fmt.Sscanf(value, "bytes %d-%d/%d", &start, &end, &total); err != nil || start != 0 || end != total-1 {
			return fmt.Errorf("content range %q is not the whole pack", value)
		}
		return nil
	default:
		return fmt.Errorf("pack HTTP status %d", resp.StatusCode)
	}
}

// packBody is a pack's response, read under the idle bound.
type packBody struct {
	*partHTTPStream
}

func (b packBody) Close() error {
	b.close()
	return nil
}

// PackOf and OpenPack pass the provider's packs through. The pack's bytes,
// and the time blocked reading them, count as the install's.
func (p *countingProvider) PackOf(blob digest.Digest) (digest.Digest, bool) {
	packs, ok := p.InfoReaderProvider.(snapshots.PackProvider)
	if !ok {
		return "", false
	}
	return packs.PackOf(blob)
}

func (p *countingProvider) OpenPack(ctx context.Context, blob digest.Digest) (io.ReadCloser, error) {
	packs, ok := p.InfoReaderProvider.(snapshots.PackProvider)
	if !ok {
		return nil, fmt.Errorf("blob %s is not in a pack", blob)
	}
	body, err := packs.OpenPack(ctx, blob)
	if err != nil {
		return nil, err
	}
	return &countingBody{ReadCloser: body, read: &p.read, readNS: &p.readNS}, nil
}

type countingBody struct {
	io.ReadCloser
	read   *atomic.Int64
	readNS *atomic.Int64
}

func (b *countingBody) Read(p []byte) (int, error) {
	start := time.Now()
	n, err := b.ReadCloser.Read(p)
	b.readNS.Add(int64(time.Since(start)))
	b.read.Add(int64(n))
	return n, err
}

// PackOf and OpenPack pass the fixture's provider's packs through; opening a
// pack is a provider read.
func (p partFixtureProvider) PackOf(blob digest.Digest) (digest.Digest, bool) {
	packs, ok := p.InfoReaderProvider.(snapshots.PackProvider)
	if !ok {
		return "", false
	}
	return packs.PackOf(blob)
}

func (p partFixtureProvider) OpenPack(ctx context.Context, blob digest.Digest) (io.ReadCloser, error) {
	packs, ok := p.InfoReaderProvider.(snapshots.PackProvider)
	if !ok {
		return nil, fmt.Errorf("blob %s is not in a pack", blob)
	}
	p.cache.recordPartFixture(p.row, p.address, "provider-read")
	return packs.OpenPack(ctx, blob)
}
