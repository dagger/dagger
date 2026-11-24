package compression

import (
	"context"
	"io"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/klauspost/compress/zstd"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

func (c zstdType) Compress(ctx context.Context, comp Config) (compressorFunc Compressor, finalize Finalizer) {
	return func(dest io.Writer, _ string) (io.WriteCloser, error) {
		return zstdWriter(comp)(dest)
	}, nil
}

func (c zstdType) Decompress(ctx context.Context, cs content.Store, desc ocispecs.Descriptor) (io.ReadCloser, error) {
	return decompress(ctx, cs, desc)
}

func (c zstdType) NeedsConversion(ctx context.Context, cs content.Store, desc ocispecs.Descriptor) (bool, error) {
	if !images.IsLayerType(desc.MediaType) {
		return false, nil
	}
	ct, err := FromMediaType(desc.MediaType)
	if err != nil {
		return false, err
	}
	if ct == Zstd {
		return false, nil
	}
	return true, nil
}

func (c zstdType) NeedsComputeDiffBySelf(comp Config) bool {
	return true
}

func (c zstdType) OnlySupportOCITypes() bool {
	return false
}

func (c zstdType) MediaType() string {
	return ocispecs.MediaTypeImageLayerZstd
}

func (c zstdType) String() string {
	return "zstd"
}

func zstdWriter(comp Config) func(io.Writer) (io.WriteCloser, error) {
	return func(dest io.Writer) (io.WriteCloser, error) {
		level := zstd.SpeedDefault
		if comp.Level != nil {
			level = toZstdEncoderLevel(*comp.Level)
		}
		enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(level))
		if err != nil {
			return nil, err
		}
		return &zstdLayerWriter{enc: enc, dest: dest}, nil
	}
}

// zstdSizedLimit is the largest layer a zstdLayerWriter holds back to write
// with its size: zstd's usual window, which no larger layer can fit anyway.
const zstdSizedLimit = 8 << 20

// zstdLayerWriter compresses a layer into one zstd frame that names the
// layer's size whenever the layer fits in zstdSizedLimit. A stream writer
// knows the size only of a layer that fits in one 128 KB block; any other
// frame declares the encoder's whole window instead, and every decoder of
// it reserves twice that, 16 MB, however small the layer. So the writer
// holds the layer back until it outgrows the limit or ends: a layer that
// ends first is written with its size, and its frame's window fits it.
type zstdLayerWriter struct {
	enc  *zstd.Encoder
	dest io.Writer
	// held is the layer so far, while it fits in zstdSizedLimit and the
	// encoder hasn't started.
	held      []byte
	streaming bool
}

func (w *zstdLayerWriter) Write(p []byte) (int, error) {
	if w.streaming {
		return w.enc.Write(p)
	}
	if len(w.held)+len(p) <= zstdSizedLimit {
		w.held = append(w.held, p...)
		return len(p), nil
	}
	// Too large to name its size: the frame declares the window.
	w.streaming = true
	w.enc.Reset(w.dest)
	held := w.held
	w.held = nil
	if _, err := w.enc.Write(held); err != nil {
		return 0, err
	}
	return w.enc.Write(p)
}

func (w *zstdLayerWriter) Close() error {
	if !w.streaming {
		w.streaming = true
		w.enc.ResetContentSize(w.dest, int64(len(w.held)))
		held := w.held
		w.held = nil
		if _, err := w.enc.Write(held); err != nil {
			return err
		}
	}
	return w.enc.Close()
}

func toZstdEncoderLevel(level int) zstd.EncoderLevel {
	// map zstd compression levels to go-zstd levels
	// once we also have c based implementation move this to helper pkg
	if level < 0 {
		return zstd.SpeedDefault
	} else if level < 3 {
		return zstd.SpeedFastest
	} else if level < 7 {
		return zstd.SpeedDefault
	} else if level < 9 {
		return zstd.SpeedBetterCompression
	}
	return zstd.SpeedBestCompression
}
