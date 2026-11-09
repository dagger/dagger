// Package compressutil decompresses image layers.
package compressutil

import (
	"bufio"
	"errors"
	"io"
	"sync"

	"github.com/containerd/containerd/v2/pkg/archive/compression"
	"github.com/klauspost/compress/zstd"
)

// zstdDecoders holds zstd decoders between streams. A new decoder allocates
// its history buffer from the stream's window, about 8 MB for the usual 8 MB
// window however small the stream is; a decoder reset onto the next stream
// keeps it. Each stream being decoded has a decoder of its own, so streams
// decode at once as they would with new decoders.
var zstdDecoders sync.Pool

// DecompressStream is containerd's compression.DecompressStream: it detects
// whether r is uncompressed, gzip or zstd and returns its decompressed bytes,
// and the same for every stream. A zstd stream is decoded by a decoder
// reused from an earlier stream, which Close returns for the next one.
// Unlike containerd's, the decoder sizes its buffers to what the streams it
// decodes need: containerd's options allocate each of its block decoders'
// sequence buffer at the most a block can hold, 2.3 MB, for every stream, and
// a kept decoder holds all of them.
func DecompressStream(r io.Reader) (compression.DecompressReadCloser, error) {
	buf := bufio.NewReaderSize(r, 32*1024)
	bs, err := buf.Peek(10)
	if err != nil && !errors.Is(err, io.EOF) {
		// An empty stream is uncompressed, as containerd treats it.
		return nil, err
	}
	if compression.DetectCompression(bs) != compression.Zstd {
		return compression.DecompressStream(buf)
	}
	dec, _ := zstdDecoders.Get().(*zstd.Decoder)
	if dec == nil {
		dec, err = zstd.NewReader(nil)
		if err != nil {
			return nil, err
		}
	}
	if err := dec.Reset(buf); err != nil {
		dec.Close()
		return nil, err
	}
	return &zstdReader{dec: dec, buf: buf}, nil
}

// zstdReader reads a zstd stream through a reused decoder.
type zstdReader struct {
	dec *zstd.Decoder
	// buf is the stream as the decoder reads it, which the decoder keeps
	// referring to after its reset.
	buf *bufio.Reader
}

func (z *zstdReader) Read(p []byte) (int, error) {
	if z.dec == nil {
		return 0, zstd.ErrDecoderClosed
	}
	return z.dec.Read(p)
}

// Close stops the stream's decoding and returns its decoder for the next
// stream. The decoder no longer reads the stream once Close returns: its
// reset waits for the decoding of the stream to stop, as closing it would.
// The decoder still refers to the buffered stream, so the buffer lets go of
// the caller's reader before the decoder is kept.
func (z *zstdReader) Close() error {
	if z.dec == nil {
		return nil
	}
	if err := z.dec.Reset(nil); err != nil {
		z.dec.Close()
	} else {
		z.buf.Reset(nil)
		zstdDecoders.Put(z.dec)
	}
	z.dec, z.buf = nil, nil
	return nil
}

func (z *zstdReader) GetCompression() compression.Compression {
	return compression.Zstd
}
