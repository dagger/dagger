package snapshots

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/pkg/archive/compression"
	cerrdefs "github.com/containerd/errdefs"
	digest "github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	// aheadChunk is the size of the pieces a layer's blob is decompressed
	// into ahead of its apply, and aheadChunks how many of them it runs
	// ahead, at most: 16 MiB.
	aheadChunk  = 1 << 20
	aheadChunks = 16
)

var aheadChunkPool = sync.Pool{New: func() any {
	b := make([]byte, aheadChunk)
	return &b
}}

// errAheadStopped stops a decompression the apply no longer reads.
var errAheadStopped = errors.New("the apply stopped reading")

// decompressedAhead is a stored blob's layer decompressed on a goroutine of
// its own, up to aheadChunks pieces ahead of its reader. An applier that
// decompresses as it reads spends the decompression and the file creation
// one after the other; read from here, they overlap.
type decompressedAhead struct {
	chunks chan []byte
	// cur is the piece being read, from its offset off; buf is the pooled
	// buffer behind it.
	cur []byte
	buf *[]byte
	// err is the decompression's end: io.EOF, or its failure. It is set
	// before chunks is closed, after the chunks read before it.
	err error
	// read counts the blob's compressed bytes decompressed so far.
	read atomic.Int64
	stop chan struct{}
	done chan struct{}
}

// decompressAhead starts decompressing desc's blob, from the content store,
// ahead of its reader. The caller closes it.
func (cm *snapshotManager) decompressAhead(ctx context.Context, desc ocispecs.Descriptor) (*decompressedAhead, error) {
	ra, err := cm.ContentStore.ReaderAt(ctx, desc)
	if err != nil {
		return nil, err
	}
	d := &decompressedAhead{chunks: make(chan []byte, aheadChunks), stop: make(chan struct{}), done: make(chan struct{})}
	ds, err := compression.DecompressStream(&countingSectionReader{r: io.NewSectionReader(ra, 0, ra.Size()), n: &d.read})
	if err != nil {
		_ = ra.Close()
		return nil, err
	}
	go func() {
		defer close(d.done)
		defer ra.Close()
		defer ds.Close()
		defer close(d.chunks)
		for {
			bp := aheadChunkPool.Get().(*[]byte)
			n, err := fillChunk(ds, *bp)
			if n > 0 {
				select {
				case d.chunks <- (*bp)[:n]:
				case <-d.stop:
					aheadChunkPool.Put(bp)
					d.err = errAheadStopped
					return
				}
			} else {
				aheadChunkPool.Put(bp)
			}
			if err != nil {
				d.err = err
				return
			}
		}
	}()
	return d, nil
}

// fillChunk reads r into b until b is full or r ends: with io.EOF at the
// stream's end, or with the decompressor's own error, a truncated stream's
// included.
func fillChunk(r io.Reader, b []byte) (int, error) {
	n := 0
	for n < len(b) {
		m, err := r.Read(b[n:])
		n += m
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

func (d *decompressedAhead) Read(p []byte) (int, error) {
	for len(d.cur) == 0 {
		if d.buf != nil {
			aheadChunkPool.Put(d.buf)
			d.buf = nil
		}
		chunk, ok := <-d.chunks
		if !ok {
			return 0, d.err
		}
		d.cur = chunk
		b := chunk[:cap(chunk)]
		d.buf = &b
	}
	n := copy(p, d.cur)
	d.cur = d.cur[n:]
	return n, nil
}

// Close stops the decompression and waits for it to end.
func (d *decompressedAhead) Close() {
	close(d.stop)
	for chunk := range d.chunks {
		b := chunk[:cap(chunk)]
		aheadChunkPool.Put(&b)
	}
	<-d.done
}

// countingSectionReader counts the bytes read through it into n.
type countingSectionReader struct {
	r *io.SectionReader
	n *atomic.Int64
}

func (c *countingSectionReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n.Add(int64(n))
	return n, err
}

// aheadProvider is the content an applier reads a layer decompressed ahead
// from: the layer's tar, under its blob's digest, and nothing else.
type aheadProvider struct {
	digest digest.Digest
	ahead  *decompressedAhead
}

func (p aheadProvider) ReaderAt(_ context.Context, desc ocispecs.Descriptor) (content.ReaderAt, error) {
	if desc.Digest != p.digest {
		return nil, fmt.Errorf("apply of %s asked for %s: %w", p.digest, desc.Digest, cerrdefs.ErrNotFound)
	}
	return aheadReaderAt{p.ahead}, nil
}

// aheadReaderAt hands an applier the decompressed stream: content.NewReader
// reads it through Reader, in order.
type aheadReaderAt struct{ ahead *decompressedAhead }

func (r aheadReaderAt) Reader() io.Reader { return r.ahead }
func (aheadReaderAt) Close() error        { return nil }
func (aheadReaderAt) Size() int64         { return -1 }
func (aheadReaderAt) ReadAt([]byte, int64) (int, error) {
	return 0, errors.New("a layer decompressed ahead is read in order")
}

// canDecompressAhead reports whether desc's blob is a tar this engine can
// decompress itself: uncompressed, or compressed as containerd knows.
func canDecompressAhead(ctx context.Context, desc ocispecs.Descriptor) bool {
	_, err := images.DiffCompression(ctx, desc.MediaType)
	return err == nil
}
